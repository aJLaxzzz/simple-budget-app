(function (P) {
  'use strict';
  const { el, button, meta } = P;
  const taskFields = ['color_id','icon_id','title', 'description', 'project_id', 'parent_task_id', 'priority', 'planned_date', 'scheduled_time', 'due_date'];
  function changed(data, record) {
    return Object.fromEntries(Object.entries(data).filter(([key, value]) => value !== (record[key] ?? null)));
  }
  // Reapply only the fields actually edited locally; keep concurrent changes to other fields.
  function rebase(form, previous, latest) {
    for (const control of form.elements) {
      if (!control.name || (!(control.name in latest) && !['color_id','icon_id'].includes(control.name))) continue;
      if (control.value === String(previous[control.name] ?? '')) {
        const value = String(latest[control.name] ?? '');
        if (control.tagName === 'SELECT' && ![...control.options].some(option => option.value === value)) {
          control.append(el('option', { value }, P.labels[value] || value));
        }
        control.value = value;
      }
    }
  }
  function remember(form, key, snapshot) {
    form.addEventListener('input', () => P.drafts.set(key, { values: P.values(form), ...snapshot() }));
    form.addEventListener('change', () => P.drafts.set(key, { values: P.values(form), ...snapshot() }));
  }
  P.taskEditor = async function (route, isCurrent) {
    const originScroll=P.editorOrigin?.url===P.panelURL()?P.editorOrigin.scrollY:window.scrollY;
    const id = route.params.get('task');
    const context = id ? await P.api('/tasks/' + encodeURIComponent(id) + '/context') : null;
    const fresh = context ? context.task : { title: '', description: '', status: 'planned', priority: 'normal', project_id: route.params.get('for_project') || null, parent_task_id: route.params.get('parent') || null, planned_date: route.params.get('for_date') || null };
    if (!isCurrent()) return;
    const key = id ? 'task:' + id : 'new:' + (fresh.project_id || '') + ':' + (fresh.parent_task_id || '') + ':' + (fresh.planned_date || '');
    const draft = P.drafts.get(key);
    { // Fetch only this project for parent choices; task lists need no historical backlog.
      const projectID = draft?.values?.project_id ?? fresh.project_id;
      const tasks = projectID ? (await P.api('/tasks?project_id=' + encodeURIComponent(projectID))).items : [];
      if (!isCurrent()) return;
      const unique = new Map([...tasks, ...(context ? [fresh, ...context.children] : [])].map(task => [task.id, task]));
      P.data.tasks = [...unique.values()];
    }
    let record = draft?.record || fresh;
    let related = draft?.related || (context ? context.children : []);
    const value = draft?.values || record;
    const drawer = el('dialog', { class: 'planner pl-drawer pl-drawer-shell', 'aria-label': id ? 'Редактор задачи' : 'Новая задача' });
    const close = async () => { if (!drawer.querySelector('[data-busy="true"]') && (!drawer.plannerGuard || await drawer.plannerGuard())) {P.deferNavigation=null;P.closeEditor();} };
    drawer.addEventListener('cancel', e => { e.preventDefault(); close(); });
    const x = button('×', close, 'pl-icon'); x.setAttribute('aria-label', 'Закрыть редактор');
    drawer.append(el('header', { class: 'pl-drawer-head' }, el('div', {}, el('h2', {}, id ? 'Детали задачи' : 'Новая задача')), x));
    const body = el('div', { class: 'pl-drawer-body' }); drawer.append(body);
    const form = el('form', { 'aria-label': 'Редактор задачи', class: 'pl-edit-form' });
    P.appearanceField(form,{...record,...value},false,()=>({project:project?.value,parent:parent?.value}));
    const title = P.field(form, 'title', 'Название', 'textarea', value.title); title.required = true;
    title.rows=value.title?.length>60?2:1; title.classList.add('pl-task-name');
    const parameters=P.disclosure('Параметры');
    const description=P.field(parameters, 'description', 'Описание', 'textarea', value.description);
    const grid = el('div', { class: 'pl-form-grid' }); form.append(grid);
    const placement=P.disclosure('Проект и подзадача', P.data.projects.find(p=>p.id===value.project_id)?.title||'Без проекта');placement.open=false;
    const project = P.field(placement, 'project_id', 'Проект', 'select', value.project_id, [['', 'Без проекта'], ...P.data.projects.filter(p => !p.archived_at || p.id === record.project_id).map(p => [p.id, p.title + (p.archived_at ? ' · архив' : '')])]);
    const parent = P.field(placement, 'parent_task_id', 'Родительская задача', 'select', '', []);
    function parents(selected) {
      const choices = P.data.tasks.filter(t => t.project_id === project.value && !t.parent_task_id && t.id !== id && !related.length);
      parent.replaceChildren(el('option', { value: '' }, 'Самостоятельная задача'), ...choices.map(t => el('option', { value: t.id }, t.title)));
      parent.value = selected || '';
    }
    parents(value.parent_task_id);
    project.addEventListener('change', async () => {
      parents('');
      if (!project.value) return;
      const selected = project.value; parent.disabled = true;
      try {
        const tasks = await P.api('/tasks?project_id=' + encodeURIComponent(selected));
        if (!drawer.isConnected || project.value !== selected) return;
        P.data.tasks = [...P.data.tasks.filter(t => t.project_id !== selected), ...tasks.items]; parents('');
      } catch (error) { P.notify(error.message); }
      finally { if (project.value === selected) parent.disabled = false; }
    });
    const priority=P.field(parameters, 'priority', 'Приоритет', 'select', value.priority, ['low', 'normal', 'high'].map(k => [k, P.labels[k]]));
    if (P.active(record)) P.field(parameters, 'status', 'Статус', 'select', value.status, ['planned', 'in_progress'].map(k => [k, P.labels[k]]));

    const date = P.field(grid, 'planned_date', 'Плановая дата', 'date', value.planned_date);
    const time = P.field(grid, 'scheduled_time', 'Время · Москва', 'time', value.scheduled_time);
    time.addEventListener('input', () => { date.required = Boolean(time.value); }); date.required = Boolean(time.value);
    const due=P.field(parameters, 'due_date', 'Крайний срок', 'date', value.due_date);
    const warning=el('p',{class:'pl-note',role:'status'});grid.append(warning);
    const summarize=()=>{warning.textContent=date.value&&due.value&&date.value>due.value?'Плановая дата позже крайнего срока. Сохранение разрешено.':P.timing({...record,planned_date:date.value,due_date:due.value}).label;warning.hidden=!warning.textContent;parameters.hint.textContent=[priority.value!=='normal'?P.labels[priority.value]:null,due.value?'Крайний срок '+P.date(due.value):null,description.value.trim()?description.value.trim().slice(0,100):null].filter(Boolean).join(' · ')||'Приоритет, крайний срок, описание';placement.hint.textContent=[project.selectedOptions[0]?.textContent||'Без проекта',parent.value?parent.selectedOptions[0]?.textContent:null].filter(Boolean).join(' · ');};
    form.append(placement,parameters);form.addEventListener('input',summarize);form.addEventListener('change',summarize);summarize();
    if(record.occurrence_id){
      date.readOnly=true;project.disabled=true;parent.disabled=true;
      grid.append(button('Перенести выполнение',()=>P.runAction(record,'reschedule')));
      form.append(meta('Дата меняется через «Перенести выполнение»: для повторения по расписанию можно выбрать область изменения.'));
    }

    if (record.completed_date) form.append(meta('Фактически выполнено: ' + P.date(record.completed_date)));
    if (record.completed_with_open_children) form.append(el('p', { class: 'pl-note' }, 'Родитель завершён, подзадачи оставлены открытыми. Они продолжают учитываться в прогрессе проекта.'));
    const archived = P.data.projects.find(p => p.id === record.project_id)?.archived_at;
    if (archived) form.append(el('p', { class: 'pl-note' }, 'Задача в архивном проекте. Для редактирования сначала верните проект из архива через его меню.'));
    let builder,repeat;
    if(!id){
      const recurrence=el('section',{class:'pl-new-recurrence'});repeat=el('input',{type:'checkbox',name:'repeat',value:'yes',checked:value.repeat==='yes'});recurrence.append(el('label',{},repeat,' Повторять задачу'));const settings=el('div');recurrence.append(settings);form.append(recurrence);
      const toggle=()=>{settings.hidden=!repeat.checked;if(repeat.checked&&!date.value)date.value=P.today();if(repeat.checked&&!builder){
        builder=P.ruleBuilder(settings,{firstDate:date.value||P.today(),rule:value.unit?{unit:value.unit,interval:Number(value.interval),mode:value.mode,allowed_days:{mode:value.allowed,weekdays:[1,2,3,4,5,6,7].filter(d=>value['weekday_'+d])},adjustment:value.adjustment}:undefined});
        builder.first.parentElement.hidden=true;builder.first.required=false;
      } if(builder){builder.first.value=date.value||P.today();settings.querySelectorAll('input,select').forEach(c=>c.disabled=!repeat.checked);if(repeat.checked)builder.refresh();}};
      repeat.addEventListener('change',toggle);date.addEventListener('input',toggle);toggle();
    }
    let scopeProject=null,changeScope=null,scopeTurn=0,scopeLoading=false,scopeError=null;
    const loadScope=async(initial=false)=>{
      const turn=++scopeTurn,selected=project.value;scopeLoading=true;scopeError=null;
      changeScope?.closest('.pl-field')?.remove();changeScope=null;scopeProject=null;
      try{
        if(repeat)repeat.closest('.pl-new-recurrence').hidden=false;if(form.elements.status){form.elements.status.disabled=false;form.elements.status.closest('.pl-field').hidden=false;}
        if(selected&&!record.series_id){const current=await P.api('/projects/'+selected);if(repeat){repeat.closest('.pl-new-recurrence').hidden=Boolean(current.repeat_series_id);if(current.repeat_series_id)repeat.checked=false;}if(turn!==scopeTurn||!isCurrent())return;scopeProject=current;if(form.elements.status){const shared=Boolean(current.repeat_series_id&&P.projectContext(current));form.elements.status.disabled=shared;form.elements.status.closest('.pl-field').hidden=shared;}changeScope=P.projectScopeField(form,current,!id);if(changeScope){const footer=form.querySelector('.pl-form-footer');if(footer)form.insertBefore(changeScope.closest('.pl-field'),footer);}}
      }catch(e){if(turn===scopeTurn){scopeError=e;P.notify(P.errorMessage(e));}}
      finally{if(turn===scopeTurn)scopeLoading=false;}
    };
    await loadScope(true);if(!isCurrent())return;
    project.addEventListener('change',()=>loadScope());
    remember(form, key, () => ({ record, related }));
    const create = P.commandSender('/tasks'),createRepeat=P.commandSender('/series');
    P.bindForm(form, () => {
      if(scopeLoading)throw Error('Дождитесь проверки выбранного проекта');if(scopeError)throw scopeError;
      const values = P.nullable(P.values(form), ['color_id','icon_id','project_id', 'parent_task_id', 'planned_date', 'scheduled_time', 'due_date']);
      const data = Object.fromEntries(Object.entries(values).filter(([k]) => taskFields.includes(k) || k === 'status'));
      if(record.occurrence_id){delete data.project_id;delete data.parent_task_id;delete data.planned_date;}
      if(changeScope&&changeScope.value!=='this'){
        if(repeat?.checked)throw Error('Отдельное повторение дочерней задачи нельзя добавить в повторяющийся состав');
        if(data.project_id!==scopeProject.id||(id&&data.parent_task_id!==(record.parent_task_id||null)))throw Error('Перемещение между проектами и родителями выполняется отдельно');
        const parentID=data.parent_task_id;const patch=id?changed(data,record):{...data};
        if(id&&'status' in patch)throw Error('Измените статус отдельно для выбранного выполнения. Область относится к параметрам задачи.');
        for(const k of ['project_id','parent_task_id','status'])delete patch[k];
        return P.scopedProjectSave(scopeProject,{scope:changeScope.value,action:id?'edit':'add',...(id?{task_id:id,expected_task_revision:record.revision}:{parent_id:parentID||''}),patch}).then(r=>({...r.task||r.project?.tasks.find(t=>t.id===id)||record,scope_notice:r.scope_notice}));
      }
      if (id) {
        const patch = changed(data, record);
        if (Object.keys(patch).length && !record.occurrence_id && Object.keys(patch).every(key => ['planned_date', 'scheduled_time'].includes(key))) return P.api('/tasks/' + id + '/reschedule', 'POST', { ...patch, planned_date: data.planned_date, expected_revision: record.revision });
        return P.api('/tasks/' + id, 'PATCH', { ...patch, expected_revision: record.revision, related_revisions: P.revisions(related) });
      }
      if(repeat?.checked){const {planned_date,due_date,status,...task}=data;return createRepeat({task,first_date:planned_date||P.today(),first_due_date:due_date,first_status:status||'planned',rule:builder.read()}).then(s=>s.occurrences.find(o=>o.cycle_index===0).task);}
      return create(data);
    }, async saved => {
      P.drafts.delete(key);
      P.guardActive=null;P.deferNavigation=null;window.history.replaceState(null,'',P.panelURL());await P.refresh();window.scrollTo(0,originScroll);
      P.notify(saved.scope_notice||('Задача «'+saved.title+'» '+(id?'сохранена':'создана')));
    }, async () => {
      const latestContext = await P.api('/tasks/' + id + '/context');
      const latest = latestContext.task;
      return { record: latest, related: latestContext.children, reload() { P.drafts.delete(key); P.refresh(); }, accept() { rebase(form, record, latest); record = latest; related = latestContext.children; P.drafts.set(key, { values: P.values(form), record, related }); } };
    });
    body.append(form);
    P.guardForm(form,key,record,drawer);
    let periods,audit;
    if (id) {
      const actions = el('div', { class: 'pl-actions pl-editor-actions' });
      if(!archived)actions.append(P.active(record)?button('Завершить задачу',()=>P.runAction(record,'complete'),'pl-primary'):button('Восстановить задачу',()=>P.runAction(record,'restore')));
      actions.append(P.status(record),P.actionButton(record));

      body.insertBefore(actions,form);
      if (!record.parent_task_id && record.project_id) {
        const children = P.data.tasks.filter(t => t.parent_task_id === id);
        const section = el('section', { class: 'pl-editor-section' }, el('div', { class: 'pl-section-head' }, el('h3', {}, 'Подзадачи'), record.project_id && !archived ? button('+ Подзадача', () => P.newTask(record.project_id, id)) : null));
        section.append(children.length ? el('div',{class:'pl-task-list'},children.map(t=>P.taskRow(t))) : meta(record.project_id ? 'Разделите задачу на небольшие шаги.' : 'Чтобы добавить подзадачи, сначала выберите проект.'));
        body.append(section);
      }
      if(!archived)P.taskProperties(body,record);
      periods=P.disclosure('Периоды работы','Загрузка…');periods.open=false;const sessions=el('section',{class:'pl-editor-section'});periods.append(sessions);body.append(periods);
      P.renderSessions(sessions, record, !archived, periods);
      audit=P.disclosure('История','Изменения задачи');audit.open=false;audit.append(history('task',id));body.append(audit);
    }
    if(id){
      const main=el('section',{role:'tabpanel',id:'task-panel-main','aria-labelledby':'task-tab-main'});
      for(const child of [...body.children])if(child!==periods&&child!==audit)main.append(child);
      body.prepend(main);
      const tabs=el('div',{role:'tablist','aria-label':'Разделы задачи',class:'pl-editor-tabs'});
      drawer.querySelector('.pl-drawer-head').after(tabs);
      const panels=[['main','Основное',main],['periods','Периоды работы',periods],['history','История',audit]];
      for(const [key,label,panel]of panels){
        panel.id='task-panel-'+key;panel.setAttribute('role','tabpanel');panel.setAttribute('aria-labelledby','task-tab-'+key);
        if(key!=='main'){panel.querySelector('summary').hidden=true;panel.hidden=true;}
        const tab=button(label,()=>select(key));tab.id='task-tab-'+key;tab.setAttribute('role','tab');tab.setAttribute('aria-controls',panel.id);tabs.append(tab);
        tab.addEventListener('keydown',event=>{const keys=['ArrowLeft','ArrowRight','Home','End'];if(!keys.includes(event.key))return;event.preventDefault();const index=panels.findIndex(p=>p[0]===key),next=event.key==='Home'?0:event.key==='End'?2:(index+(event.key==='ArrowRight'?1:2))%3;select(panels[next][0]);tabs.children[next].focus();});
      }
      function select(key){
        panels.forEach(([name,,panel],index)=>{const selected=name===key;panel.hidden=!selected;if(name!=='main')panel.open=selected;tabs.children[index].setAttribute('aria-selected',String(selected));tabs.children[index].tabIndex=selected?0:-1;});
        body.scrollTop=0;
      }
      select('main');
      form.addEventListener('invalid',()=>select('main'),true);form.addEventListener('submit',()=>select('main'));
    }
    form.id='planner-task-editor-form';
    const footer=form.querySelector('.pl-form-footer');footer.classList.add('pl-drawer-footer');footer.querySelector('button[type=submit]').setAttribute('form',form.id);drawer.append(footer);
    P.trapFocus(drawer);document.body.append(drawer); drawer.showModal();
    document.body.classList.add('planner-open');
    title.focus();
  };
  P.projectEditor = function (route) {
    const originHash = window.location.hash;
    const id = route.params.get('id');
    const key = 'project:' + (id || 'new');
    const draft = P.drafts.get(key);
    let record = draft?.record || P.data.projects.find(p => p.id === id) || { title: '', description: '', status: 'planned' };
    const value = draft?.values || record;
    const { dialog, close } = P.dialog(id ? 'Редактирование проекта' : 'Новый проект');
    // Project forms are hash-addressable just like the task drawer.
    dialog.addEventListener('close', () => { if (P.route().params.has('edit') || P.route().params.get('new') === 'project') P.closeEditor(); });
    const form = el('form', { 'aria-label': 'Редактор проекта' });
    P.appearanceField(form,{...record,...value},true);
    P.field(form, 'title', 'Название проекта', 'text', value.title).required = true;
    P.field(form,'description','Описание','textarea',value.description);
    const parameters=P.disclosure('Даты и статус',[value.start_date?'Начало '+P.date(value.start_date):null,value.due_date?'Крайний срок '+P.date(value.due_date):null].filter(Boolean).join(' · '));parameters.open=route.params.has('dates');form.append(parameters);
    const grid = el('div', { class: 'pl-form-grid' }); parameters.append(grid);
    parameters.append(meta('Изменение дат проекта не переносит дочерние задачи.'));
    if (P.active(record)) P.field(parameters, 'status', 'Статус', 'select', value.status, ['planned', 'in_progress'].map(k => [k, P.labels[k]]));
    P.field(grid, 'start_date', 'Дата начала', 'date', value.start_date);
    P.field(grid, 'due_date', 'Крайний срок', 'date', value.due_date);
    remember(form, key, () => ({ record }));
    const create = P.commandSender('/projects');
    P.bindForm(form, async () => {
      const data = P.nullable(P.values(form), ['color_id','icon_id','start_date', 'due_date']);
      if(id&&(data.start_date!==record.start_date||data.due_date!==record.due_date))await P.confirmProjectOverlap(record,data.start_date,data.due_date);
      return id ? P.api('/projects/' + id, 'PATCH', { ...changed(data, record), expected_revision: record.revision }) : create(data);
    }, async saved => { P.drafts.delete(key); dialog.remove(); if (window.location.hash === originHash) P.navigate('#/plans/projects/' + saved.id); P.notify('Проект «'+saved.title+'» сохранён'); }, async () => {
      const latest = await P.api('/projects/' + id);
      return { record: latest, reload() { P.drafts.delete(key); P.refresh(); }, accept() { rebase(form, record, latest); record = latest; P.drafts.set(key, { record, values: P.values(form) }); } };
    });
    dialog.append(form);P.guardForm(form,key,record,dialog);form.elements.title.focus();
  };
  P.projectAction = function (project, action) {
    const titles = { complete: 'Завершить проект?', cancel: 'Отменить проект?', archive: 'Переместить проект в архив?' };
    const { dialog, close } = P.dialog(titles[action]);
    dialog.append(el('p', {}, action === 'archive' ? 'Проект и его история сохранятся. Найти его можно через фильтр «Архив», а вернуть — действием «Вернуть из архива». Статус выполнения при этом не изменится.' : 'Изменится только статус проекта. Задачи и их периоды работы сохранят свои состояния.'));
    const form = el('form',{'data-submit':{complete:'Завершить проект',cancel:'Отменить проект',archive:'Архивировать проект'}[action]});
    const send=P.commandSender('/projects/'+project.id+'/'+action);
    P.bindForm(form, () => send({ expected_revision: project.revision }), async () => { close(true); P.notify('Проект «'+project.title+'» '+({complete:'завершён',cancel:'отменён',archive:'перемещён в архив'}[action])); await P.refresh(); }, async () => {
      const latest = await P.api('/projects/' + project.id);
      return { record: latest, accept() { project = latest; } };
    });
    dialog.append(form);
  };
  P.taskAction = async function (task, action) {
    const titles = { complete: 'Завершение задачи', cancel: 'Отмена задачи', restore: 'Восстановление задачи' };
    const { dialog, close } = P.dialog(titles[action]);
    dialog.append(el('p', { class: 'pl-description' }, task.title));
    const loading = el('p', { role: 'status' }, 'Проверяем подзадачи и периоды работы…'); dialog.append(loading);
    let children = [], sessions = [];
    try {
      const context = await P.api('/tasks/' + task.id + '/context');
      if (!dialog.isConnected) return;
      task = context.task;
      children = context.children.filter(P.active);
      sessions = context.sessions;
    } catch (error) { loading.textContent = error.message; dialog.append(button('Повторить', () => { close(true); P.taskAction(task, action); })); return; }
    loading.remove();
    const form = el('form', { 'aria-label': titles[action], 'data-submit':{complete:'Завершить задачу',cancel:'Отменить задачу',restore:'Восстановить задачу'}[action] });
    let actual, decision;
    if (action === 'complete') {
      actual = P.field(form, 'completed_date', 'Фактическая дата выполнения', 'date', P.today()); actual.required = true; actual.max = P.today();
      if (children.length) {
        const options = [['', 'Выберите действие'], ['complete', 'Завершить вместе с подзадачами'], ['keep_open', 'Завершить только родителя']];
        decision = P.field(form, 'children_decision', 'Открытые подзадачи: ' + children.length, 'select', '', options); decision.required = true;
        form.append(el('ul', { class: 'pl-affected-children' }, children.map(child => el('li', {}, child.title))));
        form.append(el('p', { class: 'pl-note' }, 'При завершении только родителя подзадачи останутся открытыми и продолжат учитываться в прогрессе.'));
      }
    }
    if (action === 'restore') form.append(el('p', {}, 'Задача вернётся в запланированные. История сохранится; отменённые периоды работы не восстанавливаются автоматически.'));
    if (action === 'cancel') form.append(el('p', {}, 'Задача останется в истории. Статус подзадач не изменится.'));
    const consentArea = el('div'); form.append(consentArea);
    let affected = [], consent;
    function updateConsent() {
      const ids = new Set([task.id, ...(decision?.value === 'complete' ? children.map(c => c.id) : [])]);
      affected = action === 'restore' ? [] : sessions.filter(s => ids.has(s.task_id));
      consentArea.replaceChildren(); consent = null;
      if (affected.length) {
        consent = el('input', { type: 'checkbox', required: true, name: 'sessions_consent' });
        consentArea.append(el('div', { class: 'pl-note' }, el('strong', {}, 'Активные периоды работы: ' + affected.length), el('ul', {}, affected.map(s => el('li', {}, children.find(t => t.id === s.task_id)?.title || task.title, ' · ', P.date(s.date), s.start_time ? ' · ' + s.start_time + '–' + s.end_time : ''))), el('label', { class: 'pl-checkbox' }, consent, 'Отменить эти периоды работы и сохранить их в истории')));
      }
    }
    decision?.addEventListener('change', updateConsent); updateConsent();
    P.bindForm(form, () => P.api('/tasks/' + task.id + '/' + action, 'POST', {
      expected_revision: task.revision,
      related_revisions: P.revisions(children),
      ...(actual ? { completed_date: actual.value } : {}),
      ...(decision ? { children_decision: decision.value } : {}),
      ...(affected.length && consent.checked ? { sessions_decision: 'cancel', session_revisions: P.revisions(affected) } : {})
    }), async saved => {
      const draft = P.drafts.get('task:' + task.id);
      if (draft) { draft.record = saved; draft.values.status = saved.status; delete draft.related; }
      close(true); P.notify('Задача «'+task.title+'» '+({complete:'завершена',cancel:'отменена',restore:'восстановлена'}[action])); await P.refresh(); }, async () => {
      const latest = await P.api('/tasks/' + task.id);
      // Decisions and revisions for compound mutations must be reviewed again, not silently rebased.
      return { record: latest, accept() {
        const selectedDate = actual?.value;
        close(true);
        P.taskAction(latest, action).then(() => {
          const field = document.querySelector('.pl-dialog [name="completed_date"]');
          if (field && selectedDate) field.value = selectedDate;
        });
      } };
    });
    form.append(button('Отмена действия', () => close())); dialog.append(form);
  };
  P.renderSessions = async function (section, task, editable, disclosure) {
    section.replaceChildren(el('div', { class: 'pl-section-head' }, P.active(task) && editable ? button('+ Период работы', () => sessionForm(task)) : null), meta('Загрузка…'));
    try {
      const result = await P.api('/sessions?task_id=' + encodeURIComponent(task.id));
      if (!section.isConnected) return;
      section.lastChild.remove();
      if(disclosure){const next=result.items.filter(P.active).sort((a,b)=>a.date.localeCompare(b.date))[0];disclosure.hint.textContent=result.items.length?result.items.length+(next?' · ближайший: '+P.date(next.date):' · все закрыты'):'Не запланированы';}
      if (!result.items.length) section.append(meta('Выделите время на работу над задачей.'));
      for (const session of result.items) {
        const actions = el('div', { class: 'pl-actions' });
        if (editable && P.active(task)) {
          if (P.active(session)) actions.append(button('Перенести / изменить', () => P.reschedule({...session,type:'session',title:task.title})), button('Выполнить период работы', () => sessionAction(session, 'complete')), button('Отменить период работы', () => sessionAction(session, 'cancel')));
          else if (['completed', 'cancelled'].includes(session.status)) actions.append(button('Восстановить период работы', () => sessionAction(session, 'restore')));
        }
        section.append(el('div', { class: 'pl-session' }, el('div', { class: 'pl-section-head' }, el('strong', {}, P.appearanceIcon(task),P.date(session.date)), P.status(session)), meta(session.start_time ? session.start_time + '–' + session.end_time + ' · Москва' : 'Без времени'), session.completed_date ? el('p', { class: 'pl-muted' }, 'Выполнено ' + P.date(session.completed_date)) : null, actions, el('details',{},el('summary',{},'История периода'),history('session',session.id))));
      }
    } catch (error) { section.lastChild.remove(); section.append(el('p', { role: 'alert' }, error.message), button('Повторить загрузку периодов работы', () => P.renderSessions(section, task, editable))); }
  };
  function sessionForm(task, session) {
    const create = P.commandSender('/sessions');
    const { dialog, close } = P.dialog(session ? 'Перенести или изменить период работы' : 'Новый период работы');
    const form = el('form', { 'aria-label': 'Редактор периода работы' });
    P.field(form, 'date', 'Дата периода работы', 'date', session?.date || P.today()).required = true;
    const grid = el('div', { class: 'pl-form-grid' }); form.append(grid);
    const start = P.field(grid, 'start_time', 'Начало · Москва', 'time', session?.start_time);
    const end = P.field(grid, 'end_time', 'Окончание · Москва', 'time', session?.end_time);
    const validate = () => { start.required = Boolean(end.value); end.required = Boolean(start.value); end.setCustomValidity(start.value && end.value && end.value <= start.value ? 'Окончание должно быть позже начала' : ''); };
    start.addEventListener('input', validate); end.addEventListener('input', validate); validate();
    form.append(meta('Оставьте оба поля времени пустыми, чтобы запланировать только день. Завершение периода работы не завершает задачу.'));
    P.bindForm(form, () => {
      const data = P.nullable(P.values(form), ['start_time', 'end_time']);
      return session ? P.api('/sessions/' + session.id + '/reschedule', 'POST', { ...data, expected_revision: session.revision }) : create({ ...data, task_id: task.id, timezone: 'Europe/Moscow' });
    }, async () => { close(true); P.notify('Период работы «'+task.title+'» сохранён'); await P.refresh(); }, async () => {
      const latest = await P.api('/sessions/' + session.id);
      return { record: latest, accept() { rebase(form, session, latest); session = latest; } };
    }); dialog.append(form);
  }
  function sessionAction(session, action) {
    const { dialog, close } = P.dialog({ complete: 'Завершение периода работы', cancel: 'Отмена периода работы', restore: 'Восстановление периода работы' }[action]);
    const form = el('form',{'data-submit':{complete:'Завершить период работы',cancel:'Отменить период работы',restore:'Восстановить период работы'}[action]});
    if (action === 'complete') {
      const date = P.field(form, 'completed_date', 'Фактическая дата выполнения', 'date', P.today()); date.required = true; date.max = P.today();
    }
    form.append(el('p', {}, 'Статус и даты задачи не изменятся. Событие останется в истории.'));
    P.bindForm(form, () => P.api('/sessions/' + session.id + '/' + action, 'POST', { ...P.values(form), expected_revision: session.revision }), async () => { close(true); P.notify('Период работы на '+P.date(session.date)+' '+({complete:'завершён',cancel:'отменён',restore:'восстановлен'}[action])); await P.refresh(); }, async () => {
      const latest = await P.api('/sessions/' + session.id);
      return { record: latest, accept() { rebase(form, session, latest); session = latest; } };
    }); dialog.append(form);
  }
  P.sessionAction = sessionAction;
  P.renderHistory = (host, kind, id) => host.append(history(kind, id));
  function history(kind, id) {
    const section = el('section', { class: 'pl-editor-section' });
    const list = el('div', { class: 'pl-history' });
    const actions = {project_mode_changed:'Выполнения стали независимыми',repeat_disabled:'Повторение отключено',completion_undone:'Завершение отменено',appearance_changed:'Оформление повторения изменено',unarchived:'Возвращено из архива',occurrence_deleted:'Выполнение удалено навсегда',repeat_deleted:'Повторение удалено навсегда',saved_as_template:'Сохранено как шаблон',created_from_template:'Создано из шаблона',version_created:'Новая версия шаблона',instantiated:'Создано из шаблона',archived:'Архивировано', created: 'Создано', edited: 'Изменено', status_changed: 'Изменён статус', rescheduled: 'Перенесено', completed: 'Завершено', cancelled: 'Отменено', restored: 'Восстановлено', moved_project: 'Перемещено в проект' };
    let offset = 0, loaded = false;
    const load = button('Показать ещё', async () => {
      load.disabled = true;
      try {
        const data = await P.api('/history?object_type=' + kind + '&object_id=' + encodeURIComponent(id) + '&limit=10&offset=' + offset);
        if (!data.items.length && offset === 0) list.append(meta('История пока пуста.'));
        for (const event of data.items) {
          const stamp = new Intl.DateTimeFormat('ru-RU', { timeZone: 'Europe/Moscow', dateStyle: 'medium', timeStyle: 'short' }).format(new Date(event.created_at));
          const details = el('details', {}, el('summary', {}, (actions[event.action] || 'Изменение') + ' · ' + stamp + ' МСК'));
          const changes = event.changes || {};
          for (const [key, delta] of Object.entries(changes)) {
            const labels = { color_id:'Цвет',icon_id:'Иконка',title: 'Название', description: 'Описание', status: 'Статус', priority: 'Приоритет', planned_date: 'Плановая дата', due_date: 'Крайний срок', completed_date: 'Фактическая дата', project_id: 'Проект', parent_task_id: 'Родитель', scheduled_time: 'Время', completed_at: 'Запись завершения' };
            if (!labels[key]) continue;
            if(key==='icon_id'){details.append(el('p',{},'Иконка: ',delta.before?P.appearanceIcon({icon_id:delta.before}):'Наследовать',' → ',delta.after?P.appearanceIcon({icon_id:delta.after}):'Наследовать'));continue;}
            const show = v => (key==='color_id'?(P.palette[v]?.[0]||'Наследовать'):null)||P.labels[v] || (v == null ? '—' : String(v));
            details.append(el('p', {}, labels[key] + ': ' + show(delta.before) + ' → ' + show(delta.after)));
          }
          list.append(details);
        }
        loaded=true;offset += data.items.length; load.textContent = 'Показать ещё'; load.hidden = data.items.length < 10;
      } catch (error) { list.append(el('p',{role:'alert'},P.errorMessage(error)));load.hidden=false;load.textContent='Повторить загрузку'; }
      finally { load.disabled = false; }
    });
    load.hidden=true;section.classList.add('pl-history-section');section.append(list, load);
    const open=()=>{if(!loaded&&!load.disabled)load.click();};
    queueMicrotask(()=>{const details=section.closest('details');if(details){details.addEventListener('toggle',()=>{if(details.open)open();});if(details.open)open();}else if(section.isConnected)open();});
    return section;
  }
})(window.Planner);
