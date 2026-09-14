import { z } from 'zod';

type HostRequestInput = { readonly path: string; readonly method: 'GET' | 'POST'; readonly body?: string };
type HostRequest = (input: HostRequestInput) => Promise<unknown>;
declare global { interface Window { readonly __CPAMP_PLUGIN_HOST__?: unknown } }

const capabilitySchema = z.object({
  version: z.literal(1), pluginID: z.literal('gemini-web'), resourceURL: z.url(),
  request: z.custom<HostRequest>((value) => typeof value === 'function'),
});
const responseSchema = z.object({ status: z.number().int().min(200).max(599), body: z.string() });

export function readHostRequest(): HostRequest | undefined {
  try {
    if (window.parent === window || window.parent.location.origin !== window.location.origin) return;
    const parsed = capabilitySchema.safeParse(window.__CPAMP_PLUGIN_HOST__);
    if (!parsed.success || parsed.data.resourceURL !== window.location.href
      || window.location.pathname !== '/v0/resource/plugins/gemini-web/index' || window.location.search || window.location.hash) return;
    return parsed.data.request;
  } catch {
    return;
  }
}

export async function hostResponse(transport: HostRequest, input: HostRequestInput): Promise<Response> {
  try {
    const parsed = responseSchema.safeParse(await transport(input));
    if (parsed.success) return new Response(parsed.data.status === 204 || parsed.data.status === 205 ? null : parsed.data.body,
      { status: parsed.data.status, headers: { 'Content-Type': 'application/json' } });
  } catch {
    return new Response('{"error":"host_request_failed"}', { status: 502 });
  }
  return new Response('{"error":"host_request_failed"}', { status: 502 });
}
