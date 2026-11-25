const {test,expect}=require('../browser-fixtures.cjs');
const fs=require('node:fs'),path=require('node:path');const {execFileSync}=require('node:child_process');const {randomUUID}=require('node:crypto');
const {isolatedServer}=require('../isolated-server.cjs');
test('100 projects, 1000 tasks and dense calendar remain usable with bounded requests',async({page,playwright},info)=>{
 test.setTimeout(180000);
 const server=await isolatedServer();const request=await playwright.request.newContext({baseURL:server.baseURL});
 const timings={environment:{platform:process.platform,serverGOMAXPROCS:1},fixture:{projects:100,tasks:1000},rss_samples_kib:[],api:[],screens:{},httpErrors:[],requestFailures:[]};
 try{
 const create=async(type,data)=>{const r=await request.post('/api/planner/'+type,{data});expect(r.ok(),await r.text()).toBeTruthy();return r.json();};
 const projects=[];const seedStart=Date.now();let firstTask;
 for(let batch=0;batch<10;batch++)await Promise.all(Array.from({length:10},async(_,j)=>{
  const p=await create('projects',{title:'Нагрузка '+(batch*10+j)});projects.push(p);
  const roots=await Promise.all(Array.from({length:5},(_,i)=>create('tasks',{title:'Большой список: задача '+i,project_id:p.id,planned_date:'2026-12-19'})));
  firstTask||=roots[0];await Promise.all(roots.map((r,i)=>create('tasks',{title:'Подзадача '+i,project_id:p.id,parent_task_id:r.id,planned_date:'2026-12-19'})));
 }));
 timings.seed_ms=Date.now()-seedStart;const sampleMemory=()=>timings.rss_samples_kib.push(Number(execFileSync('ps',['-o','rss=','-p',String(server.child.pid)],{encoding:'utf8'}).trim()));sampleMemory();
 await create('series',{request_id:randomUUID(),task:{title:'Сотни циклов'},first_date:'2026-01-01',rule:{schema_version:1,mode:'schedule',unit:'days',interval:1,allowed_days:{mode:'any'},adjustment:'none'}});
 for(const url of ['/tasks?status=active','/projects','/calendar?from=2026-12-01&to=2026-12-31']){
  const start=Date.now(),r=await request.get('/api/planner'+url),body=await r.body();expect(r.status()).toBe(200);timings.api.push({url,ms:Date.now()-start,bytes:body.length});
  if(url==='/tasks?status=active')expect(JSON.parse(body).items.filter(t=>t.project_id)).toHaveLength(1000);
 }
 const errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('response',r=>{if(r.status()>=400)timings.httpErrors.push({url:r.url(),status:r.status()});});page.on('requestfailed',r=>timings.requestFailures.push({url:r.url(),error:r.failure()?.errorText}));
 let requests=[];page.on('request',r=>{if(r.url().includes('/api/planner/'))requests.push(new URL(r.url()).pathname)});
 for(const [name,route,selector]of [['tasks','tasks','[data-task-id="'+firstTask.id+'"]'],['projects','projects','.pl-project-card'],['calendar','calendar?mode=month&date=2026-12-19','.pc-calendar']]){
  await page.goto('about:blank');requests=[];const started=Date.now();await page.goto(server.baseURL+'/#/plans/'+route);await expect(page.locator(selector).first()).toBeVisible();
  const ready=Date.now()-started;const resources=await page.evaluate(()=>performance.getEntriesByType('resource').filter(r=>r.name.includes('/api/planner/')).map(r=>({path:new URL(r.name).pathname,start:r.startTime,responseEnd:r.responseEnd,ms:r.responseEnd-r.startTime})));
  const browserNow=await page.evaluate(()=>performance.now());const dataEnd=Math.max(0,...resources.filter(r=>!r.path.includes('/icons/')).map(r=>r.responseEnd));timings.screens[name]={ready_ms:ready,post_data_to_observation_ms:Math.round(browserNow-dataEnd),requests:requests.length,resources};
  expect(requests.filter(p=>p.startsWith('/api/planner/tasks/'))).toHaveLength(0);expect(ready).toBeLessThan(10000);sampleMemory();
  if(name==='tasks'){
   expect(await page.locator('.pl-task').count()).toBeGreaterThanOrEqual(1000);
   const interaction=Date.now();await page.locator(selector+' .pl-task-title').click();await expect(page.getByRole('dialog',{name:'Редактор задачи',exact:true}).getByLabel('Название',{exact:true})).toHaveValue(firstTask.title);timings.screens.tasks.interaction_ms=Date.now()-interaction;
   // The hashchange starts a new mount after the click. An old networkidle state
   // can already be satisfied before that mount starts, so wait for its actual
   // responses and rendered list before navigating away.
   const refreshed=Promise.all(['/tasks?status=active','/projects','/projects?archived=true'].map(url=>page.waitForResponse(r=>r.url()===server.baseURL+'/api/planner'+url&&r.request().method()==='GET')));
   await page.getByRole('dialog',{name:'Редактор задачи',exact:true}).getByRole('button',{name:'Закрыть редактор'}).click();
   for(const response of await refreshed){expect(response.status()).toBe(200);await response.finished();}
   await expect(page.getByRole('dialog',{name:'Редактор задачи',exact:true})).toHaveCount(0);
   await expect(page.locator(selector)).toBeVisible();
  }
  // Closing an editor refreshes the list asynchronously. Finish that request before
  // leaving the document, so this probe does not manufacture ERR_ABORTED failures.
  await page.waitForLoadState('networkidle');
 }
 await expect(page.locator('.pc-more').first()).toBeVisible();await page.locator('.pc-more').first().click();await expect(page.getByRole('button',{name:'День',exact:true})).toHaveAttribute('aria-pressed','true');await expect(page.locator('.pc-time-grid')).toBeVisible();await page.waitForLoadState('networkidle');expect(errors).toEqual([]);expect(timings.httpErrors).toEqual([]);expect(timings.requestFailures).toEqual([]);timings.sqlite_files_bytes={};for(const name of ['planner.db','planner.db-wal','planner.db-shm']){const file=path.join(server.dir,name);if(fs.existsSync(file))timings.sqlite_files_bytes[name]=fs.statSync(file).size;}
 }finally{
  fs.mkdirSync('artifacts/stage11',{recursive:true});fs.writeFileSync('artifacts/stage11/browser-performance.json',JSON.stringify(timings,null,2));await info.attach('load-metrics',{body:JSON.stringify(timings,null,2),contentType:'application/json'});await request.dispose();await server.stop();
 }
});
