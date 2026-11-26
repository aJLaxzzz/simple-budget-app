const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
function model(){const c={window:{addEventListener(){},location:{hash:'#/plans/tasks'}},URLSearchParams,Date,Intl};for(const name of ['core','calendar-model','task-groups'])vm.runInNewContext(fs.readFileSync('static/js/planner/'+name+'.js','utf8'),c);return c.window.Planner;}
const today='2026-11-26';
const task=(id,project,date,status='planned',time=null)=>({id,project_id:project,planned_date:date,status,scheduled_time:time,title:id});
test('project grouping separates instance IDs, dates and temporal sections including undated',()=>{
 const p=model(),tasks=[task('a','p1',today),task('b','p1',today),task('c','p2',today),task('d','p1','2026-11-25'),task('e','p1','2026-11-24'),task('f','p1',null),task('solo',null,today)];
 const result=p.taskGroups(tasks,today).flatMap(g=>p.projectTaskGroups(g.tasks,tasks,g.key,today));
 assert.equal(result.filter(g=>g.key).length,5);assert.equal(new Set(result.filter(g=>g.key).map(g=>g.key)).size,5);
 assert.equal(result.find(g=>g.date===today&&g.projectID==='p1').tasks.length,2);assert.equal(result.find(g=>g.task)?.task.id,'solo');
});
test('progress counts hidden completed tasks only in the same instance/date/section; skipped is not done',()=>{
 const p=model(),all=[task('active','p1',today),task('done','p1',today,'completed'),task('skip','p1',today,'skipped'),task('cancel','p1',today,'cancelled'),task('other','p2',today,'completed'),task('tomorrow','p1','2026-11-27','completed')];
 const g=p.projectTaskGroups([all[0]],all,'today',today)[0];assert.equal(g.completed,1);assert.equal(g.members.length,4);assert.equal(g.tasks.length,1);
 all[1].status='planned';assert.equal(p.projectTaskGroups([all[0]],all,'today',today)[0].completed,0);
 all[0].planned_date='2026-11-27';const moved=p.projectTaskGroups([all[0]],all,'tomorrow',today)[0];assert.equal(moved.members.length,2);assert.equal(moved.completed,1);
});
test('group task ordering is date then time, untimed first; time range includes completed members',()=>{
 const p=model(),tasks=[task('a','p',today,'planned'),task('z','p',today,'planned','09:00'),task('b','p',today,'planned','08:00'),task('done','p',today,'completed','12:30')];
 const group=p.taskGroups(tasks,today).find(g=>g.key==='today');assert.equal(group.tasks.map(t=>t.id).join(','),'a,b,z');
 assert.equal(p.projectTaskGroups(group.tasks,tasks,'today',today)[0].time,'08:00–12:30');
});
test('calendar completion distinguishes cancellation/skip and deduplicates task/session badges',()=>{
 const c=model().calendarModel;
 assert.equal(c.completed({status:'planned',task_status:'completed'}),true);
 assert.equal(c.completed({status:'cancelled',task_status:'completed'}),false);
 assert.equal(c.completedCount([{task_id:'a',status:'completed'},{task_id:'a',status:'completed'},{task_id:'b',status:'skipped'}]),1);
 assert.equal(c.completedCount([{type:'session',task_id:'open',status:'completed',task_status:'planned'}]),0);
 assert.equal(c.visible({status:'skipped'},{completed:true,cancelled:true}),false);
});

test('mixed chronology uses time before title, project and priority; cards use earliest active member',()=>{
 const p=model(),all=[task('early','p',today,'completed','07:00'),task('late','p',today,'planned','16:00'),task('solo',null,today,'planned','12:00'),task('untimed',null,today),task('second','q',today,'planned','11:00')];
 const visible=all.filter(p.active).reverse();const sorted=p.taskGroups(visible,today)[0].tasks;
 assert.equal(sorted.map(t=>t.id).join(','),'untimed,second,solo,late');
 assert.equal(p.projectTaskGroups(sorted,all,'today',today).map(e=>e.task?.id||e.projectID).join(','),'untimed,q,solo,p');
 all[1].scheduled_time='08:00';assert.equal(p.projectTaskGroups(sorted,all,'today',today).map(e=>e.task?.id||e.projectID).join(','),'untimed,p,q,solo');
});
test('calendar order is day, untimed first, ascending time and stable tie',()=>{
 const c=model().calendarModel,events=[{id:'z',title:'Я',date:today,start_time:'16:00'},{id:'a',title:'А',date:today,start_time:'20:00'},{id:'b',title:'Б',date:today,start_time:null}];
 assert.equal(events.sort(c.compareEvents).map(e=>e.id).join(','),'b,z,a');
});

test('reported reversed afternoon times sort numerically regardless of titles',()=>{
 const p=model(),times=['16:00','17:00','22:00','23:00','21:00','20:00','19:00','18:00','15:00','11:00','12:00'];
 const rows=times.map((time,i)=>task(String(i),i%2?'p':null,today,'planned',time));
 assert.equal(p.taskGroups(rows,today)[0].tasks.map(t=>t.scheduled_time).join(','),[...times].sort().join(','));
});

test('addition2 shared nearest N and series filters preserve overdue, instance keys and namesakes',()=>{
 const p=model();p.repeatLimit=5;
 const rows=Array.from({length:8},(_,i)=>({...task(String(i),'p1','2026-12-'+String(i+1).padStart(2,'0')),series_id:'s'}));
 rows.push({...task('late','p1','2026-11-20'),series_id:'s'});
 assert.equal(p.limitUpcoming(rows,false,today).length,6);assert.equal(p.limitUpcoming(rows,true,today).length,9);
 p.repeatLimit=1;assert.equal(p.limitUpcoming(rows,false,today).length,2);
 const projects=[{id:'p1',title:'Уборка',repeat_series_id:'s'},{id:'p2',title:'Уборка',repeat_series_id:'s'},{id:'p3',title:'Уборка',repeat_series_id:'other'},{id:'p4',title:'Уборка'}];
 assert.deepEqual(Array.from(p.projectFilterOptions(projects),x=>x[0]),['series:s','series:other','p4']);
 assert.equal(p.matchesProject('p2','series:s',projects),true);assert.equal(p.matchesProject('p3','series:s',projects),false);
 assert.equal(p.matchesProject('p2','p1',projects),false);assert.equal(p.projectFilterOptions(projects,'p1').length,4);
});
test('addition2 calendar grouping orders status then chronology; timed layout remains chronological',()=>{
 const c=model().calendarModel,items=[{id:'done',title:'done',date:today,status:'completed',start_time:'09:00'},{id:'active',title:'active',date:today,status:'planned',start_time:'15:00'},{id:'cancel',title:'cancel',date:today,status:'cancelled',start_time:'08:00'},{id:'early',title:'early',date:today,status:'planned',start_time:'12:00'}];
 assert.deepEqual(items.slice().sort(c.compareGroupedEvents).map(x=>x.id),['early','active','done','cancel']);
 assert.deepEqual(items.slice().sort(c.compareEvents).map(x=>x.id),['cancel','done','early','active']);
 assert.equal(c.statusRank({status:'planned',task_status:'cancelled'}),2);
});

test('addition2 instance labels use factual dates, not inferred recurrence durations',()=>{
 const c={window:{addEventListener(){},location:{hash:'#/plans'}},URLSearchParams,Intl,Date};for(const f of ['core','calendar-model','views'])vm.runInNewContext(fs.readFileSync('static/js/planner/'+f+'.js','utf8'),c);
 const label=c.window.Planner.projectDateLabel;
 assert.match(label({repeat_series_id:'s',start_date:'2026-11-27',due_date:'2026-11-29'}),/^27–29 ноября/);
 assert.match(label({repeat_series_id:'s',start_date:'2026-12-01',due_date:'2026-12-31'}),/декабрь 2026/);
 assert.equal(label({repeat_series_id:'s',start_date:'2027-01-01',due_date:'2027-12-31'}),'2027');
 assert.match(label({repeat_series_id:'s',start_date:'2027-01-12'}),/^12 января/);
});
test('addition2 intentional STG backup disablement is informational; real warnings survive',()=>{
 const c={window:{Planner:{}},URLSearchParams,Intl,Date};vm.runInNewContext(fs.readFileSync('static/js/planner/production-calendar.js','utf8'),c);
 const view=c.window.Planner.backupViewModel;
 assert.match(view({automatic:'unknown',stale:true},true).automatic,/намеренно отключены/);
 assert.equal(view({automatic:'unknown',stale:true},true).warning,'');
 assert.equal(view({automatic:'unknown',warning:'Ошибка проверки'},true).warning,'Ошибка проверки');
 assert.match(view({automatic:'unknown',stale:true},false).warning,/Нет актуальной/);
});
