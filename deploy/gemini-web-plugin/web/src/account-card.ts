import type { Account, Metric } from './contract';
import { badge, button, element, icon, localTime } from './dom';

const statuses = {
  ready: { label: '사용 가능', tone: 'success' },
  expired: { label: '토큰 만료', tone: 'warning' },
  error: { label: '오류', tone: 'danger' },
  unknown: { label: '미확인', tone: 'neutral' },
} as const;
const windows = { '5h': '5시간', weekly: '주간', ai_credit: 'AI 크레딧', unknown: '기타 사용량' } as const;
const number = new Intl.NumberFormat('ko-KR', { maximumFractionDigits: 1 });

function metricView(metric: Metric): HTMLElement {
  const section = element('section', 'metric');
  const title = windows[metric.window_kind];
  const fraction = metric.usage_fraction;
  const heading = element('div', 'metric-heading');
  heading.append(element('h3', '', title), element('strong', '',
    fraction === null ? '사용률 미확인' : `${number.format(fraction * 100)}% 사용`));
  section.append(heading);
  if (fraction !== null) {
    const bounded = Math.max(0, Math.min(1, fraction));
    const track = element('div', 'quota-track');
    track.setAttribute('role', 'meter');
    track.setAttribute('aria-label', `${title} 사용률`);
    track.setAttribute('aria-valuemin', '0');
    track.setAttribute('aria-valuemax', '100');
    track.setAttribute('aria-valuenow', String(bounded * 100));
    track.setAttribute('aria-valuetext', `${number.format(fraction * 100)}% 사용`);
    const fill = element('div', `quota-fill ${bounded > 0.7 ? 'quota-low' : bounded > 0.3 ? 'quota-medium' : 'quota-high'}`);
    fill.style.setProperty('--gw-progress', String(bounded));
    track.append(fill);
    section.append(track);
  }
  const meta = element('div', 'metric-meta');
  meta.append(element('span', '', metric.remaining_units === null
    ? '남은 연산 단위 미확인'
    : `남은 연산 단위 ${number.format(metric.remaining_units)}`));
  meta.append(element('span', '', `초기화 ${localTime(metric.reset_unix_seconds)}`));
  section.append(meta);
  return section;
}

export type CardState = {
  readonly pending: string | undefined;
  readonly error: string | undefined;
  readonly locked: boolean;
};
export type CardActions = {
  readonly update: (account: Account) => void;
  readonly refresh: (account: Account) => void;
};

export function accountCard(account: Account, state: CardState, actions: CardActions): HTMLElement {
  const card = element('article', 'card account-card');
  card.dataset['accountId'] = account.id;
  card.setAttribute('aria-label', account.label);
  card.setAttribute('aria-busy', String(Boolean(state.pending)));
  const header = element('div', 'card-header');
  const title = element('h2', '', account.label);
  const tags = element('div', 'cluster');
  const status = statuses[account.status];
  tags.append(badge(state.error ? '확인 실패' : status.label, state.error ? 'warning' : status.tone));
  if (!account.enabled) tags.append(badge('비활성'));
  if (account.usage?.tier) tags.append(badge(/^pro$/i.test(account.usage.tier) ? 'Pro' : account.usage.tier, 'info'));
  header.append(title, tags);
  card.append(header);

  const modelSection = element('section', 'model-section');
  modelSection.append(element('h3', 'section-label', '사용 가능 모델'));
  const models = element('div', 'cluster models');
  if (account.status === 'ready' && account.enabled && !state.error && account.models.length) {
    for (const model of account.models) {
      const tag = badge(model.id, 'model');
      tag.title = model.name;
      models.append(tag);
    }
  } else {
    models.append(element('p', 'muted', '확인된 사용 가능 모델이 없습니다.'));
  }
  modelSection.append(models);
  card.append(modelSection);

  if (account.status === 'expired') {
    card.append(element('p', 'notice notice-warning', '웹 토큰이 만료되었습니다. 새 토큰으로 업데이트해 주세요.'));
  }
  if (state.error) {
    const warning = element('div', 'notice notice-warning');
    warning.append(element('strong', '', '새로고침 실패 · 이전 관측값'), element('p', '', state.error));
    warning.setAttribute('role', 'status');
    card.append(warning);
  } else if (account.error) {
    card.append(element('p', 'notice notice-warning', account.error));
  }

  const usage = element('section', 'usage-section');
  const usageHeading = element('div', 'usage-heading');
  usageHeading.append(element('h3', 'section-label', 'Google 사용량'));
  if (account.usage) usageHeading.append(element('span', 'source-label', 'GoogleWeb · 실측'));
  usage.append(usageHeading);
  if (account.usage?.metrics?.length) {
    for (const metric of account.usage.metrics) usage.append(metricView(metric));
  } else {
    usage.append(element('p', 'usage-empty', '사용량을 확인하지 못했습니다. 추정값은 표시하지 않습니다.'));
  }
  if (account.usage?.overage_enabled) {
    usage.append(element('p', 'caption', 'Google 초과 사용이 활성화되어 있습니다.'));
  }
  if (account.usage) usage.append(element('p', 'caption', `사용량 관측 ${localTime(account.usage.observed_at)}`));
  card.append(usage);

  const footer = element('footer', 'account-footer');
  footer.append(element('p', 'caption', `계정 확인 ${localTime(account.observed_at)}`));
  const controls = element('div', 'cluster card-actions');
  const update = button('토큰 업데이트', () => actions.update(account));
  update.id = `update-${account.id}`;
  update.setAttribute('aria-label', `${account.label} 토큰 업데이트`);
  const refresh = button(state.pending ?? '새로고침', () => actions.refresh(account));
  refresh.id = `refresh-${account.id}`;
  refresh.setAttribute('aria-label', `${account.label} 새로고침`);
  refresh.prepend(icon('refresh'));
  if (state.pending === '확인 중') refresh.classList.add('is-loading');
  update.disabled = state.locked;
  refresh.disabled = state.locked;
  controls.append(update, refresh);
  footer.append(controls);
  card.append(footer);
  return card;
}
