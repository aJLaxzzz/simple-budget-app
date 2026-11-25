const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
function planner(hash = '#/plans/inbox') {
  const context = { window:{addEventListener() {},location: { hash } }, URLSearchParams, Intl, Date, clearTimeout, document:{getElementById(){return null;}} };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/js/planner/core.js'), 'utf8'), context);
  return context.window.Planner;
}
test('Moscow completion date remains fixed across UTC date boundary', () => {
  assert.equal(planner().today(new Date('2026-11-15T22:30:00Z')), '2026-11-16');
  assert.equal(planner().today(new Date('2026-11-15T20:30:00Z')), '2026-11-15');
});
test('one open planner observes Moscow midnight, new year and leap day without reload', () => {
  let now = '2026-12-31T20:59:59.999Z';
  class Clock extends Date { constructor(...args) { super(...(args.length ? args : [now])); } }
  const context = { window: { addEventListener() {}, location: { hash: '#/plans/tasks' } }, URLSearchParams, Intl, Date: Clock };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/js/planner/core.js'), 'utf8'), context);
  const p = context.window.Planner;
  assert.equal(p.today(), '2026-12-31');
  now = '2026-12-31T21:00:00.000Z'; assert.equal(p.today(), '2027-01-01');
  now = '2028-02-28T21:00:00.000Z'; assert.equal(p.today(), '2028-02-29');
  now = '2028-02-29T21:00:00.000Z'; assert.equal(p.today(), '2028-03-01');
  assert.equal(p.today(new Date('2026-11-24T20:59:59Z')), '2026-11-24');
});
test('hash routes preserve task filters across opening and closing editor', () => {
  const p = planner('#/plans/tasks?status=completed&q=hello&project=abc');
  p.openTask('task-id');
  assert.equal(p.route().params.get('q'), 'hello');
  assert.equal(p.route().params.get('task'), 'task-id');
  p.closeEditor();
  assert.equal(p.route().params.get('status'), 'completed');
  assert.equal(p.route().params.has('task'), false);
});
test('plans startup bypasses all financial API requests', () => {
  const calls = [];
  let mounted = false;
  const root = {};
  const context = {
    window:{addEventListener() {},location: { hash: '#/plans/inbox' }, addEventListener() {}, Planner: { mount(value) { mounted = value === root; } }, budgetFinanceView() {}, budgetAPI(url) { calls.push(url); return new Promise(() => {}); } },
    document: { getElementById(id) { return id === 'root' ? root : null; } }
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/js/app.js'), 'utf8'), context);
  assert.equal(mounted, true); assert.deepEqual(calls, []);
});
test('late financial home responses do not create a month after navigating to plans', async () => {
  const pending = [], calls = [];
  const context = {
    window:{addEventListener() {},location: { hash: '#/' }, addEventListener() {}, Planner: { unmount() {}, mount() {} }, budgetFinanceView() {}, budgetAPI(url) { calls.push(url); return new Promise(resolve => pending.push(resolve)); } },
    document: { getElementById(id) { return id === 'root' ? { innerHTML: '' } : null; } }
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/js/app.js'), 'utf8'), context);
  context.window.location.hash = '#/plans/inbox';
  pending[0]({ month: 11, year: 2026 }); pending[1]([]);
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(calls, ['/api/now', '/api/months']);
});
