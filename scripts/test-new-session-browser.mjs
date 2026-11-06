#!/usr/bin/env node
// New session in Chromium, phone and desktop, against mocked APIs: with the
// agent doing the work itself, its declared start options (configuration,
// each actor's provider, model and effort) can be set before the first
// message, the model is typed or picked from the provider's known models,
// and the choices travel with session.start. With a relayed agent they are
// not offered.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createServer } from '../web/node_modules/vite/dist/node/index.js';
import react from '../web/node_modules/@vitejs/plugin-react/dist/index.js';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const {chromium}=await import(pathToFileURL(process.env.FINALECHAT_PLAYWRIGHT_MODULE || '/tmp/finalechat-browser-tools/node_modules/playwright/index.mjs').href);
const shots=process.env.FINALECHAT_SCREENSHOTS || '/tmp/finalechat-new-session';
await mkdir(shots,{recursive:true});

const actors=['orchestrator','task','narrator'];
const cap=a=>a[0].toUpperCase()+a.slice(1);
const properties={prompt:{type:'string',max_length:32768},cwd:{type:'string',max_length:4096},relay:{type:'string',max_length:120,enum:['','codex']},config:{type:'string',max_length:120,enum:['glm','anthropic-med','bundle:eric']}};
const inputs=[{name:'config',label:'Configuration'}];
for(const a of actors){
 properties[a+'_provider']={type:'string',max_length:120,enum:['openrouter','anthropic']};
 properties[a+'_model']={type:'string',max_length:200};
 properties[a+'_effort']={type:'string',max_length:120,enum:['low','medium','high','xhigh']};
 inputs.push({name:a+'_provider',label:cap(a)+' provider'},{name:a+'_model',label:cap(a)+' model',suggest:{list:'models',group_by:a+'_provider'}},{name:a+'_effort',label:cap(a)+' reasoning effort'});
}
const resource={id:'project',connector_id:'connector',key:'project-x',label:'Development',scope:'project',generation:'',updated_at:new Date().toISOString(),
 descriptor:{format:'finalechat.settings/v1',schema_version:'eagent.project/v1',adapter_version:'1',fields:[],
  actions:[{operation:'session.start',label:'Start a new session',class:'cost',parameters:{type:'object',properties,required:['prompt']},inputs}],
  suggestions:{models:[{value:'z-ai/glm-5.3',label:'GLM-5.3 · OpenRouter',group:'openrouter'},{value:'moonshotai/kimi-k3',label:'Kimi K3 · OpenRouter',group:'openrouter'},{value:'claude-opus-5-5',label:'Claude Opus 5.5 · Anthropic',group:'anthropic'}]}},
 snapshot:{version:'p1',context:'/home/u/Development',saved:{},effective:{},runtime_known:false,details:{directories:{root:'/home/u/Development',recent:[],children:[],siblings:[]},relay_profiles:[{name:'codex',agent:'codex',label:'Codex · GPT-6 Astra xhigh',model:'gpt-6-astra',effort:'xhigh'}],relays:[]}}};
const connector={id:'connector',name:'eagent · llmbox · Development',provider:'eagent',state:'active',grants:[],requested_grants:[],last_seen_at:new Date().toISOString(),expires_at:'2030-01-01T00:00:00Z'};

const server=await createServer({root:path.join(root,'web'),configFile:false,plugins:[react()],server:{host:'127.0.0.1',port:0,strictPort:false},logLevel:'warn'});
await server.listen();const base=server.resolvedUrls.local[0].replace(/\/$/,'');
const browser=await chromium.launch({headless:true});
const errors=[];const commands=[];
try {
 for(const [name,width,height] of [['phone',390,844],['desktop',1440,1000]]) {
  const page=await browser.newPage({viewport:{width,height},deviceScaleFactor:1});
  page.on('pageerror',e=>errors.push(String(e)));
  await page.route('**/*',async route=>{
   const url=new URL(route.request().url());const p=url.pathname;
   if(url.origin!==base) throw Error('Unexpected outbound request '+url.origin);
   const json=v=>route.fulfill({json:v});
   if(!p.startsWith('/api/')) return route.continue();
   if(p==='/api/v1/connectors') return json({connectors:[connector]});
   if(p==='/api/v1/connectors/connector') return json({connector,online:true,resources:[resource]});
   if(p==='/api/v1/settings-resources/project') return json({resource,connector,online:true,website:null});
   if(p==='/api/v1/settings-resources/project/commands') {const body=route.request().postDataJSON();commands.push(body);return json({created:true,command:{id:'c1',resource_id:'project',status:'succeeded',proposal:body.proposal,result:{thread:'ext:eagent:1',session_id:'1',interactive:true}}});}
   if(p.startsWith('/api/v1/threads/')) return json({thread:{id:'t1',external_id:'eagent:1',title:'Development',agent:'eagent',meta:{},unread_count:0,pending_count:0,created_at:new Date().toISOString()},messages:[],has_more:false,questions:[],artifacts:[],resources:[]});
   return json({});
  });
  await page.goto(base+'/tests/artifacts.html?draft');
  // A relayed agent brings its own models: no options.
  await page.getByRole('button',{name:'Codex',exact:true}).click();
  assert.equal(await page.getByTitle('Configuration and models for this session').count(),0);
  await page.getByRole('button',{name:'eagent',exact:true}).click();
  const chip=page.getByTitle('Configuration and models for this session');
  await chip.waitFor();
  await chip.click();
  await page.getByLabel('Configuration',{exact:true}).selectOption('anthropic-med');
  await page.getByLabel('Task provider',{exact:true}).selectOption('openrouter');
  await page.getByRole('button',{name:'Browse 2 choices for Task model'}).click();
  await page.locator('.suggest-sheet').getByRole('button',{name:/Kimi K3/}).click();
  assert.equal(await page.getByLabel('Task model',{exact:true}).inputValue(),'moonshotai/kimi-k3');
  await page.getByLabel('Narrator model',{exact:true}).fill('some/typed-model');
  await page.getByLabel('Orchestrator reasoning effort',{exact:true}).selectOption('xhigh');
  await page.waitForTimeout(500); await page.screenshot({path:`${shots}/${name}-options.png`});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'overflow');
  await page.getByRole('button',{name:'Done',exact:true}).click();
  await chip.getByText('anthropic-med +4',{exact:true}).waitFor();
  await page.waitForTimeout(400); await page.screenshot({path:`${shots}/${name}-draft.png`});
  await page.getByPlaceholder('What should it do?').fill('do the thing');
  await page.getByRole('button',{name:'Start the session'}).click();
  await page.waitForURL(/\/t\/t1/);
  const sent=commands[commands.length-1].proposal;
  assert.equal(sent.operation,'session.start');
  assert.deepEqual({...sent.parameters},{prompt:'do the thing',config:'anthropic-med',task_provider:'openrouter',task_model:'moonshotai/kimi-k3',narrator_model:'some/typed-model',orchestrator_effort:'xhigh'});
  await page.close();
 }
 assert.deepEqual(errors,[]);
 console.log(`PASS new session: options before the first message, typed or picked; screenshots in ${shots}`);
} finally {await browser.close();await server.close();}
