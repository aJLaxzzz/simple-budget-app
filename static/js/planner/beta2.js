(function(P){
 'use strict';const {el,button,meta}=P;
 const completing=new Set(),pending=new Map();
 P.quickComplete=async function(item){
  const task=P.actionObject(item);if(completing.has(task.id))return;completing.add(task.id);
  try{
   const context=await P.api('/tasks/'+task.id+'/context'),t=context.task;
   if(!P.active(t)&&!pending.has(t.id)){await P.refresh();return;}
   if(context.sessions.some(P.active)||t.dynamic){await P.taskAction(t,'complete');return;}
   let data,path;
   if(t.occurrence_id){const c=await P.api('/occurrences/'+t.occurrence_id);path='/occurrences/'+t.occurrence_id+'/complete';data={expected_revision:c.occurrence.revision,expected_task_revision:t.revision,expected_series_revision:c.series.revision,completed_date:P.today()};}
   else{path='/tasks/'+t.id+'/complete';data={expected_revision:t.revision,completed_date:P.today()};}
   if(context.children.some(P.active)){data.children_decision='keep_open';data.related_revisions=P.revisions(context.children);}
   // Retain the exact request across an uncertain network response.
   let command=pending.get(t.id);if(!command){command={send:P.commandSender(path),data:{...data,request_id:crypto.randomUUID()}};pending.set(t.id,command);}
   let result;
   try{result=await command.send(command.data);}catch(e){if(e.status===409||e.status===422){pending.delete(t.id);P.notify('Не удалось завершить задачу: '+P.errorMessage(e));return;}throw e;}
   pending.delete(t.id);await P.refresh();
   const completionID=command.data.request_id;
   const saved=result.task||result.occurrence?.task||result;
   P.notify('Задача «'+t.title+'» выполнена',{label:'Отменить завершение',run:()=>P.quickRestore(saved,{undo:result.undo,requestID:completionID})});
  }catch(e){P.notify('Не удалось завершить «'+task.title+'»: '+e.message,pending.has(task.id)?{label:'Повторить завершение',run:()=>P.quickComplete(item)}:undefined);if(pending.has(task.id))clearTimeout(P.toastTimer);}finally{completing.delete(task.id);}
 };
 P.disableRepeat=async function(seriesID,keepID){
  try{
   let series=await P.api('/series/'+seriesID);const {dialog,close}=P.dialog('Отключить повторение');
   const form=el('form',{'aria-label':'Отключение повторения','data-submit':'Посмотреть последствия'});
   form.append(el('p',{},P.seriesTitle(series)),meta('Выбранный исходный объект сохранится с прежним ID, датами, оформлением и историей. Новые выполнения больше не появятся.'));
   let keep;
   if(!keepID&&series.source_type==='task')keep=P.field(form,'keep_id','Оставить обычной задачей','select','',series.occurrences.filter(o=>o.task?.id).map(o=>[o.task.id,o.task.title+' · '+P.date(o.task.planned_date)]));
   const future=P.field(form,'future','Будущие выполнения','select','delete',[['delete','Удалить ненужные, не начатые выполнения'],['keep','Оставить самостоятельными задачами / проектами']]);
   const preview=el('section');let input;const send=P.commandSender('/series/'+seriesID+'/disable');
   const invalidate=()=>{input=null;preview.replaceChildren();};form.addEventListener('input',invalidate);
   P.bindForm(form,()=>P.api('/series/'+seriesID+'/disable-preview','POST',{expected_revision:series.revision,keep_id:keepID||keep?.value,future:future.value}),p=>{
    input={expected_revision:series.revision,keep_id:keepID||keep?.value,future:future.value,preview_token:p.preview_token,confirm:true};
    const names={keep_source:'Исходный объект сохраняется',detach:'Останется самостоятельным',delete:'Будет удалено навсегда',history:'История сохраняется'};
    preview.replaceChildren(el('h3',{},'Последствия отключения'),meta('Удалится: '+p.rows.filter(r=>r.decision==='delete').length+' · Сохранится: '+p.rows.filter(r=>r.decision!=='delete').length),el('ul',{},p.rows.map(r=>el('li',{},r.title+(r.date?' · '+P.date(r.date):'')+' — '+names[r.decision]+(r.reason?'. '+r.reason:'')))));
    const apply=el('form',{'aria-label':'Подтвердить отключение','data-submit':'Отключить повторение'});apply.append(meta('Удаление необратимо. Начатые и изменённые выполнения автоматически не удаляются.'));
    P.bindForm(apply,()=>{if(!input)throw Error('Повторите предпросмотр');return send(input);},async()=>{close(true);await P.refresh();P.notify('Повторение «'+P.seriesTitle(series)+'» отключено');},async()=>({record:await P.api('/series/'+seriesID),async accept(){series=await P.api('/series/'+seriesID);invalidate();}}));preview.append(apply);P.showPreview(preview);
   },async()=>({record:await P.api('/series/'+seriesID),async accept(){series=await P.api('/series/'+seriesID);invalidate();}}));dialog.append(form,preview);
  }catch(e){P.notify(e.message);}
 };
 P.batchRepeat=async function(project){
  const {dialog,close}=P.dialog('Настроить повторение задач');dialog.classList.add('pl-management-drawer');
  const form=el('form',{'aria-label':'Массовое повторение','data-submit':'Посмотреть даты'}),selected=new Set();
  const list=el('div',{class:'pl-existing-list'});for(const t of project.tasks.filter(P.active)){const box=el('input',{type:'checkbox','aria-label':'Выбрать: '+t.title,disabled:Boolean(t.series_id)||!t.planned_date});box.addEventListener('change',()=>box.checked?selected.add(t.id):selected.delete(t.id));list.append(el('label',{class:'pl-existing-choice'},box,el('span',{},t.title,meta(t.series_id?'Повторение уже настроено':t.planned_date?P.date(t.planned_date):'Сначала задайте плановую дату'))));}form.append(list);
  form.append(meta('У каждой задачи останется собственная первая дата и отдельное повторение. Исходные задачи не дублируются.'));
  const builder=P.ruleBuilder(form,{rule:{schema_version:1,mode:'schedule',unit:'years',interval:1,allowed_days:{mode:'any'},adjustment:'none'},firstDate:P.today(),preview:false});
  // The shared rule builder's date is not applied: each source retains its date.
  if(builder.first)builder.first.closest('.pl-field')?.setAttribute('hidden','');
  let input;const preview=el('section'),send=P.commandSender('/batch-recurrence/apply');const invalidate=()=>{input=null;preview.replaceChildren();};form.addEventListener('input',invalidate);form.addEventListener('change',invalidate);
  P.bindForm(form,()=>{const tasks=project.tasks.filter(t=>selected.has(t.id));input={task_ids:tasks.map(t=>t.id),expected_revisions:P.revisions(tasks),rule:builder.read()};return P.api('/batch-recurrence/preview','POST',input);},p=>{
   input={...input,preview_token:p.preview_token};preview.replaceChildren(el('h3',{},'Отдельные повторения: '+p.rows.length),el('ul',{},p.rows.map(r=>el('li',{},r.task.title+' · '+r.dates.map(d=>P.date(d.adjusted_date)).join(' → ')))));
   const apply=el('form',{'data-submit':'Настроить выбранные повторения'});P.bindForm(apply,()=>{if(!input)throw Error('Повторите предпросмотр');return send(input);},async()=>{close(true);await P.refresh();P.notify('Независимые повторения настроены');},async()=>({record:await P.api('/projects/'+project.id),async accept(){project=await P.api('/projects/'+project.id);invalidate();}}));preview.append(apply);P.showPreview(preview);
  },async()=>({record:await P.api('/projects/'+project.id),async accept(){project=await P.api('/projects/'+project.id);invalidate();}}));dialog.append(form,preview);
 };
})(window.Planner);
