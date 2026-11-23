const {test,expect}=require('../browser-fixtures.cjs');
const fs=require('node:fs');
const {randomUUID}=require('node:crypto');
const phase=process.env.AUDIT_PHASE||'after';
const dir='artifacts/stage14/'+phase;
let project,task,template,series;
async function post(request,path,data){const r=await request.post('/api/planner/'+path,{data});expect(r.ok(),await r.text()).toBeTruthy();return r.json();}
test.beforeAll(async({request})=>{
 fs.mkdirSync(dir,{recursive:true});
 project=await post(request,'projects',{title:'Генеральная уборка · приёмка',description:'Дом, в котором удобно жить. Подготовка в субботу, завершение в воскресенье.',start_date:'2026-12-19',due_date:'2026-12-20'});
 task=await post(request,'tasks',{title:'Подготовить комнаты и разобрать вещи — длинное название для проверки компоновки',description:'Проверить полки, убрать вещи и подготовить инвентарь. <script>window.auditXSS=1</script>',project_id:project.id,planned_date:'2026-12-19',due_date:'2026-12-21',priority:'high'});
 await post(request,'tasks',{title:'Протереть верхние полки',project_id:project.id,parent_task_id:task.id,planned_date:'2026-12-20'});
 for(let i=0;i<14;i++)await post(request,'tasks',{title:['Кухня: поверхности и техника','Ванная: зеркало и полки','Разобрать документы'][i%3]+' · '+(i+1),project_id:project.id,planned_date:'2026-12-19',...(i%2?{scheduled_time:'10:00'}:{})});
 await post(request,'sessions',{task_id:task.id,date:'2026-12-19',start_time:'10:00',end_time:'11:30'});
 await post(request,'sessions',{task_id:task.id,date:'2026-12-20'});
 await post(request,'tasks',{title:'Идея без даты · приёмка'});
 await post(request,'tasks',{title:'Оплатить доставку · просроченная',planned_date:'2020-01-02'});
 template=(await post(request,'templates',{request_id:randomUUID(),snapshot:{title:'Генеральная уборка',description:'Повторяемый порядок действий',tasks:[{key:'rooms',title:'Пропылесосить',planned_offset:0},{key:'floor',title:'Помыть полы',planned_offset:0},{key:'bath',title:'Почистить ванную',planned_offset:1},{key:'shelf',parent_key:'bath',title:'Полки',planned_offset:1}]}})).template;
 series=await post(request,'series',{request_id:randomUUID(),source_type:'project',template_id:template.id,expected_revision:template.revision,first_date:'2026-12-19',rule:{schema_version:1,mode:'schedule',unit:'weeks',interval:2,allowed_days:{mode:'weekends'},adjustment:'next',timezone:'Europe/Moscow'}});
});
for(const width of [1440,1024,768,390,360])test('visual acceptance '+width,async({page})=>{
 test.setTimeout(120000);await page.setViewportSize({width,height:1000});await page.emulateMedia({colorScheme:'dark'});
 const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.route('https://**/*',r=>r.abort());
 const metrics=[];
 async function shot(name){await page.locator('.pl-loading').waitFor({state:'detached'});await page.evaluate(()=>Promise.all([...document.getAnimations()].map(a=>a.finished.catch(()=>{}))));await page.screenshot({path:dir+'/'+name+'-'+width+'.png',fullPage:false});const size=await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth}));metrics.push({name,...size});if(phase==='after')expect(size.scroll,name+' overflows').toBeLessThanOrEqual(width+1);}
 async function open(route,name){
 const section=route.split(/[/?]/)[0] || 'calendar';const endpoint=section==='calendar'?'calendar':section==='series'?'series':section==='templates'?'templates':section==='settings'?'production-calendar':section==='attention'?'attention':section==='projects'?'projects':'tasks';
 const response=page.waitForResponse(r=>r.request().method()==='GET'&&new URL(r.url()).pathname.startsWith('/api/planner/'+endpoint));
 await page.goto('/#/plans/'+route);await response;await expect(page.locator('.pl-loading')).toHaveCount(0);
 if(section==='calendar')await expect(page.locator('.pc-calendar')).toBeVisible();
 if(section!=='settings')await expect.poll(async()=>page.locator('nav[aria-label="Разделы планировщика"] [aria-current]').evaluate(a=>{const r=a.getBoundingClientRect(),n=a.parentElement.getBoundingClientRect();return r.left>=n.left-1&&r.right<=n.right+1;})).toBe(true);
 await shot(name);
 if(section==='calendar') { await expect(page.locator('.pc-calendar')).toBeVisible(); await page.locator('.pc-calendar').scrollIntoViewIfNeeded(); await shot(name+'-schedule'); if (await page.locator('.pc-dense-group').count()) { await page.locator('.pc-dense-group').first().click(); await expect(page.locator('.pc-dense-list .pc-event')).toHaveCount(8); await shot(name+'-overlaps'); await page.keyboard.press('Escape'); } }
 }
 await open('','plans');await open('inbox','inbox');await open('projects','projects');await open('projects/'+project.id,'project');
 for(const mode of ['month','week','day'])await open('calendar?mode='+mode+'&date=2026-12-19&project='+project.id,'calendar-'+mode);
 await open('tasks?task='+task.id,'task-sessions');await expect(page.getByRole('dialog',{name:'Редактор задачи',exact:true})).toBeVisible();await shot('editor');await page.getByRole('tab',{name:'Периоды работы',exact:true}).click();await page.locator('.pl-session').first().scrollIntoViewIfNeeded();await shot('sessions');
 await open('series','series');await open('series/'+series.id,'series-detail');await page.getByRole('button',{name:'Изменить правило',exact:true}).click();await expect(page.locator('.pl-preview-row').first()).toBeVisible();await shot('constructor');await page.keyboard.press('Escape');
 await open('templates/'+template.id,'template');await page.getByRole('button',{name:'Редактировать шаблон',exact:true}).click();await shot('template-editor');await page.keyboard.press('Escape');
 await open('attention?tab=overdue','attention');await open('settings','settings');
 await open('inbox?q=нет-такого-названия-приёмка','empty');
 await page.route('**/api/planner/tasks?*',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{message:'Проверка состояния ошибки'}})}));await open('tasks','load-error');await page.unroute('**/api/planner/tasks?*');
 await open('tasks?task='+task.id,'task-before-confirmation');await page.getByRole('dialog',{name:'Редактор задачи',exact:true}).getByRole('button',{name:'Завершить задачу',exact:true}).click();await shot('confirmation');await page.keyboard.press('Escape');
 await page.emulateMedia({colorScheme:'light'});await open('settings','settings-light');
 expect(await page.evaluate(()=>window.auditXSS)).toBeUndefined();expect(errors).toEqual([]);fs.writeFileSync(dir+'/layout-'+width+'.json',JSON.stringify(metrics,null,2));
});
