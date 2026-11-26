const {plannerPreference}=require('../browser-fixtures.cjs');
const {test,expect}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto');
async function api(request,path,data,method='post') {const r=await request[method]('/api/planner'+path,data===undefined?{}:{data});expect(r.ok(),await r.text()).toBeTruthy();return r.json();}
const dateOffset=(date,n)=>{const d=new Date(date+'T12:00:00Z');d.setUTCDate(d.getUTCDate()+n);return d.toISOString().slice(0,10)};
async function setup(page,request){await page.goto('/#/plans/tasks');await expect(page.locator('.pl-results')).toBeVisible();const today=await page.evaluate(()=>Planner.today());const p=await api(request,'/projects',{title:'Уборка · UX 3.1 '+randomUUID().slice(0,5),color_id:'cyan',icon_id:'lucide:house'});return {p,today};}
async function complete(request,t,date){return api(request,'/tasks/'+t.id+'/complete',{expected_revision:t.revision,completed_date:date,request_id:randomUUID()})}
function groups(page,p){return page.locator('.pl-project-task-group').filter({has:page.locator('summary strong',{hasText:p.title})});}

test('UX31 groups preserve independent expansion, real progress and rows across live changes',async({page,request,browser,baseURL},info)=>{
 const {p,today}=await setup(page,request);
 const a=await api(request,'/tasks',{title:'Протереть поверхности',project_id:p.id,planned_date:today,scheduled_time:'09:00'});
 await api(request,'/tasks',{title:'Пропылесосить комнаты',project_id:p.id,planned_date:today,scheduled_time:'11:00'});
 const done=await api(request,'/tasks',{title:'Подготовить средства',project_id:p.id,planned_date:today,scheduled_time:'08:30'});await complete(request,done,today);
 await api(request,'/tasks',{title:'Помыть окна',project_id:p.id,planned_date:dateOffset(today,1)});
 await page.goto('/#/plans/tasks?project='+p.id);await expect(groups(page,p)).toHaveCount(2);
 const cards=groups(page,p);await expect(cards.first()).not.toHaveAttribute('open','');await expect(cards.first().locator('.pl-project-group-progress')).toContainText('1 / 3');
 await page.screenshot({path:info.outputPath('project-groups-collapsed.png'),fullPage:true});
 for(const card of await cards.all())await card.locator(':scope > summary').click();
 await expect(page.getByRole('button',{name:a.title,exact:true})).toBeVisible();
 await page.screenshot({path:info.outputPath('project-groups-expanded.png'),fullPage:true});
 const other=await browser.newContext({baseURL});const second=await other.newPage();
 try{
  await second.goto('/#/plans/tasks?project='+p.id);await expect(second.locator('.pl-results')).toBeVisible();
  await page.evaluate(()=>{window.uxProbe={loaders:0};new MutationObserver(rs=>{for(const r of rs)for(const n of r.addedNodes)if(n.nodeType===1&&(n.matches('.pl-loading')||n.querySelector('.pl-loading')))uxProbe.loaders++}).observe(document.querySelector('#root'),{childList:true,subtree:true})});
  await second.evaluate(async({id,date})=>{const t=await Planner.api('/tasks/'+id);await Planner.api('/tasks/'+id+'/complete','POST',{expected_revision:t.revision,completed_date:date,request_id:crypto.randomUUID()})},{id:a.id,date:today});
  await expect(cards.first().locator('.pl-project-group-progress')).toContainText('2 / 3');for(const card of await cards.all())await expect(card).toHaveAttribute('open','');
  expect(await page.evaluate(()=>uxProbe.loaders)).toBe(0);
  await plannerPreference(page,'Группировать по проектам',false);await expect(cards).toHaveCount(0);await expect(page.getByRole('button',{name:'Пропылесосить комнаты',exact:true})).toBeVisible();
  await plannerPreference(page,'Группировать по проектам',true);await expect(cards).toHaveCount(2);for(const card of await cards.all())await expect(card).toHaveAttribute('open','');
  const current=await api(request,'/tasks/'+a.id,undefined,'get');await api(request,'/tasks/'+a.id+'/restore',{expected_revision:current.revision,request_id:randomUUID()});
  await expect(cards.first().locator('.pl-project-group-progress')).toContainText('1 / 3');
  const input=page.getByLabel('Название новой задачи',{exact:true});await input.fill('Несохранённый локальный черновик');
  await api(request,'/tasks',{title:'Внешнее изменение при черновике',project_id:p.id,planned_date:today});
  await expect(page.getByText('Данные изменились.',{exact:false})).toBeVisible();await expect(input).toHaveValue('Несохранённый локальный черновик');
 } finally {await other.close()}
});

test('UX31 completed planned dates remain visible in month/week/day and preference survives reload',async({page,request},info)=>{
 const {p,today}=await setup(page,request),day=dateOffset(today,-3);
 const active=await api(request,'/tasks',{title:'Чтение · запланировано',project_id:p.id,planned_date:day,scheduled_time:'10:00'});
 const done=await api(request,'/tasks',{title:'Разобрать документы · выполнено',project_id:p.id,planned_date:day,scheduled_time:'09:00'});await complete(request,done,today);
 for(const mode of ['month','week','day']){
  await page.goto('/#/plans/calendar?mode='+mode+'&date='+day+'&project='+p.id);
  const card=page.locator('[data-event-id="task:'+done.id+'"]');await expect(card).toBeVisible();await expect(card).toHaveClass(/pc-completed/);await expect(card).toHaveAttribute('data-event-date',day);
  await expect(card.locator('.pc-event-name')).toHaveCSS('text-decoration-line','line-through');await expect(card.locator('.pl-appearance-icon')).toHaveCount(1);
  await page.screenshot({path:info.outputPath('calendar-completed-'+mode+'.png'),fullPage:true});
 }
 await plannerPreference(page,'Показывать выполненные задачи',false);await expect(page.locator('[data-event-id="task:'+done.id+'"]')).toHaveCount(0);
 await page.reload();expect(await page.evaluate(()=>Planner.showCompletedCalendar())).toBe(false);await expect(page.locator('[data-event-id="task:'+active.id+'"]')).toBeVisible();
 await plannerPreference(page,'Показывать выполненные задачи',true);const card=page.locator('[data-event-id="task:'+done.id+'"]');await expect(card).toBeVisible();
 await card.locator('.pc-event-title').click();await expect(page.locator('.pl-drawer')).toBeVisible();await expect(page.locator('.pl-drawer [name=title]')).toHaveValue(done.title);
 await page.goto('/#/plans/calendar?mode=day&date='+day+'&project='+p.id);
 const current=await api(request,'/tasks/'+done.id,undefined,'get');await api(request,'/tasks/'+done.id+'/restore',{expected_revision:current.revision,request_id:randomUUID()});
 await expect(page.locator('[data-event-id="task:'+done.id+'"]')).not.toHaveClass(/pc-completed/);
});

test('UX31 planner preferences save the quota and calendar choice without a schema change',async({page,request})=>{
 const original=await api(request,'/settings/project-repeat-limit',undefined,'get');
 try {
  await page.goto('/#/plans/settings');
  const form=page.getByRole('form',{name:'Ближайшие выполнения'});
  await form.getByLabel('Количество ближайших выполнений').fill('1');
  await form.getByRole('button',{name:'Сохранить лимит'}).click();
  await expect.poll(async()=>(await api(request,'/settings/project-repeat-limit',undefined,'get')).limit).toBe(1);
  await expect(form.getByLabel('Количество ближайших выполнений')).toHaveValue('1');
  await page.getByLabel('Показывать выполненные задачи',{exact:true}).uncheck();
  await page.getByRole('link',{name:'Календарь',exact:true}).click();
  expect(await page.evaluate(()=>Planner.showCompletedCalendar())).toBe(false);
  await plannerPreference(page,'Показывать выполненные задачи',true);
  await page.reload();expect(await page.evaluate(()=>Planner.showCompletedCalendar())).toBe(true);
 } finally {
  const current=await api(request,'/settings/project-repeat-limit',undefined,'get');
  await api(request,'/settings/project-repeat-limit',{limit:original.limit,expected_revision:current.revision},'put');
 }
});
