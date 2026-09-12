import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { test } from 'node:test';
import { MOCK_KEY, PRIVATE_SENTINEL } from './fixtures.ts';

test('built resource contains one V8-valid inline script and no external assets or fixtures', async () => {
  const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
  const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)];
  const syntax = spawnSync(process.execPath, ['--check'], { input: scripts[0]?.[1], encoding: 'utf8' });
  assert.equal(scripts.length, 1);
  assert.equal(syntax.status, 0, syntax.stderr);
  assert.doesNotMatch(html, /<script[^>]+src=|<link[^>]+rel="stylesheet"|@import|url\(https?:/i);
  assert.ok(!html.includes(MOCK_KEY) && !html.includes(PRIVATE_SENTINEL));
  assert.ok(!html.includes('mock-chat-model'));
});
