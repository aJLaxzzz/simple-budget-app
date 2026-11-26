const base = require('@playwright/test');
// Applies to every browser spec, including manually opened authenticated contexts.
// HTTP errors and deliberate request.abort() are not JavaScript exceptions.
// There are no allowed pageerror exceptions in the current suite.
const test = base.test.extend({
  _unhandledPageErrors: [async ({ context, browser }, use, info) => {
    const errors = [], watched = new WeakSet(), contexts = [];
    const watchPage = page => {
      if (watched.has(page)) return;
      watched.add(page);
      page.on('pageerror', error => errors.push({ url: page.url(), message: error.message, stack: error.stack }));
    };
    const watchContext = current => {
      current.pages().forEach(watchPage); current.on('page', watchPage); contexts.push(current); return current;
    };
    watchContext(context);
    const createContext = browser.newContext;
    browser.newContext = async function (...args) { return watchContext(await createContext.apply(this, args)); };
    try { await use(); }
    finally {
      browser.newContext = createContext;
      contexts.forEach(c => c.off('page', watchPage));
      if (errors.length) await info.attach('unexpected-pageerrors', { body: JSON.stringify(errors, null, 2), contentType: 'application/json' });
      base.expect(errors, 'Unexpected JavaScript pageerror').toEqual([]);
    }
  }, { auto: true }]
});
async function revealAction(page,name) {
 await page.getByRole('button',{name,exact:true,includeHidden:true}).first().waitFor({state:'attached'});
 const menus=page.locator('.pl-action-menu');
 for(const menu of await menus.all())for(const group of await menu.locator('details').all()){
  if(await group.getByRole('button',{name,exact:true,includeHidden:true}).count() && !await group.evaluate(e=>e.open))await group.locator('summary').click();
 }
}
async function login(page,password) {
 await page.goto('/login.html');await page.getByLabel('Логин',{exact:true}).fill('budget');await page.getByLabel('Пароль',{exact:true}).fill(password);await page.getByRole('button',{name:'Войти',exact:true}).click();await page.waitForURL(url=>url.pathname==='/');
}
async function plannerPreference(page,label,value) {
 const hash=new URL(page.url()).hash;
 await page.evaluate(()=>Planner.navigate('#/plans/settings'));
 await page.getByLabel(label,{exact:true}).setChecked(value);
 const [path,query]=hash.split('?'),params=new URLSearchParams(query);
 if(path==='#/plans/calendar'){params.delete('completed');params.delete('cancelled');}
 await page.evaluate(hash=>Planner.navigate(hash),path+(params.size?'?'+params:''));
 await page.locator('.pl-content .pl-loading').waitFor({state:'detached'});
}
module.exports = { ...base, test, revealAction, login, plannerPreference };
