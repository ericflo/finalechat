#!/usr/bin/env node
// The relay's controls and the composer's stop button, in Chromium (phone
// and desktop) against mocked APIs. The sheet shows what the agent runs and
// changes it (Model), hands the work to another agent with a model and an
// effort (Switch agent, one row for every other agent), and keeps Upgrade,
// Restart and the stops at the bottom, each asking first. It has no
// Interrupt, Status or Updates. Interrupting is the small stop button in
// the message field, there only while the agent works, and it leaves the
// field free to type.
//
//   FINALECHAT_PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node scripts/test-relay-controls-browser.mjs
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createServer } from '../web/node_modules/vite/dist/node/index.js';
import react from '../web/node_modules/@vitejs/plugin-react/dist/index.js';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const { chromium } = await import(pathToFileURL(process.env.FINALECHAT_PLAYWRIGHT_MODULE || '/tmp/finalechat-browser-tools/node_modules/playwright/index.mjs').href);
const shots = process.env.FINALECHAT_SCREENSHOTS || '/tmp/finalechat-relay-controls';
await mkdir(shots, { recursive: true });

const now = () => new Date().toISOString();
const ago = (min) => new Date(Date.now() - min * 60000).toISOString();
const user = { id: 'u1', email: 'u@example.com', display_name: 'U', settings: { notify_all_messages: false, remote_mode: false }, created_at: ago(9000) };
const relayMeta = (state, extra = {}) => ({ name: 'claude', agent: 'claude', profile: 'claude', label: 'Claude Code · Fable 5.2 xhigh', model: 'fable', effort: 'xhigh', state, autopilot: false, autopilot_until: '', target: 'eagent-relay-1-claude', attach: 'eagent relays attach eagent-relay-1-claude', notify_done: false, switch_profiles: ['antigravity', 'codex'], ...extra });
const thread = (id, meta) => ({
  id, external_id: `eagent:${id}`, title: 'Search ranking', agent: 'eagent', meta, created_at: ago(600), updated_at: ago(1), last_activity_at: ago(1), last_read_at: ago(0),
  archived_at: null, muted: false, preview: 'Claude is editing rank.go.', preview_sender: 'agent', unread_count: 0, pending_questions: 0,
  description: 'Reworking the ranking weights.', summary: 'Reworking the ranking weights.', title_pinned: false, description_pinned: false, activity: null,
});
const models = (...names) => names.map((n) => ({ value: n, label: n }));
const efforts = (...e) => ({ type: 'string', max_length: 120, enum: e });
const project = {
  id: 'r-proj', connector_id: 'c1', key: 'project-x', label: 'Development', scope: 'project', generation: 'g1', updated_at: now(),
  descriptor: { format: 'finalechat.settings/v1', schema_version: 'eagent.settings/v1', adapter_version: '1', fields: [],
    actions: [{ operation: 'session.start', label: 'Start a new session', class: 'cost',
      parameters: { type: 'object', required: ['prompt'], properties: {
        prompt: { type: 'string' }, relay: { type: 'string', enum: ['', 'antigravity', 'claude', 'codex'] }, config: { type: 'string' },
        relay_claude_model: { type: 'string', max_length: 200 }, relay_claude_effort: efforts('low', 'medium', 'high', 'xhigh', 'max'),
        relay_codex_model: { type: 'string', max_length: 200 }, relay_codex_effort: efforts('low', 'medium', 'high', 'xhigh', 'ultra'),
        relay_antigravity_model: { type: 'string', max_length: 200 }, relay_antigravity_effort: efforts('low', 'high'),
      } },
      inputs: [
        { name: 'config', label: 'Configuration', suggest: { list: 'configs' }, default: 'glm', when: { relay: [''] } },
        { name: 'relay_claude_model', label: 'Model', group: 'Claude Code', suggest: { list: 'claude_models' }, default: 'fable', when: { relay: ['claude'] } },
        { name: 'relay_claude_effort', label: 'Reasoning effort', group: 'Claude Code', default: 'medium', when: { relay: ['claude'] } },
        { name: 'relay_codex_model', label: 'Model', group: 'Codex', suggest: { list: 'codex_models' }, default: 'gpt-6.2-astra', when: { relay: ['codex'] } },
        { name: 'relay_codex_effort', label: 'Reasoning effort', group: 'Codex', default: 'xhigh', when: { relay: ['codex'] } },
        { name: 'relay_antigravity_model', label: 'Model', group: 'Antigravity', suggest: { list: 'antigravity_models' }, default: 'Gemini 3.1 Pro', when: { relay: ['antigravity'] } },
        { name: 'relay_antigravity_effort', label: 'Reasoning effort', group: 'Antigravity', default: 'high', when: { relay: ['antigravity'] } },
      ] }],
    suggestions: { configs: models('glm'), claude_models: models('fable', 'opus', 'sonnet', 'haiku'), codex_models: models('gpt-6.2-astra', 'gpt-6.2-sol'), antigravity_models: models('Gemini 3.1 Pro', 'Gemini 3.1 Flash') } },
  snapshot: { version: 'p1', context: '/home/u/Development', saved: {}, effective: {}, runtime_known: false, details: {
    relay_profiles: [
      { name: 'antigravity', agent: 'antigravity', label: 'Antigravity · Gemini 3.1 Pro high', model: 'Gemini 3.1 Pro', effort: 'high' },
      { name: 'claude', agent: 'claude', label: 'Claude Code · Fable 5.2 medium', model: 'fable', effort: 'medium' },
      { name: 'codex', agent: 'codex', label: 'Codex · GPT-6.2-Astra xhigh', model: 'gpt-6.2-astra', effort: 'xhigh' },
    ] } },
};
const connector = { id: 'c1', name: 'eagent · Development', provider: 'eagent', state: 'active', grants: [], requested_grants: [], last_seen_at: now(), expires_at: now() };

const server = await createServer({ root: path.join(root, 'web'), configFile: false, plugins: [react()], server: { host: '127.0.0.1', port: 0, strictPort: false }, logLevel: 'warn' });
await server.listen();
const base = server.resolvedUrls.local[0].replace(/\/$/, '');
const browser = await chromium.launch({ headless: true });
const errors = [];
try {
  for (const [name, width, height] of [['phone', 390, 844], ['desktop', 1280, 900]]) {
    const threads = { work: thread('work', { relay: relayMeta('working') }), idle: thread('idle', { relay: relayMeta('idle', { pending: 'opus at high' }) }), old: thread('old', { relay: { name: 'codex', agent: 'codex', label: 'Codex · GPT-6 Astra xhigh', state: 'working' } }) };
    const sent = [];
    let projectSettings = true;
    const page = await browser.newPage({ viewport: { width, height }, deviceScaleFactor: 2, serviceWorkers: 'block' });
    page.on('pageerror', (e) => errors.push(String(e)));
    await page.route('**/*', async (route) => {
      const req = route.request();
      const url = new URL(req.url());
      if (url.origin !== base) throw Error('Unexpected outbound request ' + url.origin);
      if (!url.pathname.startsWith('/api/')) return route.continue();
      const p = url.pathname.replace('/api/v1', '');
      const json = (v, status = 200) => route.fulfill({ status, json: v, headers: { date: new Date().toUTCString() } });
      if (p === '/auth/status') return json({ authenticated: true, user, attachments_enabled: true, features: [], push_enabled: false, signup: 'open', version: 'test' });
      if (p === '/me') return json({ auth: 'session', user, counts: { pending_questions: 0, unread_threads: 0, attention: 0 }, features: [], push_enabled: false, attachments_enabled: true, version: 'test' });
      if (p === '/threads') return json({ threads: Object.values(threads) });
      if (p === '/questions') return json({ questions: [] });
      if (p === '/live-sessions') return json({ sessions: [] });
      if (p === '/events') return new Promise(() => {});
      const t = p.match(/^\/threads\/(\w+)$/);
      if (t) return json({ thread: threads[t[1]] });
      const m = p.match(/^\/threads\/(\w+)\/messages$/);
      if (m && req.method() === 'POST') {
        const body = req.postDataJSON();
        sent.push(body.body);
        return json({ message: { id: 'm' + sent.length, thread_id: m[1], sender: 'user', kind: 'message', body: body.body, meta: {}, attachments: [], created_at: now() }, thread: threads[m[1]], created: true }, 201);
      }
      if (m) return json({ messages: [], has_more: false });
      if (p.endsWith('/questions') || p.endsWith('/artifacts')) return json({ questions: [], artifacts: [] });
      if (p.endsWith('/settings')) return json({ resources: projectSettings ? [{ id: 'r-proj', label: 'Development', scope: 'project', provider: 'eagent', available: true }] : [] });
      if (p === '/settings-resources/r-proj') return json({ resource: project, connector, online: true, website: null });
      return json({});
    });
    await page.addInitScript(() => { try { localStorage.setItem('fc.setup.dismissed', '1'); } catch {} });
    const settle = () => page.waitForTimeout(450); // a sheet slides in
    const items = async () => (await page.locator('.relay-sheet .sheet .item .item-text > span:first-child').allTextContents()).map((s) => s.trim());
    const lastSent = async (n) => { for (let i = 0; i < 75 && sent.length < n; i++) await page.waitForTimeout(40); return sent[n - 1]; };

    // The agent is working: a stop button in the field, and nothing else changes about typing.
    await page.goto(base + '/t/work');
    await page.locator('.relay-badge').waitFor();
    const stop = page.getByRole('button', { name: 'Stop what Claude is doing' });
    await stop.waitFor();
    const box = await stop.boundingBox();
    const field = await page.locator('.composer-field').boundingBox();
    assert.ok(box.width <= 32 && box.height <= 32, 'the stop button is small');
    assert.ok(box.x >= field.x && box.x + box.width <= field.x + field.width && box.y >= field.y && box.y + box.height <= field.y + field.height, 'it sits inside the message field');
    await page.locator('.composer textarea').fill('also keep the old weights behind a flag');
    await page.screenshot({ path: `${shots}/${name}-composer-working.png` });
    assert.equal(await page.getByRole('button', { name: 'Send to Claude' }).isEnabled(), true, 'a steering message can still be sent');
    await stop.click();
    assert.equal(await lastSent(1), '/interrupt');
    assert.equal(await page.locator('.composer textarea').inputValue(), 'also keep the old weights behind a flag', 'stopping keeps what was typed');
    await page.getByRole('button', { name: 'Send to Claude' }).click();
    assert.equal(await lastSent(2), 'also keep the old weights behind a flag');

    // The controls, in order: what it runs first, what ends or restarts it last.
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Model/ }).waitFor();
    await settle();
    assert.deepEqual(await items(), ['Model', "Tell me when it's done", 'Screen', 'Switch agent…', 'Autopilot…', 'Copy attach command', 'Upgrade…', 'Restart…', 'Stop Claude']);
    const sheetText = await page.locator('.relay-sheet .sheet').textContent();
    for (const gone of ['Interrupt', 'Status', 'Updates', '15m']) assert.ok(!sheetText.includes(gone), `${gone} is no longer in the sheet`);
    assert.match(await page.getByRole('button', { name: /^Model/ }).textContent(), /Fable 5\.2 xhigh/);
    assert.match(await page.getByRole('button', { name: /^Switch agent/ }).textContent(), /Antigravity or Codex/);
    await page.screenshot({ path: `${shots}/${name}-relay-sheet.png` });

    // Restart asks first; cancelling sends nothing.
    await page.getByRole('button', { name: /^Restart/ }).click();
    await page.locator('.confirm').getByText('Restart Claude?').waitFor();
    await settle();
    await page.screenshot({ path: `${shots}/${name}-restart-confirm.png` });
    await page.locator('.confirm').getByRole('button', { name: 'Cancel' }).click();
    await page.locator('.confirm').waitFor({ state: 'detached' });
    assert.equal(sent.length, 2);
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Restart/ }).click();
    await page.locator('.confirm').getByRole('button', { name: 'Restart' }).click();
    assert.equal(await lastSent(3), '/restart');
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Upgrade/ }).click();
    await page.locator('.confirm').getByText('Upgrade Claude?').waitFor();
    await page.locator('.confirm').getByRole('button', { name: 'Upgrade' }).click();
    assert.equal(await lastSent(4), '/upgrade');

    // Model: opens on what it runs; only what changed is sent.
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Model/ }).click();
    const agentSheet = page.locator('.agent-sheet');
    await agentSheet.getByRole('heading', { name: "Claude's model" }).waitFor();
    await agentSheet.locator('.sf-row').first().waitFor();
    await settle();
    const change = agentSheet.getByRole('button', { name: 'Change', exact: true });
    assert.equal(await change.isDisabled(), true, 'nothing to change yet');
    assert.match(await agentSheet.textContent(), /fable/);
    assert.match(await agentSheet.textContent(), /xhigh/);
    assert.equal(await agentSheet.getByRole('radio', { name: 'xhigh', exact: true }).getAttribute('aria-checked'), 'true', 'it opens on what runs now');
    assert.equal(await agentSheet.locator('.sf-badge').count(), 0, 'nothing reads as changed before a change');
    await page.screenshot({ path: `${shots}/${name}-model-sheet.png` });
    await choose(page, agentSheet, 'Reasoning effort', 'high');
    assert.equal(await change.isEnabled(), true);
    assert.equal((await agentSheet.locator('.sf-badge').textContent()).trim(), 'Unsaved');
    // The default (medium) can be chosen like any other, and undone.
    await choose(page, agentSheet, 'Reasoning effort', 'medium');
    assert.equal(await agentSheet.getByRole('radio', { name: 'medium', exact: true }).getAttribute('aria-checked'), 'true');
    await agentSheet.getByRole('button', { name: 'Undo' }).first().click();
    assert.equal(await change.isDisabled(), true, 'undone, nothing to change');
    await choose(page, agentSheet, 'Reasoning effort', 'high');
    await change.click();
    assert.equal(await lastSent(5), '/model fable high');
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Model/ }).click();
    await agentSheet.getByRole('heading', { name: "Claude's model" }).waitFor();
    await choose(page, agentSheet, 'Model', 'opus');
    await choose(page, agentSheet, 'Reasoning effort', 'max');
    await agentSheet.getByRole('button', { name: 'Change', exact: true }).click();
    assert.equal(await lastSent(6), '/model opus max');

    // Switch agent: one sheet for every other agent, with its model and effort.
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Switch agent/ }).click();
    await agentSheet.getByRole('heading', { name: 'Switch agent' }).waitFor();
    await agentSheet.getByRole('radio', { name: 'Codex' }).click();
    await agentSheet.getByRole('button', { name: 'Switch to Codex' }).waitFor();
    await settle();
    await page.screenshot({ path: `${shots}/${name}-switch-sheet.png` });
    await choose(page, agentSheet, 'Model', 'gpt-6.2-sol');
    await choose(page, agentSheet, 'Reasoning effort', 'ultra');
    await agentSheet.getByRole('button', { name: 'Switch to Codex' }).click();
    assert.equal(await lastSent(7), '/switch codex gpt-6.2-sol ultra');
    // Left alone, the other agent starts with its own; a name with a space is quoted.
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Switch agent/ }).click();
    await agentSheet.getByRole('button', { name: 'Switch to Antigravity' }).click();
    assert.equal(await lastSent(8), '/switch antigravity');
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Switch agent/ }).click();
    await choose(page, agentSheet, 'Model', 'Gemini 3.1 Flash');
    await agentSheet.getByRole('button', { name: 'Switch to Antigravity' }).click();
    assert.equal(await lastSent(9), '/switch antigravity "Gemini 3.1 Flash" default');

    // Idle: no stop button, and a change that waits for the turn shows on the Model row.
    await page.goto(base + '/t/idle');
    await page.locator('.relay-badge').waitFor();
    assert.equal(await page.locator('.stop-btn').count(), 0, 'no stop button while it is idle');
    await page.locator('.relay-badge').click();
    assert.match(await page.getByRole('button', { name: /^Model/ }).textContent(), /Next: opus at high, when this turn ends/);
    assert.equal(await page.getByRole('button', { name: /Tell me when/ }).count(), 0);
    await page.keyboard.press('Escape');

    // An older host and no project settings: the sheet still switches.
    projectSettings = false;
    await page.goto(base + '/t/old');
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Switch agent/ }).click();
    await agentSheet.getByText('Claude will start with its own model and reasoning effort.').waitFor();
    await agentSheet.getByRole('button', { name: 'Switch to Claude' }).click();
    assert.equal(await lastSent(10), '/switch claude');
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'overflow');
    await page.close();
  }
  assert.deepEqual(errors, []);
  console.log(`PASS relay controls: stop button in the field, Model and Switch agent sheets, confirmations; screenshots in ${shots}`);
} finally {
  await browser.close();
  await server.close();
}

// Picks a value for one of the form's fields: a segmented choice, or a
// picker that opens its own sheet.
async function choose(page, sheet, label, value) {
  const row = sheet.locator('.sf-row').filter({ has: page.locator('label', { hasText: label }) });
  const radio = row.getByRole('radio', { name: value, exact: true });
  if (await radio.count()) return void (await radio.click());
  await row.locator('.sf-pick').click();
  await page.locator('.sf-picker-sheet .sf-option').filter({ hasText: value }).first().click();
  await page.locator('.sf-picker-sheet').waitFor({ state: 'detached' });
}
