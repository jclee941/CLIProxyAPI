import { badge, button, element } from '../../../gemini-web-plugin/web/src/dom.ts';
import type { CodexSource, WebAccount, WebAccountID, WebInventory } from './account-contract.ts';
import { AccountFormatError } from './account-contract.ts';
import { accountMessage, AccountRequestError, importCodex, loadSources, loadWebAccounts, refreshWeb, setWebEnabled } from './account-api.ts';
import { capabilityMessage, importRequest, toggleRequest } from './account-actions.ts';
import { confirmAccount } from './account-confirmation.ts';
import { accountTime, sourceRow, webAccountCard } from './account-view.ts';
import { HostAuthError } from './host-auth.ts';
import { readHostRequest } from './host-request.ts';

export function createAccountsPanel(): HTMLElement {
  let sources: readonly CodexSource[] | undefined;
  let inventory: WebInventory | undefined;
  let sourceError = '';
  let inventoryError = '';
  let busy = false;
  let missingHost = false;
  let pending: AbortController | undefined;
  let selectedID: WebAccountID | undefined;
  const errors = new Map<WebAccountID, string>();
  const locked = new Set<WebAccountID>();
  const section = element('section', 'account-area'); section.id = 'account-area';
  section.setAttribute('aria-labelledby', 'account-area-title');
  const header = element('div', 'page-heading');
  const title = element('h2', '', 'Codex 소스 · ChatGPT Web 대상'); title.id = 'account-area-title';
  const reload = button('목록 다시 조회', () => void load()); reload.id = 'reload-accounts';
  header.append(title, reload);
  const notice = element('div', 'account-feedback'); notice.id = 'account-feedback'; notice.setAttribute('aria-live', 'polite'); notice.setAttribute('aria-atomic', 'true');
  const content = element('div', 'account-content');
  section.append(header, element('p', 'muted', '소스와 대상은 독립적입니다. 가져오기는 비활성 Web 대상을 만들고, 활성화는 별도 확인 후 수행합니다. 목록 조회는 Web 사용량을 갱신하지 않습니다.'), notice, content);
  const capability = () => inventoryError ? undefined : inventory?.capabilities?.preserve_disabled_accounts;
  function feedback(message: string, tone = 'neutral'): void { notice.replaceChildren(badge(tone === 'danger' ? '작업 확인 필요' : 'Web 계정', tone), element('p', '', message)); }
  function render(): void {
    const focusID = document.activeElement instanceof HTMLElement ? document.activeElement.id : '';
    section.setAttribute('aria-busy', String(busy)); reload.setAttribute('aria-disabled', String(busy));
    const sourceSection = element('section', 'resource-card');
    sourceSection.append(element('h3', '', 'Codex 물리 소스'));
    if (sourceError) sourceSection.append(element('p', 'account-notice', sourceError));
    if (sources) {
      const list = element('ul', 'source-list');
      for (const source of sources) list.append(sourceRow(source, { busy, blocked: capability() !== true || Boolean(sourceError) }, () => {
        confirmAccount({ kind: 'import', source }, (approval) => {
          void perform(async (signal) => {
            const result = await importCodex(importRequest(source, { ...approval, capability: capability() }), signal);
            updateAccount(result.account);
            feedback(result.status === 'imported_disabled' ? '가져오기 완료 · Web 대상은 비활성입니다. Codex 원본은 그대로입니다. 활성화는 별도의 명시적 작업입니다.'
              : '이미 존재하는 Web 대상입니다. 현재 상태와 메타데이터를 변경하지 않았습니다. Codex 원본도 그대로입니다.');
          });
        });
      }));
      sourceSection.append(list);
      if (!sources.length) sourceSection.append(element('p', 'muted', '가져올 수 있는 Codex 물리 소스가 없습니다. 런타임 전용 인증은 표시하지 않습니다.'));
    }
    sourceSection.append(element('p', 'caption', '선택한 access 스냅샷만 서버에서 복사합니다. 원본 인증 갱신·저장·활성화는 수행하지 않습니다.'));
    const webSection = element('section', 'account-content');
    webSection.append(element('h3', '', '독립 Web 계정 · 이미지 사용량'));
    if (inventoryError) webSection.append(element('p', 'account-notice', `${inventory ? '이전 목록 · 최신 상태 미확인. ' : ''}${inventoryError}`));
    if (capability() !== true) webSection.append(element('p', 'account-notice', capabilityMessage));
    if (inventory) {
      webSection.append(element('p', 'caption', `목록 수신 ${accountTime(inventory.observed_at)} · ChatGPTWeb · 할당량 관측 시각이 아닙니다.`));
      if (!inventory.accounts.length) webSection.append(element('p', 'muted', '조회된 Web 대상이 없습니다. 소스를 직접 선택하고 동의해야 가져옵니다.'));
      for (const account of inventory.accounts) webSection.append(webAccountCard(account, {
        busy, blocked: capability() !== true || locked.has(account.id), error: inventoryError || errors.get(account.id), pending: selectedID === account.id,
      }, {
        refresh: () => void perform(async (signal) => {
          const result = await refreshWeb(account.id, signal);
          if (result.account.refresh_error) throw new AccountRequestError(200, result.account.refresh_error);
          if (result.account.observation_source !== 'conversation/init' || result.account.observed_at === null) throw new AccountFormatError();
          updateAccount(result.account); errors.delete(account.id);
          feedback('선택한 Web 대상의 사용량만 갱신했습니다. Codex 소스와 Web 활성·비활성 상태를 자동 변경하지 않습니다.');
        }, account.id),
        toggle: () => confirmAccount({ kind: 'toggle', account, enabled: account.disabled }, (approval) => {
          void perform(async (signal) => {
            const result = await setWebEnabled(toggleRequest(account, { consent: approval.consent, enabled: account.disabled, capability: capability() }), signal);
            updateAccount(result.account);
            feedback(`선택한 Web 대상만 ${result.account.disabled ? '비활성화' : '활성화'}했습니다. Codex 원본은 그대로이며, 할당량을 갱신한 결과가 아닙니다.`);
          }, account.id);
        }),
      }));
    }
    content.replaceChildren(sourceSection, webSection);
    if (focusID) document.getElementById(focusID)?.focus({ preventScroll: true });
  }
  function updateAccount(account: WebAccount): void {
    if (!inventory) return;
    const exists = inventory.accounts.some((entry) => entry.id === account.id);
    inventory = { ...inventory, accounts: exists ? inventory.accounts.map((entry) => entry.id === account.id ? account : entry) : [...inventory.accounts, account] };
  }
  async function perform(action: (signal: AbortSignal) => Promise<void>, id?: WebAccountID): Promise<void> {
    if (busy || capability() !== true) return;
    busy = true; selectedID = id; pending = new AbortController();
    const request = pending;
    feedback('선택한 작업을 확인하고 있습니다. 자동 재시도하거나 다른 대상을 변경하지 않습니다.'); render();
    try { await action(request.signal); request.signal.throwIfAborted(); }
    catch (error) {
      if (!(error instanceof Error)) throw error;
      const message = accountMessage(error); feedback(message, 'danger');
      if (id) { errors.set(id, message); if (!(error instanceof AccountRequestError) || error.code !== 'vendor_refresh_failed') locked.add(id); }
      else { sourceError = message; }
      if (error instanceof AccountRequestError && ['host_request_failed', 'vendor_disabled_import_unsupported', 'vendor_disabled_refresh_unsupported', 'vendor_web_enabled_unsupported'].includes(error.code ?? '')) inventoryError = message;
    } finally { pending = undefined; selectedID = undefined; busy = false; render(); }
  }
  async function load(): Promise<void> {
    if (busy) return;
    busy = true; pending = new AbortController(); const request = pending;
    feedback('안전한 소스와 Web 저장 목록을 조회하고 있습니다. 실제 사용량 갱신은 선택한 대상에서만 실행됩니다.'); render();
    const [sourceResult, webResult] = await Promise.allSettled([loadSources(request.signal), loadWebAccounts(request.signal)]);
    missingHost = false;
    if (sourceResult.status === 'fulfilled') { sources = sourceResult.value; sourceError = ''; }
    else { sourceError = accountMessage(sourceResult.reason); missingHost = sourceResult.reason instanceof HostAuthError; }
    if (webResult.status === 'fulfilled') { inventory = webResult.value; inventoryError = ''; errors.clear(); locked.clear(); }
    else { inventoryError = accountMessage(webResult.reason); missingHost ||= webResult.reason instanceof HostAuthError; }
    pending = undefined; busy = false;
    feedback(sourceError || inventoryError || (capability() === true ? '목록을 조회했습니다. 새로 가져온 Web 대상은 항상 비활성이며 실제 사용 가능 여부는 미확인입니다.' : capabilityMessage), sourceError || inventoryError ? 'danger' : 'neutral'); render();
  }
  window.addEventListener('pagehide', () => pending?.abort());
  const initialLoad = load(); let retried = false;
  window.addEventListener('cpamp-plugin-request-ready', () => {
    void initialLoad.then(() => { if (!retried && missingHost && readHostRequest()) { retried = true; void load(); } });
  });
  return section;
}
