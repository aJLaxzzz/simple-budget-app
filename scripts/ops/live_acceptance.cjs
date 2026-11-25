// Synthetic acceptance only. Private stdin carries secrets; no traces or screenshots.
const {chromium,expect}=require('@playwright/test');
const crypto=require('node:crypto');
let stage='input';
async function runBrowser(cfg){
 const browser=await chromium.launch({headless:true,channel:'chrome'});const contexts=[];const errors=[];
 const options={baseURL:cfg.origin,clientCertificates:cfg.origin.startsWith('https:')?[{origin:cfg.origin,certPath:cfg.pki+'/mac-1.crt',keyPath:cfg.pki+'/mac-1.key',passphrase:cfg.keyPassword}]:[]};
 let machine;
 try{
  stage='HTML login';
  const ca=await browser.newContext(options),cb=await browser.newContext(options);contexts.push(ca,cb);
  for(const ctx of contexts)ctx.setDefaultTimeout(15000);
  const a=await ca.newPage(),b=await cb.newPage();
  for(const page of [a,b]){
   page.on('pageerror',()=>errors.push(true));
   await page.goto('/login.html');
   await expect(page.getByLabel('Логин',{exact:true})).toHaveAttribute('autocomplete','username');
   await expect(page.getByLabel('Пароль',{exact:true})).toHaveAttribute('autocomplete','current-password');
   await page.getByLabel('Логин',{exact:true}).fill('budget');await page.getByLabel('Пароль',{exact:true}).fill(cfg.password);
   await page.getByRole('button',{name:'Войти',exact:true}).click();await page.waitForURL(u=>u.pathname==='/');
  }
  // Use a separate cookie-free request context; browser cookies cannot opt into Basic.
  const {request}=require('@playwright/test');
  machine=await request.newContext({...options,extraHTTPHeaders:{'X-Budget-Admin':'1',Authorization:'Basic '+Buffer.from('budget:'+cfg.password).toString('base64'),Origin:cfg.origin}});
  cfg.password='';cfg.keyPassword='';
  stage='synthetic financial fixture';
  const list=await(await machine.get('/api/months')).json();let year=2040;
  while(list.some(m=>m.year===year&&m.month===12))year++;
  if(year>2090)throw Error('fixture bound');
  let revision=(await machine.get('/api/months')).headers()['x-budget-revision'];
  let response=await machine.post('/api/months',{headers:{'If-Match':revision},data:{year,month:12}});expect(response.status()).toBe(201);
  const month=await response.json(),prefix='VPS Live '+crypto.randomBytes(5).toString('hex');
  const update=async income=>{
   const rev=(await machine.get('/api/months')).headers()['x-budget-revision'];
   expect((await machine.patch('/api/months/'+month.id+'/income',{headers:{'If-Match':rev},data:{income}})).status()).toBe(200);
  };
  stage='financial live draft and conflict';
  for(const page of [a,b]){await page.goto('/#/month/'+month.id);await expect(page.locator('#sync-status')).toHaveText('Синхронизировано');}
  await b.evaluate(()=>window.acceptanceSentinel='retained');
  await a.locator('#income-value').click();await a.locator('#income-input').fill('99');await update(123);
  await expect(b.locator('#income-value')).toContainText('123');await expect(a.locator('#income-input')).toHaveValue('99');
  const conflict=await a.evaluate(async id=>{try{await window.budgetAPI('/api/months/'+id+'/income',{method:'PATCH',body:JSON.stringify({income:99})});return 200;}catch(e){return e.status;}},month.id);
  expect(conflict).toBe(409);await expect(a.locator('#income-input')).toHaveValue('99');
  stage='offline reconciliation';
  await cb.setOffline(true);await update(222);await cb.setOffline(false);
  await expect(b.locator('#income-value')).toContainText('222');await expect(b.locator('#sync-status')).toHaveText('Синхронизировано');
  expect(await b.evaluate(()=>window.acceptanceSentinel)).toBe('retained');
  stage='category drafts';
  const catResponse=await machine.get('/api/categories'),cats=await catResponse.json();
  cats.expense.push({id:prefix.replaceAll(' ','-'),name:prefix,subcategories:[]});
  expect((await machine.put('/api/categories',{headers:{'If-Match':catResponse.headers()['x-budget-revision']},data:cats})).status()).toBe(200);
  for(const page of [a,b])await page.goto('/#/categories');
  const field=page=>page.locator('.categories-expense .cat-name').last();
  await expect(field(a)).toHaveValue(prefix);await expect(field(b)).toHaveValue(prefix);
  await field(a).fill(prefix+' draft');await field(b).fill(prefix+' remote');
  await b.locator('#save-categories').click();await expect(b.locator('.categories-saved')).toHaveClass(/visible/);
  await expect(a.getByText('Данные изменились.',{exact:false})).toBeVisible();await expect(field(a)).toHaveValue(prefix+' draft');
  const categoryConflict=a.waitForResponse(r=>r.url().endsWith('/api/categories')&&r.request().method()==='PUT');
  a.once('dialog',d=>d.accept());await a.locator('#save-categories').click();expect((await categoryConflict).status()).toBe(409);
  await expect(field(a)).toHaveValue(prefix+' draft');
  stage='planner updates and drafts';
  const taskResponse=await machine.post('/api/planner/tasks',{data:{title:prefix}});expect(taskResponse.status()).toBe(201);const task=await taskResponse.json();
  for(const page of [a,b])await page.goto('/#/plans/inbox');
  await expect(a.getByText(prefix,{exact:true}).first()).toBeVisible();await expect(b.getByText(prefix,{exact:true}).first()).toBeVisible();
  await a.goto('/#/plans/tasks?task='+task.id);const draft=a.getByRole('dialog',{name:'Редактор задачи',exact:true}).locator('textarea[name=title]');
  await draft.fill(prefix+' draft');
  expect((await machine.patch('/api/planner/tasks/'+task.id,{data:{title:prefix+' remote',expected_revision:task.revision}})).status()).toBe(200);
  await expect(draft).toHaveValue(prefix+' draft');await expect(a.getByText('Данные изменились.',{exact:false})).toBeVisible();
  stage='specific session revocation with draft';
  const sessionID=await a.evaluate(async()=> (await(await fetch('/api/auth/session')).json()).id);
  expect(await b.evaluate(async id=>{const session=await(await fetch('/api/auth/session')).json();return(await fetch('/api/auth/revoke',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf},body:JSON.stringify({id})})).status;},sessionID)).toBe(200);
  await expect(a.locator('#sync-status')).toHaveText('Требуется повторная авторизация');await expect(draft).toHaveValue(prefix+' draft');
  await expect(a.getByRole('dialog',{name:'Редактор задачи',exact:true}).getByRole('link',{name:'Войти в новой вкладке'})).toBeVisible();
  expect(errors.length).toBe(0);
  return {passed:true,checks:['HTML form login','two live clients','finance draft and HTTP409','offline reconciliation without reload','category draft and HTTP409','planner live draft','specific session revocation preserves draft'],synthetic_month:month.id,synthetic_task:task.id};
 }finally{
  for(const ctx of contexts){
   const page=ctx.pages()[0];
   if(page)try{await page.evaluate(async()=>{const s=await fetch('/api/auth/session');if(s.ok){const x=await s.json();await fetch('/api/auth/logout',{method:'POST',headers:{'X-CSRF-Token':x.csrf}});}});}catch{}
   await ctx.close();
  }
  if(machine)await machine.dispose();await browser.close();
 }
}
module.exports={runBrowser};
if(require.main===module)(async()=>{
 let raw='';for await(const part of process.stdin){raw+=part;if(raw.length>32768)throw Error('bounded input');}
 const cfg=JSON.parse(raw);raw='';
 if(cfg.origin!=='https://171.22.237.16')throw Error('target');
 process.stdout.write(JSON.stringify(await runBrowser(cfg)));
})().catch(()=>{process.stdout.write(JSON.stringify({passed:false,stage}));process.exitCode=1;});
