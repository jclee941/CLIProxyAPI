import { file, build, write } from 'bun';

async function buildPlugin(entry: string, outDir: string) {
  const result = await build({
    entrypoints: [entry],
    target: 'browser',
    format: 'iife',
    minify: true,
  });
  if (!result.success) throw new AggregateError(result.logs, `Resource build failed for ${entry}`);
  const script = result.outputs.find((output) => output.kind === 'entry-point');
  if (!script) throw new Error(`Resource entry point missing for ${entry}`);

  const templatePath = entry.includes('/flow/') ? `${import.meta.dir}/src/flow/index.html` : `${import.meta.dir}/src/index.html`;

  const [template, tokens, styles, javascript] = await Promise.all([
    file(templatePath).text(),
    file(`${import.meta.dir}/src/tokens.css`).text(),
    file(`${import.meta.dir}/src/styles.css`).text(),
    script.text(),
  ]);
  const html = template.replace('/* RESOURCE_STYLES */', () => `${tokens}\n${styles}`)
    .replace('/* RESOURCE_SCRIPT */', () => javascript.replace(/<\/script/gi, '<\\/script'));
  await write(`${outDir}/index.html`, html);
  console.log(`Built self-contained ${outDir}/index.html (${new TextEncoder().encode(html).byteLength} bytes)`);
}

await buildPlugin(`${import.meta.dir}/src/main.ts`, import.meta.dir);
await buildPlugin(`${import.meta.dir}/src/flow/main.ts`, `${import.meta.dir}/../../flow2api-plugin/web`);
