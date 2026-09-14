export const sourceID = `codex_${'a'.repeat(64)}`;
export const disabledSourceID = `codex_${'b'.repeat(64)}`;
export const webID = `web_${'c'.repeat(64)}`;
export const secondWebID = `web_${'d'.repeat(64)}`;
export const observedAt = '2026-09-13T10:00:00Z';
export const source = { id: sourceID, label: 'Codex aaaaaaaaaaaa', disabled: false, provider: 'codex' } as const;
export const disabledSource = { ...source, id: disabledSourceID, label: 'Codex bbbbbbbbbbbb', disabled: true } as const;
export const webAccount = {
  id: webID, label: 'Web cccccccccccc', disabled: true, status: 'disabled', source_type: 'codex', type: 'Plus',
  tracked_image_remaining: 7, observed_image_remaining: 3, reset_after_seconds: 3600.5,
  observed_at: null, observation_source: 'stored_snapshot', refresh_error: null,
} as const;
export const webInventory = { accounts: [webAccount], observed_at: observedAt, source: 'ChatGPTWeb',
  capabilities: { preserve_disabled_accounts: true } } as const;
