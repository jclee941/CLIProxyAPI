import type { Account } from './contract';
import { badge, element, localTime } from '../dom';

const statuses = {
  ready: { label: '조회 정상', tone: 'success' },
  busy: { label: '사용 중', tone: 'info' },
  expired: { label: '연결 만료', tone: 'warning' },
  error: { label: '조회 오류', tone: 'danger' },
  unknown: { label: '미확인', tone: 'neutral' },
} as const;

const number = new Intl.NumberFormat('ko-KR');
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
  header.append(title, tags);
  card.append(header);

  const metric = element('section', 'usage-section');
  metric.append(element('h3', 'section-label', state.stale ? '이전 조회 크레딧' : '조회 크레딧'));
  const amount = element('strong', 'flow-credit-amount', account.credits === undefined ? '—' : number.format(account.credits));
  amount.dataset['credits'] = account.credits === undefined ? 'unknown' : String(account.credits);
  metric.append(amount);
  if (account.credits === undefined) metric.append(element('p', 'muted', '아직 크레딧을 확인하지 못했습니다.'));
  card.append(metric);
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
