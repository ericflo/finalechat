#!/usr/bin/env node
// New session in Chromium, phone and desktop, against mocked APIs.
//
// An older host (its session.start's relay enum still holds "", the host
// doing the work itself; this fixture mirrors an older eagent's per-role
// inputs): with the host doing the work itself, its declared start options (configuration,
// each actor's provider, model and effort) can be set before the first
// message; a configuration shows what it runs before anything starts, every
// choice can go back to the default, a model is picked from any provider
// (picking its provider with it) or typed, and the choices travel with
// session.start. With a relayed agent, its own model and effort are offered
// instead (inputs whose `when` names that agent), remembered on the device
// and sent with the relay.
//
// A host that always relays (its relay enum lists only profiles, default
// first): no do-it-yourself choice, the first profile is picked unless
// another is remembered, every start sends relay, an empty first message
// is fine, a fourth profile (pi, with provider/model ids) works like any
// other, and a start that races a republish is retried once.
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
const properties={prompt:{type:'string',max_length:32768},cwd:{type:'string',max_length:4096},relay:{type:'string',max_length:120,enum:['','codex','antigravity']},config:{type:'string',max_length:120,enum:['glm','anthropic-med','bundle:eric']}};
// As eagent declares them: the configuration, then a card per actor whose
// defaults are the project's own routes.
const project={orchestrator:['together','zai-org/GLM-5.3','high'],task:['together','zai-org/GLM-5.3-Flash','medium'],narrator:['together','deepseek-ai/DeepSeek-V4.1-Flash','low']};
const eagentOnly={relay:['']};
const inputs=[{name:'config',label:'Configuration',suggest:{list:'configs'},default:'glm',when:eagentOnly}];
for(const a of actors){
 properties[a+'_provider']={type:'string',max_length:120,enum:['together','openrouter','anthropic']};
 properties[a+'_model']={type:'string',max_length:200};
 properties[a+'_effort']={type:'string',max_length:120,enum:efforts};
 inputs.push({name:a+'_provider',label:'Provider',group:cap(a),suggest:{list:'providers'},default:project[a][0],when:eagentOnly},{name:a+'_model',label:'Model',group:cap(a),suggest:{list:'models',group_by:a+'_provider'},default:project[a][1],when:eagentOnly},{name:a+'_effort',label:'Reasoning effort',group:cap(a),default:project[a][2],when:eagentOnly});
}
// Codex's own model and effort, asked only when Codex does the work; left
// alone, what its own configuration says.
properties.relay_codex_model={type:'string',max_length:200};
properties.relay_codex_effort={type:'string',max_length:120,enum:['low','medium','high','xhigh','max','ultra']};
inputs.push({name:'relay_codex_model',label:'Model',group:'Codex',suggest:{list:'codex_models'},default:'gpt-6.2-astra',when:{relay:['codex']}},{name:'relay_codex_effort',label:'Reasoning effort',group:'Codex',default:'xhigh',when:{relay:['codex']}});
properties.relay_antigravity_model={type:'string',max_length:200};
properties.relay_antigravity_effort={type:'string',enum:['low','medium','high','xhigh','max']};
inputs.push({name:'relay_antigravity_model',label:'Model',group:'Antigravity',suggest:{list:'antigravity_models'},when:{relay:['antigravity']}},{name:'relay_antigravity_effort',label:'Reasoning effort',group:'Antigravity',when:{relay:['antigravity']}});
const agyModels=[{value:'gemini-4-carbon-high',label:'Gemini 4 Carbon (High)'}];
const codexModels=[{value:'gpt-6.2-sol',label:'GPT-6.2-Sol',detail:'Fast, go-to model for coding and everyday work.'},{value:'gpt-6.2-astra',label:'GPT-6.2-Astra',detail:'Our most capable model for complex coding and professional work.'},{value:'gpt-6-astra',label:'GPT-6-Astra'}];
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
  suggestions:{configs,models,providers,codex_models:codexModels,antigravity_models:agyModels}},
 snapshot:{version:'p1',context:'/home/u/Development',saved:{},effective:{},runtime_known:false,details:{directories:{root:'/home/u/Development',recent:[],children:[],siblings:[]},relay_profiles:[{name:'codex',agent:'codex',label:'Codex · GPT-6.2-Astra xhigh',model:'gpt-6.2-astra',effort:'xhigh'},{name:'antigravity',agent:'antigravity',label:'Antigravity',model:'',effort:''}],relays:[]}}};
const connector={id:'connector',name:'eagent · llmbox · Development',provider:'eagent',state:'active',grants:[],requested_grants:[],last_seen_at:new Date().toISOString(),expires_at:'2030-01-01T00:00:00Z'};

// A host that always relays: profiles only, default first, each with its
// own model and effort.
const relayed={prompt:{type:'string',max_length:32768},cwd:{type:'string',max_length:4096},relay:{type:'string',max_length:120,enum:['claude','codex','pi']}};
const relayedInputs=[];
for(const [prof,group,list,def] of [['claude','Claude Code','claude_models','claude-opus-5-5'],['codex','Codex','codex_models','gpt-6.2-astra'],['pi','Pi','pi_models','']]){
 relayed[`relay_${prof}_model`]={type:'string',max_length:200};
 relayed[`relay_${prof}_effort`]={type:'string',max_length:120,enum:['low','medium','high','xhigh']};
 relayedInputs.push({name:`relay_${prof}_model`,label:'Model',group,suggest:{list},...(def?{default:def}:{}),when:{relay:[prof]}},{name:`relay_${prof}_effort`,label:'Reasoning effort',group,when:{relay:[prof]}});
}
const piModels=[{value:'openrouter/qwen3-coder',label:'Qwen3 Coder',detail:'openrouter'},{value:'anthropic/claude-sonnet-5-5',label:'Claude Sonnet 5.5',detail:'anthropic'}];
const relayResource={...resource,
 descriptor:{format:'finalechat.settings/v1',schema_version:'eagent.project/v2',adapter_version:'2',fields:[],
  actions:[{operation:'session.start',label:'Start a new session',class:'cost',parameters:{type:'object',properties:relayed,required:['prompt']},inputs:relayedInputs}],
  suggestions:{claude_models:[{value:'claude-opus-5-5',label:'Claude Opus 5.5'}],codex_models:codexModels,pi_models:piModels}},
 snapshot:{version:'r1',context:'/home/u/Development',saved:{},effective:{},runtime_known:false,details:{directories:{root:'/home/u/Development',recent:[],children:[],siblings:[]},relay_profiles:[
  {name:'claude',agent:'claude',label:'Claude Code · Opus 5.5 xhigh',model:'claude-opus-5-5',effort:'xhigh'},
  {name:'codex',agent:'codex',label:'Codex · GPT-6.2-Astra xhigh',model:'gpt-6.2-astra',effort:'xhigh'},
  {name:'pi',agent:'pi',label:'Pi · openrouter/qwen3-coder high',model:'openrouter/qwen3-coder',effort:'high'}],relays:[]}}};
let current=resource;
let conflicts=0;
let multiHost=false;
const connector2={id:'connector2',name:'eagent · macbook · Development',provider:'eagent',state:'active',grants:[],requested_grants:[],last_seen_at:new Date().toISOString(),expires_at:'2030-01-01T00:00:00Z'};
const resource2={...relayResource,id:'project2',connector_id:'connector2',label:'Development',
 snapshot:{...relayResource.snapshot,version:'m1',context:'/Users/u/Development',details:{directories:{root:'/Users/u/Development',recent:[],children:[],siblings:[]},relay_profiles:[
  {name:'codex',agent:'codex',label:'Codex · GPT-6.2-Astra xhigh',model:'gpt-6.2-astra',effort:'xhigh'},
  {name:'pi',agent:'pi',label:'Pi · openrouter/qwen3-coder high',model:'openrouter/qwen3-coder',effort:'high'}],relays:[]}}};
const offlineConnector={id:'connector-old',name:'eagent · oldbox · Development',provider:'eagent',state:'active',grants:[],requested_grants:[],last_seen_at:'2025-01-01T00:00:00Z',expires_at:'2030-01-01T00:00:00Z'};
const offlineResource={...relayResource,id:'project-old',connector_id:'connector-old',label:'Development'};

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
   if(p==='/api/v1/connectors') return json({connectors:multiHost?[connector,connector2,offlineConnector]:[connector]});
   if(p==='/api/v1/connectors/connector') return json({connector,online:true,resources:[current]});
   if(p==='/api/v1/connectors/connector2') return json({connector:connector2,online:true,resources:[resource2]});
   if(p==='/api/v1/connectors/connector-old') return json({connector:offlineConnector,online:false,resources:[offlineResource]});
   if(p==='/api/v1/settings-resources/project') return json({resource:current,connector,online:true,website:null});
   if(p==='/api/v1/settings-resources/project2') return json({resource:resource2,connector:connector2,online:true,website:null});
   if(p==='/api/v1/settings-resources/project/commands') {const body=route.request().postDataJSON();commands.push({...body,_resource:'project'});if(conflicts>0){conflicts--;return route.fulfill({status:409,json:{error:{code:'conflict',message:'The settings changed.'}}});}return json({created:true,command:{id:'c1',resource_id:'project',status:'succeeded',proposal:body.proposal,result:{thread:'ext:eagent:1',session_id:'1',interactive:true}}});}
   if(p==='/api/v1/settings-resources/project2/commands') {const body=route.request().postDataJSON();commands.push({...body,_resource:'project2'});return json({created:true,command:{id:'c2',resource_id:'project2',status:'succeeded',proposal:body.proposal,result:{thread:'ext:eagent:2',session_id:'2',interactive:true}}});}
   if(p.startsWith('/api/v1/threads/')) return json({thread:{id:p.endsWith('2')?'t2':'t1',external_id:p.endsWith('2')?'eagent:2':'eagent:1',title:'Development',agent:'eagent',meta:{},unread_count:0,pending_count:0,created_at:new Date().toISOString()},messages:[],has_more:false,questions:[],artifacts:[],resources:[]});
   return json({});
  });
  await page.goto(base+'/tests/artifacts.html?draft');
  await page.evaluate(()=>localStorage.clear()); await page.reload();
  // A relayed agent: its own model and effort, not eagent's lineup.
  await page.getByRole('button',{name:'Codex',exact:true}).click();
  assert.equal(await page.getByTitle('Configuration and models for this session').count(),0);
  const codexChip=page.getByTitle("Codex's model and reasoning effort for this session");
  await codexChip.getByText('GPT-6.2-Astra · xhigh',{exact:true}).waitFor();
  await page.getByLabel('What it will run with').getByText('GPT-6.2-Astra · xhigh',{exact:true}).waitFor();
  await codexChip.click();
  {
   const sheet=page.locator('.start-sheet');
   const codex=sheet.getByRole('region',{name:'Codex',exact:true});
   await codex.locator('.sf-badge').filter({hasText:/^Default$/}).first().waitFor();
   assert.equal(await sheet.getByRole('region',{name:'Task',exact:true}).count(),0);
   await page.getByRole('button',{name:/^Codex model: /}).click();
   await page.locator('.sf-picker').getByRole('button',{name:/^GPT-6\.2-Sol/}).click();
   await codex.getByRole('radio',{name:'max',exact:true}).click();
   await page.waitForTimeout(400); await page.screenshot({path:`${shots}/${name}-codex-options.png`});
   await page.getByRole('button',{name:'Done',exact:true}).click();
  }
  await codexChip.getByText('GPT-6.2-Sol · max',{exact:true}).waitFor();
  await page.getByRole('button',{name:'eagent',exact:true}).click();
  // The rows of what it will run with: the configuration first.
  const chip=page.getByTitle('Configuration and models for this session').first();
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
  const choice=name=>card('Configuration').getByRole('radio',{name});
  const checked=async name=>assert.equal(await choice(name).getAttribute('aria-checked'),'true');
  // Changing one thing on the project's own lineup makes it Custom...
  await card('Task').getByRole('radio',{name:'high',exact:true}).click();
  await badge('Configuration','Custom');
  await checked(/^Custom Based on the default \(glm\): Task changed/);
  // ...and a preset sets everything, like a game's graphics preset: what it
  // runs shows on every card before anything starts.
  await choice(/^anthropic-med/).click();
  await badge('Task','From anthropic-med');
  assert.equal(await card('Task').getByRole('radio',{name:'medium',exact:true}).getAttribute('aria-checked'),'true');
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
  // One model changed: the preset reads Custom, and the rest stay as it set them.
  await badge('Configuration','Custom');
  await checked(/^Custom Based on anthropic-med: Task changed/);
  await badge('Orchestrator','From anthropic-med');
  await page.waitForTimeout(500); await page.screenshot({path:`${shots}/${name}-custom.png`});
  // Back to exactly the preset, from the card or by picking it again.
  await card('Task').getByRole('button',{name:'Back to anthropic-med',exact:true}).click();
  await badge('Configuration','Changed');
  await checked(/^anthropic-med/);
  await page.getByRole('button',{name:/^Task model: /}).click();
  await picker.getByRole('searchbox').fill('kimi');
  await picker.getByRole('button',{name:/^Kimi K3/}).click();
  await choice(/^anthropic-med/).click();
  assert.match(await page.getByRole('button',{name:/^Task model: /}).getAttribute('aria-label'),/Claude Sonnet 5\.5 at Anthropic/);
  await page.getByRole('button',{name:/^Task model: /}).click();
  await picker.getByRole('searchbox').fill('kimi');
  await picker.getByRole('button',{name:/^Kimi K3/}).click();
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
  await chip.getByText('Custom (anthropic-med)',{exact:true}).waitFor();
  assert.equal(await page.locator('.setup-row.changed').count(),4);
  await summary.getByText('Custom (anthropic-med)',{exact:true}).waitFor();
  await summary.getByText('Kimi K3 · medium',{exact:true}).waitFor();
  await page.waitForTimeout(400); await page.screenshot({path:`${shots}/${name}-draft.png`});
  await page.getByPlaceholder('What should it do?').fill('do the thing');
  await page.getByRole('button',{name:'Start the session'}).click();
  await page.waitForURL(/\/t\/t1/);
  const sent=commands[commands.length-1].proposal;
  assert.equal(sent.operation,'session.start');
  // The choices, and with each model the provider it was picked at.
  assert.deepEqual({...sent.parameters},{prompt:'do the thing',config:'anthropic-med',task_provider:'openrouter',task_model:'moonshotai/kimi-k3',narrator_model:'some/typed-model',narrator_provider:'anthropic',orchestrator_effort:'xhigh'});
  // The next new session remembers Codex's model and effort, and sends them
  // with the relay (and none of eagent's).
  await page.goto(base+'/tests/artifacts.html?draft');
  await page.getByRole('button',{name:'Codex',exact:true}).click();
  await page.getByLabel('What it will run with').getByText('GPT-6.2-Sol · max',{exact:true}).waitFor();
  await page.getByPlaceholder(/^Message Codex/).fill('ship it');
  await page.getByRole('button',{name:'Start the session'}).click();
  await page.waitForURL(/\/t\/t1/);
  assert.deepEqual({...commands[commands.length-1].proposal.parameters},{prompt:'ship it',relay:'codex',relay_codex_model:'gpt-6.2-sol',relay_codex_effort:'max'});
  await page.goto(base+'/tests/artifacts.html?draft');
  await page.getByRole('button',{name:'Antigravity',exact:true}).click();
  await page.getByTitle("Antigravity's model and reasoning effort for this session").click();
  await page.getByRole('button',{name:/^Antigravity model:/}).click();
  await page.locator('.sf-picker').getByRole('button',{name:/^Gemini 4 Carbon/}).click();
  await page.getByRole('region',{name:'Antigravity',exact:true}).getByRole('radio',{name:'high',exact:true}).click();
  await page.getByRole('button',{name:'Done',exact:true}).click();
  await page.getByPlaceholder(/^Message Antigravity/).fill('run tests');
  await page.getByRole('button',{name:'Start the session'}).click();
  await page.waitForURL(/\/t\/t1/);
  assert.deepEqual(commands.at(-1).proposal.parameters,{prompt:'run tests',relay:'antigravity',relay_antigravity_model:'gemini-4-carbon-high',relay_antigravity_effort:'high'});

  // A host that always relays. What this device remembered from the older
  // host ("" for the host itself) falls back to the first profile.
  current=relayResource;
  await page.goto(base+'/tests/artifacts.html?draft');
  await page.evaluate(()=>{localStorage.clear();localStorage.setItem('fc.draft.connector.agent','');});
  await page.reload();
  const agentPicker=page.getByRole('group',{name:'Agent'});
  await agentPicker.getByRole('button',{name:'Claude Code',exact:true}).waitFor();
  assert.deepEqual(await agentPicker.getByRole('button').allTextContents(),['Claude Code','Codex','Pi']);
  assert.equal(await page.getByRole('button',{name:'eagent',exact:true}).count(),0);
  assert.equal(await agentPicker.getByRole('button',{name:'Claude Code',exact:true}).getAttribute('aria-pressed'),'true');
  await page.getByText('eagent starts Claude Code in this folder and passes your messages on.',{exact:true}).waitFor();
  await page.getByPlaceholder(/^Message Claude Code/).waitFor();
  await page.waitForTimeout(400); await page.screenshot({path:`${shots}/${name}-relay-only-draft.png`});
  // An empty first message starts the agent, with relay always sent; a
  // version race is retried once.
  conflicts=1;
  const before=commands.length;
  await page.getByRole('button',{name:'Start the session'}).click();
  await page.waitForURL(/\/t\/t1/);
  assert.equal(commands.length,before+2,'one retry after a conflict');
  assert.deepEqual(commands.at(-1).proposal.parameters,{prompt:'',relay:'claude'});
  // Pi: a fourth profile, models named provider/model.
  await page.goto(base+'/tests/artifacts.html?draft');
  await agentPicker.getByRole('button',{name:'Pi',exact:true}).click();
  await page.getByTitle("Pi's model and reasoning effort for this session").click();
  await page.getByRole('button',{name:/^Pi model:/}).click();
  await page.locator('.sf-picker').getByRole('button',{name:/^Qwen3 Coder/}).click();
  await page.getByRole('region',{name:'Pi',exact:true}).getByRole('radio',{name:'high',exact:true}).click();
  await page.waitForTimeout(400); await page.screenshot({path:`${shots}/${name}-pi-options.png`});
  await page.getByRole('button',{name:'Done',exact:true}).click();
  await page.getByLabel('What it will run with').getByText(/openrouter\/qwen3-coder|Qwen3 Coder/).first().waitFor();
  await page.getByPlaceholder(/^Message Pi/).fill('fix it');
  await page.getByRole('button',{name:'Start the session'}).click();
  await page.waitForURL(/\/t\/t1/);
  assert.deepEqual(commands.at(-1).proposal.parameters,{prompt:'fix it',relay:'pi',relay_pi_model:'openrouter/qwen3-coder',relay_pi_effort:'high'});
  // The last choice is remembered.
  await page.goto(base+'/tests/artifacts.html?draft');
  await agentPicker.getByRole('button',{name:'Pi',exact:true}).waitFor();
  assert.equal(await agentPicker.getByRole('button',{name:'Pi',exact:true}).getAttribute('aria-pressed'),'true');
  // Single online host hides the host picker.
  assert.equal(await page.getByRole('group',{name:'Which host'}).count(),0);

  // Multiple connected hosts: /new shows the host picker at the top (hiding
  // stale offline connectors), switching preserves the draft message and swaps
  // agents/folder/target, and the chosen host is remembered next time.
  multiHost=true;
  await page.goto(base+'/tests/artifacts.html?draft&any');
  const hostBtn=page.getByRole('button',{name:/^Host: llmbox · Development\. Change$/});
  await hostBtn.waitFor();
  await page.getByPlaceholder(/^Message Pi/).fill('run on macbook');
  await page.waitForTimeout(300); await page.screenshot({path:`${shots}/${name}-multi-host-draft.png`});
  await hostBtn.click();
  const hostSheet=page.getByRole('dialog',{name:'Choose host'});
  await hostSheet.waitFor();
  assert.equal(await hostSheet.getByText(/oldbox/).count(),0,'stale offline host stays hidden');
  await page.waitForTimeout(200); await page.screenshot({path:`${shots}/${name}-host-picker.png`});
  await hostSheet.getByRole('button',{name:/macbook · Development/}).click();
  await page.getByRole('button',{name:/^Host: macbook · Development\. Change$/}).waitFor();
  assert.deepEqual(await agentPicker.getByRole('button').allTextContents(),['Codex','Pi']);
  assert.equal(await page.locator('textarea').inputValue(),'run on macbook','draft text survives host switch');
  await page.getByRole('button',{name:'Start the session'}).click();
  await page.waitForURL(/\/t\/t2/);
  assert.equal(commands.at(-1)._resource,'project2');
  assert.deepEqual(commands.at(-1).proposal.parameters,{prompt:'run on macbook',relay:'codex'});
  // Reopening /new remembers macbook.
  await page.goto(base+'/tests/artifacts.html?draft&any');
  await page.getByRole('button',{name:/^Host: macbook · Development\. Change$/}).waitFor();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'overflow');
  multiHost=false;
  current=resource;
  await page.close();
 }
 assert.deepEqual(errors,[]);
 console.log(`PASS new session: older host, relay-only host (pi included), and multi-host picker; screenshots in ${shots}`);
} finally {await browser.close();await server.close();}
