import { z } from 'zod';

const counter = z.int().nonnegative();
const statusSchema = z.object({
  plugin: z.object({
    id: z.literal('chatgpt2api'), version: z.string(), route_count: counter,
  }).readonly(),
  routing: z.object({
    provider: z.literal('chatgpt2api'), mode: z.literal('native-provider'),
    model_names: z.array(z.string()).readonly(),
    credential_source: z.literal('host-auth-manager'),
  }).readonly(),
  upstream: z.object({
    healthy: z.boolean(),
    version: z.string().nullish(),
    accounts: z.object({
      active: counter.nullish(), total: counter.nullish(),
      limited: counter.nullish(), abnormal: counter.nullish(),
    }).readonly().nullish(),
  }).readonly(),
}).readonly();

export type PluginStatus = z.infer<typeof statusSchema>;

export class StatusFormatError extends Error {
  constructor() {
    super('상태 응답 형식을 확인할 수 없습니다. 플러그인 연결을 확인한 뒤 새로고침하세요.');
    this.name = 'StatusFormatError';
  }
}

export function parseStatus(input: unknown): PluginStatus {
  const parsed = statusSchema.safeParse(input);
  if (!parsed.success) throw new StatusFormatError();
  return parsed.data;
}
