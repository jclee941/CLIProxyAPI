import { mkdir } from "node:fs/promises";
import { z } from "zod";
import { parse, stringify } from "yaml";
import { call } from "./callers";
import type { Caller } from "./callers";
import { scenarioSchema, startUpstream, terminalCode } from "./upstream";
import type { Scenario } from "./upstream";

const [binary, outputDir, python] = process.argv.slice(2);
if (!binary || !outputDir || !python) throw new Error("Usage: fixture.ts CORE OUTPUT_DIR PYTHON");
const policySchema = z.object({ "request-scoped-errors": z.array(z.object({ status: z.number(), "match-regexr": z.array(z.string()), action: z.literal("stop") })) });
const policy = policySchema.parse(parse(await Bun.file(`${import.meta.dir}/provider-fragment.yaml`).text()));
const callers: readonly Caller[] = ["direct", "openai-node", "openai-python", "vercel-default", "vercel-policy"];
const results: { caller: Caller; scenario: Scenario; submissions: string[]; checks: Record<string, boolean>; result: Awaited<ReturnType<typeof call>> }[] = [];

await mkdir(outputDir, { recursive: true });
for (const caller of callers) {
  for (const scenario of scenarioSchema.options) {
    const work = `${outputDir}/${caller}-${scenario}`;
    await mkdir(`${work}/auths`, { recursive: true });
    const upstream = startUpstream(scenario);
    const config = {
      host: "127.0.0.1", port: 18311, "auth-dir": `${work}/auths`,
      "api-keys": ["synthetic-downstream"], "request-retry": 3,
      streaming: { "bootstrap-retries": 2 }, "passthrough-headers": false,
      "remote-management": { "disable-control-panel": true },
      "openai-compatibility": [{ name: "chatgpt2api", "base-url": `${upstream.server.url}v1`,
        "api-key-entries": [{ "api-key": "synthetic-upstream-a" }, { "api-key": "synthetic-upstream-b" }],
        models: [{ name: "synthetic-pro", alias: "synthetic-pro" }], ...policy,
      }],
    };
    await Bun.write(`${work}/config.yaml`, stringify(config));
    const core = Bun.spawn([binary, "--config", `${work}/config.yaml`, "--local-model"], {
      cwd: work, env: { PATH: "/usr/bin:/bin", HOME: work, TMPDIR: work }, stdout: "pipe", stderr: "pipe",
    });
    let log = "";
    let ready: (() => void) | undefined;
    const started = new Promise<void>((resolve) => { ready = resolve; });
    const capture = async (stream: ReadableStream<Uint8Array>) => {
      const reader = stream.getReader();
      const decoder = new TextDecoder();
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done) break;
        log += decoder.decode(chunk.value, { stream: true });
        if (log.includes("file watcher started")) ready?.();
      }
    };
    const stdout = capture(core.stdout);
    const stderr = capture(core.stderr);
    try {
      await Promise.race([started, core.exited.then(() => { throw new Error(`core exited before readiness: ${log}`); })]);
      const result = await call({ caller, scenario, baseURL: "http://127.0.0.1:18311/v1", ackURL: `${upstream.server.url}partial-read`, python });
      const terminal = ["terminal", "http-stream-terminal", "sse-first", "sse-role", "sse-partial"].includes(scenario);
      const transient = ["transient503", "other502", "code-in-message"].includes(scenario);
      const vercelDefaultHTTP = caller === "vercel-default" && ["terminal", "http-stream-terminal", "sse-first"].includes(scenario);
      const expected = vercelDefaultHTTP ? 3 : transient ? 2 : 1;
      const checks: Record<string, boolean> = {
        submissions: upstream.submissions.length === expected,
        classification: result.failed === terminal,
        content: result.content === (scenario === "sse-partial" ? "PARTIAL" : terminal ? "" : "FINAL"),
        noFalseSuccess: result.stops === (terminal ? 0 : 1),
        bothCandidatesOnTransient: !transient || new Set(upstream.submissions).size === 2,
        terminalCode: !terminal || result.code === ((caller.startsWith("vercel-") && ["sse-role", "sse-partial"].includes(scenario)) ? null : terminalCode),
      };
      if (caller === "direct" && ["terminal", "http-stream-terminal", "sse-first"].includes(scenario)) {
        checks["honest502"] = result.status === 502;
        checks["mandatoryHeader"] = result.retryHeader === "false";
      }
      if (caller === "direct" && scenario === "sse-partial") checks["alreadyCommitted200"] = result.status === 200;
      const report = { caller, scenario, submissions: [...upstream.submissions], checks, result };
      results.push(report);
      console.log(JSON.stringify(report));
    } finally {
      upstream.ack();
      core.kill("SIGTERM");
      await core.exited;
      await Promise.all([stdout, stderr]);
      await Bun.write(`${work}/core.log`, log);
      await upstream.server.stop(true);
    }
  }
}
await Bun.write(`${outputDir}/report.json`, `${JSON.stringify(results, null, 2)}\n`);
const failures = results.flatMap((row) => Object.entries(row.checks).filter(([, pass]) => !pass).map(([check]) => `${row.caller}/${row.scenario}/${check}`));
console.log(JSON.stringify({ cases: results.length, failures }));
process.exitCode = Number(failures.length > 0);
