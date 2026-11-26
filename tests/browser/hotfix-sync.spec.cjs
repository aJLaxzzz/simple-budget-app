const {test,expect,login}=require('../browser-fixtures.cjs');
const {backupServer}=require('../backup-server.cjs');
test('own task save has one read/render cycle and no loading flash',async({browser})=>{
 test.setTimeout(60000);const server=await backupServer(),context=await browser.newContext({baseURL:server.baseURL});
 try{
 const page=await context.newPage();await login(page,server.password);await page.goto('/#/plans/inbox');await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
 await page.evaluate(()=>{window.probe={gets:0,loaders:0,roots:0};const fetch0=window.fetch;window.fetch=(url,...args)=>{if(String(url).endsWith('/api/planner/tasks?status=active')&&!(args[0]?.method))probe.gets++;return fetch0(url,...args)};new MutationObserver(records=>{for(const r of records)for(const n of r.addedNodes){if(n.nodeType!==1)continue;if(n.matches('.pl-loading')||n.querySelector('.pl-loading'))probe.loaders++;if(r.target.id==='root')probe.roots++;}}).observe(document.getElementById('root'),{subtree:true,childList:true});});
 await page.getByLabel('Название новой задачи',{exact:true}).fill('Синтетическая задача hotfix');await page.locator('.pl-quick').evaluate(f=>f.requestSubmit());
 await expect(page.getByRole('button',{name:'Синтетическая задача hotfix',exact:true})).toBeVisible();await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');await page.waitForTimeout(1800);
 const counts=await page.evaluate(()=>probe);console.log('own-save probe',counts);expect(counts.loaders).toBe(0);expect(counts.gets).toBe(1);expect(counts.roots).toBe(0);
 }finally{await context.close();await server.close()}
});

test('external burst updates rows without replacing unchanged task or filters',async({browser,playwright})=>{
 test.setTimeout(60000);const server=await backupServer(),context=await browser.newContext({baseURL:server.baseURL});
 const machine=await playwright.request.newContext({baseURL:server.baseURL,extraHTTPHeaders:{'X-Budget-Admin':'1',Authorization:'Basic '+Buffer.from('budget:'+server.password).toString('base64')}});
 try{
  const task=await(await machine.post('/api/planner/tasks',{data:{title:'Неизменённая строка'}})).json();
  const page=await context.newPage();await login(page,server.password);await page.goto('/#/plans/inbox');await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
  const original=await page.locator('[data-task-id="'+task.id+'"]').elementHandle(),filters=await page.locator('.pl-filters').elementHandle();
  await page.evaluate(()=>{window.probe={loaders:0};new MutationObserver(records=>{for(const r of records)for(const n of r.addedNodes)if(n.nodeType===1&&(n.matches('.pl-loading')||n.querySelector('.pl-loading')))probe.loaders++}).observe(document.getElementById('root'),{childList:true,subtree:true})});
  await Promise.all([0,1,2].map(n=>machine.post('/api/planner/tasks',{data:{title:'Быстрое изменение '+n}})));
  for(let n=0;n<3;n++)await expect(page.getByRole('button',{name:'Быстрое изменение '+n,exact:true})).toBeVisible();
  await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');expect(await original.evaluate(n=>n.isConnected)).toBe(true);expect(await filters.evaluate(n=>n.isConnected)).toBe(true);expect(await page.evaluate(()=>probe.loaders)).toBe(0);
  // The retained row must still issue a valid expected_revision command.
  await page.getByRole('button',{name:'Завершить: Неизменённая строка',exact:true}).click();await expect(page.getByRole('button',{name:'Неизменённая строка',exact:true})).toHaveCount(0);
 }finally{await context.close();await machine.dispose();await server.close()}
});

test('finance keeps table and chart nodes; consecutive local saves retain If-Match after external refresh',async({browser,playwright})=>{
 test.setTimeout(60000);const server=await backupServer(),context=await browser.newContext({baseURL:server.baseURL});
 const machine=await playwright.request.newContext({baseURL:server.baseURL,extraHTTPHeaders:{'X-Budget-Admin':'1',Authorization:'Basic '+Buffer.from('budget:'+server.password).toString('base64')}});
 try{
  const page=await context.newPage();await login(page,server.password);await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
  // Landing is the planner; enter Budget explicitly before reading its new month.
  await page.locator('#module-switch').click();await page.locator('#module-menu').getByRole('link',{name:'Бюджет',exact:true}).click();await expect(page.locator('#home-months-list a').first()).toBeVisible();
  const months=await(await machine.get('/api/months')).json(),id=months[0].id;
  await page.goto('/#/month/'+id);await expect(page.locator('#income-value')).toBeVisible();await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
  const cats=await machine.get('/api/categories');const category=(await cats.json()).expense[0];
  await page.locator('#btn-add-expense').click();await page.locator('#exp-category').selectOption(category.id);await page.locator('#exp-amount').fill('25');await page.locator('#exp-desc').fill('Синтетический расход');await page.locator('#expense-form button[type=submit]').click();
  await expect(page.locator('tr[data-expense-id]')).toHaveCount(1);await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
  const expense=await page.locator('tr[data-expense-id]').elementHandle(),canvas=await page.locator('#chart-pie').elementHandle();
  await page.evaluate(()=>window.originalChart=Chart.getChart(document.getElementById('chart-pie')));
  const income=await page.locator('#income-value').elementHandle();
  await page.evaluate(()=>{window.probe={gets:0,loaders:0};const original=window.fetch;window.fetch=(url,...args)=>{if(/^\/api\/months\//.test(url)&&!args[0]?.method)probe.gets++;return original(url,...args)};new MutationObserver(rs=>{for(const r of rs)for(const n of r.addedNodes)if(n.textContent?.includes('Загрузка...'))probe.loaders++}).observe(document.getElementById('root'),{childList:true,subtree:true});});
  for(const value of [120,240]){
   await page.locator('#income-value').click();await page.locator('#income-input').fill(String(value));await page.locator('#income-save').click();await expect(page.locator('#income-value')).toContainText(String(value));await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
  }
  await page.waitForTimeout(1200);expect(await page.evaluate(()=>probe.gets)).toBe(2);expect(await page.evaluate(()=>probe.loaders)).toBe(0);expect(await income.evaluate(n=>n.isConnected)).toBe(true);
  const revision=(await machine.get('/api/months')).headers()['x-budget-revision'];expect((await machine.patch('/api/months/'+id+'/income',{headers:{'If-Match':revision},data:{income:360}})).status()).toBe(200);
  await expect(page.locator('#income-value')).toContainText('360');await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
  await page.locator('#income-value').click();await page.locator('#income-input').fill('480');await page.locator('#income-save').click();await expect(page.locator('#income-value')).toContainText('480');
  expect(await income.evaluate(n=>n.isConnected)).toBe(true);
  expect(await expense.evaluate(n=>n.isConnected)).toBe(true);expect(await canvas.evaluate(n=>n.isConnected)).toBe(true);expect(await page.evaluate(()=>originalChart===Chart.getChart(document.getElementById('chart-pie')))).toBe(true);
  await page.locator('#filter-expense-category').selectOption(category.id);await expect(page.locator('#filter-expense-category')).toHaveValue(category.id);
 }finally{await context.close();await machine.dispose();await server.close()}
});

test('login and application load local SVG and PNG/ICO favicons',async({browser})=>{
 test.setTimeout(60000);const server=await backupServer(),context=await browser.newContext({baseURL:server.baseURL});
 try{
  const page=await context.newPage();await page.goto('/login.html');
  const icons=page.locator('link[rel=icon]');await expect(icons).toHaveCount(4);
  for(const path of ['/favicon.svg','/favicon-16.png','/favicon-32.png','/favicon.ico']){const r=await page.request.get(path);expect(r.status()).toBe(200)}
  await login(page,server.password);await page.goto('/#/plans/calendar');await expect(icons).toHaveCount(4);
  expect(await page.evaluate(async()=>{const image=new Image();image.src='/favicon.svg';await image.decode();return image.naturalWidth>0})).toBe(true);
 }finally{await context.close();await server.close()}
});

test('a background response cannot overwrite a draft started while its GET is in flight',async({browser,playwright})=>{
 test.setTimeout(60000);const server=await backupServer(),context=await browser.newContext({baseURL:server.baseURL});
 const machine=await playwright.request.newContext({baseURL:server.baseURL,extraHTTPHeaders:{'X-Budget-Admin':'1',Authorization:'Basic '+Buffer.from('budget:'+server.password).toString('base64')}});
 let release;
 try{
  const page=await context.newPage();await login(page,server.password);await page.goto('/#/plans/inbox');await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
  const held=new Promise(r=>release=r);let started;const reading=new Promise(r=>started=r);let once=true;
  await page.route('**/api/planner/tasks?status=active',async route=>{if(!once)return route.continue();once=false;const response=await route.fetch();started();await held;await route.fulfill({response})});
  await machine.post('/api/planner/tasks',{data:{title:'Внешняя задача перед черновиком'}});await reading;
  const input=page.getByLabel('Название новой задачи',{exact:true});await input.fill('Черновик во время GET');release();
  await expect(page.getByText('Данные изменились.',{exact:false})).toBeVisible();await expect(input).toHaveValue('Черновик во время GET');
  await page.locator('.pl-quick').evaluate(f=>f.requestSubmit());await expect(page.getByRole('button',{name:'Черновик во время GET',exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Внешняя задача перед черновиком',exact:true})).toBeVisible();await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
 }finally{release?.();await context.close();await machine.dispose();await server.close()}
});


test('local readback keeps old content visible but blocks commands using its stale revision',async({page,request})=>{
 const task=await(await request.post('/api/planner/tasks',{data:{title:'Синтетическая задача readback'}})).json();
 await page.goto('/#/plans/inbox?status=all');await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');
 let release,started;const held=new Promise(r=>release=r),reading=new Promise(r=>started=r);let once=true;
 await page.route('**/api/planner/tasks',async route=>{if(!once||route.request().method()!=='GET')return route.continue();once=false;const response=await route.fetch();started();await held;await route.fulfill({response})});
 try{
  await page.getByRole('button',{name:'Завершить: '+task.title,exact:true}).click();await reading;
  const row=page.locator('[data-task-id="'+task.id+'"]');await expect(row).toBeVisible();
  expect(await row.evaluate(n=>n.closest('.planner').inert)).toBe(true);await expect(page.locator('.pl-loading')).toHaveCount(0);
  release();await expect(row).toHaveClass(/pl-terminal/);expect(await row.evaluate(n=>n.closest('.planner').inert)).toBe(false);
 }finally{release?.()}
});
