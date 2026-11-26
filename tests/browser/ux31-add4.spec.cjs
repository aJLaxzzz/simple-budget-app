const {test,expect}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto');const fs=require('node:fs');
const today=()=>new Intl.DateTimeFormat('sv-SE',{timeZone:'Europe/Moscow'}).format(new Date());
const add=(d,n)=>new Date(Date.parse(d+'T12:00:00Z')+n*86400000).toISOString().slice(0,10);
async function api(r,path,data,method=data?'post':'get'){const res=await r[method]('/api/planner/'+path,data?{data}:{});expect(res.ok(),await res.text()).toBeTruthy();return res.json();}
async function daily(r){const input={anchor_date:today(),has_start:true,rule:{schema_version:1,mode:'schedule',unit:'days',interval:1,allowed_days:{mode:'any'},adjustment:'none'},snapshot:{title:'Дейлик',tasks:[{key:'root',title:'Основная задача',planned_offset:0},{key:'child',parent_key:'root',title:'Следующий шаг',planned_offset:1}]}};const preview=await api(r,'project-drafts/preview',input);return (await api(r,'project-drafts/apply',{...input,preview_token:preview.preview_token,request_id:randomUUID()})).project;}
async function shot(page,name){fs.mkdirSync('artifacts/ux31-add4',{recursive:true});await page.screenshot({path:'artifacts/ux31-add4/'+name+'.png',fullPage:true});}
async function module(page,name){await page.locator('#module-switch').click();await page.locator('#module-menu').getByRole('link',{name,exact:true}).click();}

test('Add4 shared navigation preserves modules, links, settings, backup and compact STG',async({page})=>{
 await page.route('**/api/environment',r=>r.fulfill({json:{staging:true}}));
 await page.goto('/');await expect(page).toHaveURL(/#\/plans\/calendar\?mode=month/);await expect(page.locator('#module-switch')).toContainText('Планы');await expect(page.locator('a.logo')).toHaveCount(0);await expect(page.locator('nav[aria-label="Разделы планировщика"]')).toHaveCount(1);
 await page.locator('#module-tabs').getByRole('link',{name:'Задачи',exact:true}).click();await expect(page.getByRole('link',{name:'Все задачи',exact:true})).toBeVisible();await shot(page,'plans');
 await module(page,'Бюджет');await expect(page.locator('#module-switch')).toContainText('Бюджет');await expect(page.locator('#backup-button')).toBeVisible();await expect(page.locator('#stg-banner')).toBeVisible();
 await page.locator('#module-tabs').getByRole('link',{name:'Аналитика за год',exact:true}).click();await expect(page).toHaveURL(/#\/year$/);await shot(page,'budget');
 await page.locator('#module-tabs').getByRole('link',{name:'Категории',exact:true}).click();await expect(page).toHaveURL(/#\/categories$/);const download=page.waitForEvent('download');await page.locator('#backup-button').click();expect((await download).suggestedFilename()).toBe('budget-backup.json');
 await module(page,'Планы');await expect(page).toHaveURL(/#\/plans\/tasks$/);await expect(page.locator('#backup-button')).toBeHidden();await module(page,'Бюджет');await expect(page).toHaveURL(/#\/categories$/);
 await page.getByRole('link',{name:'Настройки планировщика',exact:true}).click();await expect(page).toHaveURL(/#\/plans\/settings$/);await expect(page.getByRole('heading',{name:'Повторения',exact:true})).toBeVisible();await page.goBack();await expect(page).toHaveURL(/#\/categories$/);await expect(page.locator('#module-switch')).toContainText('Бюджет');
 for(const width of [390,700,1200]){await page.setViewportSize({width,height:900});await expect(page.locator('#stg-banner')).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);}
 await page.goto('/#/plans/calendar?mode=day&date=2026-11-27');await expect(page.locator('.pc-time-scroll')).toBeVisible();await expect(page.locator('#module-tabs [aria-current=page]')).toHaveText('Календарь');
});

test('Add4 one series card per day retains child context, progress and expansion through SSE',async({page,request})=>{
 const p=await daily(request),root=p.tasks.find(t=>!t.parent_task_id),child=p.tasks.find(t=>t.parent_task_id);
 await api(request,'tasks/'+root.id+'/complete',{expected_revision:root.revision,completed_date:today(),children_decision:'keep_open',related_revisions:{[child.id]:child.revision}});
 await page.goto('/#/plans/tasks?project=series:'+p.repeat_series_id+'&future=all');const card=page.locator('.pl-group-tomorrow .pl-project-task-group[data-series-id="'+p.repeat_series_id+'"]');await expect(card).toHaveCount(1);await expect(card.locator(':scope > summary')).toContainText('Выполнений: 2');await card.locator(':scope > summary').click();await expect(card.locator('[data-task-id]')).toHaveCount(2);await expect(card).toContainText('Подзадача · Основная задача');await expect(card).toContainText('Выполнение ·');await shot(page,'series-group');
 const current=await api(request,'tasks/'+child.id);await api(request,'tasks/'+child.id,{expected_revision:current.revision,title:'Шаг изменён другим клиентом'},'patch');await expect(card).toContainText('Шаг изменён другим клиентом');await expect(card).toHaveAttribute('open','');
 const changed=await api(request,'tasks/'+child.id);await api(request,'tasks/'+child.id+'/complete',{expected_revision:changed.revision,completed_date:today()});await expect(card.locator('.pl-project-group-progress')).toContainText('1 / 2');await expect(card.locator('[data-task-id]')).toHaveCount(1);await expect(card).toHaveAttribute('open','');
});

test('Add4 project-page date edit, daily to weekly, second edit preserve IDs and all weeks',async({page,request})=>{
 const p=await daily(request),series=await api(request,'series/'+p.repeat_series_id),selected=series.occurrences[0].project,child=selected.tasks.find(t=>t.parent_task_id);
 async function editDate(date){await page.goto('/#/plans/projects/'+selected.id+'?task='+child.id);const ed=page.getByRole('dialog',{name:'Редактор задачи'});await ed.getByLabel('Плановая дата',{exact:true}).fill(date);await ed.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);}
 await editDate(add(selected.start_date,2));await page.getByRole('button',{name:'Настроить повторение',exact:true}).click();const editor=page.getByRole('dialog',{name:'Настроить повторение'});await editor.getByLabel('Интервал',{exact:true}).selectOption('weeks');await editor.getByRole('button',{name:'Посмотреть изменения',exact:true}).click();await expect(editor).toContainText('защищено: 0');await editor.getByRole('button',{name:'Применить изменения',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);
 const moved=await api(request,'projects/'+selected.id);expect(moved.start_date).toBe(add(today(),7));await editDate(add(moved.start_date,3));const after=await api(request,'series/'+series.id);expect(after.occurrences.map(o=>o.id)).toEqual(series.occurrences.map(o=>o.id));
 for(const o of after.occurrences){expect(o.project.start_date).toBe(add(today(),o.cycle_index*7));const task=o.project.tasks.find(t=>t.parent_task_id);expect(task.planned_date).toBe(add(o.project.start_date,3));expect(o.project.tasks.some(t=>t.id===task.parent_task_id)).toBe(true);}
 await page.goto('/#/plans/calendar?mode=month&date='+today()+'&project=series:'+series.id);await expect(page.locator('.pc-calendar')).toBeVisible();await shot(page,'weekly-calendar');
});

test('Add4 switching modules respects an unsaved task draft',async({page})=>{
 await page.goto('/#/plans/tasks?new=task');const editor=page.getByRole('dialog',{name:'Новая задача'});await editor.getByLabel('Название',{exact:true}).fill('Черновик перед переключением');
 // Drawer traps pointer focus; request the same destination as the module link.
 await page.evaluate(()=>location.hash='#/');await expect(page.getByRole('dialog',{name:'Несохранённые изменения'})).toBeVisible();await page.getByRole('button',{name:'Продолжить редактирование',exact:true}).click();await expect(editor.getByLabel('Название',{exact:true})).toHaveValue('Черновик перед переключением');await expect(page.locator('#module-switch')).toContainText('Планы');
});

 test('Add4 module switch protects unsaved financial category changes',async({page})=>{
 await page.goto('/#/categories');await page.locator('#add-income-cat').click();const input=page.locator('.categories-page input').last();await input.fill('Черновик категории');
 page.once('dialog',dialog=>dialog.dismiss());await module(page,'Планы');await expect(page).toHaveURL(/#\/categories$/);await expect(input).toHaveValue('Черновик категории');await expect(page.locator('#module-switch')).toContainText('Бюджет');
 page.once('dialog',dialog=>dialog.accept());await module(page,'Планы');await expect(page).toHaveURL(/#\/plans\/calendar/);await expect(page.locator('#module-switch')).toContainText('Планы');
 });
