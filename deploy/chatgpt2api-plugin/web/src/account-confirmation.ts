import { button, element } from '../../../gemini-web-plugin/web/src/dom.ts';
import type { CodexSource, WebAccount } from './account-contract.ts';

type Confirmation = { readonly kind: 'import'; readonly source: CodexSource }
  | { readonly kind: 'toggle'; readonly account: WebAccount; readonly enabled: boolean };
export type Approval = { readonly consent: boolean; readonly allowDisabled: boolean };
function description(action: Confirmation) {
  switch (action.kind) {
    case 'import': return {
      title: 'Codex에서 Web으로 가져오기', label: action.source.label, disabledSource: action.source.disabled,
      text: '선택한 Codex access 스냅샷 하나만 서버에서 Web 대상에 저장합니다. ID·refresh 토큰은 복사하지 않습니다. 새 Web 대상은 항상 비활성이며, 이미 존재하면 현재 상태를 그대로 둡니다. Codex 파일과 활성·비활성 상태는 바꾸지 않습니다.',
      consent: '이 access 스냅샷의 Web 대상 저장에 동의합니다.', submit: '동의하고 비활성으로 가져오기',
    };
    case 'toggle': return {
      title: action.enabled ? 'Web 대상 활성화 확인' : 'Web 대상 비활성화 확인', label: action.account.label, disabledSource: false,
      text: `선택한 ChatGPT Web 대상만 ${action.enabled ? '활성화' : '비활성화'}합니다. ${action.enabled ? 'Web 요청 풀에 참여할 수 있지만 실제 사용 가능 여부나 할당량을 보장하지 않습니다.' : '이 Web 대상의 요청 풀 참여를 중지합니다.'} Codex 원본과 다른 Web 대상은 바뀌지 않습니다. 사용량을 갱신하거나 인증을 복사하지 않습니다.`,
      consent: `선택한 Web 대상만 ${action.enabled ? '활성화' : '비활성화'}하는 데 동의합니다.`, submit: action.enabled ? 'Web 활성화 확인' : 'Web 비활성화 확인',
    };
    default: return action satisfies never;
  }
}
export function confirmAccount(action: Confirmation, submit: (approval: Approval) => void): void {
  const previous = document.activeElement;
  const copy = description(action);
  const dialog = element('dialog', 'account-dialog');
  dialog.setAttribute('aria-labelledby', 'account-dialog-title');
  dialog.setAttribute('aria-describedby', 'account-dialog-description');
  const form = element('form', 'section');
  const title = element('h2', '', copy.title); title.id = 'account-dialog-title';
  const text = element('p', 'muted', copy.text); text.id = 'account-dialog-description';
  const consentRow = element('label', 'consent-row');
  const consent = element('input'); consent.type = 'checkbox'; consent.id = 'account-consent'; consent.required = true;
  consentRow.htmlFor = consent.id; consentRow.append(consent, element('span', '', copy.consent));
  const override = element('input'); override.type = 'checkbox'; override.id = 'disabled-source-consent';
  form.append(title, element('strong', '', copy.label), text);
  if (copy.disabledSource) {
    const risk = element('p', 'account-notice', '비활성 Codex 소스입니다. 비활성 사유는 이 화면에서 확인되지 않으며, access 스냅샷이 만료·차단되었을 수 있습니다. 원본을 활성화하지 않습니다.');
    const row = element('label', 'consent-row'); row.htmlFor = override.id;
    row.append(override, element('span', '', '위험을 이해하고 비활성 Codex 소스 가져오기를 별도로 허용합니다.'));
    form.append(risk, row);
  }
  const cancel = button('취소', () => dialog.close()); cancel.id = 'cancel-account';
  const confirm = element('button', 'btn btn-primary', copy.submit); confirm.type = 'submit'; confirm.id = 'confirm-account'; confirm.disabled = true;
  const footer = element('div', 'account-controls'); footer.append(cancel, confirm);
  form.append(consentRow, footer); dialog.append(form); document.body.append(dialog);
  const ready = () => consent.checked && (!copy.disabledSource || override.checked);
  form.addEventListener('change', () => { confirm.disabled = !ready(); });
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    if (!ready() || confirm.disabled) return;
    confirm.disabled = true;
    dialog.close();
    submit({ consent: true, allowDisabled: override.checked });
  });
  dialog.addEventListener('keydown', (event) => {
    if (event.key !== 'Tab') return;
    const controls = Array.from(dialog.querySelectorAll<HTMLElement>('input, button:not(:disabled)'));
    const first = controls[0]; const last = controls.at(-1);
    if (event.shiftKey && document.activeElement === first || !event.shiftKey && document.activeElement === last) {
      event.preventDefault(); (event.shiftKey ? last : first)?.focus();
    }
  });
  dialog.addEventListener('close', () => {
    dialog.remove(); document.body.classList.remove('dialog-open');
    if (previous instanceof HTMLElement) (previous.id ? document.getElementById(previous.id) : previous)?.focus();
  }, { once: true });
  document.body.classList.add('dialog-open'); dialog.showModal(); cancel.focus();
}
