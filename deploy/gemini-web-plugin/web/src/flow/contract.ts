import { z } from 'zod';
import { accountIdSchema } from '../contract';

export const accountSchema = z.object({
  id: accountIdSchema,
  label: z.string().optional(),
  credits: z.number().int().nonnegative().optional(),
  project: z.string().optional(),
  error: z.string().optional(),
  status: z.enum(['ready', 'busy', 'expired', 'error', 'unknown']).default('unknown'),
  tier: z.number().int().positive().optional(),
  observed_at: z.number().int().nonnegative().optional(),
}).readonly();

export type Account = z.infer<typeof accountSchema>;
export type Model = { readonly id: string; readonly name: string };

export const accountsResponseSchema = z.object({
  provider: z.literal('flow2api'),
  captcha: z.string().optional(),
  accounts: z.array(accountSchema).readonly(),
  models: z.array(z.object({
    ID: z.string(),
    DisplayName: z.string().optional(),
  }).readonly()).readonly().default([]),
}).readonly();

export type AccountsResponse = z.infer<typeof accountsResponseSchema>;
