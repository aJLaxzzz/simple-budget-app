const {test,expect}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto'),fs=require('node:fs');
const today=()=>new Intl.DateTimeFormat('sv-SE',{timeZone:'Europe/Moscow'}).format(new Date());
const add=(n)=>new Date(Date.parse(today()+'T12:00:00Z')+n*86400000).toISOString().slice(0,10);
async function api(r,path,data,method=data?'post':'get'){const res=await r[method]('/api/planner/'+path,data?{data}:{});expect(res.ok(),await res.text()).toBeTruthy();return res.json();}
async function shot(page,name){fs.mkdirSync('artifacts/mobile-tasks-projects-3.3',{recursive:true});await page.screenshot({path:'artifacts/mobile-tasks-projects-3.3/'+name+'.png',animations:'disabled'});}
async function ready(page){await expect(page.locator('.pl-content')).not.toBeEmpty();await expect(page.locator('.pl-content .pl-loading')).toHaveCount(0);}
async function fit(page){expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBe((await page.viewportSize()).width);}
async function daily(r){const input={anchor_date:today(),has_start:true,rule:{schema_version:1,mode:'schedule',unit:'days',interval:1,allowed_days:{mode:'any'},adjustment:'none'},snapshot:{title:'Дейлик команды',tasks:[{key:'root',title:'Обсудить план работы',planned_offset:0},{key:'child',parent_key:'root',title:'Отправить итоги встречи',planned_offset:1}]}};const preview=await api(r,'project-drafts/preview',input);return (await api(r,'project-drafts/apply',{...input,preview_token:preview.preview_token,request_id:randomUUID()})).project;}

test('3.3 mobile tasks: quick add, filters, geometry and actual screenshots',async({page,request})=>{
 for(const title of ['Подготовить презентацию','Заказать продукты','Обсудить очень длинное название задачи с командой и согласовать план следующей недели'])await api(request,'tasks',{title,planned_date:today(),scheduled_time:title.startsWith('Подготовить')?'16:00':null});
 await page.setViewportSize({width:390,height:844});await page.goto('/#/plans/tasks');await ready(page);
 for(const width of [320,360,390,412,480,1024,1440]){await page.setViewportSize({width,height:900});await fit(page);const form=page.locator('.pl-quick').first();if(width<768){expect((await form.getByRole('button',{name:'Добавить',exact:true}).boundingBox()).width).toBe(44);expect((await page.getByRole('button',{name:'Заказать продукты',exact:true}).locator('..').locator('..').boundingBox()).height).toBeLessThanOrEqual(76);}}
 await page.setViewportSize({width:390,height:844});await shot(page,'01-mobile-tasks');
 const form=page.locator('.pl-quick').first();await form.getByRole('button',{name:'Добавить',exact:true}).click();const ed=page.getByRole('dialog',{name:'Новая задача'});await expect(ed).toBeVisible();await ed.getByRole('button',{name:'Закрыть редактор',exact:true}).click();await expect(ed).toHaveCount(0);
 await form.locator('input[name=title]').fill('Быстро с телефона');await form.getByRole('button',{name:'Добавить',exact:true}).click();await expect(page.getByRole('button',{name:'Быстро с телефона',exact:true})).toBeVisible();
 await form.locator('input[name=title]').fill('Быстро через Enter');await form.locator('input[name=title]').press('Enter');await expect(page.getByRole('button',{name:'Быстро через Enter',exact:true})).toBeVisible();
 const filters=page.locator('.pl-task-filters');await filters.locator('summary').click();await filters.locator('[name=q]').fill('Быстро');await expect(filters.locator('summary')).toContainText('1');await filters.locator('summary').click();await expect(page.getByRole('button',{name:'Подготовить презентацию',exact:true})).toHaveCount(0);await filters.locator('summary').click();await expect(filters.locator('[name=q]')).toHaveValue('Быстро');await filters.getByRole('button',{name:'Сбросить фильтры',exact:true}).click();await expect(page.getByRole('button',{name:'Подготовить презентацию',exact:true})).toBeVisible();
});

test('3.3 multiday grouping, true parents, progress and expansion through SSE',async({page,request})=>{
 const p=await daily(request),root=p.tasks.find(t=>!t.parent_task_id),child=p.tasks.find(t=>t.parent_task_id);
 await api(request,'tasks/'+root.id+'/complete',{expected_revision:root.revision,completed_date:today(),children_decision:'keep_open',related_revisions:{[child.id]:child.revision}});
 await page.setViewportSize({width:390,height:844});await page.goto('/#/plans/tasks?project=series:'+p.repeat_series_id+'&future=all');
 const card=page.locator('.pl-group-tomorrow .pl-project-task-group');await expect(card).toHaveCount(1);await card.locator(':scope>summary').click();await expect(card.locator('[data-task-id]')).toHaveCount(2);await expect(card.locator('[data-parent-id="'+root.id+'"]')).toContainText(root.title);await expect(card).toContainText('Выполнений: 2');await shot(page,'02-expanded-subtasks');
 const current=await api(request,'tasks/'+child.id);await api(request,'tasks/'+child.id,{expected_revision:current.revision,title:'Итоги обновлены коллегой'},'patch');await expect(card).toContainText('Итоги обновлены коллегой');await expect(card).toHaveAttribute('open','');
 const ordinary=await api(request,'projects',{title:'Подготовить дом к праздникам'});for(const [title,day,time] of [['Разобрать шкаф',10,null],['Заказать декор',11,'12:00'],['Украсить комнату',12,'15:00']])await api(request,'tasks',{title,project_id:ordinary.id,planned_date:add(day),scheduled_time:time});
 await page.goto('/#/plans/tasks?project='+ordinary.id);const multi=page.locator('.pl-group-month .pl-project-task-group');await expect(multi).toHaveCount(1);await multi.locator(':scope>summary').click();await expect(multi.locator('.pl-group-date')).toHaveCount(3);await expect(multi.locator('[data-task-id]')).toHaveCount(3);await shot(page,'03-multiday-mobile');await fit(page);
 await page.setViewportSize({width:1440,height:1000});await shot(page,'15-multiday-desktop');await fit(page);
 await page.evaluate(()=>localStorage.setItem('planner.project-groups','off'));await page.reload();await ready(page);await expect(page.locator('.pl-project-task-group')).toHaveCount(0);expect(await page.locator('.pl-task-title').allTextContents()).toEqual(['Разобрать шкаф','Заказать декор','Украсить комнату']);
});

test('3.3 compact projects, manual completion readiness, editors and responsive screens',async({page,request})=>{
 const p=await api(request,'projects',{title:'Путешествие на выходные',description:'Билеты, маршрут и вещи для поездки',start_date:today(),due_date:add(5)}),t=await api(request,'tasks',{title:'Купить билеты',project_id:p.id,planned_date:today()});
 await page.setViewportSize({width:390,height:844});await page.goto('/#/plans/projects');await ready(page);const card=page.locator('.pl-project-card').filter({hasText:p.title});await expect(card.locator('.pl-check')).toBeDisabled();await shot(page,'04-projects-mobile');
 for(const width of [320,360,390,412,480,1024,1440]){await page.setViewportSize({width,height:900});await fit(page);const circle=await card.locator('.pl-check').boundingBox(),title=await card.locator('h3').boundingBox();expect(circle.x).toBeLessThan(title.x);}
 await shot(page,'14-project-desktop');await page.setViewportSize({width:390,height:844});await card.getByRole('link',{name:p.title}).click();await ready(page);await shot(page,'05-project-page');await fit(page);
 await page.evaluate(id=>Planner.openTask(id),t.id);const ed=page.getByRole('dialog',{name:'Редактор задачи'});await expect(ed).toBeVisible();await shot(page,'08-task-editor');await ed.locator('summary').filter({hasText:'Параметры'}).click();await ed.getByLabel('Описание',{exact:true}).fill('Черновик подробного описания');await expect(ed.locator('.pl-disclosure[open] .pl-disclosure-hint').first()).toBeHidden();await page.evaluate(()=>window.currentForm=document.querySelector('.pl-editor-shell form'));
 await page.setViewportSize({width:1024,height:900});expect(await page.evaluate(()=>currentForm===document.querySelector('.pl-editor-shell form'))).toBe(true);await expect(ed.getByLabel('Описание',{exact:true})).toHaveValue('Черновик подробного описания');await page.setViewportSize({width:390,height:844});await ed.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(ed).toHaveCount(0);
 await page.goto('/#/plans/projects/'+p.id+'?edit=project&id='+p.id);const pe=page.locator('.pl-editor-shell');await expect(pe).toBeVisible();await shot(page,'09-project-editor');await expect(pe.getByRole('button',{name:'Сохранить',exact:true})).toBeInViewport();await pe.getByRole('button',{name:'Закрыть редактор',exact:true}).click();
 const current=await api(request,'tasks/'+t.id);await api(request,'tasks/'+t.id+'/complete',{expected_revision:current.revision,completed_date:today()});await page.goto('/#/plans/projects');const done=page.locator('.pl-project-card').filter({hasText:p.title});await expect(done.locator('.pl-check')).toBeEnabled();expect((await api(request,'projects/'+p.id)).status).toBe('planned');
});

test('3.3 regular entities sort, persist selection and accessible recurrence actions',async({page,request})=>{
 const p=await daily(request);for(const [title,days] of [['Я — ближайшая задача',1],['А — дальняя задача',8]])await api(request,'series',{task:{title,priority:'normal'},first_date:add(days),rule:{schema_version:1,mode:'schedule',unit:'weeks',interval:1,allowed_days:{mode:'any'},adjustment:'none'},request_id:randomUUID()});
 await page.setViewportSize({width:390,height:844});await page.goto('/#/plans/tasks?view=regular');await ready(page);await page.getByLabel('Порядок',{exact:true}).selectOption('farthest');await expect(page.locator('.pl-series-card strong').first()).toHaveText('А — дальняя задача');await shot(page,'06-regular-tasks');await page.reload();await expect(page.getByLabel('Порядок',{exact:true})).toHaveValue('farthest');await page.getByLabel('Порядок',{exact:true}).selectOption('nearest');await expect(page.locator('.pl-series-card strong').first()).toHaveText('Я — ближайшая задача');
 await page.goto('/#/plans/projects?view=regular');await ready(page);await shot(page,'07-regular-projects');await fit(page);await page.evaluate(id=>Planner.repeatEditor(id),p.repeat_series_id);
 const ed=page.getByRole('dialog',{name:'Настроить повторение'});await expect(ed).toBeVisible();await expect(ed.getByRole('button',{name:'Посмотреть изменения',exact:true})).toBeInViewport();await shot(page,'10-repeat-editor');
 for(const width of [320,360,390,412,480]){await page.setViewportSize({width,height:700});await fit(page);await expect(ed.getByRole('button',{name:'Посмотреть изменения',exact:true})).toBeInViewport();await expect(ed.getByRole('tab',{name:'Дополнительно',exact:true})).toBeInViewport();}
 await ed.getByLabel('Интервал',{exact:true}).selectOption('weeks');await ed.getByRole('button',{name:'Посмотреть изменения',exact:true}).click();await expect(ed.getByRole('button',{name:'Применить изменения',exact:true})).toBeInViewport();await ed.getByRole('button',{name:'Применить изменения',exact:true}).click();await expect(ed).toHaveCount(0);expect((await api(request,'series/'+p.repeat_series_id)).rule.rule.unit).toBe('weeks');
});

test('3.3 attention, settings and archive touch layouts preserve actions and semantics',async({page,request})=>{
 await api(request,'tasks',{title:'Согласовать смету',planned_date:add(-2),due_date:add(-1)});
 const p=await api(request,'projects',{title:'Завершённая поездка'});await api(request,'projects/'+p.id+'/archive',{expected_revision:p.revision});
 await page.setViewportSize({width:390,height:844});await page.goto('/#/plans/attention');await ready(page);await expect(page.locator('.pl-attention-list')).toContainText('Согласовать смету');await shot(page,'11-attention');await fit(page);
 await page.goto('/#/plans/settings');await ready(page);await shot(page,'12-settings');
 for(const width of [320,360,390,412,480,1024,1440]){await page.setViewportSize({width,height:900});await fit(page);}
 await page.setViewportSize({width:390,height:844});await page.getByRole('link',{name:'Архив',exact:true}).click();await ready(page);const item=page.locator('[data-archive-id="'+p.id+'"]');await expect(item).toBeVisible();await shot(page,'13-archive');await fit(page);await item.getByRole('button',{name:'Действия в архиве'}).click();await expect(page.getByRole('button',{name:'Удалить навсегда',exact:true})).toBeVisible();await page.keyboard.press('Escape');await item.getByRole('button',{name:'Вернуть из архива',exact:true}).click();await expect(page.getByRole('dialog')).toBeVisible();
});

test('3.3 recurrence dirty Back, nested confirmation and composition footer lifecycle',async({page,request})=>{
 const p=await daily(request);await page.setViewportSize({width:390,height:844});await page.goto('/#/plans/tasks');await ready(page);await page.evaluate(id=>Planner.navigate('#/plans/projects/'+id),p.id);await ready(page);await page.getByRole('button',{name:'Настроить повторение',exact:true}).click();const ed=page.getByRole('dialog',{name:'Настроить повторение'});await expect(ed).toBeVisible();await ed.getByLabel('Интервал',{exact:true}).selectOption('weeks');await page.goBack();const guard=page.getByRole('dialog',{name:'Несохранённые изменения'});await expect(guard).toBeVisible();await guard.getByRole('button',{name:'Продолжить редактирование',exact:true}).click();await expect(ed.getByLabel('Интервал',{exact:true})).toHaveValue('weeks');await expect(page).toHaveURL(new RegExp('projects/'+p.id+'$'));
 await ed.getByRole('tab',{name:'Задачи проекта',exact:true}).click();await expect(guard).toBeVisible();await guard.getByRole('button',{name:'Отменить изменения',exact:true}).click();await expect(ed.getByRole('button',{name:'Посмотреть изменения',exact:true})).toBeInViewport();
 await ed.getByRole('button',{name:'Посмотреть изменения',exact:true}).click();await expect(ed.getByRole('button',{name:'Применить изменения',exact:true})).toBeInViewport();
 // Repeating a preview replaces its confirmation, including its pinned footer.
 await ed.getByRole('button',{name:'Посмотреть изменения',exact:true}).click();await expect(ed.getByRole('button',{name:'Применить изменения',exact:true})).toHaveCount(1);
 await ed.getByRole('button',{name:'Применить изменения',exact:true}).click();await expect(ed).toHaveCount(0);
 await page.goto('/#/plans/settings?section=planner');await ready(page);await page.setViewportSize({width:1024,height:900});await expect(page.getByRole('link',{name:'Архив',exact:true})).toBeVisible();await page.getByRole('link',{name:'Архив',exact:true}).click();await expect(page.getByRole('heading',{name:'Архив',exact:true})).toBeVisible();
});

test('3.3 geometry across related views, settings and low landscape',async({page,request})=>{
 const p=await api(request,'projects',{title:'Очень длинное название проекта для проверки узких экранов',start_date:today()});await api(request,'tasks',{title:'Корневая задача',project_id:p.id,planned_date:today()});
 for(const width of [320,360,390,412,480,1024,1440]){
  await page.setViewportSize({width,height:900});
  for(const path of ['projects/'+p.id,'tasks?view=regular','projects?view=regular','attention','settings?section=calendar','settings?section=appearance','settings?section=backups','settings?section=security','settings?section=archive']){await page.goto('/#/plans/'+path);await ready(page);await fit(page);}
 }
 await page.setViewportSize({width:740,height:360});await page.goto('/#/plans/projects?new=project');const ed=page.locator('.pl-editor-shell');await expect(ed).toBeVisible();await expect(ed.getByRole('button',{name:'Создать проект',exact:true})).toBeInViewport();await fit(page);
});

test('3.3 synthetic session cards separate login and activity and retain revoke action',async({page})=>{
 const current={id:'current-synthetic',current:true,created:'2026-11-20T10:00:00Z',active:'2026-11-26T10:00:00Z'},other={id:'other-synthetic',current:false,created:'2026-11-21T10:00:00Z',active:'2026-11-25T10:00:00Z'};let revoked=false;
 await page.route('**/api/auth/session',r=>r.fulfill({json:{enabled:true,id:current.id,csrf:'synthetic'}}));
 await page.route('**/api/auth/sessions',r=>r.fulfill({json:revoked?[current]:[current,other]}));
 await page.route('**/api/auth/revoke',r=>{expect(r.request().postDataJSON()).toEqual({id:other.id});revoked=true;return r.fulfill({json:{ok:true}});});
 await page.setViewportSize({width:320,height:760});await page.goto('/#/plans/settings?section=security');await expect(page.locator('.pl-session-card')).toHaveCount(2);await fit(page);const card=page.locator('.pl-session-card').filter({hasText:'Другая сессия'});await expect(card).toContainText('Вход:');await expect(card).toContainText('Последняя активность:');await card.getByRole('button',{name:'Завершить',exact:true}).click();await expect(page.locator('.pl-session-card')).toHaveCount(1);await expect(page.locator('.pl-session-card')).toContainText('Эта сессия');
});
