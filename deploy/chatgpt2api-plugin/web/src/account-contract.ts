import { z } from 'zod';

const sourceID = z.string().regex(/^codex_[a-f0-9]{64}$/).brand<'CodexSourceID'>();
const webID = z.string().regex(/^web_[a-f0-9]{64}$/).brand<'WebAccountID'>();
const timestamp = z.iso.datetime();
const count = z.int().nonnegative().nullish().transform((value) => value ?? null);
const sourceSchema = z.object({
  id: sourceID, label: z.string().min(1).max(160), disabled: z.boolean(), provider: z.literal('codex'),
}).readonly();
const accountSchema = z.object({
  id: webID, label: z.string().min(1).max(160), disabled: z.boolean(),
  status: z.enum(['disabled', 'normal', 'limited', 'abnormal', 'unknown']),
  source_type: z.enum(['web', 'codex', 'oauth_login', 'cpa', 'sub2api']).nullable(),
  type: z.enum(['free', 'Free', 'plus', 'Plus', 'pro', 'Pro', 'prolite', 'ProLite', 'team', 'Team', 'business', 'Business', 'enterprise', 'Enterprise']).optional(),
  tracked_image_remaining: count, observed_image_remaining: count,
  reset_after_seconds: z.number().nonnegative().nullish().transform((value) => value ?? null),
  observed_at: timestamp.nullable(), observation_source: z.enum(['stored_snapshot', 'conversation/init']),
  refresh_error: z.literal('vendor_refresh_failed').nullable(),
}).refine((account) => account.disabled === (account.status === 'disabled')).readonly();
const sourcesSchema = z.object({ sources: z.array(sourceSchema)
  .refine((sources) => new Set(sources.map((source) => source.id)).size === sources.length).readonly() });
const inventorySchema = z.object({
  accounts: z.array(accountSchema).refine((accounts) => new Set(accounts.map((account) => account.id)).size === accounts.length).readonly(),
  observed_at: timestamp, source: z.literal('ChatGPTWeb'),
  capabilities: z.object({ preserve_disabled_accounts: z.boolean() }).readonly().optional(),
}).readonly();
const importSchema = z.object({ status: z.enum(['already_present', 'imported_disabled']), account: accountSchema })
  .refine((result) => result.status !== 'imported_disabled' || result.account.disabled).readonly();
const resultSchema = z.object({ account: accountSchema, observed_at: timestamp, source: z.literal('ChatGPTWeb') }).readonly();

export type CodexSource = z.infer<typeof sourceSchema>;
export type WebAccount = z.infer<typeof accountSchema>;
export type WebAccountID = WebAccount['id'];
export type WebInventory = z.infer<typeof inventorySchema>;
export type ImportResult = z.infer<typeof importSchema>;
export type WebResult = z.infer<typeof resultSchema>;

export class AccountFormatError extends Error {
  constructor() {
    super('계정 응답 또는 선택한 스냅샷을 확인할 수 없습니다. 플러그인 버전을 확인하고 목록을 다시 조회한 뒤 직접 선택하세요.');
    this.name = 'AccountFormatError';
  }
}

function parse<Output>(schema: z.ZodType<Output>, input: unknown): Output {
  const result = schema.safeParse(input);
  if (!result.success) throw new AccountFormatError();
  return result.data;
}
export function parseSources(input: unknown): readonly CodexSource[] { return parse(sourcesSchema, input).sources; }
export function parseWebAccounts(input: unknown): WebInventory { return parse(inventorySchema, input); }
export function parseImport(input: unknown): ImportResult { return parse(importSchema, input); }
export function parseWebResult(input: unknown, selected: string): WebResult {
  const result = parse(resultSchema, input);
  if (result.account.id !== selected) throw new AccountFormatError();
  return result;
}
