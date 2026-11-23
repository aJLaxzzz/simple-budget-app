const {test,expect}=require('../browser-fixtures.cjs');
const fs=require('node:fs');const {randomUUID}=require('node:crypto');
test('100 projects, 1000 tasks and dense calendar remain usable with bounded requests',async({page,request})=>{
 test.setTimeout(180000);
 const create=async(type,data)=>{const r=await request.post('/api/planner/'+type,{data});expect(r.ok(),await r.text()).toBeTruthy();return r.json();};
 const projects=[];const seedStart=Date.now();
 for(let batch=0;batch<10;batch++)await Promise.all(Array.from({length:10},async(_,j)=>{
  const p=await create('projects',{title:'Нагрузка '+(batch*10+j)});projects.push(p);
  const roots=await Promise.all(Array.from({length:5},(_,i)=>create('tasks',{title:'Большой список: задача '+i,project_id:p.id,planned_date:'2026-12-19'})));
  await Promise.all(roots.map((r,i)=>create('tasks',{title:'Подзадача '+i,project_id:p.id,parent_task_id:r.id,planned_date:'2026-12-19'})));
 }));
 await create('series',{request_id:randomUUID(),task:{title:'Сотни циклов'},first_date:'2026-01-01',rule:{schema_version:1,mode:'schedule',unit:'days',interval:1,allowed_days:{mode:'any'},adjustment:'none'}});
 const timings={seed_ms:Date.now()-seedStart};const errors=[];page.on('pageerror',e=>errors.push(e.message));
 let requests=[];page.on('request',r=>{if(r.url().includes('/api/planner/'))requests.push(new URL(r.url()).pathname)});
 for(const [name,route,selector]of [['tasks','tasks','.pl-task'],['projects','projects','.pl-project-card'],['calendar','calendar?mode=month&date=2026-12-19','.pc-calendar']]){
  requests=[];const started=Date.now();await page.goto('/#/plans/'+route);await expect(page.locator(selector).first()).toBeVisible();timings[name+'_ms']=Date.now()-started;timings[name+'_requests']=requests.length;
  expect(requests.filter(p=>p.startsWith('/api/planner/tasks/'))).toHaveLength(0);expect(timings[name+'_ms']).toBeLessThan(10000);
 }
 await expect(page.locator('.pc-more').first()).toBeVisible();await page.locator('.pc-more').first().click();await expect(page.getByRole('button',{name:'День',exact:true})).toHaveAttribute('aria-pressed','true');expect(errors).toEqual([]);
 fs.mkdirSync('artifacts/stage14',{recursive:true});fs.writeFileSync('artifacts/stage14/browser-performance.json',JSON.stringify(timings,null,2));
});
