import { z } from 'zod';

export const accountIdSchema = z.string().min(1).brand('AccountId');
export const statusSchema = z.enum(['ready', 'expired', 'error', 'unknown']);
export const metricSchema = z.object({
  remaining_units: z.number().nullable(),
  usage_fraction: z.number().nullable(),
  usage_percent: z.number().optional(),
  reset_unix_seconds: z.number().nullable(),
  window_kind: z.enum(['5h', 'weekly', 'ai_credit', 'unknown']),
  metric_type: z.union([z.string(), z.number()]).nullish(),
  unit: z.literal('provider_compute_unit'),
}).readonly();

export const accountSchema = z.object({
  id: accountIdSchema,
  label: z.string(),
  enabled: z.boolean(),
  status: statusSchema,
  models: z.array(z.object({ id: z.string(), name: z.string() }).readonly()).readonly(),
  usage: z.object({
    tier: z.string().nullable(),
    tier_code: z.union([z.string(), z.number()]).nullable(),
    overage_enabled: z.boolean().nullish(),
    metrics: z.array(metricSchema).readonly().nullable(),
    observed_at: z.number(),
    source: z.literal('GoogleWeb'),
    estimated: z.literal(false),
  }).readonly().nullable(),
  error: z.string().optional(),
  observed_at: z.number(),
}).readonly();

export const accountsResponseSchema = z.object({
  provider: z.literal('gemini-web'),
  accounts: z.array(accountSchema).readonly(),
}).readonly();

export const savedAccountSchema = z.object({ id: accountIdSchema, status: statusSchema });
export const errorResponseSchema = z.object({
  error: z.enum(['account_unavailable', 'sidecar_request_failed', 'disabled_account_update_requires_host_enable']),
});
export type ErrorCode = z.infer<typeof errorResponseSchema>['error'];
export type Account = z.infer<typeof accountSchema>;
export type AccountId = z.infer<typeof accountIdSchema>;
export type Metric = z.infer<typeof metricSchema>;
export type SaveAccount = {
  readonly label: string;
  readonly token: string;
  readonly existing_id?: AccountId;
};

export function isWebToken(token: string): boolean {
  const match = /^gemini-web:v1:([A-Za-z0-9_-]+)$/.exec(token);
  return Boolean(match?.[1] && match[1].length % 4 !== 1);
}
