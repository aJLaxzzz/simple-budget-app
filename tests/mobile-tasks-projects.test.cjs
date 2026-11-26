const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
function model(){const c={window:{addEventListener(){},location:{hash:'#/plans/tasks'}},URLSearchParams,Date,Intl};for(const name of ['core','calendar-model','task-groups','series'])vm.runInNewContext(fs.readFileSync('static/js/planner/'+name+'.js','utf8'),c);return c.window.Planner;}
const p=model(),today='2026-11-23',projects=[{id:'a',repeat_series_id:'series'},{id:'b',repeat_series_id:'series'},{id:'c',title:'Same'},{id:'d',title:'Same'}];
const task=(id,project_id,planned_date,status='planned',scheduled_time=null)=>({id,title:id,project_id,planned_date,status,scheduled_time});
for(const [section,date,expected] of [['today',today,1],['tomorrow','2026-11-24',1],['week','2026-11-25',2],['month','2026-12-01',2],['later','2027-01-01',2],['undated',null,2]])test('3.3 grouping instance identity in '+section,()=>{
 const all=[task('x','a',date),task('y','b',date)];const result=p.projectTaskGroups(all,all,section,today,projects);assert.equal(result.length,expected);
 const ordinary=[task('x','c',date),task('y','d',date)];assert.equal(p.projectTaskGroups(ordinary,ordinary,section,today,projects).length,2);
});
test('3.3 multiday dates, deduplicated real progress and nearest active sort',()=>{
 const all=[task('early','a','2026-12-01','completed'),task('late','a','2026-12-03','planned','16:00'),task('untimed','a','2026-12-03'),task('other','b','2026-12-02'),task('outside','a','2027-03-01'),task('cancel','a','2026-12-02','cancelled')];
 const visible=all.filter(t=>p.active(t)&&t.id!=='outside');const result=p.projectTaskGroups([...visible,visible[0]],[...all,all[0]],'month',today,projects);
 assert.equal(result.length,2);assert.equal(result[0].projectID,'b');const a=result[1];assert.equal(a.completed,1);assert.equal(a.members.length,4);assert.equal(a.tasks.length,2);assert.equal(a.tasks[0].id,'untimed');assert.equal(a.date,'2026-12-01');assert.equal(a.endDate,'2026-12-03');assert.equal(a.multiDay,true);
 const key=a.key;all[1].planned_date='2026-12-04';assert.equal(p.projectTaskGroups(visible,all,'month',today,projects).find(g=>g.projectID==='a').key,key);
});
test('3.3 week dates stay separate; overdue semantics remain series/day',()=>{
 const all=[task('x','a','2026-11-25'),task('y','a','2026-11-26')];assert.equal(p.projectTaskGroups(all,all,'week',today,projects).length,2);
 all.forEach(t=>{t.planned_date='2026-11-20';t.due_date='2026-11-20';});all[1].project_id='b';assert.equal(p.projectTaskGroups(all,all,'overdue',today,projects).length,1);
});
test('3.3 recurrence sort uses calculated dates, unknown last and stable ties without mutation',()=>{
 p.seriesTitle=s=>s.title;
 const rows=[{id:'c',title:'А',next_date:null},{id:'a',title:'Б',next_date:'2027-01-01'},{id:'b',title:'А',next_date:'2026-12-01'},{id:'d',title:'А',next_date:'2026-12-01'}];
 assert.equal(p.sortSeries(rows).map(s=>s.id).join(','),'b,d,a,c');assert.equal(p.sortSeries(rows,'farthest').map(s=>s.id).join(','),'a,b,d,c');assert.equal(p.sortSeries(rows,'title').map(s=>s.id).join(','),'b,c,d,a');assert.equal(rows[0].id,'c');
});
