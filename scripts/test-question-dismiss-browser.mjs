#!/usr/bin/env node
// Dismissing a question card in Chromium, phone and desktop, against mocked
// APIs: Dismiss asks once, says nothing is sent, and then makes exactly one
// request, POST /questions/{id}/dismiss. No answer and no message is posted
// on the user's behalf, the card leaves the "needs you" list, and in the
// thread it reads as dismissed with nothing sent.
//
//   FINALECHAT_PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node scripts/test-question-dismiss-browser.mjs
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createServer } from '../web/node_modules/vite/dist/node/index.js';
import react from '../web/node_modules/@vitejs/plugin-react/dist/index.js';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const { chromium } = await import(pathToFileURL(process.env.FINALECHAT_PLAYWRIGHT_MODULE || '/tmp/finalechat-browser-tools/node_modules/playwright/index.mjs').href);
const shots = process.env.FINALECHAT_SCREENSHOTS || '/tmp/finalechat-question-dismiss';
await mkdir(shots, { recursive: true });

const ago = (min) => new Date(Date.now() - min * 60000).toISOString();
const user = { id: 'u1', email: 'u@example.com', display_name: 'U', settings: { notify_all_messages: false, remote_mode: false }, created_at: ago(9000) };
const makeThread = () => ({
  id: 'b', external_id: 'demo:b', title: 'Release 1.2', agent: 'eagent', meta: {}, created_at: ago(600), updated_at: ago(1), last_activity_at: ago(5), last_read_at: ago(0),
  archived_at: null, muted: false, preview: 'Promote to production?', preview_sender: 'question', unread_count: 0, pending_questions: 1,
  description: 'Shipping 1.2 to production. Staging is green; waiting on approval.', summary: 'Shipping 1.2 to production. Staging is green; waiting on approval.', title_pinned: false, description_pinned: false, activity: null,
});
const makeQuestion = () => ({ id: 'q1', thread_id: 'b', prompt: 'Staging is green. Promote to production?', options: [{ label: 'Ship it', description: 'Promote the same image' }, { label: 'Hold', description: 'Wait for me to look' }], allow_freeform: true, multi_select: false, status: 'pending', answer: null, meta: {}, created_at: ago(5), answered_at: null, expires_at: new Date(Date.now() + 3600000).toISOString() });
const messages = [{ id: 'm1', thread_id: 'b', sender: 'agent', origin: 'token', body: 'Staging is green on every check.', format: 'markdown', importance: 'normal', meta: {}, attachments: [], created_at: ago(6) }];

const server = await createServer({ root: path.join(root, 'web'), configFile: false, plugins: [react()], server: { host: '127.0.0.1', port: 0, strictPort: false }, logLevel: 'warn' });
await server.listen();
const base = server.resolvedUrls.local[0].replace(/\/$/, '');
const browser = await chromium.launch({ headless: true });
const errors = [];
try {
  for (const [name, width, height] of [['phone', 390, 844], ['desktop', 1280, 900]]) {
    let thread = makeThread();
    let question = makeQuestion();
    const writes = [];
    const page = await browser.newPage({ viewport: { width, height }, deviceScaleFactor: 1, serviceWorkers: 'block' });
    page.on('pageerror', (e) => errors.push(String(e)));
    await page.route('**/*', async (route) => {
      const req = route.request();
      const url = new URL(req.url());
      if (url.origin !== base) throw Error('Unexpected outbound request ' + url.origin);
      if (!url.pathname.startsWith('/api/')) return route.continue();
      const p = url.pathname.replace('/api/v1', '');
      const json = (v) => route.fulfill({ json: v, headers: { date: new Date().toUTCString() } });
      const pending = question.status === 'pending' ? 1 : 0;
      if (req.method() !== 'GET' && p !== '/threads/b/read') writes.push(`${req.method()} ${p}`);
      if (p === '/questions/q1/dismiss' && req.method() === 'POST') {
        question = { ...question, status: 'dismissed', answered_at: new Date().toISOString() };
        thread = { ...thread, pending_questions: 0 };
        return json({ question });
      }
      if (p === '/auth/status') return json({ authenticated: true, user, attachments_enabled: true, features: [], push_enabled: false, signup: 'open', version: 'test' });
      if (p === '/me') return json({ auth: 'session', user, counts: { pending_questions: pending, unread_threads: 0, attention: pending }, features: [], push_enabled: false, attachments_enabled: true, version: 'test' });
      if (p === '/counts') return json({ counts: { pending_questions: pending, unread_threads: 0, attention: pending } });
      if (p === '/threads') return json({ threads: url.searchParams.get('archived') ? [] : [thread] });
      if (p === '/threads/b') return json({ thread });
      if (p === '/threads/b/read') return json({ thread });
      if (p === '/threads/b/messages') return json({ messages, has_more: false, thread_id: 'b' });
      if (p === '/threads/b/questions') return json({ questions: [question] });
      if (p === '/questions') return json({ questions: question.status === 'pending' ? [question] : [] });
      if (p === '/live-sessions') return json({ sessions: [] });
      if (p === '/events') return new Promise(() => {}); // held open, like the real stream
      return json({ connectors: [], resources: [], artifacts: [], tokens: [], subscriptions: [] });
    });
    await page.addInitScript(() => { try { localStorage.setItem('fc.setup.dismissed', '1'); } catch {} });

    // In the inbox: the card, its Dismiss, and the one question it asks.
    await page.goto(base + '/');
    const card = page.locator('.question[data-question-id="q1"]');
    await card.waitFor();
    await card.locator('.q-foot button', { hasText: /^Dismiss$/ }).click();
    const confirm = card.locator('.q-confirm');
    await confirm.waitFor();
    assert.equal((await confirm.locator('span').first().textContent()).trim(), 'Dismiss this question? Nothing is sent to the agent.');
    await page.screenshot({ path: `${shots}/${name}-confirm.png` });
    assert.deepEqual(writes, [], 'asking to dismiss sends nothing');

    // Keep backs out; nothing was sent.
    await confirm.locator('button', { hasText: /^Keep$/ }).click();
    await confirm.waitFor({ state: 'detached' });
    assert.deepEqual(writes, []);

    // Dismiss for real: one request, and it is the dismissal.
    await card.locator('.q-foot button', { hasText: /^Dismiss$/ }).click();
    await card.locator('.q-confirm button', { hasText: /^Dismiss$/ }).click();
    await page.locator('.toast', { hasText: 'Dismissed. Nothing was sent to the agent.' }).waitFor();
    await card.waitFor({ state: 'detached' });
    assert.deepEqual(writes, ['POST /questions/q1/dismiss'], 'a dismissal is the only thing sent: no answer, no message');
    await page.screenshot({ path: `${shots}/${name}-dismissed-inbox.png` });

    // In the thread the card is closed and says what happened.
    await page.goto(base + '/t/b');
    const closed = page.locator('.question[data-question-id="q1"]');
    await closed.waitFor();
    assert.match(await closed.getAttribute('class'), /\bresolved\b/);
    assert.equal((await closed.locator('.q-eyebrow').textContent()).trim(), 'Dismissed');
    assert.equal((await closed.locator('.q-answer.gone').textContent()).trim(), 'You dismissed this question. Nothing was sent.');
    assert.equal(await closed.locator('.q-option').count(), 0);
    assert.deepEqual(writes, ['POST /questions/q1/dismiss']);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'overflow');
    await page.screenshot({ path: `${shots}/${name}-dismissed-thread.png` });
    await page.close();
  }
  assert.deepEqual(errors, []);
  console.log(`PASS question dismiss: one confirmation, one request, no answer or message sent; screenshots in ${shots}`);
} finally {
  await browser.close();
  await server.close();
}
