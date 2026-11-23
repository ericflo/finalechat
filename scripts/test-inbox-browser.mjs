#!/usr/bin/env node
// The inbox in Chromium, phone and desktop, against mocked APIs: a row is
// its title, its summary and one line for what is new (the live status does
// not replace the summary), rows carry no initials, the view chips filter,
// the order holds while the list is in use even as threads post, and a
// thread whose session is running says so (with the agent it relays to).
//
//   FINALECHAT_PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node scripts/test-inbox-browser.mjs
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createServer } from '../web/node_modules/vite/dist/node/index.js';
import react from '../web/node_modules/@vitejs/plugin-react/dist/index.js';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const { chromium } = await import(pathToFileURL(process.env.FINALECHAT_PLAYWRIGHT_MODULE || '/tmp/finalechat-browser-tools/node_modules/playwright/index.mjs').href);
const shots = process.env.FINALECHAT_SCREENSHOTS || '/tmp/finalechat-inbox';
await mkdir(shots, { recursive: true });

const ago = (min) => new Date(Date.now() - min * 60000).toISOString();
const thread = (id, title, over = {}) => ({
  id, external_id: `demo:${id}`, title, agent: 'eagent', meta: {}, created_at: ago(600), updated_at: ago(1), last_activity_at: ago(1), last_read_at: ago(0),
  archived_at: null, muted: false, preview: '', preview_sender: 'agent', unread_count: 0, pending_questions: 0, description: '', summary: '', title_pinned: false, description_pinned: false, activity: null, ...over,
});
const describe = (text) => ({ description: text, summary: text });
const makeThreads = () => [
  thread('a', 'Checkout race fix', { ...describe('Cart totals raced with coupon updates. Fix is in; the suite is running.'), last_activity_at: ago(1), preview: 'Running the suite now.',
    activity: { text: 'Running the test suite', kind: 'tool', at: ago(0.1), since: ago(12.2), expires_at: new Date(Date.now() + 600000).toISOString() } }),
  thread('b', 'Release 1.2', { ...describe('Shipping 1.2 to production. Staging is green; waiting on approval.'), last_activity_at: ago(5), pending_questions: 1, preview: 'Promote to production?', preview_sender: 'question' }),
  thread('c', 'Docs site search', { ...describe('Search for the docs site, built on the static index. Indexing is done.'), last_activity_at: ago(10), last_read_at: ago(30), unread_count: 2, preview: 'Indexed 412 pages.' }),
  thread('d', 'Billing export', { last_activity_at: ago(20), preview: 'go ahead', preview_sender: 'user' }),
  thread('g', 'Search ranking', { ...describe('Claude Code is reworking the ranking weights; first pass is in review.'), last_activity_at: ago(30),
    meta: { relay: { name: 'claude', agent: 'claude', label: 'Claude Code · Opus', state: 'idle' } } }),
  thread('h', 'Flaky upload test', { ...describe('Codex chased a flaky upload test; it stopped after the fix landed.'), last_activity_at: ago(40),
    meta: { relay: { name: 'codex', agent: 'codex', label: 'Codex', state: 'working' } } }),
];
// Running now: the working thread, an idle one, and one relaying to Claude
// Code. The Codex thread's relay meta is stale: its session is not running.
const liveSessions = ['a', 'd', 'g'].map((id) => ({ thread_id: id, resource_id: 'r-' + id, connector_id: 'c1', label: 'Session ' + id, stop: { label: 'Stop session' } }));
const archived = [thread('e', 'Old migration', { ...describe('Moved the ledger tables. Finished in March.'), archived_at: ago(900), last_activity_at: ago(900) })];
const user = { id: 'u1', email: 'u@example.com', display_name: 'U', settings: { notify_all_messages: false, remote_mode: false }, created_at: ago(9000) };
const question = { id: 'q1', thread_id: 'b', prompt: 'Promote to production?', options: [{ label: 'Ship it' }, { label: 'Hold' }], allow_freeform: true, multi_select: false, status: 'pending', answer: null, meta: {}, created_at: ago(5), answered_at: null, expires_at: new Date(Date.now() + 3600000).toISOString() };

const server = await createServer({ root: path.join(root, 'web'), configFile: false, plugins: [react()], server: { host: '127.0.0.1', port: 0, strictPort: false }, logLevel: 'warn' });
await server.listen();
const base = server.resolvedUrls.local[0].replace(/\/$/, '');
const browser = await chromium.launch({ headless: true });
const errors = [];
try {
  for (const [name, width, height] of [['phone', 390, 844], ['desktop', 1280, 900]]) {
    let threads = makeThreads();
    let lists = 0;
    const page = await browser.newPage({ viewport: { width, height }, deviceScaleFactor: 1, serviceWorkers: 'block' });
    page.on('pageerror', (e) => errors.push(String(e)));
    await page.route('**/*', async (route) => {
      const url = new URL(route.request().url());
      if (url.origin !== base) throw Error('Unexpected outbound request ' + url.origin);
      if (!url.pathname.startsWith('/api/')) return route.continue();
      const p = url.pathname.replace('/api/v1', '');
      const json = (v) => route.fulfill({ json: v, headers: { date: new Date().toUTCString() } });
      if (p === '/auth/status') return json({ authenticated: true, user, attachments_enabled: true, features: [], push_enabled: false, signup: 'open', version: 'test' });
      if (p === '/me') return json({ auth: 'session', user, counts: { pending_questions: 1, unread_threads: 1, attention: 2 }, features: [], push_enabled: false, attachments_enabled: true, version: 'test' });
      if (p === '/threads') {
        if (url.searchParams.get('archived')) return json({ threads: archived });
        lists++;
        return json({ threads });
      }
      if (p === '/questions') return json({ questions: [question] });
      if (p === '/live-sessions') return json({ sessions: liveSessions });
      if (p === '/events') return new Promise(() => {}); // held open, like the real stream
      return json({ connectors: [], resources: [], artifacts: [], tokens: [], subscriptions: [] });
    });
    await page.addInitScript(() => { try { localStorage.setItem('fc.setup.dismissed', '1'); } catch {} });
    await page.goto(base + '/');
    const rows = page.locator('.thread-row');
    await rows.first().waitFor();
    const titles = () => page.locator('.thread-row .thread-title').allTextContents();
    const row = (title) => rows.filter({ has: page.locator('.thread-title', { hasText: new RegExp('^' + title + '$') }) });

    // Questions first, then newest activity. No initials, and no agent name
    // when every thread has the same one.
    assert.deepEqual(await titles(), ['Release 1.2', 'Checkout race fix', 'Docs site search', 'Billing export', 'Search ranking', 'Flaky upload test']);
    assert.equal(await page.locator('.thread-row .avatar').count(), 0);
    assert.equal(await page.locator('.thread-row .thread-agent').count(), 0);
    assert.equal(await page.locator('.question .avatar').count(), 0);

    // A working agent's status sits under the summary, with how long.
    const working = row('Checkout race fix');
    assert.match(await working.getAttribute('class'), /\bworking\b/);
    assert.equal((await working.locator('.thread-summary').textContent()).trim(), 'Cart totals raced with coupon updates. Fix is in; the suite is running.');
    assert.equal((await working.locator('.live-text').textContent()).trim(), 'Running the test suite');
    assert.equal((await working.locator('.foot-time').textContent()).trim(), '12m');
    // A waiting question and an unread message each get the foot line.
    const needs = row('Release 1.2');
    assert.match(await needs.getAttribute('class'), /\bneeds-you\b/);
    assert.match(await needs.locator('.thread-foot').textContent(), /^Asked:\s*Promote to production\?/);
    const unread = row('Docs site search');
    assert.match(await unread.getAttribute('class'), /\bunread\b/);
    assert.match(await unread.locator('.thread-foot').textContent(), /Indexed 412 pages\./);
    assert.equal((await unread.locator('.unread-count').textContent()).trim(), '2');
    // With no summary yet, the latest message stands in for it.
    assert.match(await row('Billing export').locator('.thread-summary.is-preview').textContent(), /^You:\s*go ahead/);
    // Running sessions are marked beside the time; a relay names its agent.
    await row('Billing export').locator('.session-mark').waitFor();
    assert.equal((await row('Billing export').locator('.session-mark').textContent()).trim(), 'Active');
    assert.equal((await row('Checkout race fix').locator('.session-mark').textContent()).trim(), 'Active');
    assert.equal((await row('Search ranking').locator('.session-mark').textContent()).trim(), 'Claude');
    assert.match(await row('Search ranking').locator('.session-mark').getAttribute('aria-label'), /relaying to Claude Code · Opus \(idle\)/);
    assert.equal(await row('Flaky upload test').locator('.session-mark').count(), 0, 'stale relay meta without a running session');
    assert.equal(await row('Release 1.2').locator('.session-mark').count(), 0);
    await page.screenshot({ path: `${shots}/${name}-inbox.png` });

    // The views.
    const chip = (label) => page.locator('.view-chip', { hasText: new RegExp('^' + label) });
    await chip('Working').click();
    assert.deepEqual(await titles(), ['Checkout race fix']);
    assert.equal((await chip('Active').locator('.n').textContent()).trim(), '3');
    await chip('Active').click();
    assert.deepEqual(await titles(), ['Checkout race fix', 'Billing export', 'Search ranking']);
    await page.screenshot({ path: `${shots}/${name}-inbox-active.png` });
    await chip('Unread').click();
    assert.deepEqual(await titles(), ['Release 1.2', 'Docs site search']);
    await chip('Archived').click();
    await row('Old migration').waitFor();
    assert.deepEqual(await titles(), ['Old migration']);
    await chip('All').click();
    assert.deepEqual(await titles(), ['Release 1.2', 'Checkout race fix', 'Docs site search', 'Billing export', 'Search ranking', 'Flaky upload test']);

    // The list was just used. A thread that posts now keeps its place, and
    // a thread new to the list goes on top.
    threads = threads.map((t) => (t.id === 'd' ? { ...t, last_activity_at: new Date().toISOString(), unread_count: 1, preview: 'Export finished.', preview_sender: 'agent' } : t));
    threads.push(thread('f', 'Invoice template', { ...describe('New invoice template for EU customers. Just started.'), last_activity_at: new Date().toISOString() }));
    const before = lists;
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
    await row('Invoice template').waitFor();
    assert.ok(lists > before, 'the inbox reloaded');
    await row('Billing export').locator('.unread-count').waitFor();
    assert.deepEqual(await titles(), ['Invoice template', 'Release 1.2', 'Checkout race fix', 'Docs site search', 'Billing export', 'Search ranking', 'Flaky upload test']);

    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'overflow');
    await page.screenshot({ path: `${shots}/${name}-inbox-held.png` });
    await page.close();
  }
  assert.deepEqual(errors, []);
  console.log(`PASS inbox: title, summary and what is new on every row; views; running sessions marked; order held while in use; screenshots in ${shots}`);
} finally {
  await browser.close();
  await server.close();
}
