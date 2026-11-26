const {plannerPreference}=require('../browser-fixtures.cjs');
const {test,expect}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto');
async function api(r,path,data,method=data?'post':'get'){const a=await r[method]('/api/planner/'+path,data?{data}:{});expect(a.ok(),await a.text()).toBeTruthy();return a.json()}
const day=()=>new Intl.DateTimeFormat('sv-SE',{timeZone:'Europe/Moscow'}).format(new Date());
const add=(d,n)=>{const x=new Date(d+'T12:00:00Z');x.setUTCDate(x.getUTCDate()+n);return x.toISOString().slice(0,10)};
const event=(p,t)=>p.locator('.pc-event[data-event-id="task:'+t.id+'"]');
async function calendar(p,project,mode='day',date=day()){await p.goto('/#/plans/calendar?mode='+mode+'&date='+date+'&project='+project.id);await expect(p.locator('.pc-calendar')).toBeVisible();}
async function project(r){return api(r,'projects',{title:'Синтетический UX31 '+randomUUID(),color_id:'cyan'})}
async function task(r,p,title,time,date=day()){return api(r,'tasks',{title,project_id:p.id,planned_date:date,scheduled_time:time||null,due_date:add(day(),20)})}
async function atTime(p,t,time,date=day()){await event(p,t).dragTo(p.locator('[data-time-day="'+date+'"] [data-time="'+time+'"]'),{targetPosition:{x:100,y:10}})}
async function scroll(p){return p.locator('.pc-time-scroll').evaluate(n=>({top:n.scrollTop,left:n.scrollLeft}))}

test('UX31 direct untimed drop at 15:00 preserves both scroll axes, undo and external SSE',async({page,request,browser,baseURL})=>{
 const p=await project(request),t=await task(request,p,'Разобрать бумаги',null);
 await calendar(page,p);await page.addStyleTag({content:'.pc-time-grid{min-width:1800px}'});
 await page.locator('.pc-time-scroll').evaluate(n=>{n.scrollTop=1000;n.scrollLeft=50});const before=await scroll(page);
 await page.evaluate(()=>{window.dialogs=0;new MutationObserver(rs=>{for(const r of rs)for(const n of r.addedNodes)if(n.nodeType===1&&(n.matches('.pl-dialog')||n.querySelector('.pl-dialog')))window.dialogs++}).observe(document.body,{childList:true,subtree:true})});
 await atTime(page,t,'15:00');await expect.poll(async()=>(await api(request,'tasks/'+t.id)).scheduled_time).toBe('15:00');await expect(page.getByRole('button',{name:'Отменить перенос'})).toBeVisible();
 expect(await scroll(page)).toEqual(before);expect(await page.evaluate(()=>window.dialogs)).toBe(0);expect((await api(request,'tasks/'+t.id)).due_date).toBe(t.due_date);
 await page.getByRole('button',{name:'Отменить перенос'}).click();await expect.poll(async()=>(await api(request,'tasks/'+t.id)).scheduled_time).toBeNull();await expect(page.locator('.pc-allday-items').getByText(t.title,{exact:false})).toBeVisible();expect(await scroll(page)).toEqual(before);
 const ctx=await browser.newContext({baseURL});try{const other=await ctx.newPage();await calendar(other,p);await other.evaluate(async id=>{const t=await Planner.api('/tasks/'+id);await Planner.api('/tasks/'+id,'PATCH',{expected_revision:t.revision,title:'Внешнее обновление'});},t.id);await expect(page.getByText('Внешнее обновление',{exact:true})).toBeVisible();expect(await scroll(page)).toEqual(before);}finally{await ctx.close()}
});

test('UX31 changed time and cross-day drop are immediate; stale undo never overwrites another client',async({page,request})=>{
 const p=await project(request),t=await task(request,p,'Задача на время','15:00');await calendar(page,p,'week');await page.locator('.pc-time-scroll').evaluate(n=>n.scrollTop=1000);
 await atTime(page,t,'16:00');await expect(page.getByRole('button',{name:'Отменить перенос'})).toBeVisible();let fresh=await api(request,'tasks/'+t.id);expect(fresh.scheduled_time).toBe('16:00');
 await api(request,'tasks/'+t.id,{expected_revision:fresh.revision,scheduled_time:'17:00'},'patch');await page.getByRole('button',{name:'Отменить перенос'}).click();await expect(page.locator('#planner-toast')).toContainText('Не удалось отменить перенос');expect((await api(request,'tasks/'+t.id)).scheduled_time).toBe('17:00');
 await calendar(page,p,'month');const next=add(day(),1);await event(page,t).dragTo(page.locator('.pc-month-day[data-date="'+next+'"]'),{targetPosition:{x:10,y:10}});await expect(page.getByRole('button',{name:'Отменить перенос'})).toBeVisible();fresh=await api(request,'tasks/'+t.id);expect(fresh.planned_date).toBe(next);expect(fresh.scheduled_time).toBe('17:00');expect(fresh.due_date).toBe(t.due_date);
 await page.getByRole('button',{name:'Отменить перенос'}).click();await expect.poll(async()=>(await api(request,'tasks/'+t.id)).planned_date).toBe(day());await expect(page.locator('.pl-dialog')).toHaveCount(0);
});

test('UX31 regular schedule and completion-chain drops move only one instance, including after refresh',async({page,request})=>{
 for(const mode of ['schedule','completion']){
  const p=await project(request),s=await api(request,'series',{request_id:randomUUID(),task:{title:'Повторение '+mode,project_id:p.id},first_date:day(),rule:{schema_version:1,mode,unit:'days',interval:7,allowed_days:{mode:'any'},adjustment:'none'}}),first=s.occurrences[0];
  const originals=new Map(s.occurrences.map(o=>[o.id,o.task]));await calendar(page,p,'month');await event(page,first.task).dragTo(page.locator('.pc-month-day[data-date="'+add(day(),1)+'"]'),{targetPosition:{x:10,y:10}});await expect(page.getByRole('button',{name:'Отменить перенос'})).toBeVisible();
  let current=await api(request,'series/'+s.id);for(const o of current.occurrences)if(originals.has(o.id)&&o.id!==first.id)expect(o.task).toEqual(originals.get(o.id));
  await page.getByRole('button',{name:'Отменить перенос'}).click();await expect.poll(async()=>(await api(request,'tasks/'+first.task.id)).planned_date).toBe(day());await expect(page.locator('.pl-dialog')).toHaveCount(0);
 }
});

test('UX31 session drop keeps duration, undo clears assigned time; project-instance task does not shift its project',async({page,request})=>{
 const p=await project(request),t=await task(request,p,'Работа с периодами',null),s=await api(request,'sessions',{task_id:t.id,date:day()});await calendar(page,p);await page.locator('.pc-time-scroll').evaluate(n=>n.scrollTop=1000);
 const card=page.locator('[data-event-id="session:'+s.id+'"]');await card.dragTo(page.locator('[data-time="15:00"]'),{targetPosition:{x:100,y:10}});await expect(page.getByRole('button',{name:'Отменить перенос'})).toBeVisible();let saved=await api(request,'sessions/'+s.id);expect(saved.start_time).toBe('15:00');expect(saved.end_time).toBe('15:30');expect(await api(request,'tasks/'+t.id)).toEqual(t);
 await page.getByRole('button',{name:'Отменить перенос'}).click();await expect.poll(async()=>(await api(request,'sessions/'+s.id)).start_time).toBeNull();
 const input={request_id:randomUUID(),anchor_date:day(),has_start:true,project_mode:'independent',rule:{schema_version:1,mode:'schedule',unit:'days',interval:7,allowed_days:{mode:'any'},adjustment:'none'},snapshot:{title:'Повторяющийся проект',tasks:[{key:'a',title:'Первый шаг',planned_offset:0},{key:'b',title:'Второй шаг',planned_offset:0}]}};
 const preview=await api(request,'project-drafts/preview',input);const made=await api(request,'project-drafts/apply',{...input,preview_token:preview.preview_token});const original=await api(request,'series/'+made.project.repeat_series_id);
 await calendar(page,made.project);await page.locator('.pc-time-scroll').evaluate(n=>n.scrollTop=1000);await atTime(page,made.project.tasks[0],'15:00');await expect(page.getByRole('button',{name:'Отменить перенос'})).toBeVisible();expect((await api(request,'projects/'+made.project.id)).start_date).toBe(made.project.start_date);expect((await api(request,'tasks/'+made.project.tasks[1].id)).scheduled_time).toBeNull();expect((await api(request,'series/'+original.id)).occurrences).toEqual(original.occurrences);
 await page.goto('/#/plans/projects/'+made.project.id);const row=page.locator('[data-task-id="'+made.project.tasks[0].id+'"]');await row.locator('.pl-check').click();await page.getByRole('button',{name:'Отменить завершение',exact:true}).click();await expect.poll(async()=>(await api(request,'tasks/'+made.project.tasks[0].id)).status).toBe('planned');await expect(page.locator('.pl-dialog')).toHaveCount(0);
});

test('UX31 one-click completion undo and card restore preserve IDs for ordinary and regular tasks',async({page,request})=>{
 for(const mode of ['ordinary','schedule','completion']){
  const p=await project(request);let t;
  if(mode==='ordinary')t=await task(request,p,'Обычная',null);else{const s=await api(request,'series',{request_id:randomUUID(),task:{title:'Восстановить '+mode,project_id:p.id},first_date:day(),rule:{schema_version:1,mode,unit:'days',interval:7,allowed_days:{mode:'any'},adjustment:'none'}});t=s.occurrences[0].task;}
  await page.goto('/#/plans/projects/'+p.id);await page.locator('[data-task-id="'+t.id+'"] .pl-check').click();await page.getByRole('button',{name:'Отменить завершение',exact:true}).click();await expect.poll(async()=>(await api(request,'tasks/'+t.id)).status).toBe('planned');await expect(page.locator('.pl-dialog')).toHaveCount(0);
  const all=(await api(request,'tasks')).items.filter(x=>x.project_id===p.id);expect(new Set(all.map(x=>x.id)).size).toBe(all.length);expect(all.filter(x=>x.id===t.id)).toHaveLength(1);
  if(mode!=='completion'){
   await page.locator('[data-task-id="'+t.id+'"] .pl-check').click();await expect.poll(async()=>(await api(request,'tasks/'+t.id)).status).toBe('completed');await page.goto('/#/plans/tasks?task='+t.id);
   await page.getByRole('button',{name:'Восстановить задачу',exact:true}).click();await expect.poll(async()=>(await api(request,'tasks/'+t.id)).status).toBe('planned');await expect(page.locator('.pl-dialog')).toHaveCount(0);
  }
 }
});

test('UX31 mixed list and group chronology reorders through SSE without collapsing groups',async({page,request})=>{
 const p=await project(request),q=await project(request),prefix=randomUUID();
 const late=await task(request,p,prefix+' А поздняя','16:00'),early=await task(request,p,prefix+' Я ранняя','11:00');const other=await task(request,q,prefix+' Другая','12:00');const solo=await api(request,'tasks',{title:prefix+' Самостоятельная',planned_date:day(),scheduled_time:'15:00'});const untimed=await task(request,p,prefix+' Без времени',null);
 await page.goto('/#/plans/tasks?q='+prefix);await plannerPreference(page,'Группировать по проектам',false);const rows=()=>page.locator('.pl-results [data-task-id]').evaluateAll(ns=>ns.map(n=>n.dataset.taskId));await expect.poll(rows).toEqual([untimed.id,early.id,other.id,solo.id,late.id]);
 await plannerPreference(page,'Группировать по проектам',true);const group=page.locator('.pl-project-task-group[data-project-id="'+p.id+'"]');await group.locator(':scope > summary').click();await expect(group.locator('[data-task-id]')).toHaveCount(3);
 const changed=await api(request,'tasks/'+early.id);await api(request,'tasks/'+early.id,{expected_revision:changed.revision,scheduled_time:'18:00'},'patch');await expect.poll(()=>group.locator('[data-task-id]').evaluateAll(ns=>ns.map(n=>n.dataset.taskId))).toEqual([untimed.id,late.id,early.id]);await expect(group).toHaveAttribute('open','');
 await api(request,'tasks/'+untimed.id+'/complete',{expected_revision:untimed.revision,completed_date:day()});await expect(group.locator('.pl-project-group-progress')).toContainText('1 / 3');await expect.poll(()=>page.locator('.pl-results .pl-project-task-group').evaluateAll(ns=>ns.map(n=>n.dataset.projectId))).toEqual([q.id,p.id]);await expect(group).toHaveAttribute('open','');
 const closed=await api(request,'tasks/'+untimed.id);await api(request,'tasks/'+untimed.id+'/restore',{expected_revision:closed.revision});
 await expect.poll(()=>page.locator('.pl-results .pl-project-task-group').evaluateAll(ns=>ns.map(n=>n.dataset.projectId))).toEqual([p.id,q.id]);await expect(group).toHaveAttribute('open','');
 const latest=await api(request,'tasks/'+late.id);await api(request,'tasks/'+late.id,{expected_revision:latest.revision,planned_date:add(day(),1)},'patch');
 await expect(page.locator('.pl-project-task-group[data-project-id="'+p.id+'"]')).toHaveCount(2);
});


test('UX31 completion-chain undo retains protective review after another instance changes',async({page,request})=>{
 const p=await project(request),s=await api(request,'series',{request_id:randomUUID(),task:{title:'Безопасная отмена',project_id:p.id},first_date:day(),rule:{schema_version:1,mode:'completion',unit:'days',interval:7,allowed_days:{mode:'any'},adjustment:'none'}}),t=s.occurrences[0].task;
 await page.goto('/#/plans/projects/'+p.id);await page.locator('[data-task-id="'+t.id+'"] .pl-check').click();await expect(page.getByRole('button',{name:'Отменить завершение',exact:true})).toBeVisible();
 const fresh=await api(request,'series/'+s.id),next=fresh.occurrences[1].task;await api(request,'tasks/'+next.id,{expected_revision:next.revision,title:'Изменено другим клиентом'},'patch');
 await page.getByRole('button',{name:'Отменить завершение',exact:true}).click();await expect(page.getByRole('button',{name:'Проверить последствия'})).toBeVisible();await expect(page.locator('.pl-dialog')).toHaveCount(0);
 expect((await api(request,'tasks/'+t.id)).status).toBe('completed');expect((await api(request,'tasks/'+next.id)).title).toBe('Изменено другим клиентом');
});
