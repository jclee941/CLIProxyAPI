export const MOCK_KEY = 'MOCK_MANAGER_CONTEXT';
export const PRIVATE_SENTINEL = 'MOCK_PRIVATE_RESPONSE_DO_NOT_RENDER';
export const mockStatus = {
  plugin: { id: 'chatgpt2api', version: '1.0.0', route_count: 12 },
  routing: {
    provider: 'chatgpt2api', mode: 'native-provider',
    model_names: ['mock-chat-model', 'mock-reasoning-model'],
    credential_source: 'host-auth-manager',
  },
  upstream: { healthy: true, version: 'MOCK-2.4', accounts: { active: 3, total: 4 } },
} as const;
