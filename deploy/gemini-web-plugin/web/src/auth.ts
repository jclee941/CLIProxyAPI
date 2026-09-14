export class HostAuthError extends Error {
  constructor() {
    super('Manager 로그인 정보를 읽을 수 없습니다. 같은 주소의 Manager에서 로그인 유지 설정으로 다시 로그인한 뒤 연결을 확인하세요.');
    this.name = 'HostAuthError';
  }
}

export type HostEnvironment = {
  readonly origin: string;
  readonly host: string;
  readonly userAgent: string;
};

export type HostAuth = {
  readonly managementKey: string;
};

const storedAuthSchema = z.object({
  state: z.object({
    apiBase: z.url(),
    managementKey: z.string().trim().min(1).regex(/^[^\r\n]+$/),
  }),
});

export function decodeStoredAuth(raw: string | null, environment: HostEnvironment): HostAuth {
  if (!raw) throw new HostAuthError();
  try {
    let decoded = raw;
    if (raw.startsWith('enc::v1::')) {
      const binary = atob(raw.slice('enc::v1::'.length));
      const key = new TextEncoder().encode(
        `cli-proxy-api-webui::secure-storage|${environment.host}|${environment.userAgent}`,
      );
      const keyView = new DataView(key.buffer);
      const bytes = Uint8Array.from(binary, (character, index) =>
        character.charCodeAt(0) ^ keyView.getUint8(index % key.length),
      );
      decoded = new TextDecoder().decode(bytes);
    }
    const value: unknown = JSON.parse(decoded);
    const parsed = storedAuthSchema.safeParse(value);
    if (!parsed.success) throw new HostAuthError();
    const base = new URL(parsed.data.state.apiBase);
    if (base.origin !== environment.origin || base.username || base.password) {
      throw new HostAuthError();
    }
    return { managementKey: parsed.data.state.managementKey };
  } catch (error) {
    if (error instanceof Error) throw new HostAuthError();
    throw error;
  }
}

export const operatorKeyStorageKey = 'gemini-web-operator-key';

export function resolveHostAuth(raw: string | null, operatorKey: string | null, environment: HostEnvironment): HostAuth {
  try {
    return decodeStoredAuth(raw, environment);
  } catch (error) {
    if (!(error instanceof HostAuthError)) throw error;
  }
  const candidate = (operatorKey ?? '').trim();
  if (!candidate || !/^[^\r\n]+$/.test(candidate)) throw new HostAuthError();
  return { managementKey: candidate };
}

export function readOperatorKey(): string | null {
  try {
    return window.sessionStorage.getItem(operatorKeyStorageKey);
  } catch {
    return null;
  }
}

export function saveOperatorKey(value: string): void {
  window.sessionStorage.setItem(operatorKeyStorageKey, value.trim());
}

export function readHostAuth(): HostAuth {
  let stored: string | null = null;
  let environment: HostEnvironment = {
    origin: window.location.origin,
    host: window.location.host,
    userAgent: window.navigator.userAgent,
  };
  try {
    const host = window.parent;
    if (host.location.origin === window.location.origin) {
      stored = host.localStorage.getItem('cli-proxy-auth');
      environment = {
        origin: window.location.origin,
        host: host.location.host,
        userAgent: host.navigator.userAgent,
      };
    }
  } catch {
    stored = null;
  }
  return resolveHostAuth(stored, readOperatorKey(), environment);
}
import { z } from 'zod';
