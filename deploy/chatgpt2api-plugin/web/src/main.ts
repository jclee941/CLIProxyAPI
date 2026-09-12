import { badge, element, icon } from '../../../gemini-web-plugin/web/src/dom.ts';
import { loadStatus, statusFailure } from './api.ts';
import { statusPanel } from './panel.ts';

let pending: AbortController | undefined;
let loading = false;
const page = element('main', 'page');
const heading = element('header', 'page-heading');
const copy = element('div', 'heading-copy');
copy.append(element('h1', '', 'ChatGPT2API'), element('p', 'muted', '기존 계정·모델과 추론 엔진을 재사용하는 연결 플러그인'));
const refresh = element('button', 'btn btn-secondary');
refresh.type = 'button';
refresh.id = 'refresh';
const refreshLabel = element('span', '', '새로고침');
refresh.append(icon('refresh'), refreshLabel);
heading.append(copy, refresh);
const feedback = element('div', 'feedback');
feedback.setAttribute('role', 'status');
feedback.setAttribute('aria-live', 'polite');
feedback.setAttribute('aria-atomic', 'true');
const content = element('div', 'content');
const footer = element('footer', 'resource-footer');
const providers = element('a', 'btn btn-primary', '기존 공급자 설정 열기');
providers.href = '/management.html#/ai-providers';
providers.target = '_top';
providers.referrerPolicy = 'no-referrer';
footer.append(element('p', 'caption', '공급자와 인증 설정은 Manager에서 관리합니다.'), providers);
page.append(heading, feedback, content, footer);
document.getElementById('app')?.replaceChildren(page);

async function refreshStatus(): Promise<void> {
  if (loading) return;
  const request = new AbortController();
  pending = request;
  loading = true;
  refresh.setAttribute('aria-disabled', 'true');
  refreshLabel.textContent = '확인 중';
  content.setAttribute('aria-busy', 'true');
  page.dataset['state'] = 'loading';
  content.replaceChildren();
  feedback.replaceChildren(badge('상태 확인 중'), element('p', 'muted', '플러그인과 기존 서비스 상태를 불러오고 있습니다.'));
  try {
    const status = await loadStatus(request.signal);
    request.signal.throwIfAborted();
    page.dataset['state'] = status.upstream.healthy ? 'healthy' : 'upstream-down';
    feedback.replaceChildren(badge(status.upstream.healthy ? '서비스 정상' : '서비스 이상', status.upstream.healthy ? 'success' : 'danger'),
      element('p', 'muted', status.upstream.healthy
        ? '상태를 확인했습니다. 필요할 때 새로고침으로 다시 조회하세요.'
        : '업스트림 상태가 정상이 아닙니다. 기존 서비스 연결을 확인한 뒤 새로고침하세요.'));
    content.replaceChildren(statusPanel(status));
  } catch (error) {
    const failure = request.signal.aborted
      ? { kind: 'request-error', label: '조회 중단', tone: 'neutral', message: '상태 조회가 중단되었습니다. 새로고침으로 다시 확인하세요.' }
      : statusFailure(error);
    page.dataset['state'] = failure.kind;
    feedback.replaceChildren(badge(failure.label, failure.tone), element('p', 'muted', failure.message));
    content.replaceChildren();
  } finally {
    pending = undefined;
    loading = false;
    refresh.setAttribute('aria-disabled', 'false');
    refreshLabel.textContent = '새로고침';
    content.setAttribute('aria-busy', 'false');
  }
}

refresh.addEventListener('click', () => void refreshStatus());
window.addEventListener('pagehide', () => pending?.abort());
void refreshStatus();
