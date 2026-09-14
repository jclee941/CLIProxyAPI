import { fileURLToPath, pathToFileURL } from "node:url";
import { join } from "node:path";
import { z } from "zod";

const root = fileURLToPath(new URL(".", import.meta.url));
const executable = Bun.which("typescript-language-server");
if (!executable) throw new Error("installed_typescript_language_server_required");
const server = Bun.spawn([executable, "--stdio"], { cwd: root, stdin: "pipe", stdout: "pipe", stderr: "ignore" });
const waiting = new Map<number, (value: unknown) => void>();
const diagnostics = new Map<string, readonly { readonly severity?: number | undefined }[]>();
const files = [...new Bun.Glob("*.ts").scanSync({ cwd: root }), ...new Bun.Glob("tests/*.ts").scanSync({ cwd: root })].sort();
const expected = new Set(files.map((file) => pathToFileURL(join(root, file)).href));
const completed = Promise.withResolvers<void>();
let nextId = 1;
const diagnosticMessage = z.object({ uri: z.string(), diagnostics: z.array(z.object({ severity: z.number().optional() })) });

async function send(message: Record<string, unknown>): Promise<void> {
  const body = JSON.stringify({ jsonrpc: "2.0", ...message });
  server.stdin.write(`Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`);
  await server.stdin.flush();
}
async function request(method: string, params: unknown): Promise<unknown> {
  const id = nextId++;
  const result = Promise.withResolvers<unknown>();
  waiting.set(id, result.resolve);
  await send({ id, method, params });
  return result.promise;
}
function receive(raw: unknown): void {
  if (typeof raw !== "object" || raw === null) throw new TypeError("invalid_lsp_response");
  if ("id" in raw && typeof raw.id === "number") {
    waiting.get(raw.id)?.(raw);
    waiting.delete(raw.id);
  }
  if ("method" in raw && raw.method === "textDocument/publishDiagnostics" && "params" in raw) {
    const data = diagnosticMessage.parse(raw.params);
    if (expected.has(data.uri)) diagnostics.set(data.uri, data.diagnostics);
    if (diagnostics.size === expected.size) completed.resolve();
  }
}
async function readMessages(): Promise<void> {
  let buffer = Buffer.alloc(0);
  const output = server.stdout.getReader();
  try {
    while (true) {
      const chunk = await output.read();
      if (chunk.done) return;
      buffer = Buffer.concat([buffer, Buffer.from(chunk.value)]);
      while (true) {
        const end = buffer.indexOf("\r\n\r\n");
        if (end < 0) break;
        const match = /Content-Length: (\d+)/i.exec(buffer.subarray(0, end).toString());
        if (!match?.[1]) throw new TypeError("invalid_lsp_header");
        const length = Number(match[1]);
        if (buffer.length < end + 4 + length) break;
        const message: unknown = JSON.parse(buffer.subarray(end + 4, end + 4 + length).toString());
        buffer = buffer.subarray(end + 4 + length);
        receive(message);
      }
    }
  } finally { output.releaseLock(); }
}
const reader = readMessages();
const deadline = setTimeout(() => completed.reject(new Error("lsp_diagnostics_timeout")), 30000);
try {
  if (expected.size === 0) throw new Error("lsp_source_files_missing");
  await Promise.race([completed.promise, reader, request("initialize", { processId: process.pid, rootUri: pathToFileURL(root).href,
    capabilities: { textDocument: { publishDiagnostics: { versionSupport: true } } },
    workspaceFolders: [{ uri: pathToFileURL(root).href, name: "gemini-web-login-companion" }] })]);
  await send({ method: "initialized", params: {} });
  for (const file of files) {
    await send({ method: "textDocument/didOpen", params: { textDocument: {
      uri: pathToFileURL(join(root, file)).href, languageId: "typescript", version: 1,
      text: await Bun.file(join(root, file)).text(),
    } } });
  }
  await Promise.race([completed.promise, reader]);
  if (diagnostics.size !== expected.size) throw new Error("incomplete_lsp_diagnostics");
  const errors = [...diagnostics.values()].flat().filter((item) => item.severity === 1).length;
  const report = { server: executable, root, files, diagnostics: diagnostics.size, errors };
  await Bun.write(join(root, "evidence", "lsp-report.json"), JSON.stringify(report, null, 2) + "\n");
  console.info(JSON.stringify({ files: files.length, diagnostics: diagnostics.size, errors }));
  if (errors) throw new Error("lsp_source_errors");
  await request("shutdown", null);
  await send({ method: "exit" });
} finally {
  clearTimeout(deadline);
  server.kill();
  await server.exited;
  await Promise.allSettled([reader]);
}
