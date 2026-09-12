import { mockStatus, PRIVATE_SENTINEL } from './fixtures.ts';
import { startMockServer } from './mock-server.ts';

const mock = startMockServer();
const scenario = process.argv[2] ?? 'healthy';
switch (scenario) {
  case 'healthy':
    break;
  case 'auth-error':
    mock.state.status = 401;
    break;
  case 'upstream-down':
    mock.state.payload = { ...mockStatus, upstream: { healthy: false, error: PRIVATE_SENTINEL } };
    break;
  default:
    await mock.server.stop(true);
    throw new Error('Use healthy, auth-error, or upstream-down');
}
console.log(`MOCK ONLY ${mock.url}`);
for (const signal of ['SIGINT', 'SIGTERM'] as const) {
  process.once(signal, () => { mock.release(); void mock.server.stop(true); });
}
