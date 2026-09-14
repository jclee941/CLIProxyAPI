import OpenAI from "openai";
import { APICallError, RetryError, generateText, streamText } from "ai";
import { createOpenAICompatible, OpenAICompatibleChatLanguageModel } from "@ai-sdk/openai-compatible";
import { z } from "zod";
import { proTerminalErrorStructure } from "./vercel-policy";
import type { Scenario } from "./upstream";

export type Caller = "direct" | "openai-node" | "openai-python" | "vercel-default" | "vercel-policy";
export const resultSchema = z.object({
  failed: z.boolean(), code: z.string().nullable(), status: z.number().nullable(),
  retryHeader: z.string().nullable(), content: z.string(), stops: z.number(),
});
export type Result = z.infer<typeof resultSchema>;
type Call = { readonly caller: Caller; readonly scenario: Scenario; readonly baseURL: string; readonly ackURL: string; readonly python: string };
const errorCode = z.object({ error: z.object({ code: z.string() }) });
const choices = z.object({ choices: z.array(z.object({
  delta: z.object({ content: z.string().optional() }).optional(),
  message: z.object({ content: z.string() }).optional(), finish_reason: z.string().nullable(),
})) });

export async function call(input: Call): Promise<Result> {
  const streaming = ["http-stream-terminal", "sse-first", "sse-role", "sse-partial", "success-stream"].includes(input.scenario);
  const result: Result = { failed: false, code: null, status: null, retryHeader: null, content: "", stops: 0 };
  const ack = async () => {
    if (input.scenario === "sse-partial" && result.content) {
      const response = await fetch(input.ackURL);
      if (!response.ok) throw new Error("partial acknowledgment failed");
    }
  };
  const messages = [{ role: "user", content: "synthetic" }] satisfies OpenAI.ChatCompletionMessageParam[];
  if (input.caller === "openai-python") {
    const process = Bun.spawn([input.python, `${import.meta.dir}/python-client.py`, input.baseURL, input.scenario, input.ackURL], { stdout: "pipe", stderr: "pipe" });
    const output = await new Response(process.stdout).text();
    const errors = await new Response(process.stderr).text();
    if (await process.exited !== 0) throw new Error(errors);
    return resultSchema.parse(JSON.parse(output));
  }
  try {
    switch (input.caller) {
      case "direct": {
        const response = await fetch(`${input.baseURL}/chat/completions`, { method: "POST", headers: { authorization: "Bearer synthetic-downstream", "content-type": "application/json" }, body: JSON.stringify({ model: "synthetic-pro", messages, stream: streaming }) });
        result.status = response.status;
        result.retryHeader = response.headers.get("x-should-retry");
        if (!response.ok) {
          result.failed = true;
          const parsed = errorCode.safeParse(await response.json());
          result.code = parsed.success ? parsed.data.error.code : null;
          break;
        }
        if (!streaming) {
          const payload = choices.parse(await response.json());
          result.content = payload.choices[0]?.message?.content ?? "";
          result.stops = Number(payload.choices[0]?.finish_reason === "stop");
          break;
        }
        if (!response.body) throw new Error("missing SSE body");
        const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
        let buffer = "";
        for (;;) {
          const chunk = await reader.read();
          if (chunk.done) break;
          buffer += chunk.value;
          let boundary: number;
          while ((boundary = buffer.indexOf("\n\n")) >= 0) {
            const frame = buffer.slice(0, boundary);
            buffer = buffer.slice(boundary + 2);
            const data = frame.split("\n").filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trim()).join("\n");
            if (!data || data === "[DONE]") continue;
            const payload: unknown = JSON.parse(data);
            const error = errorCode.safeParse(payload);
            if (error.success) {
              result.failed = true;
              result.code = error.data.error.code;
            } else {
              const parsed = choices.parse(payload);
              for (const choice of parsed.choices) {
                result.content += choice.delta?.content ?? "";
                result.stops += Number(choice.finish_reason === "stop");
              }
              await ack();
            }
          }
        }
        break;
      }
      case "openai-node": {
        const client = new OpenAI({ apiKey: "synthetic-downstream", baseURL: input.baseURL });
        if (streaming) {
          const stream = await client.chat.completions.create({ model: "synthetic-pro", messages, stream: true });
          for await (const chunk of stream) {
            for (const choice of chunk.choices) {
              result.content += choice.delta.content ?? "";
              result.stops += Number(choice.finish_reason === "stop");
            }
            await ack();
          }
        } else {
          const response = await client.chat.completions.create({ model: "synthetic-pro", messages });
          result.content = response.choices[0]?.message.content ?? "";
          result.stops = Number(response.choices[0]?.finish_reason === "stop");
        }
        break;
      }
      case "vercel-default":
      case "vercel-policy": {
        const provider = createOpenAICompatible({ name: "chatgpt2api", apiKey: "synthetic-downstream", baseURL: input.baseURL });
        const model = input.caller === "vercel-policy" ? new OpenAICompatibleChatLanguageModel("synthetic-pro", {
          provider: "chatgpt2api.chat", url: ({ path }) => `${input.baseURL}${path}`,
          headers: () => ({ authorization: "Bearer synthetic-downstream" }),
          errorStructure: proTerminalErrorStructure,
        }) : provider.languageModel("synthetic-pro");
        if (streaming) {
          const stream = streamText({ model, prompt: "synthetic" });
          for await (const part of stream.fullStream) {
            if (part.type === "error") {
              if (APICallError.isInstance(part.error) || RetryError.isInstance(part.error)) throw part.error;
              result.failed = true;
              const parsed = proTerminalErrorStructure.errorSchema.safeParse({ error: part.error });
              result.code = parsed.success && typeof parsed.data.error.code === "string" ? parsed.data.error.code : null;
            }
            if (part.type === "text-delta") { result.content += part.text; await ack(); }
            if (part.type === "finish") result.stops += Number(part.finishReason === "stop");
          }
        } else {
          const response = await generateText({ model, prompt: "synthetic" });
          result.content = response.text;
          result.stops = Number(response.finishReason === "stop");
        }
        break;
      }
      default: {
        const unreachable: never = input.caller;
        throw new Error(`unknown caller ${unreachable}`);
      }
    }
  } catch (error: unknown) {
    result.failed = true;
    if (error instanceof OpenAI.APIError) {
      result.code = error.code ?? null;
      result.status = error.status ?? null;
      result.retryHeader = error.headers?.get("x-should-retry") ?? null;
    } else if (APICallError.isInstance(error) || RetryError.isInstance(error)) {
      const cause = RetryError.isInstance(error) ? error.lastError : error;
      if (!APICallError.isInstance(cause)) throw error;
      const parsed = errorCode.safeParse(cause.data);
      result.code = parsed.success ? parsed.data.error.code : null;
      result.status = cause.statusCode ?? null;
      result.retryHeader = cause.responseHeaders?.["x-should-retry"] ?? null;
    } else throw error;
  }
  return result;
}
