const {test,expect}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto');
const rule={schema_version:1,mode:'schedule',unit:'weeks',interval:1,allowed_days:{mode:'any'},adjustment:'none'};
async function api(request,path,data){const r=await request[data?'post':'get']('/api/planner/'+path,data?{data}:{});expect(r.ok(),await r.text()).toBeTruthy();return r.json();}
async function drag(page,task,project,date){
 await page.goto('/#/plans/calendar?mode=month&date=2026-12-02&project='+project);
 const event=page.locator('[data-event-id="task:'+task.id+'"]');
 await expect(event).toHaveAttribute('data-event-date','2026-12-02');
 await event.dragTo(page.locator('.pc-month-day[data-date="'+date+'"]'));
 return event;
}
test('calendar mouse move of ordinary task commits without recurrence dialog',async({page,request})=>{
 const p=await api(request,'projects',{title:'Обычный перенос '+randomUUID()});
 const t=await api(request,'tasks',{title:'Перенести мышью',project_id:p.id,planned_date:'2026-12-02',due_date:'2026-12-10'});
 const event=await drag(page,t,p.id,'2026-12-03');await expect(event).toHaveAttribute('data-event-date','2026-12-03');
 await expect(page.getByRole('dialog')).toHaveCount(0);const actual=await api(request,'tasks/'+t.id);expect(actual.planned_date).toBe('2026-12-03');expect(actual.due_date).toBe(t.due_date);
});
for(const scope of ['this_and_following','following'])test('calendar mouse move of independent recurrence confirms scope '+scope,async({page,request})=>{
 const p=await api(request,'projects',{title:'Регулярный перенос '+randomUUID()});
 const s=await api(request,'series',{request_id:randomUUID(),first_date:'2026-12-02',task:{title:'Регулярное дело',project_id:p.id},rule});const t=s.occurrences[0].task;
 await drag(page,t,p.id,'2026-12-03');const before=await api(request,'series/'+s.id+'?limit=100');const form=page.getByRole('form',{name:'Действие с выполнением',exact:true});
 await form.getByLabel('Применить изменение').selectOption(scope);await form.getByRole('button',{name:'Перенести на выбранную дату'}).click();const preview=page.getByRole('dialog',{name:'Изменение следующих выполнений'});
 await expect(preview.getByRole('button',{name:'Применить изменения'})).toBeVisible();expect((await api(request,'tasks/'+t.id)).planned_date).toBe('2026-12-02');await preview.getByRole('button',{name:'Применить изменения'}).click();await expect(preview).toHaveCount(0);
 const after=await api(request,'series/'+s.id+'?limit=100');expect(after.occurrences.map(o=>o.id)).toEqual(before.occurrences.map(o=>o.id));
 for(let i=0;i<before.occurrences.length;i++){const old=before.occurrences[i].task,current=after.occurrences[i].task,date=new Date(old.planned_date+'T12:00:00Z');if(i>0||scope==='this_and_following')date.setUTCDate(date.getUTCDate()+1);expect(current.id).toBe(old.id);expect(current.planned_date).toBe(date.toISOString().slice(0,10));}
});
for(const scope of ['this','this_and_following','following'])test('calendar mouse move of project child preserves scope '+scope,async({page,request})=>{
 const input={anchor_date:'2026-12-01',has_start:true,project_mode:'sequential',rule,snapshot:{title:'Область переноса '+randomUUID(),tasks:[{key:'floor',title:'Помыть пол',priority:'normal',planned_offset:1,due_offset:4}]}};
 const preview=await api(request,'project-drafts/preview',input);
 const p=(await api(request,'project-drafts/apply',{...input,preview_token:preview.preview_token,request_id:randomUUID()})).project,t=p.tasks[0];
 await drag(page,t,p.id,'2026-12-03');const before=await api(request,'series/'+p.repeat_series_id+'?limit=100');const form=page.getByRole('form',{name:'Перенос задачи',exact:true});
 await expect(form.getByLabel('Новая дата')).toHaveValue('2026-12-03');
 expect((await api(request,'tasks/'+t.id)).planned_date).toBe('2026-12-02');
 await form.getByLabel('Применить изменение').selectOption(scope);await form.locator('button[type=submit]').click();
 if(scope!=='this'){const dialog=page.getByRole('dialog',{name:'Изменение следующих выполнений'});await expect(dialog).toContainText('среда 02.12.2026 → четверг 03.12.2026');expect((await api(request,'tasks/'+t.id)).planned_date).toBe('2026-12-02');await dialog.getByRole('button',{name:'Применить изменения',exact:true}).click();}
 await expect(form).toHaveCount(0);
 const actual=await api(request,'tasks/'+t.id);expect(actual.planned_date).toBe(scope==='following'?'2026-12-02':'2026-12-03');expect(actual.due_date).toBe(t.due_date);
 const after=await api(request,'series/'+p.repeat_series_id+'?limit=100');expect(after.occurrences.map(o=>o.id)).toEqual(before.occurrences.map(o=>o.id));
 expect(after.project_template.tasks[0].planned_offset).toBe(scope==='this'?1:2);
 for(let i=0;i<after.occurrences.length;i++){const old=before.occurrences[i].project.tasks[0],current=after.occurrences[i].project.tasks[0];expect(current.id).toBe(old.id);expect(current.due_date).toBe(old.due_date);const date=new Date(old.planned_date+'T12:00:00Z');if(scope!=='this')date.setUTCDate(date.getUTCDate()+1);expect(current.planned_date).toBe(date.toISOString().slice(0,10));}
});
test('completion recurrence preview sends no invented anchor before completion',async({page,request})=>{
 const s=await api(request,'series',{request_id:randomUUID(),task:{title:'Предпросмотр опоры '+randomUUID()},first_date:'2026-12-02',rule:{...rule,mode:'completion'}});
 const response=page.waitForResponse(r=>r.url().endsWith('/recurrence/preview')&&r.request().method()==='POST');
 await page.goto('/#/plans/series/'+s.id);const r=await response;expect(r.status()).toBe(200);expect(r.request().postDataJSON().anchor).toEqual({cycle_index:0,kind:'unconfirmed'});
 await expect(page.locator('.pl-rule-builder .pl-recurrence-preview')).toContainText('Следующий цикл требует фактического завершения');await expect(page.locator('.pl-recurrence-preview [role=alert]')).toHaveCount(0);
 const first=s.occurrences[0];await api(request,'occurrences/'+first.id+'/complete',{request_id:randomUUID(),expected_revision:first.revision,expected_task_revision:first.task.revision,expected_series_revision:s.revision,completed_date:'2026-11-23'});
 const nextResponse=page.waitForResponse(r=>r.url().endsWith('/recurrence/preview')&&r.request().method()==='POST');await page.reload();const next=await nextResponse;expect(next.status()).toBe(200);expect(next.request().postDataJSON().anchor.date).toBe('2026-11-23');const result=await next.json();expect(result.items).toHaveLength(1);expect(result.items[0].adjusted_date).toBe('2026-11-30');await expect(page.locator('.pl-rule-builder .pl-preview-row')).toHaveCount(1);await expect(page.locator('.pl-rule-builder .pl-preview-row')).toContainText('30.11.2026 → 30.11.2026');expect((await api(request,'series/'+s.id)).occurrences.filter(o=>o.task.status==='planned')).toHaveLength(3);
});
