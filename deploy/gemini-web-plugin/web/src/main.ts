import { accountCard } from './account-card';
import { listAccounts, PluginApiError, refreshAccount, saveAccount, userMessage } from './api';
import { HostAuthError } from './auth';
import type { Account, AccountId, SaveAccount } from './contract';
import { badge, button, element, icon } from './dom';
import { openTokenDialog } from './token-dialog';

const page = element('main', 'page');
const heading = element('header', 'page-heading');
const headingText = element('div', 'heading-copy');
headingText.append(element('h1', '', 'Gemini Web'), element('p', 'muted', '계정별 웹 토큰, 사용 가능 모델과 Google 사용량을 관리합니다.'));
const toolbar = element('div', 'cluster toolbar');
const refreshAll = button('전체 새로고침', () => void refresh(accounts.map((account) => account.id)));
refreshAll.id = 'refresh-all';
refreshAll.prepend(icon('refresh'));
const add = button('계정 등록', () => openTokenDialog(undefined, save), 'primary');
add.id = 'add-account';
add.prepend(icon('plus'));
toolbar.append(refreshAll, add);
heading.append(headingText, toolbar);
const summary = element('div', 'summary cluster');
const policy = element('p', 'policy-note', '사용량은 GoogleWeb에서 확인한 값만 표시합니다. 자동 조회하지 않으며, 새로고침은 로그인이나 토큰 갱신을 수행하지 않습니다.');
const notice = element('p', 'feedback');
notice.setAttribute('role', 'status');
notice.setAttribute('aria-live', 'polite');
const statePanel = element('div', 'state-panel');
const grid = element('div', 'account-grid');
page.append(heading, summary, policy, notice, statePanel, grid);
document.getElementById('app')?.replaceChildren(page);

let accounts: readonly Account[] = [];
let loading = true;
let refreshing = false;
let saving = false;
let loadError = '';
let authBlocked = false;
const pending = new Map<AccountId, string>();
const errors = new Map<AccountId, string>();

function render(): void {
  const focusedId = document.activeElement instanceof HTMLElement ? document.activeElement.id : '';
  const locked = loading || refreshing || saving || authBlocked;
  add.disabled = locked || Boolean(loadError);
  refreshAll.disabled = locked || !accounts.length;
  summary.replaceChildren(badge(`${accounts.length}개 계정`), badge(
    `사용 가능 ${accounts.filter((account) => account.enabled && account.status === 'ready' && !errors.has(account.id) && !loadError).length}`, 'success',
  ));
  summary.hidden = loading && !accounts.length || Boolean(loadError);
  if (refreshing) summary.append(badge(`확인 대기 ${pending.size}`, 'info'));
  statePanel.replaceChildren();
  statePanel.hidden = true;
  if (loadError) {
    statePanel.hidden = false;
    statePanel.append(element('h2', '', authBlocked ? 'Manager 연결이 필요합니다' : '계정 목록을 불러오지 못했습니다'), element('p', 'muted', loadError));
    const retry = button('연결 다시 확인', () => void load());
    retry.disabled = loading || refreshing || saving;
    statePanel.append(retry);
  } else if (loading && !accounts.length) {
    statePanel.hidden = false;
    statePanel.append(element('p', 'muted', '계정 목록을 불러오는 중입니다.'));
  } else if (!accounts.length) {
    statePanel.hidden = false;
    statePanel.append(element('h2', '', '등록된 계정이 없습니다'), element('p', 'muted', '계정 등록에서 웹 토큰을 추가해 주세요. 등록된 계정만 이곳에 표시됩니다.'));
  }
  grid.replaceChildren(...accounts.map((account) => accountCard(account, {
    pending: pending.get(account.id), error: errors.get(account.id) || loadError || undefined, locked,
  }, {
    update: (selected) => openTokenDialog(selected, save),
    refresh: (selected) => void refresh([selected.id]),
  })));
  if (focusedId && !document.querySelector('dialog[open]')) document.getElementById(focusedId)?.focus();
}

function isAuthError(error: unknown): boolean {
  return error instanceof HostAuthError || error instanceof PluginApiError && error.requiresHostLogin;
}

async function load(): Promise<void> {
  loading = true;
  render();
  try {
    accounts = await listAccounts();
    loadError = '';
    authBlocked = false;
  } catch (error) {
    if (!(error instanceof Error)) throw error;
    loadError = userMessage(error);
    authBlocked = isAuthError(error);
    if (authBlocked) accounts = [];
  } finally {
    loading = false;
    render();
  }
}

async function save(payload: SaveAccount): Promise<void> {
  saving = true;
  notice.textContent = '토큰을 서버에서 확인하고 있습니다.';
  render();
  try {
    const result = await saveAccount(payload);
    errors.delete(result.id);
    notice.textContent = result.status === 'ready'
      ? '토큰이 등록되었습니다. 계정 목록에서 확인 결과를 확인하세요.'
      : '등록 요청이 처리되었습니다. 계정 상태를 확인해 주세요.';
    await load();
  } catch (error) {
    if (!(error instanceof Error)) throw error;
    notice.textContent = userMessage(error);
    if (isAuthError(error)) { authBlocked = true; loadError = userMessage(error); accounts = []; }
    throw error;
  } finally {
    saving = false;
    render();
  }
}

async function refresh(ids: readonly AccountId[]): Promise<void> {
  if (refreshing || loading || saving || authBlocked) return;
  refreshing = true;
  const queue = [...ids];
  for (const id of ids) { pending.set(id, '대기 중'); errors.delete(id); }
  notice.textContent = '최대 2개 계정을 동시에 확인합니다.';
  render();
  const worker = async () => {
    while (queue.length && !authBlocked) {
      const id = queue.shift();
      if (id === undefined) break;
      pending.set(id, '확인 중');
      render();
      try {
        const refreshed = await refreshAccount(id);
        accounts = accounts.map((account) => account.id === id ? refreshed : account);
      } catch (error) {
        if (!(error instanceof Error)) throw error;
        errors.set(id, userMessage(error));
        if (isAuthError(error)) { authBlocked = true; loadError = userMessage(error); accounts = []; }
      } finally {
        pending.delete(id);
        render();
      }
    }
  };
  try {
    await Promise.all([worker(), worker()]);
    const failed = ids.filter((id) => errors.has(id)).length;
    notice.textContent = authBlocked ? 'Manager 인증을 다시 확인해 주세요.'
      : `${ids.length}개 계정 확인 요청 완료${failed ? ` · ${failed}개 조회 실패` : ''}.`;
  } finally {
    pending.clear();
    refreshing = false;
    render();
  }
}

void load();
