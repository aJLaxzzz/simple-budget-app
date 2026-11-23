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
module.exports = { ...base, test };
