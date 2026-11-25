const {test,expect,login}=require('../browser-fixtures.cjs');
const {backupServer}=require('../backup-server.cjs');
const fs=require('node:fs'),path=require('node:path');
test('keyboard calendar control retains focus after an external live refresh',async({browser,playwright})=>{
 test.setTimeout(60000);const server=await backupServer();const context=await browser.newContext({baseURL:server.baseURL});
 const machine=await playwright.request.newContext({baseURL:server.baseURL,extraHTTPHeaders:{'X-Budget-Admin':'1',Authorization:'Basic '+Buffer.from('budget:'+server.password).toString('base64')}});
 try{
  const task=await(await machine.post('/api/planner/tasks',{data:{title:'Встречи для клавиатурной проверки'}})).json();
  for(let n=0;n<5;n++)expect((await machine.post('/api/planner/sessions',{data:{task_id:task.id,date:'2026-12-02',start_time:'10:00',end_time:'11:00'}})).status()).toBe(201);
  const page=await context.newPage();await login(page,server.password);await page.goto('/#/plans/calendar?mode=day&date=2026-12-02');
  await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
  const group=page.locator('.pc-dense-group');await expect(group).toContainText('5 дел');await group.focus();
  const original=await group.elementHandle();
  expect((await machine.post('/api/planner/tasks',{data:{title:'Внешнее изменение при клавиатурном фокусе'}})).status()).toBe(201);
  await expect.poll(()=>original.evaluate(n=>n.isConnected)).toBe(false);
  await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');await expect(group).toBeFocused();
  await page.keyboard.press('Enter');await expect(page.getByRole('dialog',{name:/Одновременные события/}).locator('.pc-event')).toHaveCount(5);
 }finally{await context.close();await machine.dispose();await server.close()}
});
test('live calendar reconciliation waits for an active mouse drag',async({browser,playwright})=>{
 test.setTimeout(60000);const server=await backupServer();const context=await browser.newContext({baseURL:server.baseURL});
 const machine=await playwright.request.newContext({baseURL:server.baseURL,extraHTTPHeaders:{'X-Budget-Admin':'1',Authorization:'Basic '+Buffer.from('budget:'+server.password).toString('base64')}});
 try{
  const task=await(await machine.post('/api/planner/tasks',{data:{title:'Перенос при внешнем событии',planned_date:'2026-12-02'}})).json();
  const page=await context.newPage();await login(page,server.password);await page.goto('/#/plans/calendar?mode=month&date=2026-12-02');
  await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
  const card=page.locator('[data-event-id="task:'+task.id+'"]');await expect(card).toBeVisible();
  const original=await card.elementHandle(),box=await card.boundingBox(),target=await page.locator('.pc-month-day[data-date="2026-12-03"]').boundingBox();
  await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.down();await page.mouse.move(box.x+box.width/2+12,box.y+box.height/2,{steps:3});
  expect((await machine.post('/api/planner/tasks',{data:{title:'Внешнее изменение во время переноса'}})).status()).toBe(201);
  await expect(page.locator('#sync-status')).toHaveText('Синхронизация');
  await page.waitForTimeout(1200); // Cover the reconciliation timer while the drag remains active.
  expect(await original.evaluate(n=>n.isConnected)).toBe(true);
  await page.mouse.move(target.x+target.width/2,target.y+target.height/2,{steps:5});await page.mouse.move(target.x+target.width/2+1,target.y+target.height/2);await page.mouse.up();
  await expect(card).toHaveAttribute('data-event-date','2026-12-03');await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
 }finally{await context.close();await machine.dispose();await server.close()}
});
test('HTML sessions, two live clients, financial CAS, drafts and session revocation',async({browser,playwright})=>{
 test.setTimeout(120000);const server=await backupServer();const first=await browser.newContext({baseURL:server.baseURL}),second=await browser.newContext({baseURL:server.baseURL});
 const machine=await playwright.request.newContext({baseURL:server.baseURL,extraHTTPHeaders:{'X-Budget-Admin':'1',Authorization:'Basic '+Buffer.from('budget:'+server.password).toString('base64')}});
 try{
  const a=await first.newPage(),b=await second.newPage();await login(a,server.password);await login(b,server.password);
  const cookie=(await first.cookies()).find(x=>x.name==='__Host-budget-session');expect(cookie.httpOnly).toBe(true);expect(cookie.secure).toBe(true);expect(cookie.sameSite).toBe('Strict');expect(await a.evaluate(async()=> (await fetch('/web-auth.json')).status)).toBe(404);
  expect(fs.readFileSync(path.join(server.live,'web-auth.json'),'utf8')).not.toContain(cookie.value);
  await a.goto('/#/plans/inbox');await b.goto('/#/plans/inbox');
  const title='Live synthetic '+Date.now();const task=await(await machine.post('/api/planner/tasks',{data:{title}})).json();
  await expect(a.getByText(title,{exact:true}).first()).toBeVisible();await expect(b.getByText(title,{exact:true}).first()).toBeVisible();
  await a.goto('/#/plans/tasks');const search=a.getByLabel('Поиск по названию');await search.fill(title);
  await machine.post('/api/planner/tasks',{data:{title:title+' extra'}});
  await expect(a.getByRole('button',{name:title+' extra',exact:true})).toBeVisible();await expect(search).toHaveValue(title);await expect(search).toBeFocused();
  const filter=a.getByLabel('Будущие повторения');await filter.focus();await machine.post('/api/planner/tasks',{data:{title:title+' extra2'}});await expect(a.getByRole('button',{name:title+' extra2',exact:true})).toBeVisible();await expect(filter).toBeFocused();await expect(a.locator('#sync-status')).toHaveText('Синхронизировано');
  await a.goto('/#/plans/tasks?task='+task.id);const input=a.getByRole('dialog',{name:'Редактор задачи',exact:true}).locator('textarea[name=title]');await input.fill('Несохранённый черновик');
  await machine.patch('/api/planner/tasks/'+task.id,{data:{title:'Изменено с другого устройства',expected_revision:task.revision}});
  await expect(input).toHaveValue('Несохранённый черновик');await expect(a.getByText('Данные изменились.',{exact:false})).toBeVisible();
  const revision=(await machine.get('/api/categories')).headers()['x-budget-revision'];const writes=await Promise.all([1,2].map(n=>machine.put('/api/categories',{headers:{'If-Match':revision},data:{expense:[{id:'test',name:'Категория '+n,subcategories:[]}],income:[]}})));expect(writes.map(r=>r.status()).sort()).toEqual([200,409]);
  await b.goto('/#/plans/settings?section=security');await expect(b.getByText('Эта сессия',{exact:false})).toBeVisible();
  await b.getByRole('button',{name:'Завершить остальные',exact:true}).click();
  await expect(a.locator('#sync-status')).toHaveText('Требуется повторная авторизация');await expect(input).toHaveValue('Несохранённый черновик');
  const newLogin=first.waitForEvent('page');await a.getByRole('dialog',{name:'Редактор задачи',exact:true}).getByRole('link',{name:'Войти в новой вкладке'}).click();const loginTab=await newLogin;await expect(loginTab.getByRole('button',{name:'Войти',exact:true})).toBeVisible();await loginTab.close();await expect(input).toHaveValue('Несохранённый черновик');
  await b.getByRole('button',{name:'Выйти',exact:true}).click();await expect(b).toHaveURL(/login.html/);
  const anonymous=await a.evaluate(async()=>{const r=await fetch('/api/months');return {status:r.status,challenge:r.headers.get('www-authenticate')}});expect(anonymous.status).toBe(401);expect(anonymous.challenge).toBeNull();
 }finally{await first.close();await second.close();await machine.dispose();await server.close();}
});
test('financial live refresh, offline reconciliation, restart persistence and restore logout',async({browser,playwright})=>{
 test.setTimeout(120000);const server=await backupServer();const ca=await browser.newContext({baseURL:server.baseURL}),cb=await browser.newContext({baseURL:server.baseURL});
 const machine=await playwright.request.newContext({baseURL:server.baseURL,extraHTTPHeaders:{'X-Budget-Admin':'1',Authorization:'Basic '+Buffer.from('budget:'+server.password).toString('base64')}});
 try{
  const a=await ca.newPage(),b=await cb.newPage();await login(a,server.password);await login(b,server.password);
  await expect(a.locator('#sync-status')).toHaveText('Синхронизировано');
  const months=await(await machine.get('/api/months')).json();const id=months[0].id;
  await a.goto('/#/month/'+id);await b.goto('/#/month/'+id);
  await expect(b.locator('#sync-status')).toHaveText('Синхронизировано');await b.evaluate(()=>window.liveSentinel='kept');
  await a.locator('#income-value').click();await a.locator('#income-input').fill('99');
  const update=async income=>{const rev=(await machine.get('/api/months')).headers()['x-budget-revision'];expect((await machine.patch('/api/months/'+id+'/income',{headers:{'If-Match':rev},data:{income}})).status()).toBe(200);};
  await update(123);await expect(b.locator('#income-value')).toContainText('123');await expect(a.locator('#income-input')).toHaveValue('99');
  expect(await a.evaluate(async id=>{try{await window.budgetAPI('/api/months/'+id+'/income',{method:'PATCH',body:JSON.stringify({income:99})});return 200}catch(e){return e.status}},id)).toBe(409);
  await a.locator('#income-cancel').click();await expect(a.locator('#income-value')).toContainText('123');await expect(a.locator('#sync-status')).toHaveText('Синхронизировано');await a.locator('#income-value').click();await a.locator('#income-input').fill('99');
  await cb.setOffline(true);await update(222);await cb.setOffline(false);await expect(b.locator('#income-value')).toContainText('222');
  await server.restart();await update(333);await expect(b.locator('#income-value')).toContainText('333');expect(await b.evaluate(()=>window.liveSentinel)).toBe('kept');
  const snap=await server.snapshot();await expect(b.locator('#sync-status')).toHaveText('Синхронизировано');expect(snap.manifest.files.map(x=>x.name)).not.toContain('web-auth.json');
  const archive=path.join(server.archives,'manual',snap.manifest.backup_id,'backup.tar.gz');await server.restore(archive);
  await expect(b).toHaveURL(/login.html/);await expect(a.locator('#sync-status')).toHaveText('Требуется повторная авторизация');await expect(a.locator('#income-input')).toHaveValue('99');
 }finally{await ca.close();await cb.close();await machine.dispose();await server.close()}
});

test('category editors preserve conflicting drafts until explicit reconciliation',async({browser})=>{
 test.setTimeout(60000);const server=await backupServer();const ca=await browser.newContext({baseURL:server.baseURL}),cb=await browser.newContext({baseURL:server.baseURL});
 try{
  const a=await ca.newPage(),b=await cb.newPage();await login(a,server.password);await login(b,server.password);
  await a.goto('/#/categories');await b.goto('/#/categories');
  const field=page=>page.locator('.categories-expense .cat-name').first();
  await field(a).fill('Мой черновик категории');await field(b).fill('Категория другого устройства');
  await b.locator('#save-categories').click();await expect(b.locator('.categories-saved')).toHaveClass(/visible/);
  await expect(a.getByText('Данные изменились.',{exact:false})).toBeVisible();await expect(field(a)).toHaveValue('Мой черновик категории');
  const conflict=a.waitForResponse(r=>r.url().endsWith('/api/categories')&&r.request().method()==='PUT');
  a.once('dialog',async d=>{expect(d.message()).toContain('Данные изменились');await d.accept()});await a.locator('#save-categories').click();expect((await conflict).status()).toBe(409);
  await expect(field(a)).toHaveValue('Мой черновик категории');
  a.once('dialog',d=>d.accept());await a.getByRole('button',{name:'Обновить, отменив черновик'}).click();await expect(field(a)).toHaveValue('Категория другого устройства');await expect(a.locator('#sync-status')).toHaveText('Синхронизировано');
 }finally{await ca.close();await cb.close();await server.close()}
});
