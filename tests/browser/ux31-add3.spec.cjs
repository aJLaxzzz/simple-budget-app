const {test,expect,revealAction}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto');
const fs=require('node:fs');
const today=()=>new Intl.DateTimeFormat('sv-SE',{timeZone:'Europe/Moscow'}).format(new Date());
const add=(date,n)=>new Date(Date.parse(date+'T12:00:00Z')+n*86400000).toISOString().slice(0,10);
async function api(r,path,data,method=data?'post':'get'){const res=await r[method]('/api/planner/'+path,data?{data}:{});expect(res.ok(),await res.text()).toBeTruthy();return res.json();}
async function project(r){const inpt={request_id:randomUUID(),anchor_date:today(),has_start:true,snapshot:{title:'Независимый '+randomUUID().slice(0,6),tasks:[]},rule:{schema_version:1,mode:'schedule',unit:'weeks',interval:1,allowed_days:{mode:'any'},adjustment:'none'}};const preview=await api(r,'project-drafts/preview',inpt);return (await api(r,'project-drafts/apply',{...inpt,preview_token:preview.preview_token})).project;}
async function shot(page,name){fs.mkdirSync('artifacts/ux31-add3',{recursive:true});await page.screenshot({path:'artifacts/ux31-add3/'+name+'.png',fullPage:true});}

test('Add3 independent create, compact settings and contextual subtask propagation',async({page,request})=>{
 await page.goto('/#/plans/projects?new=project');await page.getByLabel('Название проекта',{exact:true}).fill('Новый ритм');await page.getByText('Даты и повторение',{exact:true}).click();await page.getByLabel('Повторение',{exact:true}).selectOption('yes');
 await expect(page.locator('select[name=project_mode]')).toHaveCount(0);await expect(page.getByLabel('Отсчёт',{exact:true})).toBeHidden();await page.getByLabel('Крайний срок проекта · необязательно',{exact:true}).fill(add(today(),1));
 await page.getByRole('button',{name:'Создать проект',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);
 const id=new URL(page.url()).hash.split('/').at(-1),p=await api(request,'projects/'+id);expect(p.project_mode).toBe('independent');
 // Reproduce a legacy parent that never entered the shared snapshot.
 const root=await api(request,'tasks',{project_id:p.id,title:'Родитель только здесь'});
 await page.reload();await page.getByRole('button',{name:'Добавить подзадачу: '+root.title,exact:true}).click();
 const editor=page.getByRole('dialog',{name:'Новая задача'});await expect(editor.locator('select[name=change_scope]')).toHaveCount(0);await editor.getByLabel('Название',{exact:true}).fill('Подзадача из кнопки');await editor.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);
 let series=await api(request,'series/'+p.repeat_series_id);expect(series.occurrences.every(o=>o.project.tasks.length===2&&!o.project.preliminary)).toBe(true);
 await page.getByRole('button',{name:'+ Задача в проект',exact:true}).click();const next=page.getByRole('dialog',{name:'Новая задача'});await next.getByLabel('Название',{exact:true}).fill('Подзадача из редактора');await next.getByText('Проект и подзадача',{exact:false}).click();await next.getByLabel('Родительская задача',{exact:true}).selectOption(root.id);await next.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);
 series=await api(request,'series/'+p.repeat_series_id);expect(series.occurrences.every(o=>o.project.tasks.length===3)).toBe(true);await shot(page,'project');
 await page.getByRole('button',{name:'Настроить повторение',exact:true}).click();await expect(page.getByRole('tab',{name:'Режим выполнения'})).toHaveCount(0);
});

test('Add3 project-page date edits propagate offsets; task-list edits stay local',async({page,request})=>{
 const p=await project(request);await page.goto('/#/plans/projects/'+p.id);await page.getByLabel('Название новой задачи',{exact:true}).fill('Дата по контексту');await page.getByRole('button',{name:'Добавить',exact:true}).click();await expect(page.getByRole('button',{name:'Дата по контексту',exact:true})).toBeVisible();
 let current=await api(request,'projects/'+p.id),t=current.tasks[0];await page.getByRole('button',{name:t.title,exact:true}).click();let ed=page.getByRole('dialog',{name:'Редактор задачи'});await ed.getByLabel('Плановая дата',{exact:true}).fill(add(today(),2));await ed.getByLabel('Время · Москва',{exact:true}).fill('15:00');await ed.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);
 let series=await api(request,'series/'+p.repeat_series_id);expect(series.occurrences.every(o=>o.project.tasks[0].planned_date===add(o.project.start_date,2)&&o.project.tasks[0].scheduled_time==='15:00')).toBe(true);
 await page.goto('/#/plans/tasks?task='+t.id);ed=page.getByRole('dialog',{name:'Редактор задачи'});await ed.getByLabel('Время · Москва',{exact:true}).fill('18:00');await ed.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);
 series=await api(request,'series/'+p.repeat_series_id);expect(series.occurrences.every(o=>o.project.tasks[0].scheduled_time==='15:00')).toBe(true);expect((await api(request,'tasks/'+t.id)).scheduled_time).toBe('18:00');
});

test('Add3 archive is reachable, hides archived project tasks and restores them without deletion',async({page,request})=>{
 let p=await api(request,'projects',{title:'Архив '+randomUUID().slice(0,5)});const t=await api(request,'tasks',{title:'Сохранённая задача',project_id:p.id,planned_date:today()});p=await api(request,'projects/'+p.id+'/archive',{expected_revision:p.revision});
 await page.goto('/#/plans/tasks');await expect(page.locator('[data-task-id="'+t.id+'"]')).toHaveCount(0);await expect(page.locator('select[name=project] option[value="'+p.id+'"]')).toHaveCount(0);
 await page.goto('/#/plans/settings?section=archive');await expect(page.locator('[data-archive-id="'+p.id+'"]')).toBeVisible();await page.getByLabel('Тип',{exact:true}).selectOption('task');const row=page.locator('[data-archive-id="'+t.id+'"]');await expect(row).toContainText('Задача в архивном проекте');await shot(page,'archive');
 await row.getByRole('button',{name:'Вернуть проект с задачами'}).click();await page.getByRole('dialog',{name:'Вернуть из архива'}).getByRole('button',{name:'Вернуть из архива'}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);expect((await api(request,'projects/'+p.id)).archived_at).toBeNull();expect((await api(request,'tasks/'+t.id)).id).toBe(t.id);
});

test('Add3 Attention has distinct active groups and deferred problems are not counted',async({page,request})=>{
 const t=await api(request,'tasks',{title:'Отложенная проблема',planned_date:add(today(),-1)});await api(request,'attention/'+t.id,{type:'task',expected_revision:0,snoozed_until:add(today(),1),request_id:randomUUID()});
 await page.goto('/#/plans/attention');await expect(page.getByRole('link',{name:'Активные',exact:true})).toBeVisible();await expect(page.getByRole('link',{name:'История',exact:true})).toBeVisible();await expect(page.locator('[data-attention-group=deferred]')).toContainText(t.title);await expect(page.locator('[data-attention-group=late]').getByText(t.title,{exact:true})).toHaveCount(0);await shot(page,'attention');
 await page.getByRole('link',{name:'История',exact:true}).click();await expect(page.locator('.pl-attention-list')).not.toContainText(t.title);
});

test('Add3 explicit project move warns about overlap and leaves the other occurrence unchanged',async({page,request})=>{
 const p=await project(request),series=await api(request,'series/'+p.repeat_series_id),other=series.occurrences[0].project;
 await page.goto('/#/plans/projects/'+p.id);await page.getByRole('button',{name:'Действия с проектом',exact:true}).click();await revealAction(page,'Перенести выполнение проекта');await page.getByRole('button',{name:'Перенести выполнение проекта',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Перенести выполнение проекта'});await dialog.getByLabel('Новая дата',{exact:true}).fill(other.start_date);await dialog.getByRole('button',{name:'Перенести выполнение',exact:true}).click();await expect(page.getByRole('dialog',{name:'Даты выполнений пересекаются'})).toBeVisible();await page.getByRole('button',{name:'Сохранить оба выполнения'}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);
 expect((await api(request,'projects/'+other.id)).start_date).toBe(other.start_date);expect((await api(request,'projects/'+p.id)).start_date).toBe(other.start_date);
});

test('Add3 shared save retries identical command after a lost response without duplicate children',async({page,request})=>{
 const p=await project(request);await page.goto('/#/plans/projects/'+p.id);await page.getByRole('button',{name:'+ Задача в проект',exact:true}).click();
 const ed=page.getByRole('dialog',{name:'Новая задача'});await ed.getByLabel('Название',{exact:true}).fill('Без дублей');const commands=[];
 await page.route('**/task-change-apply',async route=>{commands.push(route.request().postDataJSON());if(commands.length===1){const response=await route.fetch();expect(response.ok()).toBeTruthy();return route.abort('failed');}return route.continue();});
 await ed.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(ed.getByRole('alert')).toContainText('Повторите сохранение');await expect(ed.getByLabel('Название',{exact:true})).toBeDisabled();
 await ed.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);expect(commands).toHaveLength(2);expect(commands[1]).toEqual(commands[0]);
 const current=await api(request,'projects/'+p.id),series=await api(request,'series/'+p.repeat_series_id);expect(current.tasks.filter(t=>t.title==='Без дублей')).toHaveLength(1);expect(series.occurrences.every(o=>o.project.tasks.filter(t=>t.title==='Без дублей').length===1)).toBe(true);
});

test('Add3 archived series exposes date, history and guarded restore in centralized archive',async({page,request})=>{
 const p=await project(request),series=await api(request,'series/'+p.repeat_series_id);
 const input={action:'archive',expected_revision:series.revision},preview=await api(request,'series/'+series.id+'/preview',input);
 await api(request,'series/'+series.id+'/archive',{...input,preview_token:preview.preview_token,request_id:randomUUID()});
 await page.goto('/#/plans/settings?section=archive');const row=page.locator('[data-archive-id="'+series.id+'"]');await expect(row).toContainText('Серия проектов');await expect(row).toContainText('В архиве с');
 await row.getByRole('button',{name:'Вернуть из архива',exact:true}).click();await page.getByRole('dialog',{name:'Вернуть из архива'}).getByRole('button',{name:'Вернуть из архива',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);expect((await api(request,'series/'+series.id)).state).toBe('active');expect((await api(request,'projects/'+p.id)).id).toBe(p.id);
});
