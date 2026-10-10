import { accountCard } from './account-card';
import { listAccounts } from './api';
import { PluginApiError, userMessage } from '../api';
import { HostAuthError, saveOperatorKey } from '../auth';
import { readHostRequest } from '../host-request';
import type { Account, Model } from './contract';
import type { AccountId } from '../contract';
import { badge, button, element, icon } from '../dom';
import { accountIsCurrent, accountMessage, reconcileAccounts } from './state';

const page = element('main', 'page');
const heading = element('header', 'page-heading');
const headingText = element('div', 'heading-copy');
headingText.append(element('h1', '', 'Google Flow'), element('p', 'muted', '계정별 크레딧과 연결 상태, 지원 모델을 확인합니다.'));
const toolbar = element('div', 'cluster toolbar');
const refreshAll = button('새로고침', () => void load(), 'primary');
refreshAll.id = 'refresh-all';
refreshAll.prepend(icon('refresh'));
toolbar.append(refreshAll);
heading.append(headingText, toolbar);
const summary = element('div', 'summary cluster');
const policy = element('p', 'policy-note', '조회 시점의 Google Flow 잔액입니다. 자동 조회하거나 생성을 시작하지 않습니다.');
const notice = element('p', 'feedback');
notice.setAttribute('role', 'status');
notice.setAttribute('aria-live', 'polite');
const statePanel = element('div', 'state-panel');
const grid = element('div', 'account-grid');
const catalogue = element('section', 'card model-section');
page.append(heading, summary, policy, notice, statePanel, grid, catalogue);
document.getElementById('app')?.replaceChildren(page);

let accounts: readonly Account[] = [];
let models: readonly Model[] = [];
let stale: ReadonlySet<AccountId> = new Set();
let loading = false;
let loadError = '';
let authBlocked = false;
let missingHostContext = false;
const number = new Intl.NumberFormat('ko-KR');

function render(): void {
  const focusedId = document.activeElement instanceof HTMLElement ? document.activeElement.id : '';
  refreshAll.disabled = loading;
  refreshAll.classList.toggle('is-loading', loading);
  refreshAll.setAttribute('aria-busy', String(loading));
  const current = loadError ? [] : accounts.filter(account => accountIsCurrent(account) && !stale.has(account.id));
  const subtotal = current.reduce((total, account) => total + (account.credits ?? 0), 0);
  summary.replaceChildren(badge(`${accounts.length}개 계정`), badge(`조회 정상 ${current.length}`, 'success'),
    badge(`확인된 잔액 ${current.length ? number.format(subtotal) : '—'}`, 'neutral'));
  if (accounts.length > current.length) summary.append(badge(`미확인 ${accounts.length - current.length}`, 'warning'));
  summary.hidden = loading && !accounts.length || authBlocked;
  statePanel.replaceChildren();
  statePanel.hidden = false;
  if (loadError) {
    statePanel.append(element('h2', '', authBlocked ? 'Manager 연결이 필요합니다' : '조회하지 못했습니다'),
      element('p', 'muted', loadError));
    const retry = button('연결 다시 확인', () => void load());
    retry.disabled = loading;
    statePanel.append(retry);
    if (authBlocked) {
      const keyInput = document.createElement('input');
      keyInput.id = 'flow-operator-key';
      keyInput.type = 'password';
      keyInput.autocomplete = 'off';
      keyInput.spellcheck = false;
      const label = element('label', 'field-label', 'Manager Admin Key');
      label.setAttribute('for', keyInput.id);
      const connect = button('키로 연결', () => {
        const value = keyInput.value.trim();
        if (!value) return;
        saveOperatorKey(value);
        keyInput.value = '';
        void load();
      });
      connect.disabled = loading;
      statePanel.append(label, keyInput, connect);
    }
  } else if (loading && !accounts.length) {
    statePanel.append(element('p', 'muted', '크레딧과 계정 상태를 확인하고 있습니다.'));
  } else if (!accounts.length) {
    statePanel.append(element('h2', '', '연결된 Flow 계정이 없습니다'),
      element('p', 'muted', '플러그인 설정에서 기존 Gemini 계정을 연결한 뒤 새로고침하세요.'));
  } else {
    statePanel.hidden = true;
  }
  grid.replaceChildren(...accounts.map(account => accountCard(account, {
    loading, stale: stale.has(account.id) || Boolean(loadError) && account.credits !== undefined,
    error: loadError || accountMessage(account) || undefined,
  })));
  catalogue.replaceChildren(element('h2', '', '지원 모델'),
    element('p', 'muted', '플러그인 카탈로그입니다. 실제 호출 가능 여부는 계정 상태와 Google Flow 제한에 따라 달라집니다.'));
  const list = element('div', 'cluster models');
  for (const model of models) {
    const tag = badge(model.id, 'model');
    tag.title = model.name;
    list.append(tag);
  }
  catalogue.append(list);
  catalogue.hidden = models.length === 0 || authBlocked;
  if (focusedId) document.getElementById(focusedId)?.focus();
}

async function load(): Promise<void> {
  if (loading) return;
  loading = true;
  notice.textContent = '조회 중입니다.';
  render();
  try {
    const result = await listAccounts();
    const merged = reconcileAccounts(accounts, result.accounts);
    accounts = merged.accounts;
    stale = merged.stale;
    models = result.models;
    loadError = '';
    authBlocked = false;
    missingHostContext = false;
    const confirmed = result.accounts.filter(accountIsCurrent).length;
    notice.textContent = `${result.accounts.length}개 계정 중 ${confirmed}개 조회 완료`;
  } catch (error) {
    if (!(error instanceof Error)) throw error;
    loadError = userMessage(error);
    authBlocked = error instanceof HostAuthError || error instanceof PluginApiError && error.requiresHostLogin;
    missingHostContext = error instanceof HostAuthError;
    notice.textContent = '조회하지 못했습니다.';
    if (authBlocked) { accounts = []; models = []; stale = new Set(); }
  } finally {
    loading = false;
    render();
  }
}

const initialLoad = load();
let retriedHostContext = false;
window.addEventListener('cpamp-plugin-request-ready', () => {
  void initialLoad.then(() => {
    if (!retriedHostContext && missingHostContext && readHostRequest('flow2api')) {
      retriedHostContext = true;
      void load();
    }
  });
});
