#!/usr/bin/env node
// The thread settings panel in Chromium, phone and desktop, against mocked
// APIs: it opens on the running session's own settings in FinaleChat's form,
// shows project defaults only when chosen, says plainly when the session is
// not running, and submits exactly one command per Save. With
// EAGENT_SETTINGS_FIXTURE (eagent's `go run ./scripts/browser-fixture DIR`,
// then DIR/archive) it also opens eagent's own editor page as the optional
// alternative and saves through it.
import assert from 'node:assert/strict';
import { readFile, mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createServer } from '../web/node_modules/vite/dist/node/index.js';
import react from '../web/node_modules/@vitejs/plugin-react/dist/index.js';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const {chromium}=await import(pathToFileURL(process.env.FINALECHAT_PLAYWRIGHT_MODULE || '/tmp/finalechat-browser-tools/node_modules/playwright/index.mjs').href);
const fixture=process.env.EAGENT_SETTINGS_FIXTURE;
const shots=process.env.FINALECHAT_SCREENSHOTS || '/tmp/finalechat-thread-settings';
await mkdir(shots,{recursive:true});

const connector={id:'connector',name:'eagent · llmbox · Development',provider:'eagent',state:'active',grants:[],requested_grants:[]};
const routeField=(actor,part,schema)=>({key:`/routes/${actor}/${part}`,label:`${actor[0].toUpperCase()+actor.slice(1)} ${part==='effort'?'reasoning effort':part}`,schema,writable:true,unset:false,class:'route',effective_when:'immediate',...(part==='model'?{suggest:{list:'models',group_by:`/routes/${actor}/provider`}}:{})});
const models=[{value:'z-ai/glm-5.3',label:'GLM-5.3 · OpenRouter',group:'openrouter'},{value:'moonshotai/kimi-k3',label:'Kimi K3 · OpenRouter',group:'openrouter'},{value:'claude-opus-5-5',label:'Claude Opus 5.5 · Anthropic',group:'anthropic'},{value:'claude-sonnet-5-5',label:'Claude Sonnet 5.5 · Anthropic',group:'anthropic'}];
const session={id:'session',connector_id:'connector',key:'project-x-session-1',label:'Development · Session 1',scope:'session',generation:'g1',updated_at:new Date().toISOString(),
 descriptor:{format:'finalechat.settings/v1',schema_version:'eagent.runtime-settings/v1',adapter_version:'1',suggestions:{models},fields:[
  {key:'/task_concurrency',label:'Task workers at once',schema:{type:'integer',minimum:1,maximum:128},writable:true,unset:false,class:'preference',effective_when:'next_task'},
  {key:'/narrator_tick_seconds',label:'Narrator check interval (seconds)',schema:{type:'integer',minimum:1,maximum:86400},writable:true,unset:false,class:'preference',effective_when:'immediate'},
  ...['orchestrator','task','narrator'].flatMap(a=>[routeField(a,'provider',{type:'string',enum:['meta','openrouter','anthropic']}),routeField(a,'model',{type:'string',max_length:200}),routeField(a,'effort',{type:'string',enum:['low','medium','high','xhigh']})]),
 ]},
 snapshot:{version:'s1',context:'Development · 1',runtime_known:true,details:{thread_external_id:'eagent:1'},
  saved:{'/task_concurrency':3,'/narrator_tick_seconds':30,'/routes/orchestrator/provider':'anthropic','/routes/orchestrator/model':'claude-opus-5-5','/routes/orchestrator/effort':'xhigh','/routes/task/provider':'openrouter','/routes/task/model':'z-ai/glm-5.3','/routes/task/effort':'high','/routes/narrator/provider':'meta','/routes/narrator/model':'muse-spark-1.3-contributor','/routes/narrator/effort':'low'},effective:{}}};
const project=fixture?JSON.parse(await readFile(path.join(fixture,'settings/state.json'),'utf8')):{
 descriptor:{format:'finalechat.settings/v1',schema_version:'eagent.project/v1',adapter_version:'1',fields:[{key:'/orchestrator/model',label:'Orchestrator model',schema:{type:'string',max_length:200},writable:true,unset:true,class:'preference',effective_when:'new_or_resumed_session'},{key:'/task_concurrency',label:'Parallel tasks',schema:{type:'integer',minimum:1,maximum:128},writable:true,unset:true,class:'preference',effective_when:'new_or_resumed_session'}]},
 snapshot:{version:'p1',context:'Development',saved:{'/orchestrator/model':'muse-spark-1.3-contributor'},effective:{'/task_concurrency':3},runtime_known:false}};
Object.assign(project,{id:'project',connector_id:'connector',key:'project-x',label:'Development',scope:'project',generation:'',updated_at:new Date().toISOString()});
const manifest=fixture?JSON.parse(await readFile(path.join(fixture,'manifest.json'),'utf8')):null;
const website=fixture?await readFile(path.join(fixture,'settings/index.html'),'utf8'):'';
const resources={session,project};

const server=await createServer({root:path.join(root,'web'),configFile:false,plugins:[react()],server:{host:'127.0.0.1',port:0,strictPort:false},logLevel:'warn'});
await server.listen();const base=server.resolvedUrls.local[0].replace(/\/$/,'');
const browser=await chromium.launch({headless:true});
const errors=[];const commands=[];
try {
 for(const [name,width,height] of [['phone',390,844],['desktop',1440,1000]]) {
  for(const running of [true,false]) {
   const page=await browser.newPage({viewport:{width,height},deviceScaleFactor:1});
   page.on('pageerror',e=>errors.push(String(e)));
   await page.route('**/*',async route=>{
    const url=new URL(route.request().url());const p=url.pathname;
    if(url.origin!==base) throw Error('Unexpected outbound request '+url.origin);
    const json=v=>route.fulfill({json:v});
    if(!p.startsWith('/api/')) return route.continue();
    if(p==='/api/v1/threads/thread') return json({thread:{id:'thread',external_id:'eagent:1',title:'Development',agent:'eagent',meta:{model:'claude-opus-5-5',cwd:'/home/u/Development'},unread_count:0,pending_count:0,created_at:new Date().toISOString()}});
    if(p==='/api/v1/threads/thread/settings') return json({resources:[...(running?[{id:'session',label:session.label,scope:'session',provider:'eagent',available:true}]:[]),{id:'project',label:'Development',scope:'project',provider:'eagent',available:true,...(fixture?{artifact_id:'artifact',revision_id:'revision'}:{})}]});
    if(p.endsWith('/artifacts')) return json({artifacts:[]});
    if(p.endsWith('/messages')) return json({messages:[],has_more:false});
    if(p.endsWith('/questions')) return json({questions:[]});
    const res=p.match(/^\/api\/v1\/settings-resources\/(session|project)$/);
    if(res) return json({resource:resources[res[1]],online:true,connector,website:null});
    if(p.endsWith('/settings-surface')) return json({lease:{id:'lease',resource_id:'project',generation:'',expires_at:'2030-01-01T00:00:00Z'}});
    if(p.endsWith('/revisions/revision')) return json({revision:{id:'revision',artifact_id:'artifact',manifest}});
    if(p.endsWith('/preview')) return route.fulfill({contentType:'text/html',body:website,headers:{'Content-Security-Policy':"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'none'"}});
    if(p.includes('/files/')) {
     const file=p.split('/files/')[1];assert.ok(manifest.files.some(f=>f.path===file));
     const bytes=await readFile(path.join(fixture,file));
     return route.fulfill({body:bytes.subarray(Number(url.searchParams.get('offset')),Number(url.searchParams.get('offset'))+Number(url.searchParams.get('length')))});
    }
    const cmd=p.match(/^\/api\/v1\/settings-resources\/(session|project)\/commands$/);
    if(cmd) {
     const body=route.request().postDataJSON();const r=resources[cmd[1]];commands.push({resource:cmd[1],...body});
     for(const e of body.proposal.edits || []) {if(e.op==='set') r.snapshot.saved[e.key]=e.value;else delete r.snapshot.saved[e.key];}
     r.snapshot.version+='-saved';
     return json({created:true,command:{id:'command',resource_id:cmd[1],status:'succeeded',proposal:body.proposal,result:{effects:[{effective_when:cmd[1]==='session'?'next_task':'new_or_resumed_session'}]}}});
    }
    if(p.endsWith('/read')) return json({ok:true});
    throw Error('Unexpected fixture request '+p);
   });
   await page.goto(base+'/tests/artifacts.html?thread');
   await page.getByRole('button',{name:'Settings',exact:true}).click();
   const before=commands.length;
   if(running) {
    // Opens on this session, in FinaleChat's own form, with its real routes.
    await page.getByRole('button',{name:'This session',exact:true,pressed:true}).waitFor();
    await page.getByLabel('Orchestrator model',{exact:true}).waitFor();
    assert.equal(await page.getByLabel('Orchestrator model',{exact:true}).inputValue(),session.snapshot.saved['/routes/orchestrator/model']);
    assert.equal(await page.getByLabel('Task model',{exact:true}).inputValue(),session.snapshot.saved['/routes/task/model']);
    assert.equal(await page.locator('iframe').count(),0,'the session panel must not frame an agent page');
    assert.equal(await page.getByRole('button',{name:'Advanced',exact:true}).count(),0,'empty tab shown');
    await page.waitForTimeout(700); await page.screenshot({path:`${shots}/${name}-session.png`});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'host overflow');
    // The model field: typed, or picked from the provider's known models.
    await page.getByRole('button',{name:/^Browse \d+ choices for Orchestrator model$/}).click();
    const sheet=page.locator('.suggest-sheet');
    await sheet.waitFor();
    assert.equal(await sheet.locator('.item').count(),2,'only the picked provider\'s models');
    await page.waitForTimeout(500); await page.screenshot({path:`${shots}/${name}-model-picker.png`});
    const pick=name==='phone'?'sonnet':'opus';
    await sheet.getByPlaceholder('Search…').fill(pick);
    await sheet.getByRole('button',{name:new RegExp('Claude '+(pick==='sonnet'?'Sonnet':'Opus')+' 5.5')}).click();
    assert.equal(await page.getByLabel('Orchestrator model',{exact:true}).inputValue(),`claude-${pick}-5-5`);
    await page.getByLabel('Task provider',{exact:true}).selectOption(name==='phone'?'openrouter':'anthropic');
    await page.getByRole('button',{name:'Browse 2 choices for Task model'}).waitFor();
    await page.getByLabel('Task model',{exact:true}).fill(`some/new-model-${name}`);
    await page.getByLabel('Task reasoning effort',{exact:true}).selectOption('xhigh');
    await page.getByRole('button',{name:'Behavior',exact:true}).click();
    await page.getByLabel('Task workers at once',{exact:true}).fill(name==='phone'?'5':'6');
    await page.getByRole('button',{name:'Save',exact:true}).click();
    await page.getByText('Saved · next task',{exact:true}).waitFor();
    const sent=commands[commands.length-1];
    assert.equal(sent.resource,'session');assert.equal(sent.surface_lease_id,undefined);
    assert.deepEqual(sent.proposal.edits.map(e=>e.key).sort(),['/routes/orchestrator/model','/routes/task/effort','/routes/task/model','/routes/task/provider','/task_concurrency']);
    assert.equal(sent.proposal.edits.find(e=>e.key==='/routes/task/model').value,`some/new-model-${name}`);
   } else {
    // Not running: say so; never fall back to showing project defaults.
    await page.getByRole('heading',{name:'This session isn’t running'}).waitFor();
    assert.equal(await page.getByRole('button',{name:'Save',exact:true}).count(),0);
    await page.waitForTimeout(700); await page.screenshot({path:`${shots}/${name}-not-running.png`});
   }
   // Project defaults only when chosen, clearly labeled.
   await page.getByRole('button',{name:running?'Project defaults':'See project defaults',exact:true}).click();
   await page.getByText('Development · project defaults',{exact:true}).waitFor();
   await page.getByText('Defaults for new or resumed sessions.',{exact:true}).waitFor();
   await page.waitForTimeout(700); await page.screenshot({path:`${shots}/${name}-${running?'':'idle-'}project.png`});
   if(fixture && running) {
    // The agent's own editor, opened on request; one Save still submits.
    await page.getByRole('button',{name:'Open eagent’s own editor',exact:true}).click();
    const frame=page.frameLocator('iframe[title="eagent settings"]');
    await frame.getByRole('heading',{name:'Choose how your agent thinks'}).waitFor();
    assert.equal(await page.getByRole('button',{name:'Save',exact:true}).count(),1);
    await frame.getByRole('button',{name:'Behavior',exact:true}).click();
    const field=frame.locator('.field').filter({hasText:'Parallel tasks'}).locator('input').first();
    const input=await field.count()?field:frame.locator('input[type=number]').first();
    await input.fill(name==='phone'?'6':'7'); await input.press('Tab');
    await page.getByRole('button',{name:'Save',exact:true}).click();
    await page.getByText('Saved · new or resumed sessions',{exact:true}).waitFor();
    assert.equal(commands[commands.length-1].surface_lease_id,'lease');
    await page.waitForTimeout(700); await page.screenshot({path:`${shots}/${name}-agent-editor.png`});
   }
   await page.getByRole('button',{name:'Close settings'}).click();
   await page.getByRole('button',{name:'Settings',exact:true}).waitFor();
   if(!running) assert.equal(commands.length,before,'opening and browsing must not submit');
   await page.close();
  }
 }
 assert.equal(commands.length,fixture?4:2);assert.deepEqual(errors,[]);
 console.log(`PASS thread settings: session first, project on request${fixture?', agent editor optional':''}; screenshots in ${shots}`);
} finally {await browser.close();await server.close();}
