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
// As eagent publishes a running session: a card per actor (provider, model,
// effort) under Models, its workers and narrator under Behavior.
const routeField=(actor,part,schema)=>({key:`/routes/${actor}/${part}`,label:{provider:'Provider',model:'Model',effort:'Reasoning effort'}[part],section:'Models',group:actor[0].toUpperCase()+actor.slice(1),schema,writable:true,unset:false,class:'route',effective_when:'immediate',...(part==='model'?{suggest:{list:'models',group_by:`/routes/${actor}/provider`}}:part==='provider'?{suggest:{list:'providers'}}:{})});
const models=[{value:'z-ai/glm-5.3',label:'GLM-5.3',group:'openrouter',detail:'$1 / $3 per M · 200K context'},{value:'moonshotai/kimi-k3',label:'Kimi K3',group:'openrouter'},{value:'claude-opus-5-5',label:'Claude Opus 5.5',group:'anthropic',detail:'$5 / $25 per M · 1M context'},{value:'claude-sonnet-5-5',label:'Claude Sonnet 5.5',group:'anthropic'}];
const providers=[{value:'meta',label:'Meta'},{value:'openrouter',label:'OpenRouter'},{value:'anthropic',label:'Anthropic'}];
// Each configuration says what it sets every actor's route to.
const lineup=(o,t,n)=>Object.fromEntries([['orchestrator',o],['task',t],['narrator',n]].flatMap(([a,[pr,m,e]])=>[[`/routes/${a}/provider`,pr],[`/routes/${a}/model`,m],[`/routes/${a}/effort`,e]]));
const configs=[
 {value:'glm',group:'OpenRouter',detail:'GLM-5.3 orchestrating and working, Kimi K3 narrating',implies:lineup(['openrouter','z-ai/glm-5.3','high'],['openrouter','z-ai/glm-5.3','high'],['openrouter','moonshotai/kimi-k3','low'])},
 {value:'anthropic-med',group:'Anthropic',detail:'Claude Opus 5.5 orchestrating, Claude Sonnet 5.5 working and narrating',implies:lineup(['anthropic','claude-opus-5-5','xhigh'],['anthropic','claude-sonnet-5-5','high'],['anthropic','claude-sonnet-5-5','low'])},
];
const session={id:'session',connector_id:'connector',key:'project-x-session-1',label:'Development · Session 1',scope:'session',generation:'g1',updated_at:new Date().toISOString(),
 descriptor:{format:'finalechat.settings/v1',schema_version:'eagent.runtime-settings/v1',adapter_version:'1',suggestions:{models,providers,configs},fields:[
  {key:'/config',label:'Configuration',description:'Choosing one sets every actor\'s provider, model and effort.',section:'Models',schema:{type:'string',enum:configs.map(c=>c.value)},writable:true,unset:false,class:'route',effective_when:'immediate',suggest:{list:'configs'}},
  ...['orchestrator','task','narrator'].flatMap(a=>[routeField(a,'provider',{type:'string',enum:['meta','openrouter','anthropic']}),routeField(a,'model',{type:'string',max_length:200}),routeField(a,'effort',{type:'string',enum:['none','minimal','low','medium','high','xhigh','max']})]),
  {key:'/task_concurrency',label:'Tasks at once',section:'Behavior',group:'Work',schema:{type:'integer',minimum:1,maximum:128},writable:true,unset:false,class:'preference',effective_when:'next_task'},
  {key:'/narrator_tick_seconds',label:'Check interval (seconds)',section:'Behavior',group:'Narrator',schema:{type:'integer',minimum:1,maximum:86400},writable:true,unset:false,class:'preference',effective_when:'immediate'},
 ]},
 snapshot:{version:'s1',context:'Development · 1',runtime_known:true,details:{thread_external_id:'eagent:1'},
  saved:{'/config':'anthropic-med','/task_concurrency':3,'/narrator_tick_seconds':30,'/routes/orchestrator/provider':'anthropic','/routes/orchestrator/model':'claude-opus-5-5','/routes/orchestrator/effort':'xhigh','/routes/task/provider':'openrouter','/routes/task/model':'z-ai/glm-5.3','/routes/task/effort':'high','/routes/narrator/provider':'meta','/routes/narrator/model':'muse-spark-1.3-contributor','/routes/narrator/effort':'low'},effective:{}}};
// Project defaults: each can be set here or left to its default.
const projectField=(key,label,section,group,schema,extra={})=>({key,label,section,group,schema,writable:true,unset:true,class:'preference',effective_when:'new_or_resumed_session',...extra});
const project=fixture?JSON.parse(await readFile(path.join(fixture,'settings/state.json'),'utf8')):{
 descriptor:{format:'finalechat.settings/v1',schema_version:'eagent.project/v1',adapter_version:'1',suggestions:{presets:[{value:'glm',group:'Together AI',detail:'GLM-5.3 orchestrating, GLM-5.3 Flash working, DeepSeek V4.1 Flash narrating',implies:{'/orchestrator/model':'zai-org/GLM-5.3','/orchestrator/base_url':'https://api.together.xyz/v1'}},{value:'anthropic-med',group:'Anthropic',detail:'Claude Opus 5.5 orchestrating, Claude Sonnet 5.5 working, Claude Haiku 4.5 narrating',implies:{'/orchestrator/model':'claude-opus-5-5','/orchestrator/base_url':'https://api.anthropic.com/v1'}}]},fields:[
  projectField('/preset','Preset','Models','Configuration',{type:'string',enum:['glm','anthropic-med']},{suggest:{list:'presets'}}),
  projectField('/orchestrator/model','Model','Models','Orchestrator',{type:'string',max_length:200}),
  projectField('/orchestrator/base_url','Base URL','Models','Orchestrator',{type:'string',max_length:8192},{advanced:true}),
  projectField('/task_concurrency','Tasks at once','Behavior','Work',{type:'integer',minimum:1,maximum:128}),
 ]},
 snapshot:{version:'p1',context:'Development',saved:{'/preset':'glm','/orchestrator/model':'muse-spark-1.3-contributor'},effective:{'/preset':'glm','/orchestrator/model':'muse-spark-1.3-contributor','/orchestrator/base_url':'https://api.together.xyz/v1','/task_concurrency':3},runtime_known:false}};
Object.assign(project,{id:'project',connector_id:'connector',key:'project-x',label:'Development',scope:'project',generation:'',updated_at:new Date().toISOString()});
const manifest=fixture?JSON.parse(await readFile(path.join(fixture,'manifest.json'),'utf8')):null;
const website=fixture?await readFile(path.join(fixture,'settings/index.html'),'utf8'):'';
const resources={session,project};
const freshSession=structuredClone(session.snapshot.saved);

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
     if(cmd[1]==='session') for(const e of body.proposal.edits || []) if(e.key==='/config') Object.assign(r.snapshot.saved,configs.find(c=>c.value===e.value).implies);
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
   session.snapshot.saved=structuredClone(freshSession);
   const model=actor=>page.getByRole('button',{name:new RegExp('^'+actor+' model: ')});
   const card=title=>page.getByRole('region',{name:title,exact:true});
   if(running) {
    // Opens on this session, in FinaleChat's own form, with its real routes.
    await page.getByRole('button',{name:'This session',exact:true,pressed:true}).waitFor();
    await model('Orchestrator').waitFor();
    const saved=session.snapshot.saved;
    const named=v=>models.find(m=>m.value===v)?.label??v;
    const at=p=>providers.find(x=>x.value===p).label;
    assert.match(await model('Orchestrator').getAttribute('aria-label'),new RegExp(`: ${named(saved['/routes/orchestrator/model'])} at ${at(saved['/routes/orchestrator/provider'])}\\.`));
    assert.match(await model('Task').getAttribute('aria-label'),new RegExp(`: ${named(saved['/routes/task/model']).replace(/[./]/g,'\\$&')} at ${at(saved['/routes/task/provider'])}\\.`));
    assert.equal(await page.locator('iframe').count(),0,'the session panel must not frame an agent page');
    assert.equal(await page.getByRole('button',{name:'Other',exact:true}).count(),0,'a section nobody declared');
    await page.waitForTimeout(700); await page.screenshot({path:`${shots}/${name}-session.png`});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'host overflow');
    // A model from any provider, found by every word typed.
    await model('Orchestrator').click();
    const picker=page.locator('.sf-picker');
    await picker.waitFor();
    assert.equal(await picker.locator('.sf-option').count(),models.length,'every provider\'s models');
    await page.waitForTimeout(500); await page.screenshot({path:`${shots}/${name}-model-picker.png`});
    const pick='Sonnet';
    await picker.getByRole('searchbox').fill(`${pick.toLowerCase()} anthropic`);
    assert.equal(await picker.locator('.sf-option').count(),2,'the match, and typing it as an id');
    await picker.getByRole('button',{name:new RegExp(`^Claude ${pick} 5\\.5`)}).click();
    assert.match(await model('Orchestrator').getAttribute('aria-label'),new RegExp(`Claude ${pick} 5\\.5 at Anthropic`));
    // Any id, at the provider chosen in the picker.
    const provider='Anthropic';
    await model('Task').click();
    await picker.getByRole('button',{name:new RegExp(`^${provider}`)}).click();
    await picker.getByRole('searchbox').fill(`some/new-model-${name}`);
    await picker.getByRole('button',{name:/^Use “/}).click();
    assert.match(await model('Task').getAttribute('aria-label'),new RegExp(`some/new-model-${name} at ${provider}`));
    await card('Task').getByRole('radio',{name:name==='phone'?'xhigh':'max',exact:true}).click();
    await card('Task').getByText('Unsaved',{exact:true}).waitFor();
    await page.getByRole('button',{name:'Behavior',exact:true}).click();
    await page.getByLabel('Tasks at once',{exact:true}).fill(name==='phone'?'5':'6');
    await page.getByText('5 unsaved changes',{exact:true}).waitFor();
    await page.getByText('Orchestrator: model · Task: model, provider, reasoning effort · Work: tasks at once',{exact:true}).waitFor();
    await page.waitForTimeout(400); await page.screenshot({path:`${shots}/${name}-session-changed.png`});
    await page.getByRole('button',{name:'Save',exact:true}).click();
    await page.getByText('Saved · next task',{exact:true}).waitFor();
    const sent=commands[commands.length-1];
    assert.equal(sent.resource,'session');assert.equal(sent.surface_lease_id,undefined);
    assert.deepEqual(sent.proposal.edits.map(e=>e.key).sort(),['/routes/orchestrator/model','/routes/task/effort','/routes/task/model','/routes/task/provider','/task_concurrency']);
    assert.equal(sent.proposal.edits.find(e=>e.key==='/routes/task/model').value,`some/new-model-${name}`);
    assert.equal(sent.proposal.edits.find(e=>e.key==='/routes/task/provider').value,provider.toLowerCase());
    // A configuration is a preset: routes changed since read as Custom, based on it.
    await page.getByRole('button',{name:'Models',exact:true}).click();
    const configCard=page.getByRole('radiogroup',{name:'Configuration'});
    const current=session.snapshot.saved['/config'];
    await configCard.getByRole('radio',{name:new RegExp(`^Custom Based on ${current}: .*changed$`),checked:true}).waitFor();
    // Choosing another sets every actor's route to it.
    const target=current==='glm'?'anthropic-med':'glm';
    const implied=configs.find(c=>c.value===target).implies;
    await configCard.getByRole('radio',{name:new RegExp(`^${target}`)}).click();
    for(const a of ['Orchestrator','Task','Narrator']) {
     const key=a.toLowerCase();
     assert.match(await model(a).getAttribute('aria-label'),new RegExp(`: ${named(implied[`/routes/${key}/model`])} at ${at(implied[`/routes/${key}/provider`])}\\.`));
     await card(a).locator('.sf-badge',{hasText:`From ${target}`}).waitFor();
    }
    await page.getByText('1 unsaved change',{exact:true}).waitFor();
    await page.getByText(`Configuration ${target} (sets Orchestrator, Task, Narrator)`,{exact:true}).waitFor();
    await page.waitForTimeout(700); await page.screenshot({path:`${shots}/${name}-session-preset.png`});
    // One change after it: Custom, based on it, the rest still from it.
    await card('Narrator').getByRole('radio',{name:'medium',exact:true}).click();
    await configCard.getByRole('radio',{name:`Custom Based on ${target}: Narrator changed`,checked:true}).waitFor();
    await card('Task').locator('.sf-badge',{hasText:`From ${target}`}).waitFor();
    await page.waitForTimeout(400); await page.screenshot({path:`${shots}/${name}-session-custom.png`});
    await page.getByRole('button',{name:'Save',exact:true}).click();
    await page.getByText('Saved',{exact:false}).first().waitFor();
    assert.deepEqual(commands[commands.length-1].proposal.edits,[{op:'set',key:'/config',value:target},{op:'set',key:'/routes/narrator/effort',value:'medium'}]);
    // Saved, it still reads Custom; back to the preset starts over from it.
    await configCard.getByRole('radio',{name:`Custom Based on ${target}: Narrator changed`,checked:true}).waitFor();
    // The card that changed can go back on its own, and the configuration as a whole.
    await card('Narrator').getByRole('button',{name:`Back to ${target}`,exact:true}).waitFor();
    await page.locator('.sf-card').filter({has:configCard}).getByRole('button',{name:`Back to ${target}`,exact:true}).click();
    await card('Narrator').getByRole('radio',{name:'low',exact:true,checked:true}).waitFor();
    await page.getByText('1 unsaved change',{exact:true}).waitFor();
    await page.getByRole('button',{name:'Discard',exact:true}).click();
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
   if(!fixture && running && name==='phone') {
    // The project's own model on top of its preset reads as Custom.
    const presets=page.getByRole('region',{name:'Configuration',exact:true});
    await presets.getByRole('radio',{name:'Custom Based on glm: Orchestrator changed',checked:true}).waitFor();
    // Another preset puts the model back to the preset's own.
    await presets.getByRole('radio',{name:/^anthropic-med/}).click();
    await page.getByText('2 unsaved changes',{exact:true}).waitFor();
    await page.getByText('Configuration: preset anthropic-med (sets Orchestrator) · Orchestrator: model',{exact:true}).waitFor();
    assert.equal(await page.getByLabel('Model',{exact:true}).getAttribute('placeholder'),'claude-opus-5-5');
    await card('Orchestrator').locator('.sf-badge',{hasText:'Unsaved'}).waitFor();
    await page.waitForTimeout(400); await page.screenshot({path:`${shots}/${name}-project-preset.png`});
    // A project default set here goes back to the default from the same list.
    assert.equal(await presets.getByRole('radio',{name:/^Default/}).isEnabled(),true);
    await presets.getByRole('radio',{name:/^Default/}).click();
    await page.getByText('Configuration: preset · Orchestrator: model',{exact:true}).waitFor();
    await page.getByRole('button',{name:'Save',exact:true}).click();
    await page.getByText('Saved · new or resumed sessions',{exact:true}).waitFor();
    assert.deepEqual(commands[commands.length-1].proposal.edits,[{op:'unset',key:'/preset'},{op:'unset',key:'/orchestrator/model'}]);
    // Rarely changed settings wait behind a link.
    assert.equal(await page.getByLabel('Base URL').count(),0);
    await page.getByRole('button',{name:'Show 1 advanced setting',exact:true}).click();
    await page.getByLabel('Base URL').waitFor();
   }
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
 assert.equal(commands.length,fixture?6:5);assert.deepEqual(errors,[]);
 console.log(`PASS thread settings: session first, project on request${fixture?', agent editor optional':''}; screenshots in ${shots}`);
} finally {await browser.close();await server.close();}
