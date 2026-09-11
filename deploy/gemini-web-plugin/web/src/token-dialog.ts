import { userMessage } from './api';
import { isWebToken, type Account, type SaveAccount } from './contract';
import { button, element, icon } from './dom';

export function openTokenDialog(account: Account | undefined, submit: (payload: SaveAccount) => Promise<void>): void {
  const previousFocus = document.activeElement;
  const dialog = element('dialog', 'token-dialog');
  dialog.setAttribute('aria-labelledby', 'token-dialog-title');
  dialog.setAttribute('aria-describedby', 'token-dialog-description');
  const form = element('form', 'token-form');
  form.noValidate = true;
  form.autocomplete = 'off';
  const header = element('div', 'dialog-header');
  header.append(element('h2', '', account ? '웹 토큰 업데이트' : '계정 등록'));
  header.firstElementChild?.setAttribute('id', 'token-dialog-title');
  const close = button('닫기', () => dialog.close());
  close.classList.add('icon-button');
  close.replaceChildren(icon('close'));
  close.setAttribute('aria-label', '닫기');
  header.append(close);
  const description = element('p', 'muted', 'Google 웹 세션 토큰을 등록합니다. 저장 후 계정 상태와 사용 가능 모델을 확인할 수 있습니다.');
  description.id = 'token-dialog-description';

  const labelGroup = element('div', 'form-group');
  const nameLabel = element('label', '', '계정 이름');
  nameLabel.htmlFor = 'account-label';
  const name = element('input');
  name.id = 'account-label';
  name.name = 'label';
  name.required = true;
  name.autocomplete = 'off';
  name.setAttribute('aria-describedby', 'token-error');
  name.value = account?.label ?? '';
  name.placeholder = '프로필을 구분할 이름';
  labelGroup.append(nameLabel, name);

  const tokenGroup = element('div', 'form-group');
  const tokenLabel = element('label', '', '웹 토큰');
  tokenLabel.htmlFor = 'account-token';
  const token = element('textarea', 'password-textarea');
  token.id = 'account-token';
  token.name = 'token';
  token.rows = 4;
  token.required = true;
  token.autocomplete = 'off';
  token.spellcheck = false;
  token.setAttribute('autocapitalize', 'off');
  token.setAttribute('autocorrect', 'off');
  token.setAttribute('aria-describedby', 'token-hint token-error');
  const hint = element('p', 'hint', 'gemini-web:v1: 형식의 토큰을 붙여넣으세요. 입력은 숨겨지며 전송 후 지워집니다. 브라우저에 저장하지 않습니다.');
  hint.id = 'token-hint';
  tokenGroup.append(tokenLabel, token, hint);
  const errorBox = element('p', 'notice notice-danger');
  errorBox.id = 'token-error';
  errorBox.hidden = true;
  errorBox.setAttribute('role', 'alert');
  const privacy = element('p', 'privacy-note');
  privacy.append(icon('shield'), document.createTextNode('토큰은 서버로만 전송되며, 서버에서 유효성을 검사합니다.'));
  const footer = element('div', 'dialog-footer');
  const cancel = button('취소', () => dialog.close());
  const save = element('button', 'btn btn-primary', account ? '토큰 업데이트' : '계정 등록');
  save.type = 'submit';
  footer.append(cancel, save);
  form.append(header, description, labelGroup, tokenGroup, errorBox, privacy, footer);
  dialog.append(form);
  document.body.append(dialog);

  dialog.addEventListener('keydown', (event) => {
    if (event.key !== 'Tab') return;
    const controls = Array.from(dialog.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled)'));
    const first = controls[0];
    const last = controls.at(-1);
    if (event.shiftKey && document.activeElement === first || !event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      (event.shiftKey ? last : first)?.focus();
    }
  });

  dialog.addEventListener('close', () => {
    token.value = '';
    dialog.remove();
    document.body.classList.remove('dialog-open');
    if (previousFocus instanceof HTMLElement) {
      const target = previousFocus.id ? document.getElementById(previousFocus.id) : previousFocus;
      target?.focus();
    }
  }, { once: true });

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (save.disabled) return;
    const label = name.value.trim();
    const opaqueToken = token.value.trim();
    name.setAttribute('aria-invalid', String(!label));
    token.setAttribute('aria-invalid', String(!isWebToken(opaqueToken)));
    if (!label || !isWebToken(opaqueToken)) {
      errorBox.textContent = !label ? '계정 이름을 입력하세요.' : 'gemini-web:v1: 형식의 유효한 웹 토큰을 입력하세요.';
      errorBox.hidden = false;
      (!label ? name : token).focus();
      return;
    }
    errorBox.hidden = true;
    save.disabled = true;
    name.disabled = true;
    token.disabled = true;
    token.value = '';
    save.textContent = '서버에서 확인 중';
    try {
      await submit({ label, token: opaqueToken, ...(account ? { existing_id: account.id } : {}) });
      dialog.close();
    } catch (error) {
      if (!(error instanceof Error)) throw error;
      errorBox.textContent = `${userMessage(error)} 토큰을 다시 입력해 주세요.`;
      errorBox.hidden = false;
    } finally {
      token.value = '';
      save.disabled = false;
      name.disabled = false;
      token.disabled = false;
      save.textContent = account ? '토큰 업데이트' : '계정 등록';
      if (dialog.open) token.focus();
    }
  });
  document.body.classList.add('dialog-open');
  dialog.showModal();
  (account ? token : name).focus();
}
