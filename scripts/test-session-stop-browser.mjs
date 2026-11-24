#!/usr/bin/env node
// Stopping a running session from its thread, in Chromium (phone and
// desktop) against mocked APIs. The header says the session is active; the
// thread menu and the relay's controls offer the stop the agent publishes
// (session.stop on its session resource); confirming shows the agent's own
// description, queues exactly one session.stop command, and once the agent
// confirms the thread no longer reads as active. The stop is quiet: the mark
// reads "Stopping…" and no toast appears. "Stop and archive" in the thread
// menu archives the thread at once and stops the session behind it; a stop
// the agent refuses brings the thread back. A thread whose session is not
// running offers no stop.
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
const description = 'Ends this eagent session now, like Stop in eagent’s web UI. Claude Code stops with it. A reply here resumes the session while eagent serve or connector run is up.';
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
const until = async (what, done) => {
  for (let i = 0; i < 100; i++) {
    if (done()) return;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw Error('timed out waiting for ' + what);
};

// One page over its own mocked account: `run` has a running session that
// offers a stop, `idle` has none. The agent answers a stop only once
// `s.answer` is set ('succeeded' or 'failed'), so each step is checked while
// the stop is still on its way.
async function open(width, height) {
  const s = {
    threads: { run: thread('run', 'Search ranking', { relay: { name: 'claude', agent: 'claude', label: 'Claude Code · Opus', state: 'idle' } }), idle: thread('idle', 'Old work') },
    live: [{ thread_id: 'run', resource_id: 'r-run', connector_id: 'c1', label: resource.label, stop: { label: 'Stop session', description } }],
    commands: [], patches: [], answer: '', answered: false,
  };
  const page = await browser.newPage({ viewport: { width, height }, deviceScaleFactor: 1, serviceWorkers: 'block' });
  page.on('pageerror', (e) => errors.push(String(e)));
  await page.route('**/*', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    if (url.origin !== base) throw Error('Unexpected outbound request ' + url.origin);
    if (!url.pathname.startsWith('/api/')) return route.continue();
    const p = url.pathname.replace('/api/v1', '');
    const json = (v, status = 200) => route.fulfill({ status, json: v, headers: { date: new Date().toUTCString() } });
    const counts = { pending_questions: 0, unread_threads: 0, attention: 0 };
    if (p === '/auth/status') return json({ authenticated: true, user, attachments_enabled: true, features: [], push_enabled: false, signup: 'open', version: 'test' });
    if (p === '/me') return json({ auth: 'session', user, counts, features: [], push_enabled: false, attachments_enabled: true, version: 'test' });
    if (p === '/counts') return json({ counts });
    if (p === '/threads') return json({ threads: Object.values(s.threads).filter((t) => !!t.archived_at === url.searchParams.has('archived')) });
    if (p === '/questions') return json({ questions: [] });
    if (p === '/live-sessions') return json({ sessions: s.live });
    if (p === '/events') return new Promise(() => {});
    const t = p.match(/^\/threads\/(\w+)$/);
    if (t && req.method() === 'PATCH') {
      const body = req.postDataJSON();
      s.patches.push({ id: t[1], body });
      if (typeof body.archived === 'boolean') s.threads[t[1]].archived_at = body.archived ? now() : null;
      return json({ thread: s.threads[t[1]] });
    }
    if (t) return json({ thread: s.threads[t[1]] });
    if (p.endsWith('/messages')) return json({ messages: [], has_more: false });
    if (p.endsWith('/questions') || p.endsWith('/artifacts')) return json({ questions: [], artifacts: [] });
    if (p.endsWith('/settings')) return json({ resources: [] });
    if (p === '/settings-resources/r-run') return json({ resource, connector, online: true, website: null });
    if (p === '/settings-resources/r-run/commands' && req.method() === 'POST') {
      const body = req.postDataJSON();
      s.commands.push(body);
      return json({ command: { id: 'cmd1', resource_id: 'r-run', status: 'queued', proposal: body.proposal, proposal_sha256: 'x', result: null, expires_at: now(), attempts: 0 }, created: true }, 202);
    }
    if (p === '/commands/cmd1') {
      // The agent stops, and its session drops off the running list; or it
      // says why it did not, and the session runs on.
      const status = s.answer || 'executing';
      if (status === 'succeeded') s.live = [];
      if (s.answer) s.answered = true;
      return json({ command: { id: 'cmd1', resource_id: 'r-run', status, proposal: s.commands[0]?.proposal, proposal_sha256: 'x', result: status === 'succeeded' ? { message: 'Stopping this session.' } : status === 'failed' ? { message: 'A deploy is running and cannot be cut short' } : null, expires_at: now(), attempts: 1 } });
    }
    return json({});
  });
  await page.addInitScript(() => {
    try { localStorage.setItem('fc.setup.dismissed', '1'); } catch {}
    // Every toast that ever shows, so "no toast" covers the whole flow.
    window.__toasts = [];
    new MutationObserver((records) => {
      for (const r of records) for (const n of r.addedNodes) {
        if (n.nodeType !== 1) continue;
        for (const el of n.classList.contains('toast') ? [n] : n.querySelectorAll('.toast')) window.__toasts.push(el.textContent);
      }
    }).observe(document, { childList: true, subtree: true });
  });
  const toasts = () => page.evaluate(() => window.__toasts);
  const row = (title) => page.locator('.thread-row').filter({ hasText: title });
  return { page, s, toasts, row };
}
const stopProposal = { operation: 'session.stop', schema_version: 'eagent.runtime-settings/v1', expected_version: 's1', generation: 'g1', parameters: {} };

try {
  for (const [name, width, height] of [['phone', 390, 844], ['desktop', 1280, 900]]) {
    // Stop session, from the thread menu.
    {
      const { page, s, toasts } = await open(width, height);
      // A thread whose session is not running: no mark, no stop.
      await page.goto(base + '/t/idle');
      await page.locator('.topbar-title').getByText('Old work').waitFor();
      assert.equal(await page.locator('.topbar-title .session-mark').count(), 0);
      await page.getByRole('button', { name: 'Thread options' }).click();
      await page.getByRole('button', { name: /Delete thread/ }).waitFor();
      assert.equal(await page.getByRole('button', { name: /Stop session/ }).count(), 0);
      assert.equal(await page.getByRole('button', { name: 'Stop and archive' }).count(), 0);
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
      await page.getByRole('button', { name: 'Stop and archive' }).waitFor();
      await page.waitForTimeout(500);
      await page.screenshot({ path: `${shots}/${name}-thread-menu.png` });
      await page.getByRole('button', { name: 'Stop session' }).click();
      const sheet = page.locator('.confirm');
      await sheet.getByText('Stop session?').waitFor();
      assert.match(await sheet.textContent(), /Claude Code stops with it\./);
      await page.waitForTimeout(500);
      await page.screenshot({ path: `${shots}/${name}-stop-confirm.png` });
      await sheet.getByRole('button', { name: 'Stop session' }).click();

      // While the agent answers, the mark says so, and the menu already has
      // the shape it keeps: nothing moves under a finger reaching for Archive.
      await page.locator('.topbar-title .session-mark.stopping').waitFor();
      assert.equal((await page.locator('.topbar-title .session-mark').textContent()).trim(), 'Stopping…');
      await page.getByRole('button', { name: 'Thread options' }).click();
      await page.getByRole('button', { name: /^Archive$/ }).waitFor();
      assert.equal(await page.getByRole('button', { name: /Stop session/ }).count(), 0);
      assert.equal(await page.getByRole('button', { name: 'Stop and archive' }).count(), 0);
      await page.waitForTimeout(500);
      await page.screenshot({ path: `${shots}/${name}-stopping.png` });
      await page.keyboard.press('Escape');
      s.answer = 'succeeded';
      await page.locator('.topbar-title .session-mark').waitFor({ state: 'detached' });
      assert.equal(s.commands.length, 1);
      assert.deepEqual(s.commands[0].proposal, stopProposal);
      assert.equal(s.commands[0].ttl_seconds, 60);
      assert.deepEqual(s.patches, []);
      assert.deepEqual(await toasts(), [], 'a stop that works says nothing');
      await page.screenshot({ path: `${shots}/${name}-stopped.png` });
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'overflow');
      await page.close();
    }

    // Stop and archive, from the thread menu: archived at once, back in the
    // inbox, and the stop goes on behind it.
    {
      const { page, s, toasts, row } = await open(width, height);
      await page.goto(base + '/');
      await row('Search ranking').locator('.session-mark').waitFor();
      await row('Search ranking').click();
      await page.locator('.topbar-title .session-mark').waitFor();
      await page.getByRole('button', { name: 'Thread options' }).click();
      await page.getByRole('button', { name: 'Stop and archive' }).click();
      const sheet = page.locator('.confirm');
      await sheet.getByText('Stop and archive?').waitFor();
      assert.match(await sheet.textContent(), /Claude Code stops with it\./);
      await page.waitForTimeout(500);
      await page.screenshot({ path: `${shots}/${name}-stop-archive-confirm.png` });
      await sheet.getByRole('button', { name: 'Stop and archive' }).click();
      await page.waitForURL(base + '/');
      await row('Old work').waitFor();
      await row('Search ranking').waitFor({ state: 'detached' });
      assert.deepEqual(s.patches, [{ id: 'run', body: { archived: true } }]);
      await until('the stop command', () => s.commands.length === 1);
      assert.deepEqual(s.commands[0].proposal, stopProposal);
      s.answer = 'succeeded';
      await until('the agent’s answer', () => s.answered);
      await page.waitForTimeout(600);
      assert.equal(await row('Search ranking').count(), 0);
      assert.equal(s.commands.length, 1);
      assert.equal(s.patches.length, 1);
      assert.deepEqual(await toasts(), [], 'stop and archive says nothing when it works');
      await page.screenshot({ path: `${shots}/${name}-stop-archived.png` });
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'overflow');
      await page.close();
    }

    // The agent refuses the stop: the session runs on, so the thread comes
    // back to the inbox and the reason is said once.
    {
      const { page, s, toasts, row } = await open(width, height);
      await page.goto(base + '/t/run');
      await page.locator('.topbar-title .session-mark').waitFor();
      await page.getByRole('button', { name: 'Thread options' }).click();
      await page.getByRole('button', { name: 'Stop and archive' }).click();
      await page.locator('.confirm').getByRole('button', { name: 'Stop and archive' }).click();
      await page.waitForURL(base + '/');
      await row('Search ranking').waitFor({ state: 'detached' });
      s.answer = 'failed';
      await row('Search ranking').waitFor();
      assert.equal((await row('Search ranking').locator('.session-mark').textContent()).trim(), 'Claude');
      assert.deepEqual(s.patches, [{ id: 'run', body: { archived: true } }, { id: 'run', body: { archived: false } }]);
      assert.deepEqual(await toasts(), ['A deploy is running and cannot be cut short. The thread is back in your inbox.']);
      await page.screenshot({ path: `${shots}/${name}-stop-refused.png` });
      await page.close();
    }
  }

  assert.deepEqual(errors, []);
  console.log(`PASS session stop: active mark, quiet stop from the thread menu and relay controls, stop and archive from the thread menu, one session.stop command each; screenshots in ${shots}`);
} finally {
  await browser.close();
  await server.close();
}
