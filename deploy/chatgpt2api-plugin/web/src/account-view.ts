import { badge, button, element } from '../../../gemini-web-plugin/web/src/dom.ts';
import type { CodexSource, WebAccount } from './account-contract.ts';
import { detail } from './panel.ts';

const number = new Intl.NumberFormat('ko-KR', { maximumFractionDigits: 3 });
const statuses = { disabled: ['비활성', 'neutral'], normal: ['정상 보고', 'neutral'], limited: ['제한 보고', 'warning'], abnormal: ['이상 보고', 'danger'], unknown: ['상태 미확인', 'warning'] } as const;
export function accountTime(value: string | null): string {
  return value === null ? '미확인' : new Intl.DateTimeFormat('ko-KR', { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value));
}
export function sourceRow(source: CodexSource, state: { readonly blocked: boolean; readonly busy: boolean }, select: () => void): HTMLElement {
  const row = element('li', 'source-row'); row.dataset['sourceId'] = source.id;
  const title = element('div', 'account-controls');
  title.append(element('strong', '', source.label), badge(source.disabled ? 'Codex 비활성' : 'Codex 활성', source.disabled ? 'warning' : 'neutral'));
  const action = button('Web으로 가져오기', () => { if (!state.busy) select(); });
  action.id = `import-${source.id}`; action.setAttribute('aria-label', `${source.label} Web으로 가져오기`);
  action.disabled = state.blocked; action.setAttribute('aria-disabled', String(state.busy || state.blocked));
  row.append(title, action); return row;
}
export type AccountViewState = { readonly busy: boolean; readonly blocked: boolean; readonly error: string | undefined; readonly pending: boolean };
export function webAccountCard(account: WebAccount, state: AccountViewState, actions: { readonly refresh: () => void; readonly toggle: () => void }): HTMLElement {
  const card = element('article', 'resource-card web-account');
  card.dataset['webId'] = account.id; card.dataset['disabled'] = String(account.disabled);
  card.dataset['stale'] = String(Boolean(state.error || account.refresh_error));
  card.setAttribute('aria-label', account.label); card.setAttribute('aria-busy', String(state.pending));
  const header = element('div', 'page-heading');
  const [text, tone] = statuses[account.status];
  const tags = element('div', 'account-controls'); tags.append(badge(text, tone));
  if (account.type) tags.append(badge(`보고 등급 · ${account.type}`));
  header.append(element('h3', '', account.label), tags); card.append(header);
  if (state.error || account.refresh_error) card.append(element('p', 'account-notice', `확인 실패 · 이전 관측값은 최신이 아닙니다. ${state.error ?? 'Web 서비스에서 갱신 실패를 보고했습니다.'}`));
  const metrics = element('dl', 'details');
  const observed = detail('Web 이미지 관측 잔여', account.observed_image_remaining === null ? '미확인' : `${number.format(account.observed_image_remaining)}개`);
  observed.dataset['metric'] = 'observed';
  const tracked = detail('저장된 이미지 추적 잔여', account.tracked_image_remaining === null ? '미확인' : `${number.format(account.tracked_image_remaining)}개`);
  tracked.dataset['metric'] = 'tracked';
  metrics.append(observed, tracked,
    detail('이미지 초기화까지', account.reset_after_seconds === null ? '미확인' : `응답 시점 기준 ${number.format(account.reset_after_seconds)}초`),
    detail('관측 출처', account.observation_source === 'conversation/init' ? 'ChatGPTWeb · conversation/init' : 'ChatGPTWeb · 저장 스냅샷'),
    detail('할당량 관측 시각', accountTime(account.observed_at)),
    detail('인증 유입 출처', account.source_type ?? '미확인'));
  card.append(metrics, element('p', 'caption', '관측 잔여는 conversation/init의 마지막 이미지 값입니다. 저장 추적값은 서비스가 자체 계산한 값으로 실제 할당량이 아닙니다. 저장 스냅샷의 측정 시각은 알 수 없습니다.'),
    element('p', 'caption', '초기화는 응답 시점의 상대 초이며 현재 남은 시간이나 확정 시각이 아닙니다. Codex 사용량·주간 한도와 별개입니다.'));
  const controls = element('div', 'account-controls');
  const refresh = button(state.pending ? 'Web 작업 확인 중' : 'Web 사용량 갱신', () => { if (!state.busy) actions.refresh(); });
  refresh.id = `web-refresh-${account.id}`; refresh.setAttribute('aria-label', `${account.label} Web 사용량 갱신`);
  const toggleLabel = account.status === 'unknown' ? 'Web 상태 변경' : account.disabled ? 'Web 활성화' : 'Web 비활성화';
  const toggle = button(toggleLabel, () => { if (!state.busy) actions.toggle(); });
  toggle.id = `web-toggle-${account.id}`; toggle.setAttribute('aria-label', `${account.label} ${toggleLabel}`);
  for (const control of [refresh, toggle]) { control.disabled = state.blocked || account.status === 'unknown'; control.setAttribute('aria-disabled', String(state.busy || control.disabled)); }
  controls.append(refresh, toggle); card.append(controls, element('p', 'caption', '상태 변경은 Web 대상만 제어합니다. 정상 보고는 실제 추론 성공을 보장하지 않습니다.'));
  return card;
}
