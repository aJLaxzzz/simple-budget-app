const {test,expect}=require('../browser-fixtures.cjs');
const fs=require('node:fs');
const today=()=>new Intl.DateTimeFormat('sv-SE',{timeZone:'Europe/Moscow'}).format(new Date());
async function api(request,path,data){const r=await request.post('/api/planner/'+path,{data});expect(r.ok(),await r.text()).toBeTruthy();return r.json();}
async function screenshot(page,name){fs.mkdirSync('artifacts/mobile-foundation-3.2',{recursive:true});await page.screenshot({path:'artifacts/mobile-foundation-3.2/'+name+'.png',animations:'disabled'});}
async function moduleTo(page,name){await page.locator('#module-switch').click();await page.locator('#module-menu').getByRole('link',{name,exact:true}).click();}
async function ready(page){await expect(page.locator('.pl-content .pl-loading')).toHaveCount(0);await expect(page.locator('.pl-content')).not.toBeEmpty();}
async function noOverflow(page){expect(await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth}))).toEqual({width:(await page.viewportSize()).width,scroll:(await page.viewportSize()).width});}

test('foundation responsive shell: all breakpoints, modules and real screenshots',async({page,request})=>{
 await page.route('**/api/environment',r=>r.fulfill({json:{staging:true}}));
 await api(request,'tasks',{title:'Подготовить план следующей недели и проверить все важные детали проекта',planned_date:today()});
 await page.goto('/#/plans/tasks');await ready(page);
 for(const width of [320,360,390,412,480,767,768,1023,1024,1440]){
  await page.setViewportSize({width,height:900});await noOverflow(page);
  const box=await page.locator('#module-tabs').boundingBox();expect(box.width).toBeGreaterThan(200);
  if(width<768){expect(box.y+box.height).toBe(900);await expect(page.locator('#module-tabs .app-icon').first()).toBeVisible();}
  else expect(box.y).toBeLessThan(100);
  if(width===390)await screenshot(page,'01-tasks-390');
  if(width===412){await screenshot(page,'02-shell-412');await page.locator('#module-switch').click();await screenshot(page,'03-module-menu');await page.keyboard.press('Escape');await expect(page.locator('#module-menu')).toBeHidden();}
  if(width===1440)await screenshot(page,'08-desktop-plans');
  await moduleTo(page,'Бюджет');await expect(page.locator('#backup-button')).toBeVisible();await expect(page.locator('#search-input')).toBeVisible();await noOverflow(page);
  if(width===1440)await screenshot(page,'09-desktop-budget');
  await moduleTo(page,'Планы');await ready(page);await expect(page).toHaveURL(/plans\/tasks$/);
 }
 await page.setViewportSize({width:740,height:360});await noOverflow(page);await expect(page.locator('#module-tabs')).toBeVisible();
});

test('foundation task editor keeps one DOM, draft, focus and selection through resize; short confirmation',async({page,request})=>{
 const t=await api(request,'tasks',{title:'Подготовить заметки',planned_date:today()});
 await page.setViewportSize({width:390,height:844});await page.goto('/#/plans/tasks');await ready(page);await page.evaluate(id=>Planner.openTask(id),t.id);
 const ed=page.getByRole('dialog',{name:'Редактор задачи'}),title=ed.getByLabel('Название',{exact:true});await expect(title).toBeVisible();
 expect(await title.evaluate(e=>document.activeElement===e)).toBe(false);await screenshot(page,'04-mobile-task-editor');
 await title.fill('Несохранённый черновик');await ed.locator('summary').filter({hasText:'Параметры'}).click();await ed.getByLabel('Описание',{exact:true}).fill('Длинная форма\n'.repeat(50));
 await page.evaluate(()=>{window.editorForm=document.querySelector('.pl-editor-shell form');window.editorBody=document.querySelector('.pl-drawer-body');window.editorBody.scrollTop=120;window.editorProbe={gets:0};const fetch=window.fetch;window.fetch=(url,options)=>{if(String(url).startsWith('/api/planner/')&&(!options?.method||options.method==='GET'))editorProbe.gets++;return fetch(url,options);};});
 await title.focus();await title.evaluate(e=>e.setSelectionRange(2,8));
 for(const width of [1440,767,768,412,390]){await page.setViewportSize({width,height:844});expect(await page.evaluate(()=>document.querySelector('.pl-editor-shell form')===editorForm)).toBe(true);await expect(title).toBeFocused();expect(await title.evaluate(e=>[e.selectionStart,e.selectionEnd])).toEqual([2,8]);await noOverflow(page);}
 expect(await page.evaluate(()=>editorProbe.gets)).toBe(0);
 await ed.locator('.pl-drawer-body').evaluate(e=>e.scrollTop=e.scrollHeight);await screenshot(page,'06-long-mobile-form');const save=ed.getByRole('button',{name:'Сохранить',exact:true});await expect(save).toBeInViewport();
 await ed.getByRole('button',{name:'Закрыть редактор',exact:true}).click();const confirm=page.getByRole('dialog',{name:'Несохранённые изменения'});await expect(confirm).toBeVisible();expect((await confirm.boundingBox()).height).toBeLessThan(700);await screenshot(page,'07-mobile-confirmation');await page.keyboard.press('Escape');await expect(confirm).toHaveCount(0);await expect(title).toHaveValue('Несохранённый черновик');
 await page.setViewportSize({width:1440,height:1000});await ed.locator('.pl-drawer-body').evaluate(e=>e.scrollTop=0);await screenshot(page,'10-desktop-editor');expect((await ed.boundingBox()).width).toBe(560);
});

test('foundation browser Back cancel retains form; discard and Forward; direct-link close',async({page})=>{
 await page.setViewportSize({width:412,height:900});await page.goto('/#/plans/tasks');await ready(page);await page.evaluate(()=>Planner.newTask());
 const ed=page.getByRole('dialog',{name:'Новая задача'});await ed.getByLabel('Название',{exact:true}).fill('Черновик истории');await page.evaluate(()=>window.originalForm=document.querySelector('.pl-editor-shell form'));
 const length=await page.evaluate(()=>history.length);await page.goBack();await page.getByRole('button',{name:'Продолжить редактирование',exact:true}).click();await expect(page).toHaveURL(/new=task/);expect(await page.evaluate(()=>history.length)).toBe(length);expect(await page.evaluate(()=>originalForm===document.querySelector('.pl-editor-shell form'))).toBe(true);
 await page.goBack();await page.getByRole('button',{name:'Отменить изменения',exact:true}).click();await expect(ed).toHaveCount(0);await expect(page).toHaveURL(/tasks$/);await page.goForward();await expect(ed).toBeVisible();await ed.getByRole('button',{name:'Закрыть редактор',exact:true}).click();await expect(ed).toHaveCount(0);
 await page.goto('/#/plans/tasks?new=task');await expect(ed).toBeVisible();await ed.getByRole('button',{name:'Закрыть редактор',exact:true}).click();await expect(page).toHaveURL(/tasks$/);await expect(ed).toHaveCount(0);
});

test('foundation new and existing project use shared fullscreen shell and save footer',async({page,request})=>{
 await page.setViewportSize({width:390,height:844});await page.goto('/#/plans/projects?new=project');const ed=page.getByRole('dialog',{name:'Новый проект',exact:true});await expect(ed).toBeVisible();await ed.getByLabel('Название проекта',{exact:true}).fill('Спокойная рабочая неделя');await screenshot(page,'05-mobile-project-editor');await noOverflow(page);await expect(ed.getByRole('button',{name:'Создать проект',exact:true})).toBeInViewport();
 await ed.getByRole('button',{name:'+ Задача',exact:true}).click();await ed.getByLabel('Название задачи',{exact:true}).fill('Первый шаг');await ed.getByRole('button',{name:'+ Задача',exact:true}).click();await ed.getByLabel('Название задачи',{exact:true}).last().fill('Второй шаг');await ed.locator('.pl-template-edit').evaluateAll(ns=>ns.forEach(n=>n.open=true));
 await page.evaluate(()=>{window.projectForm=document.querySelector('.pl-editor-shell form');document.querySelector('.pl-drawer-body').scrollTop=200;});
 for(const width of [1440,412,390]){await page.setViewportSize({width,height:844});expect(await page.evaluate(()=>projectForm===document.querySelector('.pl-editor-shell form'))).toBe(true);expect(await ed.locator('.pl-drawer-body').evaluate(e=>e.scrollTop)).toBe(200);await expect(ed.getByLabel('Название задачи',{exact:true}).last()).toHaveValue('Второй шаг');}
 
 await ed.getByRole('button',{name:'Создать проект',exact:true}).click();await expect(ed).toHaveCount(0);await expect(page).toHaveURL(/plans\/projects\/[^?]+$/);
 const id=new URL(page.url()).hash.split('/').pop();await page.evaluate(id=>Planner.navigate('#/plans/projects/'+id+'?edit=project&id='+id),id);const existing=page.getByRole('dialog',{name:'Редактирование проекта',exact:true});await expect(existing).toBeVisible();await existing.getByLabel('Название проекта',{exact:true}).fill('Недельный обзор');await existing.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(existing).toHaveCount(0);await expect(page.getByRole('heading',{name:'Недельный обзор',exact:true})).toBeVisible();
});

test('foundation mobile conflict, external SSE and uncertain create retain draft and exact command',async({page,request})=>{
 await page.setViewportSize({width:412,height:820});const t=await api(request,'tasks',{title:'Согласовать заметки',planned_date:today()});await page.goto('/#/plans/tasks?task='+t.id);
 const ed=page.getByRole('dialog',{name:'Редактор задачи'});await ed.getByLabel('Название',{exact:true}).fill('Мой мобильный черновик');
 await page.context().setOffline(true);await expect(page.locator('#sync-status')).toHaveText('Нет соединения');await request.patch('/api/planner/tasks/'+t.id,{data:{expected_revision:t.revision,title:'Изменено другим клиентом'}});await page.context().setOffline(false);await expect(ed).toContainText('Данные изменились');await ed.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(ed.getByRole('alert')).toContainText('изменена на другом устройстве');await expect(ed.getByLabel('Название',{exact:true})).toHaveValue('Мой мобильный черновик');await expect(ed.getByRole('button',{name:'Сохранить',exact:true})).toBeDisabled();
 await ed.getByRole('button',{name:'Посмотреть актуальную версию',exact:true}).click();await ed.getByRole('button',{name:'Использовать эту версию и сохранить черновик',exact:true}).click();await ed.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(ed).toHaveCount(0);
 await page.evaluate(()=>Planner.newTask());const create=page.getByRole('dialog',{name:'Новая задача'});await create.getByLabel('Название',{exact:true}).fill('Ответ потерян на телефоне');const bodies=[];let first=true;
 await page.route('**/api/planner/tasks',async r=>{if(r.request().method()!=='POST')return r.continue();bodies.push(r.request().postDataJSON());if(first){first=false;await r.fetch();await r.abort();}else await r.continue();});
 await create.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(create.locator('form')).toHaveAttribute('data-uncertain','true');await expect(create.getByLabel('Название',{exact:true})).toHaveValue('Ответ потерян на телефоне');await create.getByRole('button',{name:'Закрыть редактор',exact:true}).click();await expect(create).toBeVisible();await expect(create.locator('#planner-toast')).toBeVisible();await expect(create.getByRole('button',{name:'Сохранить',exact:true})).toBeInViewport();
 await create.getByRole('button',{name:'Сохранить',exact:true}).click();await expect(create).toHaveCount(0);expect(bodies).toHaveLength(2);expect(bodies[1]).toEqual(bodies[0]);const tasks=(await(await request.get('/api/planner/tasks')).json()).items;expect(tasks.filter(t=>t.title==='Ответ потерян на телефоне')).toHaveLength(1);
});

test('foundation calendar resize defers layout with open editor; nested Back keeps the editor',async({page,request})=>{
 const t=await api(request,'tasks',{title:'Карточка календаря',planned_date:today(),scheduled_time:'15:00'});await page.goto('/#/plans/calendar?mode=day&date='+today());const grid=page.locator('.pc-time-scroll');await expect(grid).toBeVisible();await grid.evaluate(e=>e.scrollTop=600);await page.evaluate(id=>Planner.openTask(id),t.id);const ed=page.getByRole('dialog',{name:'Редактор задачи'});await expect(ed).toBeVisible();
 await page.evaluate(()=>{window.calendarEditor=document.querySelector('.pl-editor-shell form');window.resizeGets=0;const fetch=window.fetch;window.fetch=(url,options)=>{if(String(url).startsWith('/api/planner/')&&!String(url).startsWith('/api/planner/icons')&&!options?.method)resizeGets++;return fetch(url,options);};});
 await page.setViewportSize({width:390,height:844});await expect(ed).toBeVisible();expect(await page.evaluate(()=>calendarEditor===document.querySelector('.pl-editor-shell form'))).toBe(true);await ed.getByRole('button',{name:'Оформление',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(2);await page.goBack();await expect(page.locator('dialog[open]')).toHaveCount(1);await expect(page).toHaveURL(/task=/);expect(await page.evaluate(()=>calendarEditor===document.querySelector('.pl-editor-shell form'))).toBe(true);expect(await page.evaluate(()=>resizeGets)).toBe(0);
 await ed.getByRole('button',{name:'Закрыть редактор',exact:true}).click();await expect(ed).toHaveCount(0);await expect(page).toHaveURL(new RegExp('mode=day&date='+today()+'$'));await expect(grid).toBeVisible();expect(await grid.evaluate(e=>e.scrollTop)).toBe(600);
});

test('foundation viewport smoke covers calendar, projects, settings and landscape editors',async({page})=>{
 for(const width of [320,360,390,412,480,767,768,1023,1024,1440]){
  await page.setViewportSize({width,height:900});
  for(const hash of ['#/plans/calendar?mode=month','#/plans/projects','#/plans/settings']){await page.goto('/'+hash);await ready(page);await noOverflow(page);}
  await page.evaluate(()=>Planner.newTask());const ed=page.getByRole('dialog',{name:'Новая задача'});await expect(ed).toBeVisible();await expect(ed.getByRole('button',{name:'Сохранить',exact:true})).toBeInViewport();await noOverflow(page);
 }
 await page.setViewportSize({width:740,height:360});const ed=page.getByRole('dialog',{name:'Новая задача'});await expect(ed.getByRole('button',{name:'Сохранить',exact:true})).toBeInViewport();await noOverflow(page);
});

test('foundation last item, document scroll and focus survive editor close; toast sits above nav',async({page,request})=>{
 const tasks=[];for(let n=0;n<14;n++)tasks.push(await api(request,'tasks',{title:'Прокрутка '+String(n).padStart(2,'0'),planned_date:today()}));
 await page.setViewportSize({width:390,height:650});await page.goto('/#/plans/tasks?q=Прокрутка');await ready(page);await page.evaluate(()=>scrollTo(0,document.documentElement.scrollHeight));const last=page.getByRole('button',{name:'Прокрутка 13',exact:true});await expect(last).toBeInViewport();expect((await last.boundingBox()).y+(await last.boundingBox()).height).toBeLessThan((await page.locator('#module-tabs').boundingBox()).y);const scroll=await page.evaluate(()=>scrollY);await last.click();const ed=page.getByRole('dialog',{name:'Редактор задачи'});await expect(ed).toBeVisible();await ed.getByRole('button',{name:'Закрыть редактор',exact:true}).click();await expect(ed).toHaveCount(0);await expect.poll(()=>page.evaluate(()=>scrollY)).toBe(scroll);await expect(last).toBeFocused();await page.evaluate(()=>Planner.notify('Изменения сохранены',{label:'Отменить',run(){}}));expect((await page.locator('#planner-toast').boundingBox()).y+(await page.locator('#planner-toast').boundingBox()).height).toBeLessThan((await page.locator('#module-tabs').boundingBox()).y);
});

test('foundation mobile Budget search, expense save, If-Match and no page overflow',async({page,request})=>{
 await page.setViewportSize({width:390,height:844});await page.goto('/#/');await expect(page.locator('#home-months-list a').first()).toBeVisible();
 const months=await(await request.get('/api/months')).json(),id=months[0].id;
 const response=await request.get('/api/categories'),categories=await response.json();categories.expense.push({id:'foundation-expense',name:'Мобильная проверка',subcategories:[]});const update=await request.put('/api/categories',{headers:{'If-Match':response.headers()['x-budget-revision']},data:categories});expect(update.ok()).toBe(true);
 await page.goto('/#/month/'+id);await expect(page.locator('#income-value')).toBeVisible();await noOverflow(page);const writes=[];page.on('request',r=>{if(r.method()==='POST'&&r.url().includes('/expenses'))writes.push(r.headers()['if-match']);});
 await page.locator('#btn-add-expense').click();await page.locator('#exp-category').selectOption('foundation-expense');await page.locator('#exp-amount').fill('125');await page.locator('#exp-desc').fill('Основа мобильного интерфейса');await page.locator('#expense-form button[type=submit]').click();await expect(page.locator('tr[data-expense-id]').filter({hasText:'Основа мобильного интерфейса'})).toBeVisible();expect(writes).toHaveLength(1);expect(writes[0]).toBeTruthy();await noOverflow(page);
 await page.locator('#search-input').fill('Основа мобильного интерфейса');await expect(page.locator('#search-results')).toContainText('Основа мобильного интерфейса');await page.locator('#search-input').fill('');
 await moduleTo(page,'Планы');await expect(page.locator('#module-tabs')).toHaveAttribute('aria-label','Разделы планировщика');await moduleTo(page,'Бюджет');await expect(page).toHaveURL(new RegExp('/month/'+id+'$'));
});


test('foundation guarded Save and continue closes once and leaves the saved task accessible',async({page,request})=>{
 await page.setViewportSize({width:390,height:844});await page.goto('/#/plans/tasks');await ready(page);await page.evaluate(()=>Planner.newTask());const editor=page.getByRole('dialog',{name:'Новая задача'});await editor.getByLabel('Название',{exact:true}).fill('Сохранить при выходе из формы');await editor.getByRole('button',{name:'Закрыть редактор',exact:true}).click();await page.getByRole('button',{name:'Сохранить и продолжить',exact:true}).click();await expect(page.locator('dialog[open]')).toHaveCount(0);await expect(page).toHaveURL(/plans\/tasks$/);await expect(page.getByRole('button',{name:'Сохранить при выходе из формы',exact:true})).toBeVisible();const all=(await(await request.get('/api/planner/tasks')).json()).items;expect(all.filter(t=>t.title==='Сохранить при выходе из формы')).toHaveLength(1);
});
