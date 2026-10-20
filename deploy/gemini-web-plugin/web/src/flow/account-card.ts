import type { Account, Health } from './contract';
import { badge, element, localTime } from '../dom';

const statuses = {
  ready: { label: '조회 정상', tone: 'success' },
  busy: { label: '사용 중', tone: 'info' },
  expired: { label: '연결 만료', tone: 'warning' },
  error: { label: '조회 오류', tone: 'danger' },
  unknown: { label: '미확인', tone: 'neutral' },
} as const;

const healthStates: Readonly<Record<string, { readonly label: string; readonly tone: string }>> = {
  ok: { label: '생성 정상', tone: 'success' },
  blocked: { label: 'Flow 차단 보류 중', tone: 'danger' },
  failing: { label: '헬스체크 실패', tone: 'warning' },
  unknown: { label: '헬스체크 기록 없음', tone: 'neutral' },
};
const checkKinds: Readonly<Record<string, string>> = { periodic: '정기', hold: '보류 종료' };
const checkResults: Readonly<Record<string, string>> = { passed: '통과', refused: '거절(비정상 활동)', failed: '실패' };

const number = new Intl.NumberFormat('ko-KR');

function healthSection(health: Health): HTMLElement {
  const section = element('section', 'usage-section flow-health');
  section.dataset['health'] = health.status;
  const state = healthStates[health.status] ?? { label: `상태 ${health.status}`, tone: 'neutral' };
  const tags = element('div', 'cluster');
  tags.append(badge(state.label, state.tone));
  section.append(element('h3', 'section-label', '헬스체크'), tags);
  if (health.status === 'blocked' && health.held_until) {
    section.append(element('p', 'caption', `보류 해제 확인 ${localTime(health.held_until)}`));
  }
  if (health.checked_at) {
    const kind = checkKinds[health.check_kind ?? ''] ?? '확인';
    const result = checkResults[health.check_result ?? ''] ?? (health.check_result || '결과 없음');
    const code = health.check_error ? ` (${health.check_error})` : '';
    section.append(element('p', 'caption', `마지막 확인 ${localTime(health.checked_at)} · ${kind} · ${result}${code}`));
  }
  if (health.status !== 'blocked' && health.next_check) {
    section.append(element('p', 'caption', `다음 정기 확인 ${localTime(health.next_check)}`));
  }
  if (health.accepted_at) section.append(element('p', 'caption', `마지막 생성 접수 ${localTime(health.accepted_at)}`));
  return section;
}
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export type CardState = {
  readonly loading: boolean;
  readonly stale: boolean;
  readonly error: string | undefined;
};

export function accountCard(account: Account, state: CardState): HTMLElement {
  const card = element('article', 'card account-card');
  card.dataset['accountId'] = account.id;
  card.setAttribute('aria-label', account.label || account.id);
  card.setAttribute('aria-busy', String(state.loading));
  const header = element('div', 'card-header');
  const title = element('div', '');
  title.append(element('h2', '', account.label || 'Google 계정'), element('code', 'caption', account.id));
  const tags = element('div', 'cluster');
  const status = statuses[state.error && account.status === 'ready' ? 'error' : account.status];
  tags.append(badge(status.label, status.tone));
  if (state.stale) tags.append(badge('이전 조회값', 'warning'));
  if (account.tier !== undefined) tags.append(badge(`Google 등급 ${account.tier}`, 'neutral'));
  if (account.health?.status === 'blocked' || account.health?.status === 'failing') {
    const health = healthStates[account.health.status];
    if (health) tags.append(badge(health.label, health.tone));
  }
  header.append(title, tags);
  card.append(header);

  const metric = element('section', 'usage-section');
  metric.append(element('h3', 'section-label', state.stale ? '이전 조회 크레딧' : '조회 크레딧'));
  const amount = element('strong', 'flow-credit-amount', account.credits === undefined ? '—' : number.format(account.credits));
  amount.dataset['credits'] = account.credits === undefined ? 'unknown' : String(account.credits);
  metric.append(amount);
  if (account.credits === undefined) metric.append(element('p', 'muted', '아직 크레딧을 확인하지 못했습니다.'));
  card.append(metric);
  if (account.health) card.append(healthSection(account.health));
  if (state.error) {
    const warning = element('div', 'notice notice-warning');
    warning.append(element('p', '', state.error));
    warning.setAttribute('role', 'status');
    card.append(warning);
  }
  const footer = element('footer', 'account-footer');
  footer.append(element('p', 'caption', account.observed_at
    ? `조회 시각 ${localTime(account.observed_at)}` : '조회 시각 정보 없음'));
  if (account.project && uuid.test(account.project)) {
    const link = element('a', '', '기본 Flow 프로젝트 열기');
    link.setAttribute('href', `https://flow.google.com/project/${account.project}`);
    link.setAttribute('target', '_blank');
    link.setAttribute('rel', 'noopener noreferrer');
    footer.append(link);
  }
  card.append(footer);
  return card;
}
