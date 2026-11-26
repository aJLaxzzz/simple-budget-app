const {test,expect}=require('../browser-fixtures.cjs');const fs=require('node:fs');const {randomUUID}=require('node:crypto');
async function api(request,path,data,method=data?'post':'get'){const r=await request[method]('/api/planner/'+path,data?{data}:{});expect(r.ok(),await r.text()).toBeTruthy();return r.json();}
function timing(name,ms){const dir='artifacts/ux2-stage5';fs.mkdirSync(dir,{recursive:true});let data={};try{data=JSON.parse(fs.readFileSync(dir+'/timings.json'));}catch(_){}data[name]=ms;fs.writeFileSync(dir+'/timings.json',JSON.stringify(data,null,2));}
let fixture;
test.beforeAll(async({request})=>{
 for(const year of [2026,2027]){const calendar=await api(request,'production-calendar/bundled/'+year);const p=await api(request,'production-calendar/preview',{calendar});await api(request,'production-calendar/import',{calendar,expected_revision:p.expected_revision,preview_token:p.preview_token,source_confirmed:true,request_id:randomUUID()});}
 const p=await api(request,'projects',{title:'Дом · уборка и забота о себе',color_id:'teal',icon_id:'lucide:broom',description:'Небольшие дела, которые делают дом уютнее.'});
 const other=await api(request,'projects',{title:'Работа',color_id:'purple',icon_id:'lucide:briefcase'});
 const t=await api(request,'tasks',{title:'Помыть окна и разобрать полки в гостиной — подготовиться к весне',project_id:p.id,planned_date:'2027-02-23',icon_id:'lucide:brush-cleaning',due_date:'2027-02-24'});
 await api(request,'tasks',{title:'Купить продукты к праздничному ужину',project_id:p.id,planned_date:'2027-02-23',icon_id:'lucide:shopping-basket',color_id:'orange'});
 await api(request,'tasks',{title:'Проверить рабочие документы',project_id:other.id,planned_date:'2027-02-23'});
 for(let i=0;i<7;i++)await api(request,'tasks',{title:['Постирать бельё','Убрать кухню','Вынести мусор','Почистить ванную'][i%4],project_id:p.id,planned_date:'2027-02-23',icon_id:['lucide:washing-machine','lucide:mop','lucide:trash-2','lucide:bath'][i%4]});
 for(let i=0;i<4;i++)await api(request,'sessions',{task_id:t.id,date:'2027-02-23',start_time:'10:00',end_time:'11:00'});
 fixture={p,t,other};
});
test('expanded picker Russian search, bounded DOM, categories, colors and persisted selection',async({page,request})=>{
 const t=await api(request,'tasks',{title:'Выбор оформления '+randomUUID()});
 await page.goto('/#/plans/tasks?task='+t.id);const editor=page.getByRole('dialog',{name:'Редактор задачи',exact:true});
 const start=Date.now();await editor.getByRole('button',{name:'Оформление',exact:true}).click();await expect(page.getByRole('dialog',{name:'Оформление',exact:true}).getByRole('button',{name:'Дом',exact:true})).toBeVisible();timing('picker-ms',Date.now()-start);
 expect(await page.locator('.pl-appearance-dialog .pl-icon-choice').count()).toBeLessThanOrEqual(56);
 await page.getByRole('dialog',{name:'Оформление',exact:true}).getByRole('button',{name:'Бирюзовый',exact:true}).click();const search=page.getByRole('dialog',{name:'Оформление',exact:true}).getByLabel('Поиск иконки',{exact:true});await search.fill('УБОРКА');await expect(page.getByRole('dialog',{name:'Оформление',exact:true}).getByRole('button',{name:'Метла',exact:true})).toBeVisible();
 await page.getByRole('dialog',{name:'Оформление',exact:true}).getByRole('button',{name:'Метла',exact:true}).click();await expect(editor.locator('.pl-appearance-preview .pl-appearance-icon')).toHaveCSS('background-color','rgb(101, 198, 179)');
 await search.fill('щетка');await expect(page.getByRole('dialog',{name:'Оформление',exact:true}).getByRole('button',{name:'Щётка для уборки',exact:true})).toBeVisible();await search.fill('кот');await expect(page.getByRole('dialog',{name:'Оформление',exact:true}).getByRole('button',{name:'Кошка',exact:true})).toBeVisible();
 await search.fill('');await page.getByRole('dialog',{name:'Оформление',exact:true}).getByLabel('Категория иконок').selectOption('Дом и уборка');expect(await page.locator('.pl-appearance-dialog .pl-icon-choice').count()).toBe(23);
 await page.getByRole('dialog',{name:'Оформление',exact:true}).getByRole('button',{name:'Готово',exact:true}).click();await editor.locator('button[type=submit]').click();await expect.poll(async()=>(await api(request,'tasks/'+t.id)).icon_id).toBe('lucide:broom');
});
test('official days, missing year, visible filters, reset, preference and navigation',async({page})=>{
 await page.goto('/#/plans/calendar?mode=month&date=2027-02-23&project='+fixture.p.id);
 await expect(page.locator('[data-date="2027-02-20"]')).toHaveAttribute('data-calendar-kind','working_weekend');await expect(page.locator('[data-date="2027-02-22"]')).toHaveAttribute('data-calendar-kind','transferred_day_off');await expect(page.locator('[data-date="2027-02-23"]')).toHaveAttribute('data-calendar-kind','holiday');
 await expect(page.locator('[data-date="2027-02-23"] .pc-more')).toBeVisible();await expect(page.locator('.pc-active-filters')).toContainText(fixture.p.title);
 await page.getByRole('button',{name:'Сбросить фильтры',exact:true}).click();await expect(page).not.toHaveURL(/project=/);await expect(page).toHaveURL(/date=2027-02-23/);await expect(page.locator('.pc-calendar')).toBeVisible();
 await page.goto('/#/plans/calendar?mode=week&date=2027-02-20');await expect(page.locator('[data-time-day="2027-02-20"]')).toHaveAttribute('data-calendar-kind','working_weekend');
 await page.goto('/#/plans/calendar?mode=day&date=2027-02-23');await expect(page.locator('[data-allday="2027-02-23"]')).toHaveAttribute('data-calendar-kind','holiday');
 await page.goto('/#/plans/settings');await page.getByLabel('Показывать праздники и выходные').uncheck();await page.goto('/#/plans/calendar?mode=month&date=2027-02-23');await expect(page.locator('.pc-calendar')).toBeVisible();expect(await page.locator('[data-calendar-kind]').count()).toBe(0);
 await page.goto('/#/plans/settings');await page.getByLabel('Показывать праздники и выходные').check();await page.goto('/#/plans/calendar?mode=month&date=2025-02-23');await expect(page.locator('.pc-calendar-missing')).toContainText('2025');expect(await page.locator('[data-calendar-kind]').count()).toBe(0);
 await page.goto('/#/plans/calendar?mode=week&date=2026-12-31');await expect(page.locator('[data-day="2026-12-31"]')).toHaveAttribute('data-calendar-kind','transferred_day_off');await expect(page.locator('[data-day="2027-01-01"]')).toHaveAttribute('data-calendar-kind','holiday');
});
for(const width of [1440,1024,768,390,360])test('UX final visual '+width,async({page})=>{
 test.setTimeout(60000); // Thirteen full-page captures are one visual audit workflow.
 await page.setViewportSize({width,height:1000});const dir='artifacts/ux2-stage5/after';fs.mkdirSync(dir,{recursive:true});
 const capture=async name=>{await expect.poll(()=>page.evaluate(()=>window.budgetRequests||0)).toBe(0);await expect(page.locator('.pl-loading')).toHaveCount(0);expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);await page.screenshot({path:dir+'/'+name+'-'+width+'.png',fullPage:!(await page.locator('dialog[open]').count()),animations:'disabled'});};
 for(const mode of ['month','week','day']){const start=Date.now();await page.goto('/#/plans/calendar?mode='+mode+'&date=2027-02-23&project='+fixture.p.id);await expect(page.locator('.pc-calendar')).toBeVisible();await expect(page.locator('.pc-day-description')).toContainText('Государственный праздник');timing('calendar-'+mode+'-'+width+'-ms',Date.now()-start);if(mode==='week'&&width<=900)await expect(page.locator('.pc-week-strip')).toBeVisible();await capture('calendar-'+mode);}
 await page.goto('/#/plans/tasks?project='+fixture.p.id);await page.locator('.pl-group-later > summary').click();await page.locator('.pl-group-later .pl-project-task-group > summary').click();await expect(page.locator('[data-task-id="'+fixture.t.id+'"]')).toBeVisible();await capture('tasks');
 await page.goto('/#/plans/projects/'+fixture.p.id);await expect(page.getByRole('heading',{name:fixture.p.title,exact:true})).toBeVisible();await capture('project');
 await page.getByRole('button',{name:fixture.t.title,exact:true}).click();const editor=page.getByRole('dialog',{name:'Редактор задачи',exact:true});await expect(editor).toBeVisible();await capture('editor');await editor.getByRole('button',{name:'Оформление',exact:true}).click();await expect(page.getByRole('dialog',{name:'Оформление',exact:true}).getByRole('button',{name:'Дом',exact:true})).toBeVisible();await capture('icons');await page.getByRole('dialog',{name:'Оформление',exact:true}).getByLabel('Поиск иконки',{exact:true}).fill('уборка');await capture('icons-cleaning');
 for(const [route,name,ready]of [['#/plans/projects','projects','.pl-project-card'],['#/plans/attention','attention','.pl-tabs'],['#/plans/series','recurring','.pl-tabs'],['#/plans/templates','templates','.pl-tabs'],['#/plans/settings','settings','.pl-calendar-years']]){await page.goto('/?ux5='+name+route);await expect(page.locator(ready).first()).toBeVisible();await expect(page.locator('.pl-loading')).toHaveCount(0);if(name==='settings')await expect(page.getByText('Загрузить проверенные данные',{exact:true})).toBeVisible();await capture(name);}
});

test('transient resize keeps calendar, settled mobile width switches layout',async({page})=>{
 await page.goto('/#/plans/calendar?mode=week&date=2027-02-23');await expect(page.locator('.pc-calendar')).toBeVisible();await expect.poll(()=>page.evaluate(()=>window.budgetRequests||0)).toBe(0);
 let queries=0;page.on('request',r=>{if(r.url().includes('/api/planner/calendar?'))queries++;});
 await page.setViewportSize({width:1,height:1});await page.setViewportSize({width:1440,height:1000});await page.waitForTimeout(300);expect(queries).toBe(0);await expect(page.locator('.pc-calendar')).toBeVisible();
 await page.setViewportSize({width:390,height:1000});await expect(page.locator('.pc-week-strip')).toBeVisible();expect(queries).toBe(1);
});
