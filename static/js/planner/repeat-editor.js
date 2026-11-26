(function(P){
'use strict';const {el,button,meta}=P;
const scopes=[['this','Только это выполнение'],['this_and_following','Это и все следующие'],['following','Только следующие']];
P.relativeDateLabel=function(offset,anchor){
 if(offset==null)return 'Без даты';const date=P.calendarModel.addDays(anchor,offset);const weekday=new Intl.DateTimeFormat('ru-RU',{weekday:'long',timeZone:'UTC'}).format(new Date(date+'T12:00:00Z'));
 return weekday+' · '+(offset>=7?'неделя '+(Math.floor(offset/7)+1)+' · ':'')+(offset===0?'день начала':'день '+(offset>0?'+':'')+offset)+' · '+P.date(date);
};
P.repeatEditor=async function(id,options={}){
 try{
  const existing=[...document.querySelectorAll('[data-repeat-editor]')].find(d=>d.dataset.repeatEditor===id);if(existing){existing.focus();return;}
  if(P.guardActive&&!await P.guardActive())return;
  const h=P.dialog('Настроить повторение');h.dialog.classList.add('pl-repeat-editor','pl-wide-dialog');h.dialog.dataset.repeatEditor=id;
  const title=el('h3'),tabs=el('div',{class:'pl-editor-tabs',role:'tablist','aria-label':'Настройки повторения'}),body=el('section',{class:'pl-repeat-body',role:'tabpanel'});h.dialog.append(title,tabs,body);body.closeEditor=h.close;
  let selected='',busy=false;const projectID=options.projectID;
  async function choose(tab){
   if(busy||selected===tab)return;if(body.plannerGuard&&!await body.plannerGuard())return;busy=true;tabs.querySelectorAll('button').forEach(b=>b.disabled=true);
   try{const s=await P.api('/series/'+id+'?limit=100');if(!h.dialog.isConnected)return;selected=tab;body.replaceChildren();body.plannerGuard=null;title.textContent=P.seriesTitle(s);tabs.querySelectorAll('button').forEach(b=>b.setAttribute('aria-selected',String(b.dataset.tab===tab)));
    const selectedID=projectID||s.source_project_id||(s.occurrences||[]).find(o=>o.project)?.project.id;const p=s.source_type==='project'&&selectedID?await P.api('/projects/'+selectedID):null;
    if(tab==='schedule'){
     if(['disabled','archived'].includes(s.state))body.append(meta('Повторение прекращено. Выполнения и история доступны в дополнительных настройках.'));
     else{P.seriesChange(s,'change_rule',options.extras||{},body);if(s.source_type==='project')body.prepend(meta('Даты проектов рассчитываются по расписанию. Режим «Один проект за раз» определяет текущую работу, а не дату отсчёта.'));if(s.end_date)body.prepend(meta('Дата окончания: '+P.date(s.end_date)));}
    }else if(tab==='tasks')await P.projectComposition(p||{repeat_series_id:s.id,title:P.seriesTitle(s)},false,body);
    else if(tab==='mode'){if(p)await P.projectMode(p,body);else body.append(meta('Нет сохранённых выполнений для выбора текущего. Расписание, история и отключение доступны в этом редакторе.'));}
    else{
     const leave=fn=>()=>{h.close(true);fn();};const advanced=el('details',{open:true},el('summary',{},'Дополнительные настройки'));body.append(advanced);
     if(!['disabled','archived'].includes(s.state)){
      advanced.append(button('Приостановить повторение',leave(()=>P.seriesChange(s,'pause'))));
      for(const pause of s.pauses||[])if(!pause.resumed_on)advanced.append(button('Возобновить с '+P.date(pause.start_date),leave(()=>P.seriesChange(s,'resume',{pause_id:pause.id}))));
      if(s.state==='needs_anchor')advanced.append(button('Выбрать дату продолжения',leave(()=>P.seriesChange(s,'reanchor'))));
      advanced.append(button('Оформление повторения',leave(()=>P.editSeriesAppearance(s.id))));
      if(p)advanced.append(button('Проверить и исправить будущие выполнения',leave(()=>P.projectDateRepair(p))));
     }
     const occurrences=el('details',{},el('summary',{},'Отдельные выполнения'));advanced.append(occurrences);let offset=0;
     const list=el('div'),more=button('Показать ещё',async()=>{const next=await P.api('/series/'+id+'?limit=100&offset='+offset);append(next);});occurrences.append(list,more);
     function append(data){for(const o of data.occurrences||[]){const t=o.project||o.task,row=el('article',{class:'pl-occurrence'},meta(P.date(o.manual_date||o.adjusted_date)+' · '+(P.labels[t?.status||o.project_status]||'')));if(t)row.append(button('Открыть выполнение',()=>{h.close(true);if(o.project)P.navigate('#/plans/projects/'+t.id);else P.openTask(t.id);}));const actions=el('details',{},el('summary',{},'Действия'));for(const action of P.occurrenceActions(t?.status||o.project_status))actions.append(button(P.occurrenceLabels[action],leave(()=>P.occurrenceAction(s,o,action))));actions.append(P.lifecycleMenuButton('occurrences',o));row.append(actions);list.append(row);}offset+=(data.occurrences||[]).length;more.hidden=!data.has_more;}append(s);
     const history=P.disclosure('История');advanced.append(history);P.renderHistory(history,'series',id);
     const danger=el('div',{class:'pl-repeat-danger'});body.append(danger);
     if(!['disabled'].includes(s.state))danger.append(button('Отключить повторение',leave(()=>P.disableRepeat(id,p?.id)),'pl-danger'));
     danger.append(button('Удалить всё повторение навсегда',leave(()=>P.deleteForever('series',s)),'pl-danger'));
    }
    h.dialog.plannerGuard=async()=>!body.plannerGuard||await body.plannerGuard();
   }catch(e){body.append(el('p',{role:'alert'},P.errorMessage(e)));}finally{busy=false;tabs.querySelectorAll('button').forEach(b=>b.disabled=false);}
  }
  const series=await P.api('/series/'+id+'?limit=1');for(const [tab,label]of [['schedule','Расписание'],...(series.source_type==='project'?[['tasks','Задачи проекта'],['mode','Режим выполнения']]:[]),['advanced','Дополнительно']]){const b=button(label,()=>choose(tab));b.dataset.tab=tab;b.setAttribute('role','tab');tabs.append(b);}
  await choose(options.tab||'schedule');
 }catch(e){P.notify(P.errorMessage(e));}
};
P.projectScopeField=function(form,project,adding=false){return project?.repeat_series_id?P.field(form,'change_scope',adding?'Добавить задачу в':'Применить изменение','select',adding?'this_and_following':'this',adding?scopes.filter(([s])=>s!=='following'):scopes):null;};
P.scopedProjectSave=async function(project,input){
 const latest=await P.api('/projects/'+project.id),series=await P.api('/series/'+latest.repeat_series_id+'?limit=1');
 // The selected record revisions come from the editing form, never silently refreshed.
 let request={...input,expected_revision:project.revision,expected_series_revision:series.revision};
 const h=P.dialog(input.action==='remove'?'Удалить задачи из выполнений':'Изменение следующих выполнений');h.dialog.classList.add('pl-wide-dialog');
 const area=el('section',{'aria-live':'polite'});h.dialog.append(area);let preview,send=P.commandSender('/projects/'+project.id+'/task-change-apply');
 return new Promise((resolve,reject)=>{
  let settled=false,previewError;h.dialog.addEventListener('close',()=>{if(!settled){settled=true;reject(previewError||Error('Изменения не применены. Черновик сохранён.'));}});
  const review=button('Пересчитать предпросмотр',async()=>{
   if(area.querySelector('[data-busy=true],[data-uncertain=true]')){P.notify('Дождитесь ответа или повторите подтверждение той же операции.');return;}previewError=null;preview=null;area.replaceChildren(meta('Проверяем изменения…'));review.disabled=true;
   try{const currentProject=await P.api('/projects/'+project.id);request.expected_revision=currentProject.revision;const currentSeries=await P.api('/series/'+latest.repeat_series_id+'?limit=1');request.expected_series_revision=currentSeries.revision;preview=await P.api('/projects/'+project.id+'/task-change-preview','POST',request);area.replaceChildren();
    if(preview.before&&preview.after){const from=preview.before,to=preview.after;area.append(el('h3',{},to.title));for(const key of ['title','description','priority','color_id','icon_id','planned_date','due_date','scheduled_time'])if(from[key]!==to[key])area.append(el('p',{},({title:'Название',description:'Описание',priority:'Приоритет',color_id:'Цвет',icon_id:'Иконка',planned_date:'Плановая дата',due_date:'Крайний срок',scheduled_time:'Время'})[key]+': '+(['planned_date','due_date'].includes(key)?(from[key]?new Intl.DateTimeFormat('ru-RU',{weekday:'long',timeZone:'UTC'}).format(new Date(from[key]+'T12:00:00Z'))+' '+P.date(from[key]):'Без даты'):(from[key]||'—'))+' → '+(['planned_date','due_date'].includes(key)?(to[key]?new Intl.DateTimeFormat('ru-RU',{weekday:'long',timeZone:'UTC'}).format(new Date(to[key]+'T12:00:00Z'))+' '+P.date(to[key]):'Без даты'):(to[key]||'—'))));}
    const changed=preview.future.rows.filter(r=>r.after),protectedRows=preview.future.rows.filter(r=>!r.after);area.append(meta((request.scope==='following'?'Выбранное выполнение сохранится. ':'Выбранное выполнение изменится. ')+'Следующих обновится: '+changed.length+' · защищено: '+protectedRows.length+'. Новые выполнения используют обновлённый состав.'));
    for(const row of preview.future.rows.slice(0,3)){const block=el('details',{open:true},el('summary',{},P.projectDateLabel(row.before)+' · '+row.reason));if(row.after)block.append(el('ul',{},row.after.tasks.filter(t=>!['edit','add','remove'].includes(request.action)||t.title===preview.after?.title||t.title===preview.before?.title).map(t=>el('li',{},t.title+' · '+P.date(t.planned_date)+(t.due_date?' · срок '+P.date(t.due_date):'')+(t.scheduled_time?' · '+t.scheduled_time:'')))));area.append(block);}
    if(preview.future.rows.length>3){const rest=el('details',{},el('summary',{},'Остальные выполнения · '+(preview.future.rows.length-3)));for(const row of preview.future.rows.slice(3))rest.append(meta(P.projectDateLabel(row.before)+' · '+row.reason));area.append(rest);}
    if(request.action==='remove'){const count=preview.deletions.reduce((n,d)=>n+(d.counts.tasks||0),0);area.append(el('p',{class:'pl-warning'},'Будут физически удалены '+count+' задач и подзадач с их историей. В новых выполнениях этих задач не будет. Восстановление через интерфейс невозможно.'));}
    const confirm=el('form',{'data-submit':request.action==='remove'?'Удалить навсегда':'Применить изменения'});P.bindForm(confirm,()=>send({...request,preview_token:preview.preview_token,confirm:request.action==='remove'}),result=>{settled=true;result.scope_notice='Изменения сохранены: следующих выполнений обновлено '+changed.length+', защищено '+protectedRows.length;h.close(true);resolve(result);});area.append(confirm);P.guardForm(confirm,'scope-preview:'+project.id,null,h.dialog,{previewOnly:true});
   }catch(e){previewError=e;area.replaceChildren(el('p',{role:'alert'},P.errorMessage(e)),button('Вернуться к редактированию',()=>h.close(true)));}finally{review.disabled=false;}
  });h.dialog.append(review);review.click();
 });
};
// Infrequent structural operations choose the scope here; ordinary edits carry
// the same selector directly in their existing form.
P.chooseProjectScope=function(project,run){const h=P.dialog('Область изменения');h.dialog.append(el('p',{},project.title),...scopes.map(([scope,label])=>button(label,()=>{h.close(true);run(scope);})));};
const remove=P.deleteForever;P.deleteForever=async function(kind,item){
 if(kind!=='tasks'||!item.project_id||item.series_id)return remove(kind,item);
 const project=await P.api('/projects/'+item.project_id);if(!project.repeat_series_id)return remove(kind,item);
 P.chooseProjectScope(project,async scope=>{if(scope==='this')return remove(kind,item);try{const result=await P.scopedProjectSave(project,{scope,action:'remove',task_id:item.id,expected_task_revision:item.revision});P.closeEditor();await P.refresh();P.notify(result.scope_notice);}catch(e){P.notify(e.message);}});
};
P.regularScopedMove=async function(series,occurrence,values){
 if((values.scheduled_time||null)!==(occurrence.task.scheduled_time||null))throw Error('Для изменения времени выберите только это выполнение. Распространение дат повторения сохраняет время задач.');
 let first=values.planned_date,cycle=occurrence.cycle_index;
 if(values.change_scope==='following'){const p=await P.api('/recurrence/preview','POST',{rule:series.rule.rule,anchor:{date:first,cycle_index:0,kind:'schedule'},start_cycle:1,count:1});if(!p.items?.length)throw Error('Правило не допускает следующую дату');first=p.items[0].base_date;cycle++;}
 const input={expected_revision:series.revision,action:'change_rule',rule:series.rule.rule,from_cycle:cycle,first_date:first};
 const h=P.dialog('Изменение следующих выполнений'),area=el('section');h.dialog.append(area);const send=P.commandSender('/series/'+series.id+'/change-rule');
 return new Promise((resolve,reject)=>{let done=false;h.dialog.addEventListener('close',()=>{if(!done)reject(Error('Изменения не применены. Черновик сохранён.'));});const review=button('Пересчитать предпросмотр',async()=>{if(area.querySelector('[data-busy=true],[data-uncertain=true]')){P.notify('Дождитесь ответа или повторите подтверждение той же операции.');return;}area.replaceChildren();review.disabled=true;try{const latest=await P.api('/series/'+series.id+'?limit=1');input.expected_revision=latest.revision;const preview=await P.api('/series/'+series.id+'/preview','POST',input);for(const row of preview.rows)area.append(el('p',{},P.date(row.before_date)+' → '+P.date(row.after_date)+' · '+(row.reason||(['manual_exception','protected_closed','protected_started','protected_user_changes'].includes(row.disposition)?'Индивидуальные изменения или история сохранены':'Будет обновлено'))));const problems=[...(preview.conflicts||[]),...(preview.calendar_errors||[]).map(e=>e.message)];for(const problem of problems)area.append(el('p',{role:'alert'},problem));if(problems.length)return;const form=el('form',{'data-submit':'Применить изменения'});P.bindForm(form,()=>send({...input,preview_token:preview.preview_token}),result=>{done=true;result.scope_notice='Расписание обновлено. Защищённые и ручные выполнения сохранены.';h.close(true);resolve(result);});area.append(form);P.guardForm(form,'regular-date-scope:'+series.id,null,h.dialog,{previewOnly:true});}catch(e){area.append(el('p',{role:'alert'},P.errorMessage(e)));}finally{review.disabled=false;}});h.dialog.append(review);review.click();});
};
})(window.Planner);
