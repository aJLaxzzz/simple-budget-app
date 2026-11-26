(function(P){
  'use strict';
  const {el,button,meta}=P;
  const changeLabels={unchanged:'Без изменений',change_date:'Дата изменится',same_date_new_version:'Дата сохраняется, правило обновляется',change_project:'Изменится состав или дата проекта',manual_exception:'Ручной перенос сохранён',protected_closed:'Защищено: выполнение закрыто',protected_started:'Защищено: работа начата',protected_history:'История сохраняется',protected_past:'Прошедшее выполнение сохраняется',protected_sessions_or_children:'Сохранено: есть периоды работы или подзадачи',protected_sessions:'Сохранено: есть периоды работы',protected_user_changes:'Сохранено: есть пользовательские изменения',protected_archived:'Архивный проект сохраняется',completion_pending_preserved:'Ожидающее выполнение сохраняется',change_date_pause_pending_preserved:'Дата изменится, ожидающее выполнение сохранится',skip_pause:'Будет пропущено на время паузы',future_pause_skip:'Будущий пропуск во время паузы',change_and_skip:'Новая дата попадает в паузу — будет пропуск',restore_after_resume:'Будет восстановлено после паузы',not_materialized:'Будущее выполнение',calculation_error:'Не удалось рассчитать',conflict_structure:'Конфликт структуры проекта'};
  const stateLabels={active:'Активна',paused:'Приостановлена',needs_anchor:'Нужно выбрать дату продолжения',archived:'В архиве',disabled:'Повторение отключено'};
  P.seriesList=async()=>{const items=[];let after='';do{const page=await P.api('/series'+(after?'?after='+encodeURIComponent(after):''));items.push(...page.items);after=page.next_after;}while(after);return items;};
  P.renderSeries=async function(host,route){
    host.replaceChildren();
    if(route.id){const value=await P.api('/series/'+route.id+'?limit=1');host.append(el('h2',{},'Повторение'),meta(P.seriesTitle(value)),button('Настроить повторение',()=>P.repeatEditor(route.id)));if(P.repeatAutoOpenedRoute!==window.location.hash){P.repeatAutoOpenedRoute=window.location.hash;await P.repeatEditor(route.id,{tab:route.params.get('tab')||'schedule'});}return;}
    const scope=route.view==='tasks'?'task':route.view==='projects'?'project':null;
    host.append(el('div',{class:'pl-section-head'},el('div',{},el('h2',{},scope==='task'?'Регулярные задачи':scope==='project'?'Регулярные проекты':'Регулярные дела'),meta('Ритм повседневных дел — без необходимости помнить о каждом.')),button(scope==='project'?'+ Проект':'+ Регулярное дело',()=>scope==='project'?P.navigate('#/plans/projects?new=project'):P.createRecurring(), 'pl-primary')));
    const items=(await P.seriesList()).filter(s=>s.state!=='archived'&&(!scope||s.source_type===scope));const filters=el('form',{class:'pl-filters'});filters.addEventListener('submit',e=>e.preventDefault());
    const search=P.field(filters,'q','Поиск','search',route.params.get('q')||'');const type=P.field(filters,'type','Показывать','select',route.params.get('filter')||'all',[['all','Все'],...(!scope?[['task','Задачи'],['project','Проекты']]:[]),['active','Активные'],['paused','Приостановленные'],['disabled','Отключённые · история']]);
    const grid=el('div',{class:'pl-project-grid'});
    function draw(){const found=items.filter(s=>(s.state!=='disabled'||type.value==='disabled')&&P.seriesTitle(s).toLocaleLowerCase('ru').includes(search.value.toLocaleLowerCase('ru'))&&(type.value==='all'||s.source_type===type.value||s.state===type.value));grid.replaceChildren(...found.map(s=>{
      const appearance={...(s.project_template||s.template),effective_color_id:s.effective_color_id,effective_icon_id:s.effective_icon_id},card=P.paint(el('article',{class:'pl-project-card pl-series-card'}),appearance);
      const title=el('a',{href:'#/plans/series/'+s.id+'?source='+s.source_type},P.appearanceIcon(appearance,s.source_type==='project'),el('strong',{},P.seriesTitle(s)));
      const menu=button('···',()=>{const {dialog,close}=P.dialog('Действия с повторением');dialog.append(button('Настроить повторение',()=>{close(true);P.repeatEditor(s.id);}),button('Оформление повторения',()=>{close(true);P.editSeriesAppearance(s.id);}),P.lifecycleMenuButton('series',s));},'pl-icon');menu.setAttribute('aria-label','Действия с повторением');
      card.append(...[el('div',{class:'pl-card-top'},title,menu),meta(stateLabels[s.state]),s.calculation_error?el('p',{class:'pl-note'},s.calculation_error):null,el('p',{},P.ruleText(s.rule.rule)),s.next_date?meta((s.next_date<P.today()?'Ожидает выполнения с ':s.anchor_mode==='completion'?'Текущее выполнение: ':'Ближайшее: ')+P.date(s.next_date)):meta(s.state==='needs_anchor'?'Выберите дату продолжения':s.anchor_mode==='completion'?'Следующая дата появится после выполнения':'Нет подтверждённых будущих выполнений'),s.last_completed_date?meta('Последнее выполнение: '+P.date(s.last_completed_date)):null,s.unresolved_count?el('span',{class:'pl-overdue'},'Требуют решения: '+s.unresolved_count):null].filter(Boolean));return card;
    }));if(!found.length)grid.append(P.empty('Повторения ещё не настроены','Создайте задачу или проект и включите повторение.'));}
    filters.addEventListener('input',()=>{const params=new URLSearchParams(route.params);params.delete('q');params.delete('filter');if(search.value)params.set('q',search.value);if(type.value!=='all')params.set('filter',type.value);history.replaceState(null,'',P.base()+(params.size?'?'+params:''));draw();});host.append(filters,grid);draw();
  };
  async function detail(host,id){
    let series=await P.api('/series/'+id+'?limit=100');
    if(!host.isConnected)return;
    const chrome=document.querySelector('.pl-navigation'), route=P.route();
    route.params.set('source',series.source_type); if(chrome){chrome.replaceChildren();P.navigation(chrome,route);}
    host.append(el('a',{class:'pl-back',href:series.source_type==='project'?'#/plans/projects?view=regular':'#/plans/tasks?view=regular'},'← Регулярные дела'),el('div',{class:'pl-section-head'},el('h2',{},P.seriesTitle(series)),meta(stateLabels[series.state])),el('p',{class:'pl-description'},series.project_template?.description||series.template.description),el('p',{class:'pl-info'},P.ruleText(series.rule.rule)));
    if(series.calculation_error)host.append(el('p',{class:'pl-note'},series.calculation_error));
    if(series.end_date)host.append(meta('Автоматическое создание до '+P.date(series.end_date)+' включительно. Существующие выполнения сохраняются.'));
    host.append(el('h3',{},series.anchor_mode==='completion'?'Текущее ожидающее выполнение':'Ближайшее выполнение'),meta(series.next_date?P.date(series.next_date):series.state==='needs_anchor'?'Выберите дату, от которой считать дальше':'Нет ожидающих выполнений'));
    if(series.upcoming?.length)host.append(el('div',{class:'pl-actions'},series.upcoming.map(o=>button((o.task?.dynamic?'≈ ':'')+P.date(o.manual_date||o.adjusted_date),()=>o.project?P.navigate('#/plans/projects/'+o.project.id):P.openTask(o.task.id)))));
    const actions=el('div',{class:'pl-actions'});if(series.state!=='disabled'&&(series.source_type==='task'||series.source_project_id))actions.append(button('Отключить повторение',()=>P.disableRepeat(series.id,series.source_project_id)));
    if(!['archived','disabled'].includes(series.state)){
      actions.append(button('Изменить правило',()=>P.seriesChange(series,'change_rule')),button('Приостановить',()=>P.seriesChange(series,'pause')));
      for(const pause of series.pauses||[])if(!pause.resumed_on)actions.append(button('Возобновить · '+P.date(pause.start_date),()=>P.seriesChange(series,'resume',{pause_id:pause.id})));
      if(series.state==='needs_anchor')actions.append(button('Дата, от которой считать дальше',()=>P.seriesChange(series,'reanchor')));
      if(series.source_type==='project')actions.append(button('Изменить состав будущих проектов',()=>P.projectComposition({repeat_series_id:series.id,id:series.source_project_id,title:P.seriesTitle(series)})));
      if(series.source_type==='project')actions.append(button('Проверить и исправить будущие выполнения',()=>P.projectDateRepair({repeat_series_id:series.id,title:P.seriesTitle(series)})));
      actions.append(button('Архивировать повторение',()=>P.seriesChange(series,'archive')));
    }
    actions.append(button('Оформление повторения',()=>P.editSeriesAppearance(series.id)),P.lifecycleMenuButton('series',series));
    host.append(el('p',{class:'pl-info'},series.anchor_mode==='completion'?'Одно текущее выполнение и зависимые предварительно запланированные задачи. Перенос и фактическое завершение текущего дела уточняют их даты.':'По расписанию: даты рассчитываются заранее независимо от завершения предыдущих выполнений.'),button('Предпросмотр дальних дат',()=>{const h=P.dialog('Прогноз повторения');h.dialog.append(meta('Предпросмотр не создаёт задачи.'));P.ruleBuilder(h.dialog,{rule:series.rule.rule,firstDate:series.rule.first_date,changing:true,completedDate:series.anchor_date,anchorKind:series.anchor_kind,project:series.source_type==='project'});}),actions,el('a',{href:'#/plans/attention?series_id='+id+'&tab=skipped'},'Пропуски этого повторения'));
    if(series.anchor_mode==='completion')host.append(el('p',{class:'pl-info'},series.state==='archived'?'Повторение прекращено. Уже созданные выполнения и история сохраняются.':'После завершения текущего выполнения будет рассчитана следующая дата. После пропуска или отмены восстановите выполнение либо выберите дату продолжения.'));
    const list=el('div',{class:'pl-occurrences'});host.append(el('h3',{},'Выполнения и история'),list);
    let offset=0;
    function append(items){for(const o of items){const record=o.project||o.task;const status=record?.status||o.project_status;const row=el('article',{class:'pl-occurrence'},el('div',{},el('strong',{},P.date(o.manual_date||o.adjusted_date)),meta(P.labels[status]||status),o.task?.dynamic&&P.active(record)?meta('Предварительно запланировано'):null,o.manual_override?meta('Перенесено вручную'):null));
      const controls=el('div',{class:'pl-actions'});if(record)controls.append(button('Открыть',()=>o.project?P.navigate('#/plans/projects/'+o.project.id):P.openTask(o.task.id)));
      if(o.task?.id)controls.append(P.actionButton(o.task));else controls.append(P.lifecycleMenuButton('occurrences',o));
      for(const action of P.occurrenceActions(status))controls.append(button(P.occurrenceLabels[action],()=>action==='reschedule'&&o.task?P.reschedule(o.task):P.occurrenceAction(series,o,action)));
      row.append(controls);list.append(row);
    }}
    append(series.occurrences||[]);offset+=(series.occurrences||[]).length;
    const more=button('Ещё выполнения',async()=>{more.disabled=true;try{const next=await P.api('/series/'+id+'?limit=100&offset='+offset);append(next.occurrences||[]);offset+=(next.occurrences||[]).length;more.hidden=!next.has_more;}catch(e){P.notify(e.message);}finally{more.disabled=false;}});more.hidden=!series.has_more;host.append(more);
    const history=P.disclosure('История повторения');host.append(history);P.renderHistory?.(history,'series',id);
  }
  P.renderRepeatHistory=detail;
  P.seriesChange=function(series,action,extras={},host){
    if(action==='change_rule'&&!host)return P.repeatEditor(series.id,{tab:'schedule',extras});
    const labels={change_rule:'Изменение будущих выполнений',pause:'Пауза повторения',resume:'Возобновление повторения',archive:'Архивирование повторения',reanchor:'Дата, от которой считать дальше',change_template:'Обновление состава проектов'};
    const {dialog,close}=P.dialog(labels[action],host);dialog.classList.add('pl-management-drawer');const form=el('form',{'aria-label':labels[action]});let builder;
    if(action==='change_rule'){
      form.append(meta('Закрытые, начатые и вручную перенесённые выполнения сохраняются. Изменение всего повторения использует тот же механизм, начиная с первого выполнения.'));
      const choices=[['0','Все будущие выполнения с сохранением истории'],...(series.occurrences||[]).filter(o=>P.active(o.task||o.project||{})).map(o=>[String(o.cycle_index),'Начиная с '+P.date(o.adjusted_date)])];if(extras.from_cycle!==undefined&&!choices.some(c=>c[0]===String(extras.from_cycle)))choices.push([String(extras.from_cycle),'Выбранное выполнение и будущие']);const scope=P.field(form,'from_cycle','Применить','select',String(extras.from_cycle??0),choices);
      builder=P.ruleBuilder(form,{rule:series.rule.rule,firstDate:extras.first_date||(series.occurrences||[]).find(o=>o.cycle_index===(extras.from_cycle??0))?.base_date||series.rule.first_date,changing:true,completedDate:series.anchor_date,anchorKind:series.anchor_kind,project:series.source_type==='project'});
      scope.addEventListener('change',()=>{builder.first.value=(series.occurrences||[]).find(o=>o.cycle_index===Number(scope.value))?.base_date||series.rule.first_date;builder.refresh();});
      if(series.source_type==='project'){const dates=P.field(form,'tasks_date_decision','Даты задач проектов','select','shift',[['keep','Сохранить даты задач'],['shift','Пересчитать относительно нового начала']]);const note=el('p',{class:'pl-warning'},'Даты уже созданных задач останутся на прежних днях, а новые выполнения будут рассчитаны по новому расписанию.');note.hidden=true;dates.addEventListener('change',()=>note.hidden=dates.value!=='keep');form.append(note);}
    }
    if(action==='pause'){P.field(form,'start_date','Начало паузы','date',P.today()).required=true;P.field(form,'end_date','Последний день паузы','date',P.today()).required=true;P.field(form,'reason','Причина','text','');}
    if(action==='reanchor'){P.field(form,'anchor_date','Дата, от которой считать дальше','date',P.today()).required=true;form.append(meta('Продолжение рассчитывается от этой даты. Старые пропуски остаются в истории.'));}
    if(action==='change_template'){
      P.field(form,'from_cycle','Применить начиная с','select','0',[['0','Первого подходящего проекта'],[String(series.next_cycle),'Только ещё не созданных проектов']]);
      form.append(meta('Будет использован обновлённый исходный шаблон. Проекты с ручными изменениями защищены.'));
      P.field(form,'removed_tasks_decision','Если из шаблона удалены задачи','select','',[['','Не отменять без подтверждения'],['cancel','Подтверждаю отмену удалённых задач с сохранением истории']]);
    }
    if(action==='archive')form.append(meta('Будущая автоматическая генерация прекратится. Уже созданные задачи и проекты, в том числе будущие, останутся доступными. История не удаляется.'));
    const previewArea=el('section',{'aria-live':'polite',class:'pl-recurrence-preview'});let preview,input,lockedControls;const send=P.commandSender('/series/'+series.id+'/'+action.replaceAll('_','-'));
    const apply=button(({archive:'Архивировать повторение',pause:'Приостановить повторение',resume:'Возобновить повторение'})[action]||'Применить изменения',async()=>{apply.disabled=true;form.dataset.busy='true';lockedControls ||= [...form.elements].map(c=>[c,c.disabled]);lockedControls.forEach(([c])=>{c.disabled=true;});try{await send({...input,preview_token:preview.preview_token});close(true);P.notify('«'+P.seriesTitle(series)+'»: повторение обновлено');await P.refresh();}catch(e){previewArea.append(el('p',{role:'alert'},e.message+(e.status===409?' Черновик сохранён. Повторите предпросмотр.':' При потере ответа повторите применение той же команды.')));if(e.uncertainCommand){form.dataset.uncertain='true';}else {delete form.dataset.uncertain;}if(e.status===409){preview=null;apply.hidden=true;settings.open=true;} }finally{apply.disabled=false;delete form.dataset.busy;if(!form.dataset.uncertain){lockedControls?.forEach(([c,disabled])=>{c.disabled=disabled;});lockedControls=null;}}},'pl-primary');apply.hidden=true;
    const invalidate=()=>{preview=null;apply.hidden=true;previewArea.hidden=true;};form.addEventListener('input',invalidate);form.addEventListener('change',invalidate);
    form.dataset.submit='Посмотреть изменения';
    P.bindForm(form,async()=>{
      const values=P.values(form); const proposedRule=builder?.read(); const proposedFirst=builder?.first.value;
      const latest=await P.api('/series/'+series.id+'?limit=1');
      input={expected_revision:latest.revision,action,...extras};
      for(const key of ['start_date','end_date','reason','anchor_date','tasks_date_decision','removed_tasks_decision'])if(values[key])input[key]=values[key];
      if(values.from_cycle!==undefined)input.from_cycle=Number(values.from_cycle);
      if(builder){input.rule=proposedRule;if(input.rule.mode==='schedule')input.first_date=proposedFirst;}
      if(action==='change_template'){const template=await P.api('/templates/'+series.template_id);input.template_version_id=template.version_id;}
      return P.api('/series/'+series.id+'/preview','POST',input);
    },result=>{delete form.dataset.saved;preview=result;previewArea.hidden=false;settings.open=false;previewArea.replaceChildren(el('h3',{},'Было → Станет'),el('p',{},'Сейчас: '+P.ruleText(result.current_rule.rule)),result.proposed_rule?el('p',{},'Будет: '+P.ruleText(result.proposed_rule.rule)):meta('Правило расписания сохраняется.'));
      const rows=result.rows||[];previewArea.append(el('p',{class:'pl-preview-summary'},'Выполнений в предпросмотре: '+rows.length+' · защищено: '+rows.filter(r=>r.disposition.startsWith('protected')).length+' · ручные переносы: '+rows.filter(r=>r.manual||r.disposition==='manual_exception').length));
      if(series.source_type==='project'&&rows.some(r=>r.disposition.startsWith('protected')||r.disposition==='manual_exception'))previewArea.append(el('p',{class:'pl-warning'},'Защищённые выполнения сохраняют свои даты и номера циклов. На новые даты этих циклов копии не создаются: возможны наложения и свободные недели. Проверьте сохранённые выполнения перед применением.'));
      for(const row of rows)previewArea.append(el('div',{class:'pl-preview-row'},el('strong',{},P.date(row.before_date)+' → '+P.date(row.after_date)),meta(changeLabels[row.disposition]||'Посмотрите изменения'),row.reason?meta(row.reason):null,row.manual && row.disposition!=='manual_exception'?meta('Ручной перенос сохранён'):null,row.pause_conflict?el('p',{class:'pl-overdue'},'Конфликт с паузой'):null,row.proposed_project?el('details',{},el('summary',{},'Состав проекта'),el('ul',{},row.proposed_project.tasks.map(t=>el('li',{},t.title+' · '+P.date(t.planned_date)+' · срок '+P.date(t.due_date))))):null));
      for(const issue of [...(result.conflicts||[]),...(result.calendar_errors||[]).map(e=>e.message)])previewArea.append(el('p',{role:'alert'},issue));
      apply.hidden=!!((result.conflicts||[]).length||(result.calendar_errors||[]).length);previewArea.append(el('div',{class:'pl-preview-confirm'},apply,button('Изменить настройки',()=>{settings.open=true;P.showPreview(settings);})));P.showPreview(previewArea);});
    const settings=el('details',{class:'pl-preview-settings',open:true},el('summary',{},'1. Настройка'),form);dialog.append(settings,previewArea);P.guardForm(form,'series-change:'+series.id, null,dialog,{previewOnly:true});
  };
})(window.Planner);
