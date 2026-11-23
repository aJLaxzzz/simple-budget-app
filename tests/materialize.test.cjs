const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
function planner(api) {
  const P = { api, calendarModel: {} };
  vm.runInNewContext(fs.readFileSync('static/js/planner/calendar.js', 'utf8'), {
    window: { addEventListener() {}, Planner: P, matchMedia: () => ({ addEventListener() {} }) },
    sessionStorage: { getItem: () => null }, URLSearchParams
  });
  return P;
}
test('calendar materialization follows series pages and catch-up batches', async () => {
  const calls = [], responses = [
    { results: [], pending: true, next_after_series_id: 'page-2' },
    { results: [], pending: false },
    { results: [], pending: false }
  ];
  const P = planner(async (path, method, body) => { calls.push([path, method, body.through, body.after_series_id]); return responses.shift(); });
  const errors = await P.materializeCalendar('2027-03-31', () => true);
  assert.equal(errors.length, 0);
  assert.deepEqual(calls, ['', 'page-2', ''].map(after => ['/materialize', 'POST', '2027-03-31', after]));
});
test('series errors are visible, transport errors reject, navigation stops catch-up', async () => {
  const P = planner(async () => ({ results: [{ series_id: 'bad', error: { message: 'Нет календаря 2027' } }], pending: false }));
  assert.equal((await P.materializeCalendar('2027-03-31', () => true))[0], 'Нет календаря 2027');
  let calls = 0;
  const cancelled = planner(async () => { calls++; return { results: [], pending: true }; });
  assert.equal(await cancelled.materializeCalendar('2027-03-31', () => calls === 0), null);
  assert.equal(calls, 1);
  const failed = planner(async () => { throw new Error('network error'); });
  await assert.rejects(failed.materializeCalendar('2027-03-31', () => true), /network error/);
});
test('huge catch-up is bounded with an explicit continuation message', async () => {
  let calls = 0;
  const P = planner(async () => { calls++; return { results: [], pending: true }; });
  await assert.rejects(P.materializeCalendar('2027-03-31', () => true), /продолжить/);
  assert.equal(calls, 100);
});
test('a generation limit leaves distant ordinary calendar dates readable', async () => {
  const P = planner(async () => { throw Object.assign(new Error('Доступны уже созданные задачи'), { status: 422, code: 'materialization_range_limit' }); });
  const warnings = await P.materializeCalendar('2099-01-31', () => true);
  assert.equal(warnings[0], 'Доступны уже созданные задачи');
});
