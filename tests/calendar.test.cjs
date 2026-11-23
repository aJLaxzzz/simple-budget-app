const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const context = { window: { Planner: {} }, Date, Intl };
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/js/planner/calendar-model.js'), 'utf8'), context);
const C = context.window.Planner.calendarModel;
test('Monday-first month grids have four, five or six whole weeks', () => {
  for (const [date, from, to, count] of [
    ['2021-02-15', '2021-02-01', '2021-02-28', 28],
    ['2026-11-15', '2026-10-26', '2026-12-06', 42],
    ['2027-01-15', '2026-12-28', '2027-01-31', 35]
  ]) { const range = C.range('month', date); assert.equal(range.from, from); assert.equal(range.to, to); assert.equal(C.days(range.from, range.to).length, count); }
});
test('civil arithmetic crosses December, leap days and DST without device timezone', () => {
  assert.equal(C.addDays('2026-12-31', 1), '2027-01-01');
  assert.equal(C.addMonths('2026-12-31', 1), '2027-01-31');
  assert.equal(C.addMonths('2027-01-31', 1), '2027-02-28');
  assert.equal(C.addMonths('2024-03-31', -1), '2024-02-29');
  assert.equal(C.addDays('2026-03-08', 1), '2026-03-09');
  assert.equal(C.addDays('2026-11-01', 1), '2026-11-02');
  assert.equal(C.range('week', '2027-01-01').from, '2026-12-28');
  assert.equal(C.range('day', '2024-02-29').to, '2024-02-29');
  assert.equal(C.valid('2026-02-29'), false); assert.equal(C.valid('2024-02-29'), true);
});
test('overlapping intervals have distinct columns; adjacent events reuse space', () => {
  const items = [{ id: 'a', start_time: '08:00', end_time: '10:00' }, { id: 'b', start_time: '08:30', end_time: '09:30' }, { id: 'c', start_time: '09:30', end_time: '11:00' }, { id: 'd', start_time: '12:00', end_time: null }];
  const entries = C.layout(items);
  assert.equal(entries[0].columns, 2); assert.notEqual(entries[0].column, entries[1].column);
  assert.equal(entries[1].column, entries[2].column); assert.equal(entries[3].columns, 1);
  assert.equal(items[3].end_time, null);
});
test('project and terminal filters apply to task and session status', () => {
  const item = { project_id: 'p', status: 'planned', task_status: 'completed' };
  assert.equal(C.visible(item, { project: 'p', completed: false, cancelled: false }), false);
  assert.equal(C.visible(item, { project: 'p', completed: true, cancelled: false }), true);
  assert.equal(C.visible(item, { project: 'q', completed: true }), false);
  assert.equal(C.visible({ ...item, status: 'cancelled' }, { completed: true, cancelled: false }), false);
  assert.equal(C.visible({ project_id: null, status: 'planned' }, { project: 'none' }), true);
});
test('UX22 overlap groups use readable width, preserve partial chains and never mutate inputs',()=>{
 const make=n=>Array.from({length:n},(_,i)=>({id:String(i),start_time:'10:00',end_time:'11:00'}));
 for(const n of [1,2,3,5,10]){const items=make(n),before=JSON.stringify(items),groups=C.overlapGroups(items,720);assert.equal(groups.length,1);assert.equal(groups[0].grouped,n>3);assert.equal(groups[0].entries.length,n);assert.equal(JSON.stringify(items),before);}
 assert.equal(C.overlapGroups(make(2),300)[0].grouped,true);
 assert.equal(C.overlapGroups(make(3),500)[0].grouped,true);
 const chain=[{id:'a',start_time:'10:00',end_time:'11:00'},{id:'b',start_time:'10:30',end_time:'11:30'},{id:'c',start_time:'11:00',end_time:'12:00'},{id:'d',start_time:'12:00',end_time:'13:00'}];
 const groups=C.overlapGroups(chain,150);assert.equal(groups.length,2);assert.equal(groups[0].entries.length,3);assert.equal(groups[1].grouped,false);assert.equal(new Set(groups.flatMap(g=>g.entries.map(e=>e.item.id))).size,4);
});
