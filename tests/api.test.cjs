const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(require('node:path').join(__dirname, '../static/js/api.js'), 'utf8');
function api(fetch) {
  const context = { window: {}, fetch };
  vm.runInNewContext(source, context);
  return context.window.budgetAPI;
}
test('successful JSON and 204 responses', async () => {
  assert.equal((await api(async () => ({ ok: true, status: 200, json: async () => ({ income: 42 }) }))('/api/months')).income, 42);
  assert.equal(await api(async () => ({ ok: true, status: 204 }))('/api/months/1'), null);
});
test('failed save rejects and never executes success callback', async () => {
  let saved = false;
  const call = api(async () => ({ ok: false, status: 500, json: async () => ({ error: 'Изменения не применены.' }) }));
  await assert.rejects(call('/api/months', { method: 'POST' }).then(() => { saved = true; }), /Изменения не применены/);
  assert.equal(saved, false);
});
test('non-JSON error, network loss and malformed success response reject', async () => {
  await assert.rejects(api(async () => ({ ok: false, status: 503, json: async () => { throw Error(); } }))('/api'), /503/);
  await assert.rejects(api(async () => { throw Error('offline'); })('/api'), /Сохранение не подтверждено/);
  await assert.rejects(api(async () => ({ ok: true, status: 200, json: async () => { throw Error(); } }))('/api'), /Некорректный ответ/);
});

test('planner errors preserve status, code and current revision', async () => {
  const call = api(async () => ({ ok: false, status: 409, json: async () => ({ error: { code: 'revision_conflict', message: 'Версия изменилась', current: { id: 'task', revision: 4 } } }) }));
  await assert.rejects(call('/api/planner/tasks/task', { method: 'PATCH' }), error => {
    assert.equal(error.status, 409);
    assert.equal(error.code, 'revision_conflict');
    assert.equal(error.current.revision, 4);
    assert.equal(error.message, 'Версия изменилась');
    return true;
  });
});
