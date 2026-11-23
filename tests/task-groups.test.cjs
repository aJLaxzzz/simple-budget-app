const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const vm=require('node:vm');
function load(){const c={window:{addEventListener() {},location:{hash:'#/plans'}},URLSearchParams,Intl,Date};for(const file of ['core','calendar-model','task-groups'])vm.runInNewContext(fs.readFileSync('static/js/planner/'+file+'.js','utf8'),c);return c.window.Planner;}
const p=load();const task=(id,date,due,status='planned')=>({id,title:id,status,planned_date:date,due_date:due});
function groups(tasks,date){return JSON.parse(JSON.stringify(p.taskGroups(tasks,date))).map(g=>[g.key,g.tasks.map(t=>t.id)]);}
test('root opens calendar; old routes and filters retain their meanings',()=>{assert.equal(p.route().view,'calendar');for(const v of ['inbox','tasks','projects','calendar','series','templates','attention','settings'])assert.equal(p.route('#/plans/'+v+'?date=2026-12-31').view,v);assert.equal(p.route('#/plans/tasks?view=undated').params.get('view'),'undated');});
test('exclusive civil date groups with overdue deadline taking precedence',()=>{
 const tasks=[task('past','2026-11-15'),task('today','2026-11-16'),task('tomorrow','2026-11-17'),task('week','2026-11-22'),task('month','2026-11-30'),task('later','2026-12-01'),task('no-date'),task('late-deadline','2027-01-01','2026-11-15'),task('deadline-only',null,'2026-11-16')];
 assert.deepEqual(groups(tasks,'2026-11-16'),[['overdue',['late-deadline']],['plan_missed',['past']],['today',['deadline-only','today']],['tomorrow',['tomorrow']],['week',['week']],['month',['month']],['later',['later']],['undated',['no-date']]]);
});
test('Monday boundary, tomorrow precedes week/month, year rollover',()=>{
 assert.deepEqual(groups([task('monday','2026-11-23'),task('tuesday','2026-11-24')],'2026-11-22'),[['tomorrow',['monday']],['month',['tuesday']]]);
 assert.deepEqual(groups([task('new-year','2027-01-01'),task('sunday','2027-01-03'),task('next-week','2027-01-04')],'2026-12-31'),[['tomorrow',['new-year']],['week',['sunday']],['later',['next-week']]]);
});
test('leap days and calendar months do not depend on milliseconds or timezone',()=>{
 assert.deepEqual(groups([task('leap','2028-02-29'),task('march','2028-03-01')],'2028-02-28'),[['tomorrow',['leap']],['week',['march']]]);
 assert.equal(p.today(new Date('2026-11-15T22:00:00Z')),'2026-11-16');
});
test('closed and skipped items are never active overdue; repeated IDs appear once',()=>{
 const repeat=task('repeat','2020-01-01');repeat.series_id='series';
 assert.deepEqual(groups([repeat,repeat,task('done','2020-01-01',null,'completed'),task('cancel','2020-01-01',null,'cancelled'),task('skip','2020-01-01',null,'skipped')],'2026-11-16'),[['plan_missed',['repeat']],['completed',['done']],['cancelled',['cancel']],['skipped',['skip']]]);
});
