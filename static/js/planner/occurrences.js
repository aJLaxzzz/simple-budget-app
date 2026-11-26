(function(P){
 'use strict';const {el,button,meta}=P;
 P.occurrenceLabels={complete:'Выполнить',reschedule:'Перенести',skip:'Пропустить',cancel:'Отменить',restore:'Восстановить',review:'Рассмотрено'};
 P.occurrenceActions=status=>['planned','in_progress'].includes(status)?['complete','reschedule','skip','cancel']:status==='skipped'?['reschedule','restore','complete','cancel','review']:['completed','cancelled'].includes(status)?['restore']:[];
 P.openOccurrence=async function(id,action,options={}){try{const data=await P.api('/occurrences/'+id);await P.occurrenceAction(data.series,data.occurrence,action,options);}catch(e){P.notify(e.message);}};
 P.occurrenceAction=async function(series,occurrence,action,options={}){
   const {dialog,close}=P.dialog(P.occurrenceLabels[action]+' выполнение');const form=el('form',{'aria-label':'Действие с выполнением','data-submit':{complete:'Завершить выполнение',reschedule:'Перенести на выбранную дату',skip:'Пропустить выполнение',cancel:'Отменить выполнение',restore:'Восстановить выполнение',review:'Отметить рассмотренным'}[action]});dialog.append(form);
   let current=await P.api('/occurrences/'+occurrence.id).catch(e=>{form.append(el('p',{role:'alert'},e.message));});if(!current||!dialog.isConnected)return;
   series=current.series;occurrence=current.occurrence;
   const project=occurrence.source_type==='project';let children=[],sessions=[];
   try{
     if(project&&occurrence.project){children=occurrence.project.tasks||[];const all=await P.api('/sessions');const ids=new Set(children.map(t=>t.id));sessions=(all.items||[]).filter(s=>ids.has(s.task_id)&&P.active(s));}
     else if(!project){const context=await P.api('/tasks/'+occurrence.task.id+'/context');children=context.children;sessions=context.sessions;}
   }catch(e){form.append(el('p',{role:'alert'},e.message));return;}
   form.append(el('h3',{},occurrence.project?.title||occurrence.task?.title||P.seriesTitle(series)),meta('Выполнение на '+P.date(occurrence.manual_date||occurrence.adjusted_date)));
   if(action==='complete'){const date=P.field(form,'completed_date','Фактическая дата выполнения','date',P.today());date.required=true;date.max=P.today();}
   if(action==='reschedule'){
     const date=P.field(form,'planned_date','Новая дата','date',options.targetDate||occurrence.manual_date||occurrence.adjusted_date);date.required=true;P.datePresets(form,date);
     if(!project){P.field(form,'scheduled_time','Время · Москва','time',options.targetTime===undefined?occurrence.task.scheduled_time:options.targetTime);form.append(meta(series.anchor_mode==='completion'?'Крайний срок сохраняется. Даты зависимых выполнений будут пересчитаны; ручные исключения требуют решения.':'Переносится только это выполнение. Крайний срок и расписание следующих повторений сохранятся.'),button('Настроить повторение',()=>{close(true);P.repeatEditor(series.id);}));}
   }
   if(['skip','cancel'].includes(action))P.field(form,'reason','Причина (необязательно)','text','');
   if(action==='review')form.append(meta('Пропуск останется пропуском и сохранится в истории. Активное напоминание будет снято.'));
   if(project){
     if(!occurrence.compact&&['skip','cancel','complete','restore','reschedule'].includes(action)){
       const restoring=!P.active(occurrence.project)&&['restore','reschedule'].includes(action);
       const decision=restoring?'restore_closed':{skip:'skip_open',cancel:'cancel_open',complete:'complete_open'}[action];
       if(decision){const text=restoring?'Восстановить закрытые этим действием задачи':action==='skip'?'Пропустить открытые задачи':action==='cancel'?'Отменить открытые задачи':'Завершить открытые задачи';P.field(form,'tasks_decision','Задачи проекта','select','',[['','Выберите действие'],[decision,text]]).required=true;}
       if(children.length)form.append(el('details',{},el('summary',{},'Затрагиваемое дерево задач'),el('ul',{},children.map(t=>el('li',{},t.title+' · '+P.labels[t.status])))));
     }
     if(action==='reschedule')P.field(form,'tasks_date_decision','Даты дочерних задач','select','',[['','Выберите действие'],['keep','Сохранить даты задач'],['shift','Перенести вместе с проектом']]).required=true;
   }else if(action==='complete'&&children.some(P.active))P.field(form,'children_decision','Открытые подзадачи','select','',[['','Выберите действие'],['complete','Завершить вместе с подзадачами'],['keep_open','Оставить подзадачи открытыми']]).required=true;
   if(!project && series.anchor_mode==='completion' && !P.active(occurrence.task) && ['complete','restore'].includes(action)) {
     P.field(form,'branch_decision','Если повторение уже продолжено','select','',[['','Проверить возможность продолжения'],...(action==='complete'?[['historical_only','Записать выполнение только в историю, без новых повторений']]:[['replace_anchor','Вернуться к этому выполнению, если после новой даты ещё нет выполнений']])]);
     form.append(meta('Существующие следующие выполнения не удаляются. Если уже есть другая активная ветка, сервер остановит восстановление.'));
   }
   if(!project&&series.anchor_mode==='completion'&&['complete','reschedule','restore'].includes(action)){
     P.field(form,'dynamic_decision','Ручные даты будущих выполнений','select','',[['','Остановиться при конфликте'],['keep_manual','Сохранить ручные даты и рассчитать остальные от них']]);
     if(occurrence.task.dynamic&&P.active(occurrence.task)&&action==='complete'){P.field(form,'branch_decision','Завершение вне очереди','select','',[['','Выберите решение'],['historical_only','Завершить только это выполнение, текущую опору не менять']]).required=true;}
   }
   const changeScope=!project&&action==='reschedule'&&series.anchor_mode==='schedule'?P.field(form,'change_scope','Применить изменение','select','this',[['this','Только это выполнение'],['this_and_following','Это и все следующие'],['following','Только следующие']]):null;
   const closing=['complete','cancel','skip'].includes(action);
   if(sessions.length&&closing){P.field(form,'sessions_decision','Активные периоды работы: '+sessions.length,'select','',[['','Выберите действие'],['cancel','Подтверждаю отмену затронутых периодов работы']]).required=true;form.append(meta('Периоды работы останутся в истории; при восстановлении не возобновляются автоматически.'));}
   const send=P.commandSender('/occurrences/'+occurrence.id+'/'+action);
   P.bindForm(form,async()=>{
     const values=P.values(form);if(project&&action==='reschedule'){const p=occurrence.project;const end=p?.due_date&&p.start_date?P.calendarModel.addDays(values.planned_date,Math.round((Date.parse(p.due_date+'T12:00:00Z')-Date.parse(p.start_date+'T12:00:00Z'))/86400000)):null;await P.confirmProjectOverlap(p,values.planned_date,end);}if(changeScope&&values.change_scope!=='this')return P.regularScopedMove(series,occurrence,values);delete values.change_scope;if('scheduled_time' in values)values.scheduled_time=values.scheduled_time||null;const affectedIDs=new Set(project?children.filter(P.active).map(t=>t.id):[occurrence.task.id,...(values.children_decision==='complete'?children.filter(P.active).map(t=>t.id):[])]);
     return send({...values,expected_revision:occurrence.revision,expected_series_revision:series.revision,...(project?{expected_project_revision:occurrence.project?.revision||0}:{expected_task_revision:occurrence.task.revision}),related_revisions:P.revisions(children),...(values.sessions_decision?{session_revisions:P.revisions(sessions.filter(s=>affectedIDs.has(s.task_id)))}:{})});
   },async result=>{close(true);P.notify(result.scope_notice||('«'+(occurrence.project?.title||occurrence.task?.title||P.seriesTitle(series))+'»: '+({complete:'выполнено',reschedule:'перенесено на '+P.date(form.elements.namedItem('planned_date')?.value),skip:'пропущено',cancel:'отменено',restore:'восстановлено',review:'отмечено рассмотренным'}[action])));await P.refresh();},async()=>{
     const latest=await P.api('/occurrences/'+occurrence.id);
     return {record:latest.occurrence.project||latest.occurrence.task||latest.series,accept(){const draft=P.values(form);close(true);P.occurrenceAction(latest.series,latest.occurrence,action).then(()=>{const next=document.querySelector('form[aria-label="Действие с выполнением"]');if(next)for(const [key,value] of Object.entries(draft)){const field=next.elements.namedItem(key);if(field)field.value=value;}});}};
   });
   if(options.immediate && !project && !changeScope && P.active(occurrence.task))form.requestSubmit();
 };
 const taskAction=P.taskAction;P.taskAction=(task,action)=>task.occurrence_id?P.openOccurrence(task.occurrence_id,action):taskAction(task,action);
 const projectAction=P.projectAction;P.projectAction=(project,action)=>project.occurrence_id&&action!=='archive'?P.openOccurrence(project.occurrence_id,action):projectAction(project,action);
})(window.Planner);
