#!/usr/bin/env node
import assert from 'node:assert/strict';
import { readFile, mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createServer } from '../web/node_modules/vite/dist/node/index.js';
import react from '../web/node_modules/@vitejs/plugin-react/dist/index.js';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const {chromium}=await import(pathToFileURL(process.env.FINALECHAT_PLAYWRIGHT_MODULE || '/tmp/finalechat-browser-tools/node_modules/playwright/index.mjs').href);
const fixture=process.env.EAGENT_SETTINGS_FIXTURE;
if(!fixture) throw new Error('Set EAGENT_SETTINGS_FIXTURE to the archive directory produced by eagent/scripts/browser-fixture.');
const manifest=JSON.parse(await readFile(path.join(fixture,'manifest.json'),'utf8'));
const resource=JSON.parse(await readFile(path.join(fixture,'settings/state.json'),'utf8'));
Object.assign(resource,{id:'resource',connector_id:'connector',scope:'project',generation:''});
const website=await readFile(path.join(fixture,'settings/index.html'),'utf8');
const revision={id:'revision',artifact_id:'artifact',manifest};
const view={resource,online:true,connector:{id:'connector',name:'eagent · SoundboxingWork',provider:'eagent',state:'active',grants:[],requested_grants:[]}};
const server=await createServer({root:path.join(root,'web'),configFile:false,plugins:[react()],server:{host:'127.0.0.1',port:0,strictPort:false}});
await server.listen();const base=server.resolvedUrls.local[0].replace(/\/$/,'');
const browser=await chromium.launch({headless:true});
const errors=[];const commands=[];
await mkdir('/tmp/course-settings-screenshots',{recursive:true});
try {
 for(const [name,width,height] of [['phone',390,844],['desktop',1440,1000]]) {
  const page=await browser.newPage({viewport:{width,height},deviceScaleFactor:1});
  page.on('pageerror',e=>errors.push(String(e)));
  await page.route('**/*',async route=>{
   const url=new URL(route.request().url());const p=url.pathname;
   if(url.origin!==base) throw Error('Unexpected outbound request '+url.origin);
   const json=v=>route.fulfill({json:v});
   if(!p.startsWith('/api/')) return route.continue();
   if(p==='/api/v1/threads/thread') return json({thread:{id:'thread',external_id:'eagent:1788827736789',title:'SoundboxingWork',agent:'eagent',meta:{},unread_count:0,pending_count:0,created_at:new Date().toISOString()}});
   if(p==='/api/v1/threads/thread/settings') return json({resources:[{id:'resource',label:'SoundboxingWork',scope:'project',provider:'eagent',available:true,artifact_id:'artifact',revision_id:'revision'}]});
   if(p.endsWith('/artifacts')) return json({artifacts:[]});
   if(p.endsWith('/settings-resources')) return json({resources:[]});
   if(p.endsWith('/messages')) return json({messages:[],has_more:false});
   if(p.endsWith('/questions')) return json({questions:[]});
   if(p==='/api/v1/settings-resources/resource') return json(view);
   if(p.endsWith('/settings-surface')) return json({lease:{id:'lease',resource_id:'resource',generation:'',expires_at:'2030-01-01T00:00:00Z'}});
   if(p.endsWith('/revisions/revision')) return json({revision});
   if(p.endsWith('/preview')) return route.fulfill({contentType:'text/html',body:website,headers:{'Content-Security-Policy':"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'none'"}});
   if(p.includes('/files/')) {
    const file=p.split('/files/')[1];assert.ok(manifest.files.some(f=>f.path===file));
    const bytes=await readFile(path.join(fixture,file));
    return route.fulfill({body:bytes.subarray(Number(url.searchParams.get('offset')),Number(url.searchParams.get('offset'))+Number(url.searchParams.get('length')))});
   }
   if(p.endsWith('/commands')) {
    const body=route.request().postDataJSON();commands.push(body);
    assert.equal(body.surface_lease_id,'lease');
    for(const e of body.proposal.edits || []) {if(e.op==='set') {resource.snapshot.saved[e.key]=e.value;resource.snapshot.effective[e.key]=e.value;}else delete resource.snapshot.saved[e.key];}
    resource.snapshot.version+='-saved';
    return json({created:true,command:{id:'command',resource_id:'resource',status:'succeeded',proposal:body.proposal,result:{effects:[{effective_when:'new_or_resumed_session'}]}}});
   }
   if(p.endsWith('/read')) return json({ok:true});
   throw Error('Unexpected fixture request '+p);
  });
  await page.goto(base+'/tests/artifacts.html?thread');
  await page.getByRole('button',{name:'Settings',exact:true}).click();
  const frame=page.frameLocator('iframe[title="Integration settings"]');
  await frame.getByRole('heading',{name:'Choose how your agent thinks'}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Save',exact:true}).count(),1);
  assert.equal(await frame.getByRole('button',{name:'Review project changes'}).count(),0);
  await page.screenshot({path:'/tmp/course-settings-screenshots/'+name+'.png'});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'host overflow');
  assert.equal(await frame.locator('body').evaluate(el=>el.scrollWidth>innerWidth),false,'iframe overflow');
  await frame.getByRole('button',{name:'Behavior',exact:true}).click();
  // The shared editor uses its label/field hierarchy rather than for/id pairs.
  const field=frame.locator('.field').filter({hasText:'Parallel tasks'}).locator('input').first();
  const count=await field.count();
  const input=count?field:frame.locator('input[type=number]').first();
  await input.fill(name==='phone'?'6':'7'); await input.press('Tab');
  await page.getByRole('button',{name:'Save',exact:true}).click();
  await page.getByText('Saved · new or resumed sessions',{exact:true}).waitFor();
  assert.match(page.url(),/panel=settings/);
  await page.getByRole('button',{name:'Close settings'}).click();
  await page.getByRole('button',{name:'Settings',exact:true}).waitFor();
  await page.close();
 }
 assert.equal(commands.length,2);assert.deepEqual(errors,[]);
 console.log('PASS thread → settings website → one Save on phone and desktop; screenshots in /tmp/course-settings-screenshots');
} finally {await browser.close();await server.close();}
