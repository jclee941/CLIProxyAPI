import type { Account, AccountId } from './contract';
import { badge, button, element, localTime } from './dom';
import { LoginFlow, type LoginSnapshot } from './login-flow';
import { loginOutcome } from './login-contract';

const phases = {
  idle: ['로그인 준비', 'neutral'], starting: ['로그인 요청 중', 'info'],
  pending: ['확장 프로그램 연결 대기', 'info'], connecting: ['확장 프로그램 응답 대기', 'info'],
  approval: ['Google 세션 선택·승인 대기', 'info'], processing: ['세션 확인·저장 처리 중', 'info'],
  host_sync_pending: ['저장됨 · Manager 동기화 대기', 'warning'], saved: ['저장됨 · 모델 준비 미확인', 'warning'],
  ready: ['사용 가능', 'success'], error: ['로그인 확인 실패', 'danger'],
  cancelled: ['로그인 취소됨', 'neutral'], expired: ['로그인 요청 만료', 'warning'],
} as const;
type PanelEvents = {
  readonly changed: (active: boolean) => void;
  readonly failure: (error: unknown) => void;
  readonly saved: (id: AccountId | undefined) => Promise<Account | undefined>;
};

export function createLoginPanel(events: PanelEvents) {
  const panel = element('section', 'login-panel account-card');
  panel.hidden = true;
  panel.setAttribute('aria-labelledby', 'login-title');
  const header = element('div', 'dialog-header');
  const title = element('h2', '', 'Google 로그인 연결');
  title.id = 'login-title';
  const hide = button('닫기', () => {
    if (!flow.snapshot.canRestart) return;
    panel.hidden = true;
    trigger?.focus();
  });
  header.append(title, hide);
  const description = element('p', 'muted', 'Google 웹 세션을 승인된 companion으로 연결합니다. Google OAuth 토큰 발급이 아니며, 비밀번호와 2단계 인증은 Google 화면에서만 입력하세요.');
  const steps = element('ol', 'login-steps');
  for (const step of [
    '계정 이름을 확인하고 선택한 세션의 서버 저장에 동의합니다.',
    '승인된 companion을 연결한 뒤, 확장 프로그램 팝업에서 Gemini 탭 하나와 계정을 직접 선택합니다.',
    '필요한 Google 로그인을 마치고 팝업에서 승인하면 자동 전송합니다. 모델 준비 결과를 이곳에서 확인하세요.',
  ]) steps.append(element('li', '', step));
  const form = element('form', 'login-form');
  form.autocomplete = 'off';
  const group = element('div', 'form-group');
  const label = element('label', '', '계정 이름');
  label.htmlFor = 'login-label';
  const name = element('input');
  name.id = 'login-label';
  name.name = 'label';
  name.required = true;
  name.autocomplete = 'off';
  name.placeholder = '연결할 계정을 구분할 이름';
  group.append(label, name);
  const consentLabel = element('label', 'consent-row');
  const consent = element('input');
  consent.type = 'checkbox';
  consent.id = 'login-consent';
  consent.required = true;
  consentLabel.htmlFor = consent.id;
  consentLabel.append(consent, element('span', '', '직접 선택·승인한 Google 웹 세션 하나를 이 서버에 저장하고 계정·모델을 확인하는 데 동의합니다.'));
  const start = element('button', 'btn btn-primary', 'Google 로그인 시작');
  start.id = 'login-start';
  start.type = 'submit';
  start.disabled = true;
  form.append(group, consentLabel, start);
  const status = element('div', 'login-status');
  status.id = 'login-status';
  status.tabIndex = -1;
  status.setAttribute('role', 'status');
  status.setAttribute('aria-live', 'polite');
  const detail = element('p', 'muted');
  const expiry = element('p', 'caption');
  const extension = element('p', 'caption');
  const error = element('p', 'notice notice-danger');
  error.id = 'login-error';
  error.setAttribute('role', 'alert');
  error.hidden = true;
  const actions = element('div', 'cluster');
  const connect = button('확장 프로그램 연결', () => flow.connect(), 'primary');
  connect.id = 'login-connect';
  const check = button('상태 확인', () => void flow.action('status'));
  check.id = 'login-check';
  const reconcile = button('Manager 저장 재연결', () => void flow.action('reconcile'));
  reconcile.id = 'login-reconcile';
  const cancel = button('로그인 취소', () => void flow.action('cancel'));
  cancel.id = 'login-cancel';
  const restart = button('새 로그인 준비', () => { flow.reset(); consent.checked = false; render(flow.snapshot); name.focus(); });
  restart.id = 'login-restart';
  actions.append(connect, check, reconcile, cancel, restart);
  panel.append(header, description, steps, form, status, detail, expiry, extension, error, actions,
    element('p', 'caption', '요청은 최대 10분간 유효합니다. 자동 재시도하지 않습니다. 취소해도 이미 서버에 저장된 세션은 삭제되지 않습니다.'));
  let selected: Account | undefined;
  let trigger: HTMLElement | undefined;
  let synced: LoginSnapshot['result'];
  let confirmed: LoginSnapshot['result'];
  const flow = new LoginFlow({ changed: render, failure: events.failure });

  function render(snapshot: LoginSnapshot): void {
    const priorPhase = panel.dataset['phase'];
    const [text, tone] = phases[snapshot.phase];
    const outcome = snapshot.result ? loginOutcome(snapshot.result, selected?.enabled === false) : undefined;
    const ready = snapshot.phase === 'ready' && outcome === 'ready' && confirmed === snapshot.result
      && selected?.id === snapshot.result?.account_id && selected?.enabled === true && selected.status === 'ready' && selected.models.length > 0;
    const disabledSave = snapshot.phase === 'saved' && outcome === 'saved_disabled';
    status.replaceChildren(badge(snapshot.phase === 'ready' && !ready ? '모델 준비 미확인'
      : disabledSave ? 'Saved, disabled' : text, snapshot.phase === 'ready' && !ready ? 'warning' : tone));
    panel.dataset['phase'] = snapshot.phase;
    panel.dataset['registry'] = ready ? 'ready' : 'pending';
    status.setAttribute('aria-busy', String(snapshot.busy));
    detail.textContent = snapshot.phase === 'approval' || snapshot.phase === 'connecting'
      ? 'Chrome 도구 모음의 companion 팝업에서 Gemini 탭과 Google 계정을 확인하고 승인하세요. 팝업이 보이지 않으면 확장 아이콘을 직접 여세요.'
      : snapshot.phase === 'host_sync_pending'
        ? '세션은 저장되었지만 Manager 반영이 완료되지 않았습니다. 설정을 확인한 뒤 저장 재연결을 직접 실행하세요.'
        : disabledSave ? '세션은 저장되었지만 계정은 비활성입니다. 활성화는 Manager에서 별도로 수행하세요.'
          : ready ? 'Manager의 계정별 모델 등록을 확인했습니다. 아래 계정 목록에서 실측 상태를 확인하세요.'
            : snapshot.phase === 'ready' ? '세션은 저장되었지만 Manager 모델 등록은 아직 확인되지 않았습니다. 상태 확인으로 직접 다시 확인하세요.' : '';
    detail.hidden = !detail.textContent;
    error.textContent = snapshot.message;
    error.hidden = !snapshot.message;
    expiry.textContent = snapshot.expiresAt ? `로그인 요청 기한 ${localTime(snapshot.expiresAt)} · 세션 만료 시각이 아닙니다.` : '';
    extension.textContent = snapshot.extensionId ? `서버 승인 companion ID · ${snapshot.extensionId}` : '';
    form.hidden = snapshot.phase !== 'idle' && snapshot.phase !== 'starting';
    if (!panel.hidden && priorPhase !== snapshot.phase && form.hidden) status.focus();
    start.disabled = snapshot.busy || !consent.checked || !name.value.trim();
    name.disabled = snapshot.busy;
    consent.disabled = snapshot.busy;
    hide.disabled = !snapshot.canRestart;
    connect.hidden = !snapshot.canConnect;
    check.hidden = !snapshot.canCheck;
    reconcile.hidden = !snapshot.canReconcile;
    cancel.hidden = !snapshot.canCancel;
    cancel.disabled = snapshot.busy && snapshot.phase !== 'processing';
    restart.hidden = !snapshot.canRestart || snapshot.phase === 'idle';
    events.changed(snapshot.active);
    const identity = snapshot.result?.account_id;
    const result = snapshot.result;
    if (result && !snapshot.busy && (snapshot.phase === 'ready' || snapshot.phase === 'saved') && synced !== result) {
      synced = result;
      confirmed = undefined;
      void events.saved(identity).then((account) => {
        if (synced !== result) return;
        selected = account;
        confirmed = result;
        render(flow.snapshot);
      });
    }
  }
  form.addEventListener('input', () => render(flow.snapshot));
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    if (start.disabled || !consent.checked || !name.value.trim()) return;
    synced = undefined;
    confirmed = undefined;
    void flow.start({ label: name.value.trim(), consent: true, ...(selected ? { existing_id: selected.id } : {}) });
  });
  window.addEventListener('pagehide', () => flow.dispose(), { once: true });
  return {
    element: panel,
    open(account?: Account): void {
      trigger = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
      if (flow.snapshot.canRestart) {
        selected = account;
        synced = undefined;
        confirmed = undefined;
        consent.checked = false;
        name.value = account?.label ?? '';
        flow.reset();
      }
      panel.hidden = false;
      render(flow.snapshot);
      (flow.snapshot.phase === 'idle' ? name : status).focus();
    },
  };
}
