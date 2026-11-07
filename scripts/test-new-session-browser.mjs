#!/usr/bin/env node
// New session in Chromium, phone and desktop, against mocked APIs: with the
// agent doing the work itself, its declared start options (configuration,
// each actor's provider, model and effort) can be set before the first
// message; a configuration shows what it runs before anything starts, every
// choice can go back to the default, a model is picked from any provider
// (picking its provider with it) or typed, and the choices travel with
// session.start. With a relayed agent they are not offered.
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
const efforts=['none','minimal','low','medium','high','xhigh','max'];
const properties={prompt:{type:'string',max_length:32768},cwd:{type:'string',max_length:4096},relay:{type:'string',max_length:120,enum:['','codex']},config:{type:'string',max_length:120,enum:['glm','anthropic-med','bundle:eric']}};
// As eagent declares them: the configuration, then a card per actor whose
// defaults are the project's own routes.
const project={orchestrator:['together','zai-org/GLM-5.3','high'],task:['together','zai-org/GLM-5.3-Flash','medium'],narrator:['together','deepseek-ai/DeepSeek-V4.1-Flash','low']};
const inputs=[{name:'config',label:'Configuration',suggest:{list:'configs'},default:'glm'}];
for(const a of actors){
 properties[a+'_provider']={type:'string',max_length:120,enum:['together','openrouter','anthropic']};
 properties[a+'_model']={type:'string',max_length:200};
 properties[a+'_effort']={type:'string',max_length:120,enum:efforts};
 inputs.push({name:a+'_provider',label:'Provider',group:cap(a),suggest:{list:'providers'},default:project[a][0]},{name:a+'_model',label:'Model',group:cap(a),suggest:{list:'models',group_by:a+'_provider'},default:project[a][1]},{name:a+'_effort',label:'Reasoning effort',group:cap(a),default:project[a][2]});
}
const implies=lineup=>Object.fromEntries(actors.flatMap((a,i)=>[[a+'_provider',lineup[i][0]],[a+'_model',lineup[i][1]],[a+'_effort',lineup[i][2]]]));
const configs=[
 {value:'glm',group:'Together AI',detail:'GLM-5.3 orchestrating, GLM-5.3 Flash working, DeepSeek V4.1 Flash narrating',implies:implies(actors.map(a=>project[a]))},
 {value:'anthropic-med',group:'Anthropic',detail:'Claude Opus 5.5 orchestrating, Claude Sonnet 5.5 working, Claude Haiku 4.5 narrating',implies:implies([['anthropic','claude-opus-5-5','high'],['anthropic','claude-sonnet-5-5','medium'],['anthropic','claude-haiku-4-5','low']])},
 {value:'bundle:eric',label:'eric',group:'Saved in this project',detail:'Claude Opus 5.5 orchestrating, GLM-5.3 Flash working, DeepSeek V4.1 Flash narrating'},
];
const models=[
 {value:'zai-org/GLM-5.3',label:'GLM-5.3',group:'together',detail:'$1 / $3 per M · 200K context'},{value:'zai-org/GLM-5.3-Flash',label:'GLM-5.3 Flash',group:'together'},{value:'deepseek-ai/DeepSeek-V4.1-Flash',label:'DeepSeek V4.1 Flash',group:'together'},
 {value:'z-ai/glm-5.3',label:'GLM-5.3',group:'openrouter'},{value:'moonshotai/kimi-k3',label:'Kimi K3',group:'openrouter',detail:'$3 / $15 per M · 1M context'},
 {value:'claude-opus-5-5',label:'Claude Opus 5.5',group:'anthropic'},{value:'claude-sonnet-5-5',label:'Claude Sonnet 5.5',group:'anthropic'},{value:'claude-haiku-4-5',label:'Claude Haiku 4.5',group:'anthropic'},
];
const providers=[{value:'together',label:'Together AI'},{value:'openrouter',label:'OpenRouter'},{value:'anthropic',label:'Anthropic'}];
const resource={id:'project',connector_id:'connector',key:'project-x',label:'Development',scope:'project',generation:'',updated_at:new Date().toISOString(),
 descriptor:{format:'finalechat.settings/v1',schema_version:'eagent.project/v1',adapter_version:'1',fields:[],
  actions:[{operation:'session.start',label:'Start a new session',class:'cost',parameters:{type:'object',properties,required:['prompt']},inputs}],
  suggestions:{configs,models,providers}},
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
  await page.evaluate(()=>localStorage.clear()); await page.reload();
  // A relayed agent brings its own models: no options.
  await page.getByRole('button',{name:'Codex',exact:true}).click();
  assert.equal(await page.getByTitle('Configuration and models for this session').count(),0);
  await page.getByRole('button',{name:'eagent',exact:true}).click();
  const chip=page.getByTitle('Configuration and models for this session');
  await chip.waitFor();
  // Before anything is chosen, the draft says what it will run with.
  const summary=page.getByLabel('What it will run with');
  await summary.getByText('GLM-5.3 Flash · medium',{exact:true}).waitFor();
  await chip.getByText('glm',{exact:true}).waitFor();
  await chip.click();
  const sheet=page.locator('.start-sheet');
  // A group's card is a region named by its title; a lone setting's card holds its label.
  const card=title=>title==='Configuration'?sheet.locator('.sf-card').filter({has:page.locator('label',{hasText:/^Configuration$/})}):sheet.getByRole('region',{name:title,exact:true});
  const badge=(title,text)=>card(title).locator('.sf-badge').filter({hasText:new RegExp('^'+text+'$')}).first().waitFor();
  await badge('Task','Default');
  // A preset: what it runs shows on every card before anything starts...
  const choice=name=>card('Configuration').getByRole('radio',{name});
  await choice(/^anthropic-med/).click();
  await badge('Configuration','Changed');
  await badge('Task','From anthropic-med');
  assert.match(await page.getByRole('button',{name:/^Task model: /}).getAttribute('aria-label'),/Claude Sonnet 5\.5 at Anthropic/);
  // ...and going back to the project's own is one tap, in the list itself.
  assert.equal(await choice(/^Default/).isEnabled(),true);
  await choice(/^Default/).click();
  await badge('Configuration','Default');
  assert.match(await page.getByRole('button',{name:/^Task model: /}).getAttribute('aria-label'),/GLM-5\.3 Flash at Together AI/);
  // Or from the card.
  await choice(/^anthropic-med/).click();
  await card('Configuration').getByRole('button',{name:'Use default',exact:true}).click();
  await badge('Configuration','Default');
  await choice(/^anthropic-med/).click();
  const picker=page.locator('.sf-picker');
  // A model from any provider: search finds it, and picking it picks its provider.
  await page.getByRole('button',{name:/^Task model: /}).click();
  await picker.getByRole('searchbox').fill('kimi');
  await page.waitForTimeout(300); await page.screenshot({path:`${shots}/${name}-model-picker.png`});
  await picker.getByRole('button',{name:/^Kimi K3/}).click();
  assert.match(await page.getByRole('button',{name:/^Task model: /}).getAttribute('aria-label'),/Kimi K3 at OpenRouter/);
  await badge('Task','Changed');
  // Any id can be typed in, at the provider shown.
  await page.getByRole('button',{name:/^Narrator model: /}).click();
  await picker.getByRole('searchbox').fill('some/typed-model');
  await picker.getByRole('button',{name:/^Use “some\/typed-model”/}).click();
  // Effort: one tap, and tapping the default again goes back to it.
  await card('Orchestrator').getByRole('radio',{name:'max',exact:true}).click();
  await badge('Orchestrator','Changed');
  await card('Orchestrator').getByRole('radio',{name:/^high/}).click();
  await badge('Orchestrator','From anthropic-med');
  await card('Orchestrator').getByRole('radio',{name:'xhigh',exact:true}).click();
  await page.waitForTimeout(500); await page.screenshot({path:`${shots}/${name}-options.png`});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'overflow');
  await page.getByRole('button',{name:'Done',exact:true}).click();
  await chip.getByText('anthropic-med · Orchestrator, Task +1 changed',{exact:true}).waitFor();
  await summary.getByText('Kimi K3 · medium',{exact:true}).waitFor();
  await page.waitForTimeout(400); await page.screenshot({path:`${shots}/${name}-draft.png`});
  await page.getByPlaceholder('What should it do?').fill('do the thing');
  await page.getByRole('button',{name:'Start the session'}).click();
  await page.waitForURL(/\/t\/t1/);
  const sent=commands[commands.length-1].proposal;
  assert.equal(sent.operation,'session.start');
  // The choices, and with each model the provider it was picked at.
  assert.deepEqual({...sent.parameters},{prompt:'do the thing',config:'anthropic-med',task_provider:'openrouter',task_model:'moonshotai/kimi-k3',narrator_model:'some/typed-model',narrator_provider:'anthropic',orchestrator_effort:'xhigh'});
  await page.close();
 }
 assert.deepEqual(errors,[]);
 console.log(`PASS new session: options before the first message, typed or picked; screenshots in ${shots}`);
} finally {await browser.close();await server.close();}
