const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

test('closing or reloading the page sends no shutdown request', () => {
  const handlers = new Map();
  const requests = [];
  const root = { innerHTML: '' };
  const context = {
    window: {
      location: { hash: '#/', origin: 'http://127.0.0.1:3001' },
      addEventListener(name, fn) {
        const list = handlers.get(name) || [];
        list.push(fn); handlers.set(name, list);
      },
      budgetViews: {run: render => render()}, budgetHTML() {}, budgetFinanceView() {}, budgetAPI(url) { requests.push(url); return new Promise(() => {}); }
    },
    document: { getElementById(id) { return id === 'root' ? root : null; } },
    navigator: { sendBeacon(url) { requests.push(url); } },
    fetch(url) { requests.push(url); return new Promise(() => {}); }
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/js/app.js'), 'utf8'), context);
  const before = requests.slice();
  for (const name of ['beforeunload', 'pagehide', 'unload']) {
    for (const callback of handlers.get(name) || []) callback({ type: name });
  }
  assert.deepEqual(requests, before);
  assert.equal(requests.some(url => url.includes('/shutdown')), false);
});
