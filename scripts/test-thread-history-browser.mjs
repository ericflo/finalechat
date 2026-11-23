#!/usr/bin/env node
// "Load earlier messages" in Chromium, phone and desktop, against mocked
// APIs: a long thread whose questions sit near its start. Every page the
// button brings in lands directly under it, so the top of the conversation
// moves each time until the first message shows. An answered question waits
// for its page and then sits in time order; a pending one stays in view
// above the button until its page arrives.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createServer } from '../web/node_modules/vite/dist/node/index.js';
import react from '../web/node_modules/@vitejs/plugin-react/dist/index.js';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const {chromium}=await import(pathToFileURL(process.env.FINALECHAT_PLAYWRIGHT_MODULE || '/tmp/finalechat-browser-tools/node_modules/playwright/index.mjs').href);
const shots=process.env.FINALECHAT_SCREENSHOTS || '/tmp/finalechat-thread-history';
await mkdir(shots,{recursive:true});

const start=Date.parse('2026-11-22T11:00:00Z');
const at=s=>new Date(start+s*1000).toISOString();
// 357 messages, 30s apart: the first page holds the newest 100, three more
// pages reach back to the first.
const total=357;
const mid=i=>`m${String(i).padStart(4,'0')}`;
const messages=Array.from({length:total},(_,i)=>({id:mid(i),thread_id:'thread',sender:i%2?'user':'agent',origin:'token',body:`Message ${i}`,format:'text',importance:'normal',meta:{},created_at:at(i*30),attachments:[]}));
const question=(id,i,status)=>({id,thread_id:'thread',prompt:`Question ${id}`,options:[{id:'yes',label:'Yes'}],allow_freeform:false,multi_select:false,status,answer:status==='answered'?{selected:['yes']}:null,meta:{},created_at:at(i*30+10),answered_at:status==='answered'?at(i*30+20):null,expires_at:null});
// Between messages 20 and 21, and 40 and 41: both older than every message
// on the first three pages.
const questions=[question('q-answered',20,'answered'),question('q-pending',40,'pending')];

const server=await createServer({root:path.join(root,'web'),configFile:false,plugins:[react()],server:{host:'127.0.0.1',port:0,strictPort:false},logLevel:'warn'});
await server.listen();const base=server.resolvedUrls.local[0].replace(/\/$/,'');
const browser=await chromium.launch({headless:true});
const errors=[];
// The conversation as rendered, top to bottom: questions, messages and the
// button, by id.
const layout=page=>page.$$eval('.messages [data-question-id], .messages [id^="message-"], .messages > button',els=>els.map(e=>e.dataset.questionId?`q:${e.dataset.questionId}`:e.id?e.id.slice(8):e.textContent.trim()==='Load earlier messages'||e.textContent.trim()==='Loading…'?'button':`other:${e.textContent.trim()}`));
try {
 for(const [name,width,height] of [['phone',390,844],['desktop',1440,1000]]) {
  const page=await browser.newPage({viewport:{width,height},deviceScaleFactor:1});
  page.on('pageerror',e=>errors.push(String(e)));
  await page.route('**/*',async route=>{
   const url=new URL(route.request().url());const p=url.pathname;
   if(url.origin!==base) throw Error('Unexpected outbound request '+url.origin);
   const json=v=>route.fulfill({json:v});
   if(!p.startsWith('/api/')) return route.continue();
   if(p==='/api/v1/threads/thread') return json({thread:{id:'thread',external_id:'eagent:1',title:'Long thread',agent:'eagent',meta:{},unread_count:0,pending_questions:1,last_read_at:at(total*30),created_at:at(0)}});
   if(p==='/api/v1/threads/thread/messages') {
    const limit=Number(url.searchParams.get('limit')||100);const before=url.searchParams.get('before');
    assert.equal(url.searchParams.get('after'),null);
    const end=before?messages.findIndex(m=>m.id===before):total;assert.ok(end>=0);
    const from=Math.max(0,end-limit);
    return json({messages:messages.slice(from,end),has_more:from>0});
   }
   if(p==='/api/v1/threads/thread/questions') return json({questions});
   if(p==='/api/v1/threads/thread/settings') return json({resources:[]});
   if(p.endsWith('/artifacts')) return json({artifacts:[]});
   if(p.endsWith('/read')) return json({ok:true});
   throw Error('Unexpected fixture request '+p);
  });
  await page.goto(base+'/tests/artifacts.html?thread');
  await page.locator(`#message-${mid(total-1)}`).waitFor();
  let order=await layout(page);
  // Newest page only: the pending question is held above the button, the
  // answered one waits for its page.
  assert.deepEqual(order.slice(0,3),['q:q-pending','button',mid(total-100)],`${name}: first page ${order.slice(0,4)}`);
  assert.ok(!order.includes('q:q-answered'),`${name}: an answered question from an unloaded page shows`);
  for(let loaded=100;loaded<total;loaded+=100) {
   // Scrolled up to the button, as a reader would be.
   await page.evaluate(()=>scrollTo(0,0));
   const prev=`#message-${mid(total-loaded)}`;
   const before=await page.$eval(prev,e=>e.getBoundingClientRect().top);
   await page.getByRole('button',{name:'Load earlier messages'}).click();
   const oldest=Math.max(0,total-loaded-100);
   await page.locator(`#message-${mid(oldest)}`).waitFor({state:'attached'});
   await page.waitForFunction(()=>![...document.querySelectorAll('.messages > button')].some(b=>b.textContent.trim()==='Loading…'));
   order=await layout(page);
   // The new page sits right under the button (or at the top once the
   // first message is in), so scrolling up shows something new each time.
   const top=order.filter(k=>k!=='q:q-pending'||oldest<=40);
   const expected=oldest>0?['button',mid(oldest)]:[mid(oldest)];
   assert.deepEqual(top.slice(0,expected.length),expected,`${name}: after loading to ${oldest}, top is ${order.slice(0,4)}`);
   // The reader stays where they were: what was the oldest message does
   // not move on screen, and the new page waits above it.
   const after=await page.$eval(prev,e=>e.getBoundingClientRect().top);
   assert.ok(Math.abs(after-before)<2,`${name}: anchor moved ${before} -> ${after} after loading to ${oldest}`);
  }
  assert.ok(!order.includes('button'),`${name}: button still shown with everything loaded`);
  // Every question in time order once its page is in, the pending one too.
  const pos=k=>order.indexOf(k);
  assert.ok(pos('m0020')<pos('q:q-answered')&&pos('q:q-answered')<pos('m0021'),`${name}: answered question out of order`);
  assert.ok(pos('m0040')<pos('q:q-pending')&&pos('q:q-pending')<pos('m0041'),`${name}: pending question out of order`);
  assert.equal(order.filter(k=>k==='q:q-pending').length,1,`${name}: pending question shown twice`);
  await page.evaluate(()=>scrollTo(0,0));
  await page.screenshot({path:path.join(shots,`thread-history-${name}.png`)});
  await page.close();
 }
 assert.deepEqual(errors,[]);
 console.log(`PASS thread history: each page lands under the button, questions wait for their page; screenshots in ${shots}`);
} finally {
 await browser.close();await server.close();
}
