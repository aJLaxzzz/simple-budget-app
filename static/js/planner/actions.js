(function(P){
  'use strict';
  const {el,button,meta}=P, C=P.calendarModel;
  // Calendar projections and records share one command adapter. A session is
  // deliberately checked before occurrence_id: it belongs to work, not a cycle.
  P.actionObject=item=>item.type==='session'?item:item.type&&item.task_id?{...item,id:item.task_id,revision:item.task_revision,status:item.task_status,type:'task',date:item.type==='deadline'?null:item.date}:{...item,type:'task',date:item.date??item.planned_date,start_time:item.start_time??item.scheduled_time};
  P.actionNames=item=>{
    const task=P.actionObject(item), active=P.active(task), archived=item.project_archived||P.data.projects.some(p=>p.id===task.project_id&&p.archived_at);
    return ['open',...(!archived?(active?['reschedule','complete','completeDate','cancel']:['completed','cancelled','skipped'].includes(task.status)?['restore']:[]):[]),...(!archived&&task.type!=='session'?['move',...(!task.series_id?task.project_repeat_series_id?['projectRepeat']:[...(['planned','in_progress','completed'].includes(task.status)&&!P.data.tasks.some(t=>t.parent_task_id===task.id)?['repeat']:[])]:task.series_state==='archived'?[]:['changeRule'])]:[]),...(task.type!=='session'?['deleteForever']:[]),...(task.project_id?['project']:[]),...(task.series_id&&!active&&task.series_state==='archived'?['series']:[]),...(task.occurrence_id&&task.type!=='session'&&active&&!archived?['skip']:[])].filter(action=>!(['deadline','planning'].includes(item.type)&&['complete','cancel','restore','skip'].includes(action)) && !(item.type==='deadline'&&action==='reschedule'));
  };
  P.datePresets=(host,input)=>host.append(el('div',{class:'pl-date-presets'},[ ['Сегодня',0],['Завтра',1],['Через неделю',7] ].map(([label,days])=>button(label,()=>{input.value=C.addDays(P.today(),days);input.dispatchEvent(new Event('input',{bubbles:true}));})),button('Выбрать дату',()=>{input.focus();try{input.showPicker?.();}catch(_){/* native date field remains available */}})));
  P.actionButton=task=>{const b=button('···',()=>P.objectMenu(task),'pl-icon pl-task-menu');b.setAttribute('aria-label','Действия с задачей');return b;};
  P.objectMenu=async function(item){
    if(item.series_id){try{const s=await P.api('/series/'+item.series_id+'?limit=1');item={...item,series_state:s.state};}catch(e){P.notify(e.message);return;}}
    if(item.project_id&&!item.series_id&&item.type!=='session'){try{const project=await P.api('/projects/'+item.project_id);item={...item,project_repeat_series_id:project.repeat_series_id};}catch(e){P.notify(e.message);return;}}
    const task=P.actionObject(item), session=task.type==='session';
    const {dialog,close}=P.dialog(session?'Действия с периодом работы':'Действия с задачей');dialog.classList.add('pl-action-menu');dialog.setAttribute('aria-label',session?'Действия с периодом работы':'Действия с задачей');
    dialog.append(el('p',{class:'pl-menu-title'},task.title||'Период работы'));
    if(item.type==='deadline')dialog.append(meta('Это срок задачи, а не отдельное выполнение. Крайний срок меняется в редакторе.'));
    if(item.type==='planning')dialog.append(meta('Плановая дата задачи. Работа по периодам показана отдельно.'));
    const labels={deleteForever:'Удалить навсегда',open:'Открыть / редактировать',reschedule:'Перенести',complete:'Отметить выполненной',cancel:'Отменить задачу',restore:'Восстановить',move:'Переместить в проект',project:'Перейти к проекту',series:'Настроить повторение',skip:'Пропустить выполнение',repeat:'Сделать регулярной',saveTemplate:'Сохранить как шаблон',projectRepeat:'Настроить повторение проекта',changeRule:'Настроить повторение',pauseRepeat:'Приостановить повторение',stopRepeat:'Архивировать повторение',disableRepeat:'Отключить повторение',completeDate:'Выполнить другой датой'};
    const actions=el('div',{class:'pl-menu-items'});
    const danger=['cancel','stopRepeat','deleteForever'];const names=P.actionNames(item);const ordered=[...names.filter(a=>!danger.includes(a)),...danger.filter(a=>names.includes(a))];
    const sections={};const groupFor=a=>['move','completeDate','project'].includes(a)?'Планирование':['repeat','projectRepeat','changeRule','pauseRepeat','disableRepeat','series'].includes(a)?'Повторение':'Статус';
    for(const action of ordered){let target=actions;if(!danger.includes(action)&&!['open','reschedule','complete'].includes(action)){const key=groupFor(action);if(!sections[key]){sections[key]=el('details',{},el('summary',{},key));actions.append(sections[key]);}target=sections[key];}if(action===ordered.find(a=>danger.includes(a)))actions.append(el('hr',{class:'pl-menu-separator'}));target.append(button(session&&action==='complete'?'Завершить период':session&&action==='cancel'?'Отменить период работы':labels[action],()=>{close(true);P.runAction(task,action);},danger.includes(action)?'pl-danger':''));}
    dialog.append(actions);
  };
  P.runAction=async function(item,action){
    const editorOpen=Boolean(P.guardActive);
    if(P.guardActive && !await P.guardActive())return;
    let task=P.actionObject(item);
    try{
      if(action==='deleteForever')return P.deleteForever('tasks',task);
      if(action==='open')return P.openTask(task.type==='session'?task.task_id:task.id);
      if(action==='project')return P.navigate('#/plans/projects/'+task.project_id);
      if(action==='projectRepeat')return P.repeatEditor(task.project_repeat_series_id,{projectID:task.project_id});
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
      if(action==='disableRepeat')return P.disableRepeat(task.series_id,task.id);
 if(action==='completeDate')return P.taskAction(task,'complete');
 if(action==='complete'&&task.type!=='session')return P.quickComplete(task);
 if(action==='move')return P.moveTask(task);
      if(action==='reschedule'){
        // Saving the open editor may have advanced the record revision.
        if(editorOpen && task.type!=='session' && !task.occurrence_id)task=P.actionObject(await P.api('/tasks/'+task.id));
        return P.reschedule(task);
      }
      if(task.type==='session')return P.sessionAction(task,action);
      if(action==='restore')return P.quickRestore(task);
      return await P.taskAction(task,action);
    }catch(e){P.notify(e.message);}
  };
  P.moveTask=async function(task){
    const {dialog,close}=P.dialog('Переместить в проект');
    if(task.occurrence_id){close(true);return P.moveExistingTasks([task]);}
    try{
      let context=await P.api('/tasks/'+task.id+'/context');const projects=await P.api('/projects');if(!dialog.isConnected)return;task=context.task;
      if(task.parent_task_id){dialog.append(el('p',{},'Подзадача перемещается вместе с родителем. Откройте родительскую задачу, чтобы сохранить дерево.'),button('Открыть родительскую задачу',()=>{close(true);P.openTask(task.parent_task_id);}));return;}
      const form=el('form',{'aria-label':'Перемещение задачи','data-submit':'Переместить задачу'});const project=P.field(form,'project_id','Проект','select',task.project_id,[['','Без проекта'],...projects.items.map(p=>[p.id,p.title])]);
      if(context.children.length)form.append(meta('Подзадачи будут перемещены вместе с родителем. Их даты и история сохранятся.'));confirmClosedChildren(form,context.children);
      P.bindForm(form,()=>P.api('/tasks/'+task.id,'PATCH',{project_id:project.value||null,expected_revision:task.revision,related_revisions:P.revisions(context.children)}),async()=>{close(true);P.notify('«'+task.title+'»: проект изменён');await P.refresh();},async()=>{const latest=await P.api('/tasks/'+task.id+'/context');return {record:latest.task,related:latest.children,accept(){context=latest;task=latest.task;}};});dialog.append(form);
    }catch(e){dialog.append(el('p',{role:'alert'},e.message));}
  };
  function confirmClosedChildren(form,tasks){
    const closed=tasks.filter(t=>t.parent_task_id&&!P.active(t));if(!closed.length)return;
    const consent=el('input',{type:'checkbox',required:true,'aria-label':'Переместить закрытые подзадачи вместе с родителем'});
    form.append(el('label',{class:'pl-existing-choice'},consent,el('span',{},'Подтверждаю перемещение закрытых подзадач: '+closed.length+'. Их статусы, фактические даты и история сохранятся.')));
  }
  P.addExistingTasks=async function(project){
    try{
      const records=(await P.api('/tasks?status=active')).items.filter(t=>!t.parent_task_id&&t.project_id!==project.id);
      const {dialog,close}=P.dialog('Добавить существующие задачи');dialog.append(meta('Задачи будут перемещены вместе с подзадачами. Копии не создаются.'));
      const form=el('form',{'aria-label':'Выбор существующих задач'}),query=P.field(form,'q','Поиск задач','search',''),list=el('div',{class:'pl-existing-list'}),chosen=new Set();
      const names=new Map(P.data.projects.map(p=>[p.id,p.title]));
      const draw=()=>{const rows=records.filter(t=>t.title.toLocaleLowerCase('ru').includes(query.value.toLocaleLowerCase('ru')));list.replaceChildren(...rows.map(t=>{const box=el('input',{type:'checkbox','aria-label':'Выбрать: '+t.title});box.checked=chosen.has(t.id);box.addEventListener('change',()=>box.checked?chosen.add(t.id):chosen.delete(t.id));return el('label',{class:'pl-existing-choice'},box,el('span',{},t.title,meta((names.get(t.project_id)||'Без проекта')+(t.series_id?' · повторяется':''))));}));if(!rows.length)list.append(meta('Подходящих задач нет'));};
      query.addEventListener('input',draw);const next=button('Продолжить',()=>{const selected=records.filter(t=>chosen.has(t.id));if(!selected.length){P.notify('Выберите хотя бы одну задачу');return;}close(true);P.moveExistingTasks(selected,project);},'pl-primary');form.addEventListener('submit',e=>e.preventDefault());form.append(list,next);dialog.append(form);draw();
    }catch(e){P.notify(e.message);}
  };
  P.moveExistingTasks=async function(tasks,target){
    try{
      const projects=(await P.api('/projects')).items.filter(p=>!p.archived_at&&P.active(p));
      const {dialog,close}=P.dialog('Переместить задачи в проект');const form=el('form',{'aria-label':'Настройка перемещения','data-submit':'Предпросмотр перемещения'});
      const project=P.field(form,'project_id','Проект','select',target?.id||tasks[0].project_id||'',[['','Без проекта'],...projects.map(p=>[p.id,p.title])]);
      const regular=tasks.some(t=>t.series_id);const scope=regular?P.field(form,'scope','Область перемещения','select','series',[['series','Все незавершённые и будущие выполнения'],['instance','Только выбранные выполнения']]):null;
      form.append(meta('ID, даты, оформление, периоды работы и история сохраняются. Закрытые самостоятельные выполнения остаются в прежнем проекте. Дерево родителя перемещается целиком; для закрытых подзадач потребуется отдельное подтверждение.'));
      const preview=el('section');let selected=null;const send=P.commandSender('/task-moves/apply');
      const invalidate=()=>{selected=null;preview.replaceChildren();};form.addEventListener('input',invalidate);form.addEventListener('change',invalidate);
      P.bindForm(form,async()=>{const input={task_ids:tasks.map(t=>t.id),project_id:project.value||null,scope:scope?.value||'instance',expected_revisions:P.revisions(tasks)};const result=await P.api('/task-moves/preview','POST',input);selected={...input,preview_token:result.preview_token};return result;},p=>{
        const names=new Map(projects.map(p=>[p.id,p.title]));preview.replaceChildren(el('h3',{},'Будет перемещено: '+p.tasks.length),meta('Назначение: '+(p.project?.title||'Без проекта')),el('ul',{},p.tasks.map(t=>el('li',{},t.title+' · '+P.labels[t.status]+' · '+(names.get(t.project_id)||'Без проекта')+' → '+(p.project?.title||'Без проекта')))));
        const apply=el('form',{'aria-label':'Подтверждение перемещения','data-submit':'Переместить задачи'});confirmClosedChildren(apply,p.tasks);P.bindForm(apply,()=>{if(!selected)throw Error('Повторите предпросмотр');return send(selected);},async()=>{close(true);P.notify('Задачи перемещены: '+p.tasks.length);await P.refresh();},async()=>({record:await P.api('/tasks/'+tasks[0].id),async accept(){tasks=await Promise.all(tasks.map(t=>P.api('/tasks/'+t.id)));invalidate();}}));preview.append(apply);P.showPreview(preview);
      },async()=>({record:await P.api('/tasks/'+tasks[0].id),async accept(){tasks=await Promise.all(tasks.map(t=>P.api('/tasks/'+t.id)));invalidate();}}));dialog.append(form,preview);
    }catch(e){P.notify(e.message);}
  };
  const directBusy = new Set();
  const actionError = (message,error) => {P.notify(message+': '+P.errorMessage(error));clearTimeout(P.toastTimer);};
  function sameRevision(record,expected) {
    if(record.revision!==expected)throw Error('Запись изменилась на другом устройстве. Обновите данные и повторите действие.');
  }
  // Drag carries an explicit destination. It never chooses a series-wide scope.
  P.calendarDrop = async function(item,date,time) {
    item=P.actionObject(item);const session=item.type==='session',key=(session?'session:':'task:')+item.id;
    if(directBusy.has(key))return;directBusy.add(key);
    try {
      const occurrence=item.occurrence_id&&!session?await P.api('/occurrences/'+item.occurrence_id):null;
      const path='/'+(session?'sessions':'tasks')+'/'+item.id;
      const before=occurrence?.occurrence.task||await P.api(path);
      sameRevision(before,item.revision);
      const oldDate=session?before.date:before.planned_date,oldTime=session?before.start_time:before.scheduled_time;
      const nextTime=time===undefined?oldTime:time;
      let end=before.end_time;
      if(session&&time!==undefined){
        if(!time)end=null;
        else {const minutes=C.minutes(time)+(before.start_time&&before.end_time?C.minutes(before.end_time)-C.minutes(before.start_time):30);if(minutes>=1440)throw Error('Период работы выходит за границу суток. Используйте действие «Перенести» для выбора интервала.');end=C.time(minutes);}
      }
      if(date===oldDate&&nextTime===oldTime&&(!session||end===before.end_time))return;
      const fields=session?{date,start_time:nextTime||null,end_time:end||null}:{planned_date:date,scheduled_time:nextTime||null};
      const previous=session?{date:oldDate,start_time:oldTime||null,end_time:before.end_time||null}:{planned_date:oldDate,scheduled_time:oldTime||null};
      const send=async (fields,record,cycle) => {
        if(occurrence){
          const current=cycle||await P.api('/occurrences/'+item.occurrence_id);
          return P.commandSender('/occurrences/'+item.occurrence_id+'/reschedule')({...fields,instance_only:true,expected_revision:cycle?cycle.occurrence.revision:record.occurrenceRevision,expected_task_revision:record.revision,expected_series_revision:current.series.revision});
        }
        return P.api(path+'/reschedule','POST',{...fields,expected_revision:record.revision});
      };
      const result=await send(fields,before,occurrence),saved=result.occurrence?{...result.occurrence.task,occurrenceRevision:result.occurrence.revision}:result;
      await P.refresh();
      let undone=false,undoBusy=false;
      P.notify(date===oldDate?'Время изменено':'Перенесено на '+P.date(date),{label:'Отменить перенос',run:async()=>{
        if(undone||undoBusy)return;undoBusy=true;
        try{await send(previous,saved);undone=true;await P.refresh();P.notify('Исходные дата и время восстановлены');}
        catch(e){actionError('Не удалось отменить перенос',e);}finally{undoBusy=false;}
      }});
    }catch(e){actionError('Не удалось изменить дату или время',e);}finally{directBusy.delete(key);}
  };
  P.quickRestore = async function(item,completion) {
    const task=P.actionObject(item),key='restore:'+task.id;
    if(directBusy.has(key))return;directBusy.add(key);
    try {
      let result;
      if(task.occurrence_id){
        const current=await P.api('/occurrences/'+task.occurrence_id);
        sameRevision(current.occurrence.task,task.revision);
        const undo=completion?.undo;
        result=await P.commandSender('/occurrences/'+task.occurrence_id+(undo?'/undo-complete':'/restore'))({
          expected_revision:current.occurrence.revision,expected_task_revision:task.revision,expected_series_revision:current.series.revision,
          ...(undo?{completion_request_id:completion.requestID}:{})
        });
      }else result=await P.commandSender('/tasks/'+task.id+'/restore')({expected_revision:task.revision});
      const saved=result.occurrence?.task||result;
      const draft=P.drafts.get('task:'+task.id);if(draft){draft.record=saved;draft.values.status=saved.status;delete draft.related;}
      await P.refresh();P.notify('Задача «'+task.title+'» восстановлена');
    }catch(e){
      // Completion-based branches can affect other occurrences. Keep their
      // existing explicit decision workflow; never silently rebase a conflict.
      if(task.occurrence_id&&['completion_branch_conflict','undo_changed','undo_requires_decision','dynamic_manual_conflict'].includes(e.code)){
        P.notify(P.errorMessage(e),{label:'Проверить последствия',run:()=>P.openOccurrence(task.occurrence_id,'restore')});clearTimeout(P.toastTimer);
      }else actionError('Не удалось восстановить задачу',e);
    }finally{directBusy.delete(key);}
  };
  P.reschedule = async function (item, targetDate, targetTime, immediate = false) {
    if(immediate)return P.calendarDrop(item,targetDate,targetTime);
    item = P.actionObject(item);
    if (item.occurrence_id && item.type !== 'session') return P.openOccurrence(item.occurrence_id, 'reschedule', {targetDate,targetTime,immediate});
    const session = item.type === 'session';
    const scopeProject=!session&&item.project_id?await P.api('/projects/'+item.project_id):null;
    const { dialog, close } = P.dialog(session ? 'Перенос периода работы' : 'Перенос задачи');
    dialog.append(el('p', {}, item.title), meta('Крайний срок задачи не изменится.' + (session ? ' Плановая дата задачи также сохранится.' : '')));
    const form = el('form', { 'data-submit':'Перенести на выбранную дату', 'aria-label': session ? 'Перенос периода работы' : 'Перенос задачи' });
    const date = P.field(form, 'date', 'Новая дата', 'date', targetDate || item.date); date.required = true;
    P.datePresets(form, date);
    let startValue = targetTime === undefined ? (item.start_time||item.scheduled_time) : targetTime, endValue = item.end_time;
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
    const changeScope=P.projectScopeField(form,scopeProject);
    let revision = item.revision, baseline = item;
    const path = '/' + (session ? 'sessions' : 'tasks') + '/' + item.id;
    P.bindForm(form, () => changeScope&&changeScope.value!=='this'?P.scopedProjectSave(scopeProject,{scope:changeScope.value,action:'edit',task_id:item.id,expected_task_revision:revision,patch:{planned_date:date.value,...(start.value!==(item.start_time||item.scheduled_time||'')?{scheduled_time:start.value||null}:{})}}):P.api(path + '/reschedule', 'POST', { expected_revision: revision, ...(session ? { date: date.value, start_time: start.value || null, end_time: end.value || null } : { planned_date: date.value, scheduled_time: start.value || null }) }), async result => { close(true); P.notify(result.scope_notice||((session?'Период работы':'Задача')+' «'+(item.title||'Без названия')+'» перенесён'+(session?'':'а')+' на '+P.date(date.value))); await P.refresh(); }, async () => {
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
    if (immediate&&(!changeScope||changeScope.value==='this')) form.requestSubmit();
  };
  P.projectMenu=function(project){
    const {dialog,close}=P.dialog('Действия с проектом');dialog.setAttribute('aria-label','Действия с проектом');dialog.classList.add('pl-action-menu');const list=el('div',{class:'pl-menu-items'});dialog.append(list);
    const run=fn=>()=>{close(true);fn();};
    if(!project.archived_at){
      list.append(button('Редактировать проект',run(()=>P.navigate('#/plans/projects/'+project.id+'?edit=project&id='+project.id))),button('Изменить даты',run(()=>P.navigate('#/plans/projects/'+project.id+'?edit=project&id='+project.id+'&dates=1'))));
      if(P.active(project)){const complete=button('Завершить проект',run(()=>P.completeProject(project)));complete.disabled=!project.completion_ready;complete.title=P.projectCompleteControl(project).title;list.append(complete);}
      list.append(button('Добавить существующие задачи',run(()=>P.addExistingTasks(project))),button('Добавить задачу',run(()=>project.repeat_series_id?P.newTask(project.id):P.compactTask({view:'projects',id:project.id,params:new URLSearchParams()}))));

      if(project.repeat_series_id)list.append(button('Перенести выполнение проекта',run(()=>P.moveProjectExecution(project))),button('Настроить повторение',run(()=>P.repeatEditor(project.repeat_series_id,{projectID:project.id}))));
      else if(!project.occurrence_id)list.append(button('Повторять проект',run(()=>P.projectTemplateFlow(project,true))));

      if((project.occurrence_id||project.managed_repeat)&&P.active(project))list.append(button('Пропустить выполнение',run(()=>P.projectAction(project,'skip'))));
      if((project.occurrence_id||project.managed_repeat)&&!P.active(project)&&['completed','cancelled','skipped'].includes(project.status))list.append(button('Восстановить проект',run(()=>P.projectAction(project,'restore'))));

    }
    if(project.archived_at)list.append(button('Вернуть из архива',run(()=>P.unarchive('projects',project))));

    list.append(button('Просмотреть историю',run(()=>{const h=P.dialog('История проекта');P.renderHistory(h.dialog,'project',project.id);})));list.append(el('hr',{class:'pl-menu-separator'}));if(!project.archived_at){if(P.active(project))list.append(button('Отменить проект',run(()=>P.projectAction(project,'cancel')),'pl-danger'));list.append(button('В архив',run(()=>P.projectAction(project,'archive')),'pl-danger'));}list.append(button('Удалить навсегда',run(()=>P.deleteForever('projects',project)),'pl-danger'));P.groupProjectMenu(list);return dialog;
  };
  P.projectMenuButton=project=>{const b=button('···',()=>P.projectMenu(project),'pl-icon');b.setAttribute('aria-label','Действия с проектом');return b;};
})(window.Planner);
