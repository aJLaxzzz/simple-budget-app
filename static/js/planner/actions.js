(function(P){
  'use strict';
  const {el,button,meta}=P, C=P.calendarModel;
  // Calendar projections and records share one command adapter. A session is
  // deliberately checked before occurrence_id: it belongs to work, not a cycle.
  P.actionObject=item=>item.type==='session'?item:item.type&&item.task_id?{...item,id:item.task_id,revision:item.task_revision,status:item.task_status,type:'task',date:item.type==='deadline'?null:item.date}:{...item,type:'task',date:item.date??item.planned_date,start_time:item.start_time??item.scheduled_time};
  P.actionNames=item=>{
    const task=P.actionObject(item), active=P.active(task), archived=item.project_archived||P.data.projects.some(p=>p.id===task.project_id&&p.archived_at);
    return ['open',...(!archived?(active?['reschedule','complete','cancel']:['completed','cancelled','skipped'].includes(task.status)?['restore']:[]):[]),...(!archived&&task.type!=='session'?['saveTemplate',...(!task.series_id?['move',...(['planned','in_progress','completed'].includes(task.status)&&!P.data.tasks.some(t=>t.parent_task_id===task.id)?['repeat']:[])]:task.series_state==='archived'?[]:['changeRule','pauseRepeat','stopRepeat'])]:[]),...(task.project_id?['project']:[]),...(task.series_id?['series']:[]),...(task.occurrence_id&&task.type!=='session'&&active&&!archived?['skip']:[])].filter(action=>!(['deadline','planning'].includes(item.type)&&['complete','cancel','restore','skip'].includes(action)) && !(item.type==='deadline'&&action==='reschedule'));
  };
  P.datePresets=(host,input)=>host.append(el('div',{class:'pl-date-presets'},[ ['Сегодня',0],['Завтра',1],['Через неделю',7] ].map(([label,days])=>button(label,()=>{input.value=C.addDays(P.today(),days);input.dispatchEvent(new Event('input',{bubbles:true}));})),button('Выбрать дату',()=>{input.focus();try{input.showPicker?.();}catch(_){/* native date field remains available */}})));
  P.actionButton=task=>{const b=button('···',()=>P.objectMenu(task),'pl-icon pl-task-menu');b.setAttribute('aria-label','Действия с задачей');return b;};
  P.objectMenu=async function(item){
    if(item.series_id){try{const s=await P.api('/series/'+item.series_id+'?limit=1');item={...item,series_state:s.state};}catch(e){P.notify(e.message);return;}}
    const task=P.actionObject(item), session=task.type==='session';
    const {dialog,close}=P.dialog(session?'Действия с периодом работы':'Действия с задачей');dialog.classList.add('pl-action-menu');dialog.setAttribute('aria-label',session?'Действия с периодом работы':'Действия с задачей');
    dialog.append(el('p',{class:'pl-menu-title'},task.title||'Период работы'));
    if(item.type==='deadline')dialog.append(meta('Это срок задачи, а не отдельное выполнение. Крайний срок меняется в редакторе.'));
    if(item.type==='planning')dialog.append(meta('Плановая дата задачи. Работа по периодам показана отдельно.'));
    const labels={open:'Открыть / редактировать',reschedule:'Перенести',complete:'Отметить выполненной',cancel:'Отменить',restore:'Восстановить',move:'Переместить в проект',project:'Перейти к проекту',series:'Настройки повторения',skip:'Пропустить выполнение',repeat:'Сделать регулярной',saveTemplate:'Сохранить как шаблон',changeRule:'Изменить повторение',pauseRepeat:'Приостановить повторение',stopRepeat:'Прекратить будущие повторения'};
    const actions=el('div',{class:'pl-menu-items'});
    for(const action of P.actionNames(item))actions.append(button(session&&action==='complete'?'Завершить период':labels[action],()=>{close(true);P.runAction(task,action);},action==='cancel'?'pl-danger':''));
    dialog.append(actions);
  };
  P.runAction=async function(item,action){
    const editorOpen=Boolean(P.guardActive);
    if(P.guardActive && !await P.guardActive())return;
    let task=P.actionObject(item);
    try{
      if(action==='open')return P.openTask(task.type==='session'?task.task_id:task.id);
      if(action==='project')return P.navigate('#/plans/projects/'+task.project_id);
      if(action==='series')return P.navigate('#/plans/series/'+task.series_id);
      if(['repeat','saveTemplate'].includes(action)){
        const context=await P.api('/tasks/'+task.id+'/context');task=context.task;
        if(action==='saveTemplate')return P.taskTemplateEditor(null,task);
        if(context.children.length){const h=P.dialog('Повторение задачи');h.dialog.append(meta('Задача содержит подзадачи. Сохраните дерево через действие «Повторять проект» в карточке проекта.'));return;}
        return P.createRecurring(task);
      }
      if(['changeRule','pauseRepeat','stopRepeat'].includes(action)){
        const series=await P.api('/series/'+task.series_id+'?limit=100');
        if(series.state==='archived'){P.notify('Будущие повторения уже прекращены. История доступна в настройках повторения.');return;}
        return P.seriesChange(series,{changeRule:'change_rule',pauseRepeat:'pause',stopRepeat:'archive'}[action]);
      }
      if(action==='move')return P.moveTask(task);
      if(action==='reschedule'){
        // Saving the open editor may have advanced the record revision.
        if(editorOpen && task.type!=='session' && !task.occurrence_id)task=P.actionObject(await P.api('/tasks/'+task.id));
        return P.reschedule(task);
      }
      if(task.type==='session')return P.sessionAction(task,action);
      return await P.taskAction(task,action);
    }catch(e){P.notify(e.message);}
  };
  P.moveTask=async function(task){
    const {dialog,close}=P.dialog('Переместить в проект');
    if(task.occurrence_id){dialog.append(el('p',{},'Это выполнение регулярной задачи. Связь с проектом задаётся для всего повторения; менять её здесь пока нельзя.'),button('Настройки повторения',()=>{close(true);P.navigate('#/plans/series/'+task.series_id);}));return;}
    try{
      let context=await P.api('/tasks/'+task.id+'/context');const projects=await P.api('/projects');if(!dialog.isConnected)return;task=context.task;
      if(task.parent_task_id){dialog.append(el('p',{},'Подзадача перемещается вместе с родителем. Откройте родительскую задачу, чтобы сохранить дерево.'),button('Открыть родительскую задачу',()=>{close(true);P.openTask(task.parent_task_id);}));return;}
      const form=el('form',{'aria-label':'Перемещение задачи','data-submit':'Переместить задачу'});const project=P.field(form,'project_id','Проект','select',task.project_id,[['','Без проекта'],...projects.items.map(p=>[p.id,p.title])]);
      if(context.children.length)form.append(meta('Подзадачи будут перемещены вместе с родителем. Их даты и история сохранятся.'));
      P.bindForm(form,()=>P.api('/tasks/'+task.id,'PATCH',{project_id:project.value||null,expected_revision:task.revision,related_revisions:P.revisions(context.children)}),async()=>{close(true);P.notify('«'+task.title+'»: проект изменён');await P.refresh();},async()=>{const latest=await P.api('/tasks/'+task.id+'/context');return {record:latest.task,related:latest.children,accept(){context=latest;task=latest.task;}};});dialog.append(form);
    }catch(e){dialog.append(el('p',{role:'alert'},e.message));}
  };
  P.reschedule = function (item, targetDate, targetTime, immediate = false) {
    item = P.actionObject(item);
    if (item.occurrence_id && item.type !== 'session') return P.openOccurrence(item.occurrence_id, 'reschedule', {targetDate,targetTime,immediate});
    const session = item.type === 'session';
    const { dialog, close } = P.dialog(session ? 'Перенос периода работы' : 'Перенос задачи');
    dialog.append(el('p', {}, item.title), meta('Крайний срок задачи не изменится.' + (session ? ' Плановая дата задачи также сохранится.' : '')));
    const form = el('form', { 'data-submit':'Перенести на выбранную дату', 'aria-label': session ? 'Перенос периода работы' : 'Перенос задачи' });
    const date = P.field(form, 'date', 'Новая дата', 'date', targetDate || item.date); date.required = true;
    P.datePresets(form, date);
    let startValue = targetTime === undefined ? item.start_time : targetTime, endValue = item.end_time;
    if (session && targetTime !== undefined) {
      if (targetTime && item.start_time && item.end_time) {
        const end = C.minutes(targetTime) + C.minutes(item.end_time) - C.minutes(item.start_time);
        if (end < 1440) endValue = C.time(end); else { startValue = item.start_time; dialog.append(el('p', { class: 'pl-note' }, 'Интервал выходит за границу дня. Сохранено исходное время; при необходимости измените его вручную.')); }
      } else if (targetTime) endValue = C.time(Math.min(C.minutes(targetTime) + 30, 1439));
      else endValue = null;
    }
    const start = P.field(form, 'time', session ? 'Начало · Москва' : 'Время · Москва', 'time', startValue);
    const end = session ? P.field(form, 'end', 'Окончание · Москва', 'time', endValue) : null;
    function validate() { if (!end) return; start.required = Boolean(end.value); end.required = Boolean(start.value); end.setCustomValidity(start.value && end.value && end.value <= start.value ? 'Окончание должно быть позже начала' : ''); }
    start.addEventListener('input', validate); end?.addEventListener('input', validate); validate();
    let revision = item.revision, baseline = item;
    const path = '/' + (session ? 'sessions' : 'tasks') + '/' + item.id;
    P.bindForm(form, () => P.api(path + '/reschedule', 'POST', { expected_revision: revision, ...(session ? { date: date.value, start_time: start.value || null, end_time: end.value || null } : { planned_date: date.value, scheduled_time: start.value || null }) }), async () => { close(true); P.notify((session?'Период работы':'Задача')+' «'+(item.title||'Без названия')+'» перенесён'+(session?'':'а')+' на '+P.date(date.value)); await P.refresh(); }, async () => {
      const latest = await P.api(path);
      return { record: latest, reload() { close(true); P.refresh(); }, accept() {
        const latestDate = session ? latest.date : latest.planned_date;
        const latestStart = session ? latest.start_time : latest.scheduled_time;
        if (date.value === baseline.date && latestDate) date.value = latestDate;
        if (start.value === (baseline.start_time || '')) start.value = latestStart || '';
        if (end && end.value === (baseline.end_time || '')) end.value = latest.end_time || '';
        revision = latest.revision;
        baseline = { date: latestDate, start_time: latestStart, end_time: latest.end_time };
        validate();
      } };
    });
    dialog.append(form);
    // No optimistic DOM move: a rejected drop leaves the original event in place.
    if (immediate) form.requestSubmit();
  };
  P.projectMenu=function(project){
    const {dialog,close}=P.dialog('Действия с проектом');dialog.setAttribute('aria-label','Действия с проектом');dialog.classList.add('pl-action-menu');const list=el('div',{class:'pl-menu-items'});dialog.append(list);
    const run=fn=>()=>{close(true);fn();};
    if(!project.archived_at){
      list.append(button('Редактировать проект',run(()=>P.navigate('#/plans/projects/'+project.id+'?edit=project&id='+project.id))),button('Изменить даты',run(()=>P.navigate('#/plans/projects/'+project.id+'?edit=project&id='+project.id+'&dates=1'))));
      list.append(button('Добавить задачу',run(()=>P.compactTask({view:'projects',id:project.id,params:new URLSearchParams()}))),button('Сохранить как шаблон',run(()=>P.projectTemplateFlow(project))));
      if(project.repeat_series_id)list.append(button('Изменить повторение',run(async()=>{try{const s=await P.api('/series/'+project.repeat_series_id+'?limit=100');P.seriesChange(s,'change_rule');}catch(e){P.notify(e.message);}})));
      else if(!project.occurrence_id)list.append(button('Повторять проект',run(()=>P.projectTemplateFlow(project,true))));
      if(P.active(project))list.append(button('Завершить проект',run(()=>P.projectAction(project,'complete'))),button('Отменить проект',run(()=>P.projectAction(project,'cancel'))));
      list.append(button('В архив',run(()=>P.projectAction(project,'archive'))));
    }
    list.append(button('Просмотреть историю',run(()=>{const h=P.dialog('История проекта');P.renderHistory(h.dialog,'project',project.id);})));return dialog;
  };
  P.projectMenuButton=project=>{const b=button('···',()=>P.projectMenu(project),'pl-icon');b.setAttribute('aria-label','Действия с проектом');return b;};
})(window.Planner);
