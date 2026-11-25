// Called only by caddy_staging_integration.py with temporary synthetic PKI.
const {chromium}=require('@playwright/test');
const assert=require('node:assert/strict');
const path=require('node:path');
(async()=>{
 const [root,prodPort,stgPort]=process.argv.slice(2);
 const prod=`https://127.0.0.1:${prodPort}`,stg=`https://127.0.0.1:${stgPort}`;
 const browser=await chromium.launch({channel:process.env.PLAYWRIGHT_CHANNEL||'chrome',args:['--ignore-certificate-errors']});
 try {
  const context=await browser.newContext({ignoreHTTPSErrors:true,clientCertificates:[prod,stg].map(origin=>({origin,certPath:path.join(root,'client.crt'),keyPath:path.join(root,'client.key')}))});
  const p=await context.newPage(),s=await context.newPage();
  await p.goto(prod);await p.evaluate(()=>localStorage.setItem('stg-test','prod'));
  await context.addCookies([{name:'__Host-budget-session',value:'prod-sentinel',url:prod,httpOnly:true,secure:true,sameSite:'Strict'}]);
  await s.goto(stg);
  assert.equal(await s.evaluate(()=>localStorage.getItem('stg-test')),null);
  await s.evaluate(()=>localStorage.setItem('stg-test','stg'));
  assert.equal(await p.evaluate(()=>localStorage.getItem('stg-test')),'prod');
  assert.equal((await context.cookies()).find(c=>c.name==='__Host-budget-session').value,'prod-sentinel','STG overwrote PROD cookie');
  await p.evaluate(()=>navigator.serviceWorker.register('/sw.js'));
  await p.evaluate(()=>navigator.serviceWorker.ready);
  assert.equal(await s.evaluate(async()=>(await navigator.serviceWorker.getRegistrations()).length),0);
  await s.evaluate(()=>navigator.serviceWorker.register('/sw.js'));
  assert.equal(await s.evaluate(async()=>(await navigator.serviceWorker.ready).scope),stg+'/');
  assert.equal(await p.evaluate(async()=>(await navigator.serviceWorker.ready).scope),prod+'/');
  await context.close();console.log('PASS: Chromium cookie confinement, independent localStorage and service worker origins');
 } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
