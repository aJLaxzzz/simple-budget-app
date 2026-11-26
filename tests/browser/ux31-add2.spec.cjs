const {test,expect}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto');
const fs=require('node:fs');
async function api(r,path,data,method=data?'post':'get'){const a=await r[method]('/api/planner/'+path,data?{data}:{});expect(a.ok(),await a.text()).toBeTruthy();return a.json();}
const today=()=>new Intl.DateTimeFormat('sv-SE',{timeZone:'Europe/Moscow'}).format(new Date());
const add=(d,n)=>{const a=new Date(d+'T12:00:00Z');a.setUTCDate(a.getUTCDate()+n);return a.toISOString().slice(0,10)};
async function settings(p){await p.goto('/#/plans/settings');await expect(p.getByRole('heading',{name:'Повторения',exact:true})).toBeVisible();}
async function shot(p,name){fs.mkdirSync('artifacts/ux31-add2',{recursive:true});await p.screenshot({path:'artifacts/ux31-add2/'+name+'.png',fullPage:true});}
async function task(r,p,title,time=null){return api(r,'tasks',{title,project_id:p.id,planned_date:today(),scheduled_time:time});}
async function done(r,t){return api(r,'tasks/'+t.id+'/complete',{expected_revision:t.revision,completed_date:today(),request_id:randomUUID()});}

test('Addition2 empty recurring project: single atomic create, default shared addition, all future tasks and series filter',async({page,request})=>{
 const title='Уборка приёмка '+randomUUID().slice(0,5);
 await page.goto('/#/plans/projects?new=project');await page.getByLabel('Название проекта',{exact:true}).fill(title);
 await page.getByText('Даты и повторение',{exact:true}).click();await page.getByLabel('Повторение',{exact:true}).selectOption('yes');await page.getByLabel('Интервал',{exact:true}).selectOption('weeks');await page.getByLabel('Каждые',{exact:true}).fill('1');
 await page.getByRole('button',{name:'Создать проект',exact:true}).click();await expect(page.locator('.pl-dialog')).toHaveCount(0);
 const id=new URL(page.url()).hash.split('/').at(-1);let p=await api(request,'projects/'+id);expect(p.tasks).toHaveLength(0);expect(p.repeat_series_id).toBeTruthy();
 await expect(page.getByLabel('Добавить задачу в',{exact:true})).toHaveValue('this_and_following');
 await page.getByLabel('Название новой задачи',{exact:true}).fill('Пропылесосить комнаты');await page.getByRole('button',{name:'Добавить',exact:true}).click();
 await expect(page.getByText(/Следующих обновится: 2/)).toBeVisible();await page.getByRole('button',{name:'Применить изменения',exact:true}).click();await expect(page.locator('.pl-dialog')).toHaveCount(0);
 p=await api(request,'projects/'+id);const series=await api(request,'series/'+p.repeat_series_id);expect(p.tasks).toHaveLength(1);for(const o of series.occurrences)expect(o.project.tasks).toHaveLength(1);
 await shot(page,'project');
 await page.goto('/#/plans/tasks?project=series:'+p.repeat_series_id+'&future=all');await expect(page.locator('.pl-list-count')).toHaveText('Задач: 3');await expect(page.locator('select[name=project] option').filter({hasText:title})).toHaveCount(1);
 await page.locator('select[name=future]').selectOption('nearest');await expect(page.locator('.pl-list-count')).toHaveText('Задач: 1');
 await page.goto('/#/plans/calendar?mode=month&project=series:'+p.repeat_series_id);await expect(page.locator('select[name=calendar_project] option').filter({hasText:title})).toHaveCount(1);
 await page.goto('/#/plans/projects/'+series.occurrences[0].project.id);await expect(page.getByRole('button',{name:'Пропылесосить комнаты',exact:true})).toBeVisible();
});

test('Addition2 quick complete preserves children; project circle waits for every task and never auto-closes',async({page,request})=>{
 const p=await api(request,'projects',{title:'Проверить документы '+randomUUID().slice(0,5)}),parent=await task(request,p,'Документы'),child=await api(request,'tasks',{title:'Проверить подписи',project_id:p.id,parent_task_id:parent.id,planned_date:today()});
 await page.goto('/#/plans/projects/'+p.id);await expect(page.locator('.pl-project-complete')).toBeDisabled();
 await page.locator('[data-task-id="'+parent.id+'"] .pl-check').first().click();await expect(page.locator('#planner-toast')).toContainText('выполнена');await expect(page.locator('.pl-dialog')).toHaveCount(0);
 expect((await api(request,'tasks/'+child.id)).status).toBe('planned');await expect(page.getByRole('button',{name:'Проверить подписи',exact:true})).toBeVisible();await expect(page.locator('.pl-project-complete')).toBeDisabled();
 await page.getByRole('button',{name:'Отменить завершение',exact:true}).click();await expect.poll(async()=>(await api(request,'tasks/'+parent.id)).status).toBe('planned');await expect(page.locator('.pl-dialog')).toHaveCount(0);
 await page.locator('[data-task-id="'+parent.id+'"] .pl-check').first().click();await expect(page.locator('#planner-toast')).toContainText('выполнена');
 await page.locator('[data-task-id="'+child.id+'"] .pl-check').first().click();await expect(page.locator('.pl-project-complete')).toBeEnabled();expect((await api(request,'projects/'+p.id)).status).toBe('planned');
 await page.locator('.pl-project-complete').click();await expect(page.locator('.pl-project-complete')).toHaveAttribute('aria-label','Проект завершён');expect((await api(request,'projects/'+p.id)).status).toBe('completed');
 await page.goto('/#/plans/projects');await expect(page.locator('[data-project-id="'+p.id+'"] .pl-project-complete')).toHaveText('✓');
});

test('Addition2 calendar groups month and untimed by status, timed events keep positions, settings persist',async({page,request})=>{
 const p=await api(request,'projects',{title:'Календарь состояний',color_id:'cyan'});
 const cancelled=await task(request,p,'Отменённая встреча','08:00'),completed=await task(request,p,'Готовый отчёт','09:00'),active=await task(request,p,'Встреча с командой','15:00');
 await done(request,completed);await api(request,'tasks/'+cancelled.id+'/cancel',{expected_revision:cancelled.revision});

 await settings(page);await page.getByLabel('Показывать отменённые задачи',{exact:true}).check();await shot(page,'settings');
 await page.goto('/#/plans/calendar?mode=month&date='+today()+'&project='+p.id);await expect(page.locator('.pc-calendar')).toBeVisible();
 const cell=page.locator('.pc-month-day[data-date="'+today()+'"]');
 const scope=cell;
 const ids=await scope.locator('.pc-event').evaluateAll(ns=>ns.map(n=>n.dataset.eventId));expect(ids.indexOf('task:'+active.id)).toBeLessThan(ids.indexOf('task:'+completed.id));expect(ids.indexOf('task:'+completed.id)).toBeLessThan(ids.indexOf('task:'+cancelled.id));
 if(await page.locator('.pl-dialog').count())await page.keyboard.press('Escape');await shot(page,'calendar-month');
 const unDone=await task(request,p,'Подготовить материалы'),unActive=await task(request,p,'Разобрать почту');await done(request,unDone);
 for(const mode of ['day','week']){await page.goto('/#/plans/calendar?mode='+mode+'&date='+today()+'&project='+p.id);await expect(page.locator('.pc-time-scroll')).toBeVisible();const untimed=page.locator('[data-allday="'+today()+'"]');await expect(untimed.locator('.pc-event').first()).toHaveAttribute('data-event-id','task:'+unActive.id);await expect(untimed.locator('.pc-completed')).toHaveCount(1);
 const top=async t=>page.locator('[data-event-id="task:'+t.id+'"]').evaluate(n=>parseFloat(n.style.top));expect(await top(cancelled)).toBeLessThan(await top(completed));expect(await top(completed)).toBeLessThan(await top(active));}
 await shot(page,'calendar-week');await settings(page);await page.getByLabel('Показывать выполненные задачи',{exact:true}).uncheck();await page.goto('/#/plans/calendar?mode=month&project='+p.id);await expect(page.locator('.pc-completed')).toHaveCount(0);await page.reload();await expect(page.locator('.pc-completed')).toHaveCount(0);
 await settings(page);await page.getByLabel('Показывать выполненные задачи',{exact:true}).check();await page.getByLabel('Показывать отменённые задачи',{exact:true}).uncheck();
});

test('Addition2 landing, Today and empty quick Add preserve explicit routes and creation access',async({page})=>{
 await page.goto('/');await expect(page).toHaveURL(/#\/plans\/calendar\?mode=month&date=/);await expect(page.locator('.pc-month')).toBeVisible();
 await page.goto('/#/');await expect(page.locator('.planner')).toHaveCount(0);
 await page.goto('/#/plans/calendar?mode=day&date=2026-01-01');await page.getByRole('button',{name:'Сегодня',exact:true}).click();await expect(page.locator('.pc-month')).toBeVisible();expect(new URLSearchParams(new URL(page.url()).hash.split('?')[1]).get('date')).toBe(today());
 await page.goto('/#/plans/tasks');await expect(page.getByRole('button',{name:'Создать',exact:true})).toHaveCount(0);await page.getByLabel('Название новой задачи',{exact:true}).fill('  ');await page.getByRole('button',{name:'Добавить',exact:true}).click();await expect(page.getByLabel('Повторять задачу',{exact:true})).toBeVisible();
});

test('Addition2 shared limit, settings-only controls, flat list and visual settings tabs',async({page,request})=>{
 const original=await api(request,'settings/project-repeat-limit');
 try{
 await settings(page);await page.getByLabel('Количество ближайших выполнений').fill('5');await page.getByRole('button',{name:'Сохранить лимит',exact:true}).click();await expect(page.locator('#planner-toast')).toContainText('Лимит сохранён');await page.getByLabel('Группировать по проектам',{exact:true}).uncheck();
 await page.goto('/#/plans/tasks');await expect(page.locator('select[name=future] option').first()).toHaveText('5 ближайших выполнений');await expect(page.getByLabel('Группировать по проектам',{exact:true})).toHaveCount(0);
 await settings(page);await expect(page.getByLabel('Группировать по проектам',{exact:true})).not.toBeChecked();await page.getByLabel('Группировать по проектам',{exact:true}).check();
 for(const name of ['Производственный календарь','Оформление','Резервные копии','Безопасность']){await page.getByRole('link',{name,exact:true}).click();await expect(page.getByText('Ранее сохранённые шаблоны',{exact:true})).toHaveCount(0);await expect(page.locator('.pl-settings')).toBeVisible();}
 }finally{const fresh=await api(request,'settings/project-repeat-limit');await api(request,'settings/project-repeat-limit',{limit:original.limit,expected_revision:fresh.revision},'put');}
});

test('Addition2 choosing a recurring project in a new editor offers shared scope; editing keeps this-only',async({page,request})=>{
 const input={request_id:randomUUID(),anchor_date:today(),has_start:true,project_mode:'independent',snapshot:{title:'Состав '+randomUUID().slice(0,5),due_offset:2,tasks:[]},rule:{schema_version:1,mode:'schedule',unit:'weeks',interval:1,allowed_days:{mode:'any'},adjustment:'none'}};
 const preview=await api(request,'project-drafts/preview',input),created=await api(request,'project-drafts/apply',{...input,preview_token:preview.preview_token});const p=created.project;
 await page.goto('/#/plans/tasks');await page.getByRole('button',{name:'Добавить',exact:true}).click();
 const editor=page.getByRole('dialog',{name:'Новая задача'});await editor.getByText('Проект и подзадача',{exact:false}).click();
 await editor.locator('select[name=project_id]').selectOption(p.id);await expect(editor.getByLabel('Добавить задачу в',{exact:true})).toHaveValue('this_and_following');
 await editor.getByLabel('Название',{exact:true}).fill('Из полного редактора');await editor.getByRole('button',{name:'Сохранить',exact:true}).click();await page.getByRole('button',{name:'Применить изменения',exact:true}).click();await expect(page.locator('.pl-dialog')).toHaveCount(0);
 const fresh=await api(request,'projects/'+p.id);expect(fresh.tasks).toHaveLength(1);const series=await api(request,'series/'+p.repeat_series_id);expect(series.occurrences.every(o=>o.project.tasks.length===1)).toBe(true);
 await page.goto('/#/plans/tasks?task='+fresh.tasks[0].id);await expect(page.getByLabel('Применить изменение',{exact:true})).toHaveValue('this');
 await page.goto('/#/plans/projects');await expect(page.locator('[data-project-id="'+p.id+'"]')).toBeVisible();fs.mkdirSync('artifacts/ux31-add2',{recursive:true});await page.locator('[data-project-id="'+p.id+'"]').screenshot({path:'artifacts/ux31-add2/project-card.png'});
 await page.locator('[data-project-id="'+p.id+'"] .pl-project-date').click();await expect(page).toHaveURL(new RegExp('#/plans/projects/'+p.id+'$'));await expect(page.getByRole('heading',{name:p.title,exact:true})).toBeVisible();
});

test('Addition2 security cards and STG backup status use shared settings styling',async({page})=>{
 await page.route('**/api/auth/session',r=>r.fulfill({json:{enabled:true,id:'local-test-session',csrf_token:'synthetic'}}));
 await page.route('**/api/auth/sessions',r=>r.fulfill({json:[{id:'local-test-session',current:true,created:'2026-11-26T08:00:00Z',active:'2026-11-26T09:00:00Z'},{id:'other-test-session',current:false,created:'2026-11-25T08:00:00Z',active:'2026-11-26T07:00:00Z'}]}));
 await page.goto('/#/plans/settings?section=security');await expect(page.locator('.pl-session-card')).toHaveCount(2);await expect(page.locator('.pl-security button:not(.pl-button)')).toHaveCount(0);await shot(page,'security');
 await page.route('**/api/environment',r=>r.fulfill({json:{staging:true}}));await page.route('**/api/backups/status',r=>r.fulfill({json:{automatic:'unknown',daily_count:0,stale:true}}));
 await page.getByRole('link',{name:'Резервные копии',exact:true}).click();await expect(page.locator('.pl-backups')).toContainText('намеренно отключены');await expect(page.locator('.pl-backups')).not.toContainText('требует проверки');await shot(page,'backups-stg');
});
