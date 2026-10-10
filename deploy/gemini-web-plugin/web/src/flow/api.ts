import { readHostRequest } from '../host-request';
import { request, PluginApiError, readJson } from '../api';
import { accountsResponseSchema, type Account, type Model } from './contract';

export async function listAccounts(): Promise<{ readonly accounts: readonly Account[]; readonly models: readonly Model[] }> {
  const transport = readHostRequest('flow2api') ?? null;
  const response = await request('accounts', undefined, transport, 'flow2api');
  const parsed = accountsResponseSchema.safeParse(await readJson(response));
  if (!parsed.success) throw new PluginApiError(0, undefined, 'flow2api');
  return { accounts: parsed.data.accounts, models: parsed.data.models.map(model => ({ id: model.ID, name: model.DisplayName || model.ID })) };
}
