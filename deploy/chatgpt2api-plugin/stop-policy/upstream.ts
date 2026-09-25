import { z } from "zod";

export const scenarioSchema = z.enum([
  "terminal", "http-stream-terminal", "sse-first", "sse-role", "sse-partial",
  "success", "success-stream", "transient503", "other502", "code-in-message",
]);
export type Scenario = z.infer<typeof scenarioSchema>;
export const terminalCode = "incomplete_upstream_response";
export const terminalBody = {
  error: { message: "Synthetic unfinished turn. Do not automatically resubmit.", type: "server_error", code: terminalCode, param: null },
};
const requestSchema = z.object({ model: z.string(), stream: z.boolean().optional() });

export function startUpstream(scenario: Scenario) {
  const submissions: string[] = [];
  let releasePartial: (() => void) | undefined;
  const partialRead = new Promise<void>((resolve) => { releasePartial = resolve; });
  const frame = (content: string, finish: string | null = null) => ({
    id: "synthetic-completion", object: "chat.completion.chunk", created: 1, model: "synthetic-pro",
    choices: [{ index: 0, delta: { role: "assistant", content }, finish_reason: finish }],
  });
  const server = Bun.serve({
    hostname: "127.0.0.1", port: 0,
    async fetch(request) {
      const path = new URL(request.url).pathname;
      if (path === "/partial-read") {
        releasePartial?.();
        return new Response("ack");
      }
      if (path !== "/v1/chat/completions") return new Response("not found", { status: 404 });
      requestSchema.parse(await request.json());
      const key = request.headers.get("authorization");
      if (key !== "Bearer synthetic-upstream-a" && key !== "Bearer synthetic-upstream-b") {
        return new Response("invalid synthetic key", { status: 401 });
      }
      submissions.push(key.replace("Bearer synthetic-upstream-", ""));
      if (["terminal", "http-stream-terminal"].includes(scenario)) {
        return Response.json(terminalBody, { status: 502 });
      }
      if (["transient503", "other502", "code-in-message"].includes(scenario) && submissions.length === 1) {
        return Response.json({ error: { code: "ordinary_transient", type: "server_error", message: scenario === "code-in-message" ? terminalCode : "Synthetic transient" } }, { status: scenario === "transient503" ? 503 : 502 });
      }
      if (["sse-first", "sse-role", "sse-partial", "success-stream"].includes(scenario)) {
        const encoder = new TextEncoder();
        const body = new ReadableStream<Uint8Array>({
          async start(controller) {
            const emit = (payload: unknown) => controller.enqueue(encoder.encode(`data: ${JSON.stringify(payload)}\n\n`));
            if (scenario === "sse-role") emit(frame(""));
            if (scenario === "sse-partial") {
              emit(frame("PARTIAL"));
              await partialRead;
            }
            if (scenario === "success-stream") {
              emit(frame("FINAL"));
              emit(frame("", "stop"));
              controller.enqueue(encoder.encode("data: [DONE]\n\n"));
            } else {
              emit(terminalBody);
            }
            controller.close();
          },
        });
        return new Response(body, { headers: { "content-type": "text/event-stream" } });
      }
      return Response.json({ id: "synthetic-completion", object: "chat.completion", created: 1, model: "synthetic-pro", choices: [{ index: 0, message: { role: "assistant", content: "FINAL" }, finish_reason: "stop" }], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } });
    },
  });
  return { server, submissions, ack: () => releasePartial?.() };
}
