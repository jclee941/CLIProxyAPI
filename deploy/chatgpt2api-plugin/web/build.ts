import { build, file, write } from 'bun';

const result = await build({
  entrypoints: [`${import.meta.dir}/src/main.ts`],
  target: 'browser', format: 'iife', minify: true,
});
if (!result.success) throw new Error('Resource build failed', { cause: result.logs });
const script = result.outputs.find((output) => output.kind === 'entry-point');
if (!script) throw new Error('Resource entry point missing');
const [template, tokens, styles, javascript] = await Promise.all([
  file(`${import.meta.dir}/src/index.html`).text(),
  file(`${import.meta.dir}/../../gemini-web-plugin/web/src/tokens.css`).text(),
  file(`${import.meta.dir}/src/styles.css`).text(),
  script.text(),
]);
const html = template.replace('/* RESOURCE_STYLES */', () => `${tokens}\n${styles}`)
  .replace('/* RESOURCE_SCRIPT */', () => javascript.replace(/<\/script/gi, '<\\/script'));
await write(`${import.meta.dir}/index.html`, html);
console.log(`Built self-contained index.html (${new TextEncoder().encode(html).byteLength} bytes)`);
