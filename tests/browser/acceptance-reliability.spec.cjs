const {test,expect}=require('../browser-fixtures.cjs');
const {spawn,execFileSync}=require('node:child_process');
const {once}=require('node:events');
const fs=require('node:fs'),os=require('node:os'),path=require('node:path');
const {randomUUID}=require('node:crypto');
const password='isolated-acceptance-test-password-123456789';
const base='http://127.0.0.1:31372';
function run(dir){return spawn(path.join(process.env.PLANNER_TEST_DIR,'budget-test-server'),[],{cwd:dir,env:{...process.env,BUDGET_ADDR:'127.0.0.1:31372',BUDGET_DATA_DIR:dir,BUDGET_STATIC_DIR:path.resolve('static'),BUDGET_PASSWORD:password,BUDGET_TLS_CERT:'',BUDGET_TLS_KEY:'',BUDGET_ALLOWED_HOSTS:''},stdio:['ignore','pipe','pipe']});}
async function ready(child){await new Promise((resolve,reject)=>{let output='';const timer=setTimeout(()=>reject(Error('Server startup timeout: '+output)),15000);child.stderr.on('data',b=>{output+=b;if(output.includes('Личный бюджет:')){clearTimeout(timer);resolve();}});child.once('exit',code=>{clearTimeout(timer);reject(Error('Server exited '+code+': '+output));});});}
async function stop(child,signal='SIGTERM'){if(child.exitCode!==null)return;const exited=once(child,'exit');child.kill(signal);const [code,sig]=await exited;expect(code).toBe(0);expect(sig).toBeNull();}
test('protected offline app: financial lifecycle, actual restart, recurrence replay, privacy and integrity',async({browser,playwright})=>{
 test.setTimeout(90000);const dir=fs.mkdtempSync(path.join(os.tmpdir(),'budget-acceptance-'));fs.writeFileSync(path.join(dir,'data.json'),'broken JSON');let child=run(dir);await once(child,'exit');expect(child.exitCode).not.toBe(0);expect(fs.readFileSync(path.join(dir,'data.json'),'utf8')).toBe('broken JSON');fs.writeFileSync(path.join(dir,'data.json'),'{"months":[]}');
 const auth={username:'budget',password};const api=await playwright.request.newContext({baseURL:base,httpCredentials:auth});const anon=await playwright.request.newContext({baseURL:base});const context=await browser.newContext({baseURL:base,httpCredentials:auth});let page;
 async function req(method,url,data,code=200){const r=await api.fetch(url,{method,data,headers:{"Content-Type":"application/json"}});expect(r.status(),await r.text()).toBe(code);return code===204?null:r.json();}
 try{
  child=run(dir);await ready(child);
  for(const route of ['/api/months','/api/backup','/api/planner/tasks','/api/planner/attention','/vendor/chart.umd-4.4.0.js'])expect((await anon.get(route)).status()).toBe(401);
  for(const route of ['/data.json','/categories.json','/planner.db','/planner.db-wal','/backup.db','/.env','/../data.json','/%2e%2e/data.json','/vendor/Chart.js-LICENSE.md'])expect((await api.get(route)).status()).toBe(404);
  await req('PUT','/api/categories',{expense:[{id:'food',name:'Еда',subcategories:['Продукты']}],income:[{id:'salary',name:'Зарплата'}]});
  await req('POST','/api/months',{month:11,year:2026},201);const m='/api/months/month-2026-11';
  const inc=await req('POST',m+'/incomes',{category:'salary',amount:1000,day:2,description:'Доход'},201);const exp=await req('POST',m+'/expenses',{category:'food',subcategory:'Продукты',amount:125,day:3,description:'<img src=x onerror="window.financeXSS=1">'},201);
  await req('PATCH',m+'/incomes/'+inc.id,{amount:1200});await req('PATCH',m+'/expenses/'+exp.id,{amount:150});const totals=await req('GET','/api/years/2026');expect(totals.totalIncome).toBe(1200);expect(totals.totalExpenses).toBe(150);expect(totals.balance).toBe(1050);
  const finance=fs.readFileSync(path.join(dir,'data.json'),'utf8');expect(await(await api.get('/api/backup')).text()).toBe(finance);
  page=await context.newPage();const external=[],errors=[];page.on('request',r=>{if(!r.url().startsWith(base))external.push(r.url());});page.on('pageerror',e=>errors.push(e.message));await page.route('https://**/*',r=>r.abort());
  await page.goto('/#/month/month-2026-11');await expect(page.locator('#btn-add-expense')).toBeVisible();await expect(page.locator('canvas').first()).toBeVisible();expect(await page.evaluate(()=>typeof Chart)).toBe('function');expect(await page.evaluate(()=>window.financeXSS)).toBeUndefined();
  await page.goto('/#/year/2026');await expect(page.locator('#year-select')).toBeVisible();await page.goto('/#/categories');await expect(page.locator('#save-categories')).toBeVisible();
  await page.goto('/#/plans/inbox');await expect(page.getByRole('heading',{name:'Входящие',exact:true})).toBeVisible();expect(fs.readFileSync(path.join(dir,'data.json'),'utf8')).toBe(finance);expect(external).toEqual([]);expect(errors).toEqual([]);
  const input={request_id:randomUUID(),task:{title:'Перезапуск'},first_date:'2026-11-01',rule:{schema_version:1,unit:'weeks',interval:1,mode:'schedule',allowed_days:{mode:'any'},adjustment:'none'}};
  const series=await req('POST','/api/planner/series',input,201);const ids=series.occurrences.map(o=>o.id);const task=series.occurrences[0].task;
  const results=await Promise.all([api.patch('/api/planner/tasks/'+task.id,{data:{expected_revision:task.revision,title:'Клиент A'}}),api.patch('/api/planner/tasks/'+task.id,{data:{expected_revision:task.revision,title:'Клиент B'}})]);expect(results.map(r=>r.status()).sort()).toEqual([200,409]);
  await stop(child);child=run(dir);await ready(child);expect((await req('POST','/api/planner/series',input,201)).id).toBe(series.id);await req('POST','/api/planner/materialize',{});const latest=await req('GET','/api/planner/series/'+series.id);expect(latest.occurrences.map(o=>o.id)).toEqual(ids);expect(latest.occurrences[0].task.revision).toBe(2);expect(await(await api.get('/api/backup')).text()).toBe(finance);
  await req('DELETE',m+'/expenses/'+exp.id,undefined,204);await req('DELETE',m+'/incomes/'+inc.id,undefined,204);await stop(child,'SIGINT');
  expect(execFileSync('/usr/bin/sqlite3',['file:'+path.join(dir,'planner.db')+'?immutable=1','PRAGMA integrity_check; PRAGMA foreign_key_check;'],{encoding:'utf8'}).trim()).toBe('ok');
  child=run(dir);await ready(child);expect((await req('GET',m)).expenses).toHaveLength(0);expect((await req('GET',m)).incomes).toHaveLength(0);await stop(child);
 }finally{if(child.exitCode===null){child.kill('SIGTERM');await once(child,'exit');}await context.close();await api.dispose();await anon.dispose();}
});
