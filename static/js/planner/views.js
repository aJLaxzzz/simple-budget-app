(function (P) {
  'use strict';
  const { el, button, meta } = P;
  let generation = 0;
  let host, mountedHash;
  const screenKey = hash => { const [path, query] = (hash || '').split('?'); const params = new URLSearchParams(query); ['task','new','edit','id','for_project','parent','for_date'].forEach(k => params.delete(k)); return path + '?' + params; };
  P.unmount = function () {
    P.guardActive = null; P.deferNavigation = null;
    generation++;
    document.querySelectorAll('.pl-drawer-shell, .pl-dialog').forEach(n => n.remove());
    document.body.classList.remove('planner-open', 'planner-page', 'planner-calendar');
  };
  P.refresh = async (background = false) => {
    if (!window.location.hash.startsWith('#/plans')) return;
    const task = P.route().params.get('task');
    const selected = document.querySelector('.pl-editor-tabs [aria-selected="true"]')?.id;
    if (await P.mount(host, background === true) === false) return false;
    // Refresh the same open card after a command without sending the user back to Main.
    // Opening another card still starts on Main and does not fetch its history.
    if (task && task === P.route().params.get('task') && selected) {
      const tab = document.getElementById(selected); tab?.click();
      if (selected !== 'task-tab-main') tab?.focus();
    }
    return true;
  };
  P.mount = (root, background = false) => window.budgetViews.run(async () => {
    const returning=P.editorReturn || (document.querySelector('.pl-editor-shell') ? P.editorOrigin : null);
    const result=await mount(root, background === true);
    if(returning?.url===location.hash&&!document.querySelector('.pl-editor-shell')){
      // Restore after reconciliation releases inert and native dialog focus.
      if(returning.focus?.isConnected)returning.focus.focus({preventScroll:true});
      window.scrollTo(returning.scrollX||0,returning.scrollY||0);
      P.editorReturn=null;P.editorOrigin=null;
    }
    return result;
  }, background === true);
  async function mount(root, background) {
    const sameView = host === root && screenKey(mountedHash) === screenKey(location.hash) && root.querySelector('.planner');
    if (sameView) generation++; else P.unmount();
    host = root;
    const tx = window.budgetViews.capture();
    const previousPanels = sameView ? [...document.querySelectorAll('.pl-drawer-shell,.pl-dialog')] : [];
    // Keep the old view visible while a local save is read back, but do not
    // offer commands captured from its previous revision. Background SSE reads
    // stay interactive and are discarded if the user starts interacting.
    const held = sameView && !background ? [root.querySelector('.planner'), ...previousPanels].map(n => [n, n.inert]) : [];
    held.forEach(([n]) => { n.inert = true; });
    const turn = generation;
    document.body.classList.add('planner-page');
    const route = P.route();if(route.view!=='series'||!route.id)P.repeatAutoOpenedRoute=null;
    const page = el('section', { class: 'planner', 'aria-label': 'Планы' });
    const chrome=el('div',{class:'pl-navigation'}); const navReady = P.navigation(chrome, route); page.append(chrome);
    const content = el('div', { class: 'pl-content', 'aria-live': 'polite' }, el('p', { class: 'pl-loading', role: 'status' }, 'Загружаем планы…'));
    page.append(content);
    const previous = sameView ? root.querySelector('.planner') : null;
    if (!previous) root.replaceChildren(page);
    let committed = false;
    const commit = async () => {
      await navReady;
      window.budgetViews.check(tx);
      if (committed || generation !== turn) return;
      committed = true;
      mountedHash = location.hash;
      if (sameView) {
        previousPanels.forEach(n => n.remove());
        if (!document.querySelector('.pl-drawer-shell')) document.body.classList.remove('planner-open');
      }
      if (previous?.isConnected) {
        P.reuseRows(previous, page);
        const oldChrome = previous.querySelector('.pl-navigation');
        if (sameView && oldChrome?.isEqualNode(chrome)) chrome.replaceWith(oldChrome);
        const viewport=sameView?{x:scrollX,y:scrollY,positions:[...previous.querySelectorAll('.pc-time-scroll,.pc-calendar,.pc-allday-items')].map(n=>({selector:n.matches('.pc-allday-items')?'[data-allday="'+n.closest('[data-allday]').dataset.allday+'"] .pc-allday-items':n.matches('.pc-time-scroll')?'.pc-time-scroll':'.pc-calendar',top:n.scrollTop,left:n.scrollLeft}))}:null;
        root.replaceChildren(page);
        if(viewport){for(const pos of viewport.positions){const n=page.querySelector(pos.selector);if(n){n.scrollTop=pos.top;n.scrollLeft=pos.left;}}scrollTo(viewport.x,viewport.y);}
      }
    };
    try {
      if (route.view === 'calendar') {
        document.body.classList.add('planner-calendar');
        const ready = await P.renderCalendar(content, route, () => generation === turn);
        if (generation !== turn) return;
        await commit();
        content.budgetAfterMount?.();
        if (ready === false) return;
        if (route.params.has('task') || route.params.get('new') === 'task') await P.taskEditor(route, () => generation === turn);
        return;
      }
      if (['series','templates','task-templates','attention','settings'].includes(route.view) || (['tasks','projects'].includes(route.view) && route.params.get('view')==='regular')) {
        const render = route.params.get('view')==='regular' ? P.renderSeries : {series:P.renderSeries, templates:P.renderTemplates, 'task-templates':P.renderTaskTemplates, attention:P.renderAttention, settings:P.renderCalendarSettings}[route.view];
        await render(content, route);
        await commit();
        if (generation === turn && (route.params.has('task')||route.params.get('new')==='task')) {
          const [tasks,projects,archive]=await Promise.all([P.api('/tasks?status=active'),P.api('/projects'),P.api('/projects?archived=true')]);
          if(generation!==turn)return; P.data={tasks:tasks.items||[],projects:[...(projects.items||[]),...(archive.items||[])]};
          await P.taskEditor(route, () => generation === turn);
        }
        return;
      }
      const selectedStatus = route.params.get('status') || 'active';
      const [tasks, projects, archive, repeatLimit] = await Promise.all([P.api('/tasks'+(selectedStatus==='all'||route.view==='tasks'?'':'?status='+encodeURIComponent(selectedStatus))), P.api('/projects'), P.api('/projects?archived=true'),P.api('/settings/project-repeat-limit')]);
      P.repeatLimit=repeatLimit.limit;
      if (generation !== turn) return;
      P.data = { tasks: tasks.items || [], projects: [...(projects.items || []), ...(archive.items || [])] };
      content.replaceChildren();
      if(route.view==='tasks' && !route.id && route.params.get('view')!=='undated' && selectedStatus==='active'){
        const series=await P.seriesList();let added=false;
        for(const s of series.filter(s=>s.source_type==='task'&&s.state==='active')){
          const count=P.data.tasks.filter(t=>P.active(t)&&t.series_id===s.id&&t.planned_date>=P.today()&&(!t.due_date||t.due_date>=P.today())).length;
          if(count>=P.repeatLimit)continue;
          try{const result=await P.api('/series/'+s.id+'/ensure-upcoming','POST',{});added ||= result.created>0; }catch(e){console.warn('Повторение требует внимания',s.id,e.message);}
        }
        if(added)P.data.tasks=(await P.api('/tasks')).items;
        if(generation!==turn)return;
      }
      if (!route.params.has('task') && !route.params.has('new') && P.updateTaskList?.connected() && P.updateTaskList.key === location.hash && P.updateTaskList.projects === JSON.stringify(P.data.projects) && P.updateTaskList.repeatLimit === P.repeatLimit && previous?.isConnected) {
        await navReady;
        window.budgetViews.check(tx);
        P.updateTaskList.draw();
        const oldChrome = previous.querySelector('.pl-navigation');
        if (!oldChrome.isEqualNode(chrome)) oldChrome.replaceWith(chrome);
        committed = true; mountedHash = location.hash;
      } else if (route.view === 'projects' && !route.id && !route.params.has('new') && P.updateProjects?.key === location.hash && P.updateProjects.connected()) {
        await navReady;
        window.budgetViews.check(tx);
        P.updateProjects.draw(); committed = true; mountedHash = location.hash;
        const oldChrome = previous.querySelector('.pl-navigation');
        if (!oldChrome.isEqualNode(chrome)) oldChrome.replaceWith(chrome);
      } else if (route.view === 'projects' && route.id) {
        const project = await P.api('/projects/' + encodeURIComponent(route.id));
        if (generation !== turn) return;
        P.data.tasks = project.tasks;
        renderProject(content, project);
      } else if (route.view === 'projects') renderProjects(content);
      else renderTasks(content, route);
      if (generation !== turn) return;
      await commit();
      if (route.params.has('task') || route.params.get('new') === 'task') await P.taskEditor(route, () => generation === turn);
      if (route.params.get('edit') === 'project' || route.params.get('new') === 'project') P.projectEditor(route);
    } catch (error) {
      if (error.superseded) throw error;
      if (generation !== turn) return;
      content.replaceChildren(el('div', { class: 'pl-empty' }, el('h2', {}, 'Не удалось загрузить планы'), el('p', {}, error.message), button('Попробовать снова', P.refresh)));
      await commit();
    } finally { held.forEach(([n, inert]) => { n.inert = inert; }); }
  };
  // Only reuse self-contained rows with identical command inputs, including
  // expected_revision. Never graft new handlers onto an old captured closure.
  P.reuseRows = (before, after) => {
    const groups=new Map([...before.querySelectorAll('[data-project-group]')].map(n=>[n.dataset.projectGroup,n]));
    for(const node of after.querySelectorAll('[data-project-group]')){const old=groups.get(node.dataset.projectGroup);if(old?.budgetIdentity===node.budgetIdentity&&old.isEqualNode(node))node.replaceWith(old);}

    const rows = new Map([...before.querySelectorAll('[data-task-id],.pc-event[data-event-id]')].map(n => [n.dataset.taskId || n.dataset.eventId, n]));
    for (const n of after.querySelectorAll('[data-task-id],.pc-event[data-event-id]')) {
      const old = rows.get(n.dataset.taskId || n.dataset.eventId);
      if (old?.budgetIdentity && old.budgetIdentity === n.budgetIdentity && old.isEqualNode(n)) n.replaceWith(old);
    }
  };
  P.empty = (title, text) => el('div', { class: 'pl-empty' }, el('span', { class: 'pl-empty-symbol', 'aria-hidden': 'true' }, '◇'), el('h3', {}, title), el('p', {}, text));
  P.quickForm = function (project, parent, projectRecord) {
    const key = 'quick:' + (project || '') + ':' + (parent || '');
    const form = el('form', { class: 'pl-quick', 'aria-label': 'Быстрое создание задачи', 'data-submit': 'Добавить' });
    const input = el('input', { name: 'title', placeholder: parent ? 'Название подзадачи…' : 'Что нужно сделать?', 'aria-label': 'Название новой задачи', autocomplete: 'off', value: P.drafts.get(key) || '' });
    input.addEventListener('input', () => P.drafts.set(key, input.value));
    form.append(input);
    const scope=projectRecord?.repeat_series_id?P.projectScopeField(form,projectRecord,true):null;
    form.addEventListener('submit',event=>{if(!input.value.trim()){event.preventDefault();event.stopImmediatePropagation();P.newTask(project,parent,project?null:P.today());}});
    const create = P.commandSender('/tasks');
    P.bindForm(form, () => scope&&scope.value!=='this'?P.scopedProjectSave(projectRecord,{scope:scope.value,action:'add',parent_id:parent||'',patch:{title:input.value.trim()}}).then(r=>({...r.task,scope_notice:r.scope_notice})):create( { title: input.value.trim(), ...(!project&&P.route().view==='tasks'&&P.route().params.get('view')!=='undated'?{planned_date:P.today()}:{}), ...(project ? { project_id: project } : {}), ...(parent ? { parent_task_id: parent } : {}) }), async saved => {
      P.drafts.delete(key); input.value=''; P.notify(saved.scope_notice||('Задача «'+saved.title+'» добавлена'), saved.id?{label:'Открыть',run:()=>P.openTask(saved.id)}:undefined);
      if(['tasks','inbox'].includes(P.route().view)){const q=new URLSearchParams(P.route().params);['q','status','project'].forEach(k=>q.delete(k));P.replaceRoute(P.base()+(q.size?'?'+q:''));}
      await P.refresh();

      document.querySelector('.pl-quick input')?.focus();
    });
    const add=form.querySelector('button[type=submit]');add.setAttribute('aria-label','Добавить');add.classList.add('pl-quick-add');add.replaceChildren(el('span',{class:'pl-quick-label'},'Добавить'),el('span',{class:'pl-quick-plus','aria-hidden':'true'},'+'));
    return form;
  };
  P.taskRow = function (task, child = false, context = '', presentation = {}) {
    const row = el('div', { class: 'pl-task' + (child ? ' pl-child' : '') + (P.active(task) ? '' : ' pl-terminal')+(presentation.parentID?' pl-group-child':''), 'data-task-id': task.id });
    row.budgetIdentity = JSON.stringify([task, child, context, presentation, P.data.projects.find(p => p.id === task.project_id)]);
    const complete = button(task.status === 'completed' ? '✓' : task.status === 'cancelled' ? '−' : '', () => P.runAction(task, 'complete'), 'pl-check');
    complete.setAttribute('aria-label', 'Завершить: ' + task.title);
    complete.disabled = task.project_pending || !P.active(task) || Boolean(P.data.projects.find(p => p.id === task.project_id)?.archived_at);
    const title = button(task.title, () => P.openTask(task.id), 'pl-task-title');
    const details = [(task.dynamic||task.project_pending)&&P.active(task)&&meta('Дата может измениться'),task.series_id && el('span', {class:'pl-repeat-mark', title:'Повторяющаяся задача', 'aria-label':'Повторяющаяся задача'}, '↻'), context && meta(context), task.priority === 'high' && el('span', { class: 'pl-priority' }, '↑ Высокий'), task.priority === 'low' && meta('↓ Низкий'), task.planned_date && meta(((presentation.dateInHeading||['today','tomorrow'].includes(presentation.section))?'':P.date(task.planned_date)) + (task.scheduled_time ? ((presentation.dateInHeading||['today','tomorrow'].includes(presentation.section))?'':' · ') + task.scheduled_time : '')), task.due_date && el('span', { class: P.overdue(task) ? 'pl-overdue' : 'pl-muted' }, 'Крайний срок: ' + P.date(task.due_date)), task.completed_with_open_children && meta('Есть открытые подзадачи')].filter(Boolean);
    P.paint(row,task);const icon=P.appearanceIcon(task);if(!task.icon_id&&(task.effective_icon_id||'lucide:circle-check')==='lucide:circle-check')icon.classList.add('pl-neutral-icon');title.prepend(icon);
    const timing=P.timing(task);if(timing.label)details.push(el('span',{class:timing.key==='overdue'?'pl-overdue':'pl-plan-missed'},timing.label));
    row.append(complete, el('div', { class: 'pl-task-copy' }, title, presentation.parentID?el('span',{class:'pl-parent-context','data-parent-id':presentation.parentID},'↳ Подзадача · '+presentation.parentTitle):null,el('div', { class: 'pl-task-meta' }, details,task.status==='planned'?null:P.status(task))), P.actionButton(task));
    return row;
  };
  P.taskTree = function (tasks) {
    const list = el('div', { class: 'pl-task-list' });
    const ids = new Set(tasks.map(t => t.id));
    for (const task of tasks.filter(t => !t.parent_task_id || !ids.has(t.parent_task_id))) {
      const parent = P.data.tasks.find(t => t.id === task.parent_task_id);
      list.append(P.taskRow(task, false, parent ? 'Подзадача · ' + parent.title : ''));
      for (const child of tasks.filter(t => t.parent_task_id === task.id)) list.append(P.taskRow(child, true));
    }
    return list;
  };
  function renderTasks(content, route) {
    const inbox = route.view === 'inbox';
    const undated = route.params.get('view') === 'undated';
    if(!inbox)content.classList.add('pl-task-screen');
    content.append(el('div', { class: 'pl-section-head' }, el('div', {}, el('h2', {}, inbox ? 'Входящие' : undated ? 'Без даты' : 'Все задачи'), el('p', { class: 'pl-muted' }, inbox ? 'Соберите мысли здесь. Дату и проект можно выбрать позже.' : 'Всё, над чем вы работаете, в одном месте.')), inbox ? button('+ Задача', () => P.newTask(), 'pl-primary') : null));
    content.append(P.quickForm());
    const filters = el('form', { class: 'pl-filters', 'aria-label': 'Фильтры задач' });
    filters.addEventListener('submit', e => e.preventDefault());
    const search = P.field(filters, 'q', 'Поиск по названию', 'search', route.params.get('q') || '');
    const status = P.field(filters, 'status', 'Статус', 'select', route.params.get('status') || 'active', [['active', 'Открытые'], ['all', 'Все статусы'], ...Object.keys(P.labels).filter(k => ['planned', 'in_progress', 'completed', 'cancelled'].includes(k)).map(k => [k, P.labels[k]])]);
    const showAll=P.field(filters,'future','Будущие повторения задач','select',route.params.get('future')||'nearest',[['nearest',(P.repeatLimit||3)+' ближайших выполнений'],['all','Все выполнения']]);
    let project;
    if (!inbox) project = P.field(filters, 'project', 'Проект', 'select', route.params.get('project') || 'all', [['all', 'Все проекты'], ['none', 'Без проекта'], ...P.projectFilterOptions(P.data.projects,route.params.get('project'))]);

    const results = el('div', { class: 'pl-results' });
    const draw = () => {
      let tasks = P.data.tasks.filter(t => !P.data.projects.some(p=>p.id===t.project_id&&p.archived_at) && (!t.project_pending || !P.active(t) || showAll.value==='all') && (!inbox || (!t.project_id && !t.planned_date)) && (!undated || (!t.planned_date && !t.due_date)));
      tasks = tasks.filter(t => (status.value === 'all' || (status.value === 'active' ? P.active(t) : t.status === status.value)) && t.title.toLocaleLowerCase('ru').includes(search.value.toLocaleLowerCase('ru')) && (!project || P.matchesProject(t.project_id,project.value)));
      const total=tasks.length;tasks=P.limitUpcoming(tasks,inbox||showAll.value==='all');
      const fresh = el('div');
      fresh.replaceChildren(el('p', { class: 'pl-list-count' }, 'Задач: ' + tasks.length), tasks.length ? (inbox ? P.taskTree(tasks) : P.groupedTasks(tasks,Boolean(search.value.trim()),P.data.tasks.filter(t=>!project||P.matchesProject(t.project_id,project.value)))) : P.empty(inbox ? 'Входящие разобраны' : 'Задач не найдено', inbox ? 'Новая идея? Добавьте её одной строкой выше.' : 'Измените фильтры или создайте первую задачу.'));
      P.reuseRows(results, fresh);
      results.replaceChildren(...fresh.childNodes);
    };
    P.updateTaskList = {key: location.hash, repeatLimit:P.repeatLimit, projects: JSON.stringify(P.data.projects), connected: () => results.isConnected, draw};
    let loadedStatus = status.value, loadTurn = 0;
    filters.addEventListener('input', async event => {
      if(event.target.name==='group_projects')return;
      const params = new URLSearchParams(window.location.hash.split('?')[1] || '');
      params.set('q', search.value); params.set('status', status.value);params.set('future',showAll.value);
      if (project) params.set('project', project.value);
      // Filters survive reload and editor back/forward without creating one history entry per keystroke.
      P.replaceRoute(P.base() + '?' + params);
      const turn=++loadTurn;
      if(inbox&&loadedStatus!==status.value){
        results.replaceChildren(el('p',{class:'pl-loading'},'Загружаем задачи…'));
        try {
          const selected=status.value;
          const data=await P.api('/tasks'+(selected==='all'?'':'?status='+encodeURIComponent(selected)));
          if(turn!==loadTurn || !content.isConnected)return;
          P.data.tasks=data.items; loadedStatus=selected;
        }catch(e){if(turn===loadTurn)results.replaceChildren(el('p',{role:'alert'},e.message),button('Попробовать снова',P.refresh));return;}
      }
      updateFilterSummary();draw();
    });
    const filterSummary=el('summary',{},'Поиск и фильтры');
    const reset=button('Сбросить фильтры',()=>{search.value='';status.value='active';showAll.value='nearest';if(project)project.value='all';search.dispatchEvent(new Event('input',{bubbles:true}));});
    const filterPanel=el('details',{class:'pl-task-filters',open:P.taskFiltersOpen??!window.matchMedia('(max-width:767px)').matches},filterSummary,filters);
    filters.append(reset);filterPanel.addEventListener('toggle',()=>{if(filterPanel.isConnected)P.taskFiltersOpen=filterPanel.open;});
    function updateFilterSummary(){const count=[search.value.trim(),status.value!=='active',showAll.value!=='nearest',project&&project.value!=='all'].filter(Boolean).length;filterSummary.textContent='Поиск и фильтры'+(count?' · '+count:'');reset.hidden=!count;}
    updateFilterSummary();
    content.append(filterPanel, results); draw();
  }
  P.completeProject=async function(project){
    try{
      let path='/projects/'+project.id+'/complete',data={expected_revision:project.revision};
      if(project.occurrence_id){const c=await P.api('/occurrences/'+project.occurrence_id);path='/occurrences/'+project.occurrence_id+'/complete';data={expected_revision:c.occurrence.revision,expected_project_revision:project.revision,expected_series_revision:c.series.revision,completed_date:P.today()};}
      else if(project.managed_repeat&&project.repeat_series_id){const s=await P.api('/series/'+project.repeat_series_id);path='/projects/'+project.id+'/close-current';data={...data,action:'complete',expected_series_revision:s.revision,completed_date:P.today()};}
      await P.commandSender(path)(data);await P.refresh();P.notify('Проект «'+project.title+'» завершён');
    }catch(e){P.notify('Не удалось завершить проект: '+P.errorMessage(e));}
  };
  P.projectCompleteControl=function(p){
    const done=p.status==='completed',b=button(done?'✓':'',()=>P.completeProject(p),'pl-check pl-project-complete'+(done?' is-complete':''));
    b.disabled=!p.completion_ready;
    b.setAttribute('aria-label',done?'Проект завершён':'Завершить проект: '+p.title);
    b.title=done?'Проект завершён':p.completion_ready?'Все задачи выполнены — завершить проект':!p.task_count?'В проекте пока нет задач':p.preliminary?'Устаревший режим серии: требуется обновление данных':'Осталось выполнить задач и подзадач: '+p.remaining_tasks;
    return b;
  };
  P.projectDisplayProgress = function(project) {
    if(!project.routines&&Number.isInteger(project.remaining_tasks)){
      const total=project.task_count,completed=total-project.remaining_tasks;
      return {total,completed,open:project.progress.open_tasks||0,cancelled:project.progress.cancelled||0,skipped:project.progress.skipped||0,percent:total?Math.round(100*completed/total):null};
    }
    const value=project.routines?.ordinary||project.progress;
    const total=value.total+(value.cancelled||0)+(value.skipped||0);
    return {total,completed:value.completed,open:value.total-value.completed,cancelled:value.cancelled||0,skipped:value.skipped||0,percent:total?Math.round(100*value.completed/total):null};
  };
  function progress(project,compact=false) {
    const v=P.projectDisplayProgress(project);
    if(project.routines){const r=project.routines;return el('div',{class:'pl-progress-block pl-routine-summary'},el('strong',{},'Регулярных дел: '+r.count),r.next_date?meta('Ближайшее выполнение · '+P.date(r.next_date)):null,meta('Актуальных просрочек: '+r.open_dates.filter(d=>d<P.today()).length),v.total?meta('Обычные задачи: выполнено '+v.completed+' из '+v.total+' · открыто '+v.open):null);}

    if(!v.total)return el('div',{class:'pl-progress-block'},meta('В проекте пока нет задач'));
    return el('div',{class:'pl-progress-block'},el('div',{class:'pl-progress-label'},meta('Выполнено '+v.completed+' из '+v.total),el('strong',{},v.percent+'%')),
      el('progress',{max:100,value:v.percent,'aria-label':'Доля выполненных задач и подзадач'}),
      el('div',{class:'pl-progress-details'},(!compact||v.skipped||v.cancelled)?meta('Открыто: '+v.open):null,v.skipped?meta('Пропущено: '+v.skipped):null,v.cancelled?meta('Отменено: '+v.cancelled):null),
      el('details',{class:'pl-progress-explanation'},el('summary',{},'Как считается'),meta('Каждая задача и подзадача завершается отдельно. Для завершения проекта должны быть выполнены все. Пропуски и отмены не означают выполнения.'),meta('Всего задач и подзадач: '+(project.task_count??project.tasks?.length??v.total))));
  }
  P.projectOrder = function(a,b,today=P.today()) {
    const rank=p=>p.archived_at?3:!P.active(p)?2:p.start_date&&p.start_date>today?1:0;
    return rank(a)-rank(b)||(rank(a)>=2?(b.closed_at||b.updated_at||'').localeCompare(a.closed_at||a.updated_at||''):(a.start_date||a.due_date||'9999').localeCompare(b.start_date||b.due_date||'9999'))||a.title.localeCompare(b.title,'ru');
  };
  P.projectDateLabel = project => {
    const start=project.start_date,end=project.due_date;if(!start)return '';
    if(!project.repeat_series_id)return P.date(start)+(end&&end>=start?' — '+P.date(end):'');
    const C=P.calendarModel;
    if(end&&end>start){
      if(start.slice(5)==='01-01'&&end===start.slice(0,4)+'-12-31')return start.slice(0,4);
      if(start.endsWith('-01')&&C.addDays(C.addMonths(start,1),-1)===end)return C.caption(start,{month:'long',year:'numeric'});
      if(start.slice(0,7)===end.slice(0,7))return Number(start.slice(8))+'–'+C.caption(end,{day:'numeric',month:'long',year:'numeric'});
      return P.date(start)+' — '+P.date(end);
    }
    return C.caption(start,{day:'numeric',month:'long',year:'numeric'});
  };
  function renderProjects(content) {
    content.classList.add('pl-project-screen');
    content.append(el('div', { class: 'pl-section-head' }, el('div', {}, el('h2', {}, 'Проекты'), el('p', { class: 'pl-muted' }, 'Большие цели, разложенные на понятные шаги.')), button('+ Проект', () => P.navigate(P.base() + '?new=project'), 'pl-primary')));
    const filters = el('div', { class: 'pl-filters' });
    const status = P.field(filters, 'status', 'Статус проекта', 'select', '', [['', 'Все статусы'], ...['planned', 'in_progress', 'completed', 'cancelled'].map(k => [k, P.labels[k]])]);

    const grid = el('div', { class: 'pl-project-grid' });
    function draw() {
      const projects = P.projectCards(P.data.projects.filter(p => !p.archived_at && (!status.value || p.status === status.value)));
      const oldCards = new Map([...grid.querySelectorAll('.pl-project-card')].map(n => [n.dataset.projectId, n]));
      grid.replaceChildren(...projects.map(project => {
        const count = project.task_count ?? project.tasks?.length ?? 0;
        const card = P.paint(el('article', { class: 'pl-project-card', 'data-project-id':project.id }, el('div', { class: 'pl-card-top' },P.projectCompleteControl(project),el('h3',{},el('a',{href:'#/plans/projects/'+project.id},P.appearanceIcon(project,true),project.title)),project.status==='planned'?null:P.status(project)), project.description?el('p', { class: 'pl-card-description' }, project.description):null, project.start_date?el('p',{class:'pl-project-date'},(project.repeat_series_id?'↻ Выполнение · ':'Начало · ')+P.projectDateLabel(project)):null, progress(project,true), el('div', { class: 'pl-card-bottom' }, project.repeat_series_id&&!project.start_date&&!project.occurrence_id?meta('↻ Повторение'):null,project.due_date&&(!project.start_date||P.overdue(project)) ? el('span', { class: P.overdue(project) ? 'pl-overdue' : 'pl-muted' }, (P.overdue(project) ? 'Просрочен · ' : 'До ') + P.date(project.due_date)) : null)),project);
        card.budgetIdentity=JSON.stringify(project);
        card.addEventListener('click',event=>{if(!event.target.closest('a,button,details,input,select'))P.navigate('#/plans/projects/'+project.id);});
        const old = oldCards.get(project.id);
        return old?.budgetIdentity===card.budgetIdentity&&old.isEqualNode(card) ? old : card;
      }));
      if (!projects.length) grid.append(P.empty('Здесь появятся ваши проекты', 'Создайте проект и добавляйте задачи в удобном темпе.'));
    }
    P.updateProjects = {key: location.hash, connected: () => grid.isConnected, draw};
    filters.addEventListener('change', draw); content.append(filters, grid); draw();
  }
  function renderProject(content, project) {
    content.classList.add('pl-project-detail');
    content.append(el('a', { class: 'pl-back', href: '#/plans/projects' }, '← Все проекты'));
    const overview=el('section',{class:'pl-project-overview pl-project-compact'});
    const heading=el('div',{class:'pl-project-heading'},P.projectCompleteControl(project),el('h2',{},P.appearanceIcon(project,true),project.title),project.status==='planned'?null:P.status(project));
    const controls=el('div',{class:'pl-actions'},!project.archived_at?button('+ Задача в проект',()=>P.newTask(project.id),'pl-primary'):null,!project.archived_at?button('Добавить существующие',()=>P.addExistingTasks(project),'pl-secondary-add'):null,P.projectMenuButton(project));
    overview.append(el('div',{class:'pl-project-top'},heading,controls));
    const dates=el('div',{class:'pl-task-meta'},project.start_date?meta((project.repeat_series_id?'↻ Выполнение: ':'Начало: ')+P.projectDateLabel(project)):null,project.due_date?el('span',{class:P.overdue(project)?'pl-overdue':'pl-muted'},'Крайний срок: '+P.date(project.due_date)):null,project.archived_at?meta('В архиве · только просмотр'):null);
    overview.append(dates);
    if(project.description){
      if(project.description.length>140)overview.append(el('details',{class:'pl-project-description'},el('summary',{},project.description.slice(0,110)+'… · Подробнее'),el('p',{class:'pl-description'},project.description)));
      else overview.append(el('p',{class:'pl-project-description'},project.description));
    }
    overview.append(progress(project));content.append(overview);P.projectRepeatPanel(content,project);
    if(!project.repeat_series_id&&!project.archived_at&&!project.occurrence_id)dates.append(button('Повторять проект',()=>P.projectTemplateFlow(project,true)));
    const taskTools=el('div',{class:'pl-project-task-tools'},el('h3',{class:'pl-project-tasks-heading'},'Задачи проекта'));
    const projectList=el('div');const filterBox=el('div',{class:'pl-project-task-filters'});
    const sortKey='planner.project-sort:'+(project.repeat_series_id||project.id);let sortValue='planned';try{sortValue=localStorage.getItem(sortKey)||localStorage.getItem('planner.project-sort:'+project.id)||'planned';}catch(_){}
 const sort=P.field(filterBox,'project_sort','Порядок задач','select',sortValue,[['manual','Вручную'],['planned','По плановой дате'],['due','По крайнему сроку'],['priority','По приоритету'],['title','По названию'],['created','По дате создания']]);
 if(!project.archived_at&&!project.occurrence_id&&!project.repeat_series_id)filterBox.append(button('Настроить повторение задач',()=>P.batchRepeat(project)));
 const display=P.field(filterBox,'project_tasks','Выполнения в проекте','select','nearest',[['nearest','Ближайшие и обычные задачи'],['all','Все задачи и история']]);
    const drawTasks=()=>{const tasks=display.value==='all'?project.tasks:P.projectNearest(project.tasks);projectList.replaceChildren(tasks.length?P.taskTree(P.sortProjectTasks(tasks,sort.value)):P.empty('Первый шаг ещё впереди','Добавьте задачу — даты можно назначить позже.'));
    if (!project.archived_at) for (const task of tasks.filter(t => !t.parent_task_id)) {
      const row = [...projectList.querySelectorAll('[data-task-id]')].find(n => n.dataset.taskId === task.id);
      row.append(button('+', () => P.newTask(project.id, task.id), 'pl-icon'));
      row.lastChild.setAttribute('aria-label', 'Добавить подзадачу: ' + task.title);
    }
    if(sort.value==='manual'&&!project.archived_at){const series=tasks.filter(t=>t.series_id).map(t=>t.series_id);if(new Set(series).size<series.length)projectList.prepend(button('Показать ближайшие для ручного порядка',()=>{display.value='nearest';drawTasks();}));else P.manualTaskOrder(projectList,project,P.sortProjectTasks(tasks,'manual'));}
    };display.addEventListener('change',drawTasks);sort.addEventListener('change',()=>{try{localStorage.setItem(sortKey,sort.value);}catch(_){}drawTasks();});if(!project.tasks.some(t=>t.series_id))display.closest('.pl-field')?.setAttribute('hidden','');taskTools.append(filterBox);content.append(taskTools);if(!project.archived_at)content.append(P.quickForm(project.id,null,project));content.append(projectList);drawTasks();
    const history=P.disclosure('История');content.append(history);P.renderHistory(history, 'project', project.id);
  }
})(window.Planner);
