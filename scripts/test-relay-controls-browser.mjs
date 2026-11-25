#!/usr/bin/env node
// The relay's controls and the composer's stop button, in Chromium (phone
// and desktop) against mocked APIs. The sheet shows what the agent runs and
// changes it (Model), hands the work to another agent with a model and an
// effort (Switch agent, one row for every other agent), and keeps Upgrade,
// Restart and the stops at the bottom, each asking first. It has no
// Interrupt, Status or Updates. Interrupting is the small stop button in
// the message field, there only while the agent works, and it leaves the
// field free to type. A fourth profile (pi, whose models are named
// provider/model) is offered and controlled like the others.
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
const relayMeta = (state, extra = {}) => ({ name: 'claude', agent: 'claude', profile: 'claude', label: 'Claude Code · Fable 5.2 xhigh', model: 'fable', effort: 'xhigh', state, autopilot: false, autopilot_until: '', target: 'eagent-relay-1-claude', attach: 'eagent relays attach eagent-relay-1-claude', notify_done: false, switch_profiles: ['antigravity', 'codex', 'pi'], ...extra });
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
        prompt: { type: 'string' }, relay: { type: 'string', enum: ['claude', 'antigravity', 'codex', 'pi'] },
        relay_claude_model: { type: 'string', max_length: 200 }, relay_claude_effort: efforts('low', 'medium', 'high', 'xhigh', 'max'),
        relay_codex_model: { type: 'string', max_length: 200 }, relay_codex_effort: efforts('low', 'medium', 'high', 'xhigh', 'ultra'),
        relay_antigravity_model: { type: 'string', max_length: 200 }, relay_antigravity_effort: efforts('low', 'high'),
        relay_pi_model: { type: 'string', max_length: 200 }, relay_pi_effort: efforts('low', 'medium', 'high', 'xhigh'),
      } },
      inputs: [
        { name: 'relay_claude_model', label: 'Model', group: 'Claude Code', suggest: { list: 'claude_models' }, default: 'fable', when: { relay: ['claude'] } },
        { name: 'relay_claude_effort', label: 'Reasoning effort', group: 'Claude Code', default: 'medium', when: { relay: ['claude'] } },
        { name: 'relay_codex_model', label: 'Model', group: 'Codex', suggest: { list: 'codex_models' }, default: 'gpt-6.2-astra', when: { relay: ['codex'] } },
        { name: 'relay_codex_effort', label: 'Reasoning effort', group: 'Codex', default: 'xhigh', when: { relay: ['codex'] } },
        { name: 'relay_antigravity_model', label: 'Model', group: 'Antigravity', suggest: { list: 'antigravity_models' }, default: 'Gemini 3.1 Pro', when: { relay: ['antigravity'] } },
        { name: 'relay_antigravity_effort', label: 'Reasoning effort', group: 'Antigravity', default: 'high', when: { relay: ['antigravity'] } },
        { name: 'relay_pi_model', label: 'Model', group: 'Pi', suggest: { list: 'pi_models' }, when: { relay: ['pi'] } },
        { name: 'relay_pi_effort', label: 'Reasoning effort', group: 'Pi', when: { relay: ['pi'] } },
      ] }],
    suggestions: { pi_models: models('openrouter/qwen3-coder', 'anthropic/claude-sonnet-5-5'), claude_models: models('fable', 'opus', 'sonnet', 'haiku'), codex_models: models('gpt-6.2-astra', 'gpt-6.2-sol'), antigravity_models: models('Gemini 3.1 Pro', 'Gemini 3.1 Flash') } },
  snapshot: { version: 'p1', context: '/home/u/Development', saved: {}, effective: {}, runtime_known: false, details: {
    relay_profiles: [
      { name: 'claude', agent: 'claude', label: 'Claude Code · Fable 5.2 medium', model: 'fable', effort: 'medium' },
      { name: 'antigravity', agent: 'antigravity', label: 'Antigravity · Gemini 3.1 Pro high', model: 'Gemini 3.1 Pro', effort: 'high' },
      { name: 'codex', agent: 'codex', label: 'Codex · GPT-6.2-Astra xhigh', model: 'gpt-6.2-astra', effort: 'xhigh' },
      { name: 'pi', agent: 'pi', label: 'Pi · openrouter/qwen3-coder high', model: 'openrouter/qwen3-coder', effort: 'high' },
      { name: 'opencode', agent: 'opencode', label: 'OpenCode · Kimi K3', model: 'kimi-k3', effort: '' },
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
    const threads = { work: thread('work', { relay: relayMeta('working') }), idle: thread('idle', { relay: relayMeta('idle', { pending: 'opus at high' }) }), old: thread('old', { relay: { name: 'codex', agent: 'codex', label: 'Codex · GPT-6 Astra xhigh', state: 'working' } }), pi: thread('pi', { relay: { name: 'pi', agent: 'pi', profile: 'pi', label: 'Pi · openrouter/qwen3-coder high', model: 'openrouter/qwen3-coder', effort: 'high', state: 'idle', switch_profiles: ['claude', 'antigravity', 'codex', 'opencode'] } }),
      // Stopped on a prompt on its screen: the session waits, on the user.
      blocked: { ...thread('blocked', { relay: relayMeta('blocked') }), title: 'Billing export', description: 'Claude is exporting last month\'s invoices.', summary: 'Claude is exporting last month\'s invoices.',
        activity: { text: 'Claude is stopped on a prompt on its screen', kind: 'waiting', at: ago(0.2), since: ago(3), expires_at: new Date(Date.now() + 600000).toISOString() } } };
    // Turn reports: one the user asked to hear about (important), one not.
    const report = (id, body, importance, min) => ({ id, thread_id: 'blocked', sender: 'agent', body, format: 'markdown', importance, meta: { eagent: 'relay', kind: 'relay_report', relay: 'claude', agent: 'claude', duration_ms: 95000, tools: 7, interrupted: false, images: [] }, origin: 'token', attachments: [], created_at: ago(min) });
    const blockedMessages = [report('r1', 'Exported January. Moving on to February.', 'normal', 9), report('r2', 'Done: all twelve months are exported to exports/.\n\nThe totals match the ledger.', 'important', 4)];
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
      if (p === '/live-sessions') return json({ sessions: Object.values(threads).filter((x) => x.meta.relay?.state !== 'exited').map((x) => ({ thread_id: x.id, resource_id: 'r-' + x.id, connector_id: 'c1', label: x.title })) });
      if (p === '/events') return new Promise(() => {});
      const t = p.match(/^\/threads\/(\w+)$/);
      if (t) return json({ thread: threads[t[1]] });
      const m = p.match(/^\/threads\/(\w+)\/messages$/);
      if (m && req.method() === 'POST') {
        const body = req.postDataJSON();
        sent.push(body.body);
        return json({ message: { id: 'm' + sent.length, thread_id: m[1], sender: 'user', kind: 'message', body: body.body, meta: {}, attachments: [], created_at: now() }, thread: threads[m[1]], created: true }, 201);
      }
      if (m) return json({ messages: m[1] === 'blocked' ? blockedMessages : [], has_more: false });
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
    // Who gets the next message: the relayed agent, or the host by the
    // thread's own name for it.
    await page.locator('.route-pip').click();
    const pop = page.getByRole('menu', { name: 'Who gets your message' });
    await pop.getByText('Kept for eagent: status, settings, questions.').waitFor();
    await settle();
    await page.screenshot({ path: `${shots}/${name}-route-pip.png` });
    await pop.getByRole('menuitemradio', { name: /^eagent/ }).click();
    await page.getByPlaceholder('Message eagent…').waitFor();
    await page.locator('.route-pip').click();
    await pop.getByRole('menuitemradio', { name: /^Claude/ }).click();

    // The controls, in order: what it runs first, what ends or restarts it last.
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Model/ }).waitFor();
    await settle();
    assert.deepEqual(await items(), ['Model', "Tell me when it's done", 'Screen', 'Switch agent…', 'Autopilot…', 'Copy attach command', 'Upgrade…', 'Restart…', 'Stop Claude']);
    const sheetText = await page.locator('.relay-sheet .sheet').textContent();
    for (const gone of ['Interrupt', 'Status', 'Updates', '15m']) assert.ok(!sheetText.includes(gone), `${gone} is no longer in the sheet`);
    assert.match(await page.getByRole('button', { name: /^Model/ }).textContent(), /Fable 5\.2 xhigh/);
    assert.match(await page.getByRole('button', { name: /^Switch agent/ }).textContent(), /Hand the work to Antigravity, Codex or Pi$/);
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
    // Every agent keeps its whole name, however many there are.
    for (const b of await agentSheet.locator('.agent-pick button').all()) assert.ok(await b.evaluate((el) => { const r = document.createRange(); r.selectNodeContents(el); return r.getClientRects().length === 1 && el.scrollWidth <= el.clientWidth; }), `${await b.textContent()} is squeezed onto more than one line`);
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
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Switch agent/ }).click();
    await agentSheet.getByRole('radio', { name: 'Pi' }).click();
    await choose(page, agentSheet, 'Model', 'openrouter/qwen3-coder');
    await agentSheet.getByRole('button', { name: 'Switch to Pi' }).click();
    assert.equal(await lastSent(10), '/switch pi openrouter/qwen3-coder default');

    // A pi relay: named from its label, its own models, sent as typed.
    await page.goto(base + '/t/pi');
    await page.locator('.relay-badge').getByText('Pi · idle').waitFor();
    await page.locator('.relay-badge').click();
    assert.match(await page.getByRole('button', { name: /^Model/ }).textContent(), /openrouter\/qwen3-coder high/);
    assert.match(await page.getByRole('button', { name: /^Switch agent/ }).textContent(), /Hand the work to Claude, Antigravity, Codex or Opencode$/);
    await page.getByRole('button', { name: /^Model/ }).click();
    await agentSheet.getByRole('heading', { name: "Pi's model" }).waitFor();
    await agentSheet.locator('.sf-row').first().waitFor();
    await choose(page, agentSheet, 'Model', 'anthropic/claude-sonnet-5-5');
    await choose(page, agentSheet, 'Reasoning effort', 'xhigh');
    await settle();
    await page.screenshot({ path: `${shots}/${name}-pi-model-sheet.png` });
    await agentSheet.getByRole('button', { name: 'Change', exact: true }).click();
    assert.equal(await lastSent(11), '/model anthropic/claude-sonnet-5-5 xhigh');
    // Four agents to switch to: each keeps its whole name on a phone.
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: /^Switch agent/ }).click();
    await agentSheet.getByRole('radio', { name: 'OpenCode' }).waitFor();
    await settle();
    for (const b of await agentSheet.locator('.agent-pick button').all()) assert.ok(await b.evaluate((el) => { const r = document.createRange(); r.selectNodeContents(el); return r.getClientRects().length === 1 && el.scrollWidth <= el.clientWidth; }), `${await b.textContent()} is squeezed onto more than one line`);
    assert.ok(await agentSheet.locator('.agent-pick').evaluate((el) => el.scrollWidth <= el.clientWidth && [...el.children].every((b) => b.offsetLeft - el.offsetLeft + b.offsetWidth <= el.offsetWidth)), 'the agents fit the sheet');
    await page.screenshot({ path: `${shots}/${name}-switch-four.png` });
    await page.keyboard.press('Escape');

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
    assert.equal(await lastSent(12), '/switch claude');
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'overflow');

    // Stopped on its screen: said in amber under the conversation, not as
    // the faint "ready" line; the report it was asked to buzz about reads
    // as important, the other does not.
    projectSettings = true;
    await page.goto(base + '/t/blocked');
    const waiting = page.locator('.activity-waiting');
    await waiting.getByText('Claude is stopped on a prompt on its screen').waitFor();
    assert.match(await waiting.getAttribute('class'), /\bblocked\b/);
    const reports = page.locator('.relay-report');
    await reports.nth(1).waitFor();
    assert.equal(await reports.nth(0).locator('.important-chip').count(), 0);
    await reports.nth(1).locator('.important-chip', { hasText: 'Important' }).waitFor();
    assert.match(await reports.nth(1).getAttribute('class'), /\bimportant\b/);
    await settle();
    await page.screenshot({ path: `${shots}/${name}-blocked-thread.png` });
    // In the inbox, the row says so under its summary, in amber; a relay
    // that is only ready says nothing there.
    await page.goto(base + '/');
    const row = page.locator('.thread-row', { hasText: 'Billing export' });
    await row.locator('.foot-text.live.blocked', { hasText: 'Claude is stopped on a prompt on its screen' }).waitFor();
    assert.equal(await page.locator('.thread-row .foot-text.live').count(), 1, 'only the blocked row');
    assert.ok(!(await row.getAttribute('class')).includes('working'), 'a blocked agent is not working');
    await settle();
    await page.screenshot({ path: `${shots}/${name}-blocked-inbox.png` });
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
