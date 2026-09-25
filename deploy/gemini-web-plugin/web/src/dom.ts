export function element<Tag extends keyof HTMLElementTagNameMap>(tag: Tag, className = '', text?: string): HTMLElementTagNameMap[Tag] {
  const node = document.createElement(tag);
  node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

const iconPaths = {
  refresh: 'M3 12a9 9 0 1 0 3-6.7L3 8m0-5v5h5',
  plus: 'M12 5v14M5 12h14',
  close: 'm6 6 12 12M6 18 18 6',
  shield: 'M12 3 3 7v5c0 5 9 9 9 9s9-4 9-9V7l-9-4Zm-4 9 3 3 5-6',
} as const;

export function icon(name: keyof typeof iconPaths): SVGSVGElement {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.75');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  const path = document.createElementNS(svg.namespaceURI, 'path');
  path.setAttribute('d', iconPaths[name]);
  svg.append(path);
  return svg;
}

export function button(label: string, action: () => void, variant = 'secondary'): HTMLButtonElement {
  const control = element('button', `btn btn-${variant}`, label);
  control.type = 'button';
  control.addEventListener('click', action);
  return control;
}

export function badge(text: string, tone = 'neutral'): HTMLSpanElement {
  return element('span', `badge badge-${tone}`, text);
}

export function localTime(seconds: number | null): string {
  if (seconds === null || seconds <= 0) return '확인되지 않음';
  const date = new Date(seconds * 1000);
  if (Number.isNaN(date.getTime())) return '확인되지 않음';
  return new Intl.DateTimeFormat('ko-KR', {
    month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit',
    timeZoneName: 'short',
  }).format(date);
}
