#!/usr/bin/env node
// Stopping a running session from its thread, in Chromium (phone and
// desktop) against mocked APIs. The header says the session is active; the
// thread menu and the relay's controls offer the stop the agent publishes
// (session.stop on its session resource); confirming shows the agent's own
// description, queues exactly one session.stop command, and once the agent
// confirms the thread no longer reads as active. A thread whose session is
// not running offers no stop.
//
//   FINALECHAT_PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node scripts/test-session-stop-browser.mjs
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createServer } from '../web/node_modules/vite/dist/node/index.js';
import react from '../web/node_modules/@vitejs/plugin-react/dist/index.js';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const { chromium } = await import(pathToFileURL(process.env.FINALECHAT_PLAYWRIGHT_MODULE || '/tmp/finalechat-browser-tools/node_modules/playwright/index.mjs').href);
const shots = process.env.FINALECHAT_SCREENSHOTS || '/tmp/finalechat-session-stop';
await mkdir(shots, { recursive: true });

const now = () => new Date().toISOString();
const ago = (min) => new Date(Date.now() - min * 60000).toISOString();
const description = 'Ends this session now, as eagent’s Stop does. A reply here resumes it. Claude Code keeps running in its own terminal; stop it from its controls.';
const thread = (id, title, meta = {}) => ({
  id, external_id: `eagent:${id}`, title, agent: 'eagent', meta, created_at: ago(600), updated_at: ago(1), last_activity_at: ago(1), last_read_at: ago(0),
  archived_at: null, muted: false, preview: 'Done with the first pass.', preview_sender: 'agent', unread_count: 0, pending_questions: 0,
  description: 'Reworking the ranking weights; first pass is in review.', summary: 'Reworking the ranking weights; first pass is in review.', title_pinned: false, description_pinned: false, activity: null,
});
const user = { id: 'u1', email: 'u@example.com', display_name: 'U', settings: { notify_all_messages: false, remote_mode: false }, created_at: ago(9000) };
const resource = {
  id: 'r-run', connector_id: 'c1', key: 'project-x-session-1', label: 'Development · Session 1', scope: 'session', generation: 'g1', updated_at: now(),
  descriptor: { format: 'finalechat.settings/v1', schema_version: 'eagent.runtime-settings/v1', adapter_version: '1', fields: [],
    actions: [{ operation: 'session.stop', label: 'Stop session', description, class: 'preference', parameters: { type: 'object' } }] },
  snapshot: { version: 's1', context: 'Development · 1', saved: {}, effective: {}, runtime_known: true, details: { thread_external_id: 'eagent:run' } },
};
const connector = { id: 'c1', name: 'eagent · Development', provider: 'eagent', state: 'active', grants: [], requested_grants: [], last_seen_at: now(), expires_at: now() };

const server = await createServer({ root: path.join(root, 'web'), configFile: false, plugins: [react()], server: { host: '127.0.0.1', port: 0, strictPort: false }, logLevel: 'warn' });
await server.listen();
const base = server.resolvedUrls.local[0].replace(/\/$/, '');
const browser = await chromium.launch({ headless: true });
const errors = [];
try {
  for (const [name, width, height] of [['phone', 390, 844], ['desktop', 1280, 900]]) {
    const threads = { run: thread('run', 'Search ranking', { relay: { name: 'claude', agent: 'claude', label: 'Claude Code · Opus', state: 'idle' } }), idle: thread('idle', 'Old work') };
    let live = [{ thread_id: 'run', resource_id: 'r-run', connector_id: 'c1', label: resource.label, stop: { label: 'Stop session', description } }];
    const commands = [];
    let polls = 0;
    const page = await browser.newPage({ viewport: { width, height }, deviceScaleFactor: 1, serviceWorkers: 'block' });
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
      if (p === '/live-sessions') return json({ sessions: live });
      if (p === '/events') return new Promise(() => {});
      const t = p.match(/^\/threads\/(\w+)$/);
      if (t) return json({ thread: threads[t[1]] });
      if (p.endsWith('/messages')) return json({ messages: [], has_more: false });
      if (p.endsWith('/questions') || p.endsWith('/artifacts')) return json({ questions: [], artifacts: [] });
      if (p.endsWith('/settings')) return json({ resources: [] });
      if (p === '/settings-resources/r-run') return json({ resource, connector, online: true, website: null });
      if (p === '/settings-resources/r-run/commands' && req.method() === 'POST') {
        const body = req.postDataJSON();
        commands.push(body);
        return json({ command: { id: 'cmd1', resource_id: 'r-run', status: 'queued', proposal: body.proposal, proposal_sha256: 'x', result: null, expires_at: now(), attempts: 0 }, created: true }, 202);
      }
      if (p === '/commands/cmd1') {
        // The agent stops, and its session drops off the running list.
        if (++polls >= 2) live = [];
        return json({ command: { id: 'cmd1', resource_id: 'r-run', status: polls >= 2 ? 'succeeded' : 'executing', proposal: commands[0]?.proposal, proposal_sha256: 'x', result: polls >= 2 ? { message: 'Stopping this session.' } : null, expires_at: now(), attempts: 1 } });
      }
      return json({});
    });
    await page.addInitScript(() => { try { localStorage.setItem('fc.setup.dismissed', '1'); } catch {} });

    // A thread whose session is not running: no mark, no stop.
    await page.goto(base + '/t/idle');
    await page.locator('.topbar-title').getByText('Old work').waitFor();
    assert.equal(await page.locator('.topbar-title .session-mark').count(), 0);
    await page.getByRole('button', { name: 'Thread options' }).click();
    await page.getByRole('button', { name: /Delete thread/ }).waitFor();
    assert.equal(await page.getByRole('button', { name: /Stop session/ }).count(), 0);
    await page.keyboard.press('Escape');

    // The running one says so, and its relay's controls offer the stop too.
    await page.goto(base + '/t/run');
    await page.locator('.topbar-title .session-mark').waitFor();
    assert.equal((await page.locator('.topbar-title .session-mark').textContent()).trim(), 'Active');
    await page.screenshot({ path: `${shots}/${name}-thread-active.png` });
    await page.locator('.relay-badge').click();
    await page.getByRole('button', { name: 'Stop Claude' }).waitFor();
    assert.equal(await page.getByRole('button', { name: 'Stop session' }).count(), 1);
    await page.waitForTimeout(500); // the sheet slides in
    await page.screenshot({ path: `${shots}/${name}-relay-sheet.png` });
    await page.keyboard.press('Escape');

    // From the thread menu: the agent's words, then one stop command.
    await page.getByRole('button', { name: 'Thread options' }).click();
    await page.getByRole('button', { name: 'Stop session' }).click();
    const sheet = page.locator('.confirm');
    await sheet.getByText('Stop session?').waitFor();
    assert.match(await sheet.textContent(), /A reply here resumes it\./);
    await page.waitForTimeout(500);
    await page.screenshot({ path: `${shots}/${name}-stop-confirm.png` });
    await sheet.getByRole('button', { name: 'Stop session' }).click();
    await page.getByText('Session stopped').waitFor();
    assert.equal(commands.length, 1);
    assert.deepEqual(commands[0].proposal, { operation: 'session.stop', schema_version: 'eagent.runtime-settings/v1', expected_version: 's1', generation: 'g1', parameters: {} });
    assert.equal(commands[0].ttl_seconds, 60);
    await page.locator('.topbar-title .session-mark').waitFor({ state: 'detached' });
    await page.screenshot({ path: `${shots}/${name}-stopped.png` });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'overflow');
    await page.close();
  }
  assert.deepEqual(errors, []);
  console.log(`PASS session stop: active mark, stop in the thread menu and relay controls, one session.stop command; screenshots in ${shots}`);
} finally {
  await browser.close();
  await server.close();
}
