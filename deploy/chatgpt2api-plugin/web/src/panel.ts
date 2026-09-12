import { element } from '../../../gemini-web-plugin/web/src/dom.ts';
import type { PluginStatus } from './contract.ts';

const number = new Intl.NumberFormat('ko-KR');

function detail(label: string, value: string, id?: string): HTMLElement {
  const row = element('div', 'detail-row');
  const content = element('dd', '', value);
  if (id) content.id = id;
  row.append(element('dt', '', label), content);
  return row;
}

export function statusPanel(status: PluginStatus): HTMLElement {
  const card = element('article', 'resource-card');
  card.setAttribute('aria-label', '플러그인 연결 정보');
  const routing = element('section', 'section');
  routing.append(element('h2', '', '플러그인 연결'));
  const details = element('dl', 'details');
  details.append(
    detail('플러그인 버전', status.plugin.version),
    detail('공급자', status.routing.provider),
    detail('추론 엔진', 'CPA 기본 OpenAI 호환 실행기'),
    detail('인증 소스', 'Host Auth Manager'),
    detail('플러그인 라우팅 선택', number.format(status.plugin.route_count), 'route-count'),
  );
  routing.append(details, element('p', 'caption', '라우팅 선택 횟수이며, 요청 성공 횟수가 아닙니다.'));

  const models = element('section', 'section separated');
  const modelHeading = element('h2', '', '라우팅 모델');
  const count = element('span', 'model-count', `${number.format(status.routing.model_names.length)}개`);
  count.id = 'model-count';
  modelHeading.append(count);
  const list = element('ul', 'model-list');
  list.id = 'model-list';
  for (const name of status.routing.model_names) list.append(element('li', 'model-chip', name));
  models.append(modelHeading, list);
  if (!status.routing.model_names.length) models.append(element('p', 'muted', '플러그인이 선택하는 모델이 없습니다. 기존 라우팅을 유지합니다.'));
  models.append(element('p', 'caption', '목록에 없는 auto·공유 모델은 기존 라우팅 유지'));

  const upstream = element('section', 'section separated');
  upstream.append(element('h2', '', '기존 서비스'));
  const observations = element('dl', 'details');
  if (status.upstream.version != null) observations.append(detail('업스트림 버전', status.upstream.version));
  if (status.upstream.accounts?.active != null) observations.append(detail('활성 계정', number.format(status.upstream.accounts.active), 'active-accounts'));
  if (status.upstream.accounts?.total != null) observations.append(detail('전체 계정', number.format(status.upstream.accounts.total), 'total-accounts'));
  upstream.append(observations, element('p', 'caption', '실제 ChatGPT Web 계정 풀은 기존 서비스에서 관리합니다. 이 페이지에서는 계정을 추가하거나 삭제하지 않습니다.'));
  card.append(routing, models, upstream);
  return card;
}
