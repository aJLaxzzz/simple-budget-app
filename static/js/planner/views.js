(function (P) {
  'use strict';
  const { el, button, meta } = P;
  let generation = 0;
  let host;
  P.unmount = function () {
    P.guardActive = null; P.deferNavigation = null;
    generation++;
    document.querySelectorAll('.pl-drawer-shell, .pl-dialog').forEach(n => n.remove());
    document.body.classList.remove('planner-open', 'planner-page', 'planner-calendar');
  };
  P.refresh = async () => {
    if (!window.location.hash.startsWith('#/plans')) return;
    const task = P.route().params.get('task');
    const selected = document.querySelector('.pl-editor-tabs [aria-selected="true"]')?.id;
    await P.mount(host);
    // Refresh the same open card after a command without sending the user back to Main.
    // Opening another card still starts on Main and does not fetch its history.
    if (task && task === P.route().params.get('task') && selected) {
      const tab = document.getElementById(selected); tab?.click();
      if (selected !== 'task-tab-main') tab?.focus();
    }
  };
  P.mount = async function (root) {
    P.unmount(); host = root;
    const turn = generation;
    document.body.classList.add('planner-page');
    const route = P.route();if(route.view!=='series'||!route.id)P.repeatAutoOpenedRoute=null;
    const page = el('section', { class: 'planner', 'aria-label': 'Планы' });
    const chrome=el('div',{class:'pl-navigation'}); P.navigation(chrome, route); page.append(chrome);
    const content = el('div', { class: 'pl-content', 'aria-live': 'polite' }, el('p', { class: 'pl-loading', role: 'status' }, 'Загружаем планы…'));
    page.append(content); root.replaceChildren(page);
    try {
      if (route.view === 'calendar') {
        document.body.classList.add('planner-calendar');
        const ready = await P.renderCalendar(content, route, () => generation === turn);
        if (generation !== turn || ready === false) return;
        if (route.params.has('task') || route.params.get('new') === 'task') await P.taskEditor(route, () => generation === turn);
        return;
      }
      if (['series','templates','task-templates','attention','settings'].includes(route.view) || (['tasks','projects'].includes(route.view) && route.params.get('view')==='regular')) {
        const render = route.params.get('view')==='regular' ? P.renderSeries : {series:P.renderSeries, templates:P.renderTemplates, 'task-templates':P.renderTaskTemplates, attention:P.renderAttention, settings:P.renderCalendarSettings}[route.view];
        await render(content, route);
        if (generation === turn && (route.params.has('task')||route.params.get('new')==='task')) {
          const [tasks,projects,archive]=await Promise.all([P.api('/tasks?status=active'),P.api('/projects'),P.api('/projects?archived=true')]);
          if(generation!==turn)return; P.data={tasks:tasks.items||[],projects:[...(projects.items||[]),...(archive.items||[])]};
          await P.taskEditor(route, () => generation === turn);
        }
        return;
      }
      const selectedStatus = route.params.get('status') || 'active';
      const [tasks, projects, archive] = await Promise.all([P.api('/tasks'+(selectedStatus==='all'?'':'?status='+encodeURIComponent(selectedStatus))), P.api('/projects'), P.api('/projects?archived=true')]);
      if (generation !== turn) return;
      P.data = { tasks: tasks.items || [], projects: [...(projects.items || []), ...(archive.items || [])] };
      content.replaceChildren();
      if(route.view==='tasks' && !route.id && route.params.get('view')!=='undated' && selectedStatus==='active'){
        const series=await P.seriesList();let added=false;
        for(const s of series.filter(s=>s.source_type==='task'&&s.state==='active')){
          const count=P.data.tasks.filter(t=>t.series_id===s.id&&t.planned_date>=P.today()&&(!t.due_date||t.due_date>=P.today())).length;
          if(count>=3)continue;
          try{const result=await P.api('/series/'+s.id+'/ensure-upcoming','POST',{});added ||= result.created>0; }catch(e){console.warn('Повторение требует внимания',s.id,e.message);}
        }
        if(added)P.data.tasks=(await P.api('/tasks?status=active')).items;
        if(generation!==turn)return;
      }
      if (route.view === 'projects' && route.id) {
        const project = await P.api('/projects/' + encodeURIComponent(route.id));
        if (generation !== turn) return;
        P.data.tasks = project.tasks;
        renderProject(content, project);
      } else if (route.view === 'projects') renderProjects(content);
      else renderTasks(content, route);
      if (generation !== turn) return;
      if (route.params.has('task') || route.params.get('new') === 'task') await P.taskEditor(route, () => generation === turn);
      if (route.params.get('edit') === 'project' || route.params.get('new') === 'project') P.projectEditor(route);
    } catch (error) {
      if (generation !== turn) return;
      content.replaceChildren(el('div', { class: 'pl-empty' }, el('h2', {}, 'Не удалось загрузить планы'), el('p', {}, error.message), button('Попробовать снова', P.refresh)));
    }
  };
  P.empty = (title, text) => el('div', { class: 'pl-empty' }, el('span', { class: 'pl-empty-symbol', 'aria-hidden': 'true' }, '◇'), el('h3', {}, title), el('p', {}, text));
  P.quickForm = function (project, parent, projectRecord) {
    const key = 'quick:' + (project || '') + ':' + (parent || '');
    const form = el('form', { class: 'pl-quick', 'aria-label': 'Быстрое создание задачи', 'data-submit': 'Добавить' });
    const input = el('input', { name: 'title', placeholder: parent ? 'Название подзадачи…' : 'Что нужно сделать?', required: true, 'aria-label': 'Название новой задачи', autocomplete: 'off', value: P.drafts.get(key) || '' });
    input.addEventListener('input', () => P.drafts.set(key, input.value));
    form.append(input);
    const scope=projectRecord?.repeat_series_id?P.projectScopeField(form,projectRecord):null;
    const create = P.commandSender('/tasks');
    P.bindForm(form, () => scope&&scope.value!=='this'?P.scopedProjectSave(projectRecord,{scope:scope.value,action:'add',parent_id:parent||'',patch:{title:input.value.trim()}}).then(r=>({...r.task,scope_notice:r.scope_notice})):create( { title: input.value.trim(), ...(!project&&P.route().view==='tasks'&&P.route().params.get('view')!=='undated'?{planned_date:P.today()}:{}), ...(project ? { project_id: project } : {}), ...(parent ? { parent_task_id: parent } : {}) }), async saved => {
      P.drafts.delete(key); input.value=''; P.notify(saved.scope_notice||('Задача «'+saved.title+'» добавлена'), saved.id?{label:'Открыть',run:()=>P.openTask(saved.id)}:undefined);
      if(['tasks','inbox'].includes(P.route().view)){const q=new URLSearchParams(P.route().params);['q','status','project'].forEach(k=>q.delete(k));window.history.replaceState(null,'',P.base()+(q.size?'?'+q:''));}
      await P.refresh();

      document.querySelector('.pl-quick input')?.focus();
    });
    return form;
  };
  P.taskRow = function (task, child = false, context = '') {
    const row = el('div', { class: 'pl-task' + (child ? ' pl-child' : '') + (P.active(task) ? '' : ' pl-terminal'), 'data-task-id': task.id });
    const complete = button(task.status === 'completed' ? '✓' : task.status === 'cancelled' ? '−' : '', () => P.runAction(task, 'complete'), 'pl-check');
    complete.setAttribute('aria-label', 'Завершить: ' + task.title);
    complete.disabled = task.project_pending || !P.active(task) || Boolean(P.data.projects.find(p => p.id === task.project_id)?.archived_at);
    const title = button(task.title, () => P.openTask(task.id), 'pl-task-title');
    const details = [(task.dynamic||task.project_pending)&&P.active(task)&&meta('Дата может измениться'),task.series_id && el('span', {class:'pl-repeat-mark', title:'Повторяющаяся задача', 'aria-label':'Повторяющаяся задача'}, '↻'), context && meta(context), task.priority === 'high' && el('span', { class: 'pl-priority' }, '↑ Высокий'), task.priority === 'low' && meta('↓ Низкий'), task.priority === 'normal' && el('span',{class:'pl-muted pl-priority-normal'},'Обычный приоритет'), task.planned_date && meta(P.date(task.planned_date) + (task.scheduled_time ? ' · ' + task.scheduled_time : '')), task.due_date && el('span', { class: P.overdue(task) ? 'pl-overdue' : 'pl-muted' }, 'Крайний срок: ' + P.date(task.due_date)), task.completed_with_open_children && meta('Есть открытые подзадачи')].filter(Boolean);
    P.paint(row,task);title.prepend(P.appearanceIcon(task));
    const timing=P.timing(task);if(timing.label)details.push(el('span',{class:timing.key==='overdue'?'pl-overdue':'pl-plan-missed'},timing.label));
    row.append(complete, el('div', { class: 'pl-task-copy' }, title, el('div', { class: 'pl-task-meta' }, details)), P.status(task), P.actionButton(task));
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
    const showAll=P.field(filters,'future','Будущие повторения','select',route.params.get('future')||'nearest',[['nearest','Три ближайших выполнения'],['all','Все выполнения']]);
    let project;
    if (!inbox) project = P.field(filters, 'project', 'Проект', 'select', route.params.get('project') || 'all', [['all', 'Все проекты'], ['none', 'Без проекта'], ...P.data.projects.map(p => [p.id, p.title + (p.archived_at ? ' · архив' : '')])]);
    const results = el('div', { class: 'pl-results' });
    const draw = () => {
      let tasks = P.data.tasks.filter(t => (!t.project_pending || !P.active(t)) && (!inbox || (!t.project_id && !t.planned_date)) && (!undated || (!t.planned_date && !t.due_date)));
      tasks = tasks.filter(t => (status.value === 'all' || (status.value === 'active' ? P.active(t) : t.status === status.value)) && t.title.toLocaleLowerCase('ru').includes(search.value.toLocaleLowerCase('ru')) && (!project || project.value === 'all' || (project.value === 'none' ? !t.project_id : t.project_id === project.value)));
      const total=tasks.length;tasks=P.limitUpcoming(tasks,inbox||showAll.value==='all');
      results.replaceChildren(el('p', { class: 'pl-list-count' }, 'Задач: ' + tasks.length), tasks.length ? (inbox ? P.taskTree(tasks) : P.groupedTasks(tasks,Boolean(search.value.trim()))) : P.empty(inbox ? 'Входящие разобраны' : 'Задач не найдено', inbox ? 'Новая идея? Добавьте её одной строкой выше.' : 'Измените фильтры или создайте первую задачу.'));
    };
    let loadedStatus = status.value, loadTurn = 0;
    filters.addEventListener('input', async () => {
      const params = new URLSearchParams(window.location.hash.split('?')[1] || '');
      params.set('q', search.value); params.set('status', status.value);params.set('future',showAll.value);
      if (project) params.set('project', project.value);
      // Filters survive reload and editor back/forward without creating one history entry per keystroke.
      window.history.replaceState(null, '', P.base() + '?' + params);
      const turn=++loadTurn;
      if(loadedStatus!==status.value){
        results.replaceChildren(el('p',{class:'pl-loading'},'Загружаем задачи…'));
        try {
          const selected=status.value;
          const data=await P.api('/tasks'+(selected==='all'?'':'?status='+encodeURIComponent(selected)));
          if(turn!==loadTurn || !content.isConnected)return;
          P.data.tasks=data.items; loadedStatus=selected;
        }catch(e){if(turn===loadTurn)results.replaceChildren(el('p',{role:'alert'},e.message),button('Попробовать снова',P.refresh));return;}
      }
      draw();
    });
    const filterPanel=el('details',{class:'pl-task-filters',open:!window.matchMedia('(max-width:700px)').matches},el('summary',{},'Поиск и фильтры'+(route.params.has('q')||route.params.has('project')||route.params.has('status')?' · применены':'')),filters);
    content.append(filterPanel, results); draw();
  }
  P.projectDisplayProgress = function(project) {
    const value=project.routines?.ordinary||project.progress;
    const total=value.total+(value.cancelled||0)+(value.skipped||0);
    return {total,completed:value.completed,open:value.total-value.completed,cancelled:value.cancelled||0,skipped:value.skipped||0,percent:total?Math.round(100*value.completed/total):null};
  };
  function progress(project) {
    const v=P.projectDisplayProgress(project);
    if(project.routines){const r=project.routines;return el('div',{class:'pl-progress-block pl-routine-summary'},el('strong',{},'Регулярных дел: '+r.count),r.next_date?meta('Ближайшее выполнение · '+P.date(r.next_date)):null,meta('Актуальных просрочек: '+r.open_dates.filter(d=>d<P.today()).length),v.total?meta('Обычные задачи: выполнено '+v.completed+' из '+v.total+' · открыто '+v.open):null);}

    if(!v.total)return el('div',{class:'pl-progress-block'},meta('В проекте пока нет задач'));
    return el('div',{class:'pl-progress-block'},el('div',{class:'pl-progress-label'},meta('Выполнено '+v.completed+' из '+v.total),el('strong',{},v.percent+'%')),
      el('progress',{max:100,value:v.percent,'aria-label':'Доля выполненных конечных задач'}),
      el('div',{class:'pl-progress-details'},meta('Открыто: '+v.open),v.skipped?meta('Пропущено: '+v.skipped):null,v.cancelled?meta('Отменено: '+v.cancelled):null),
      el('details',{class:'pl-progress-explanation'},el('summary',{},'Как считается'),meta('Считаются задачи без подзадач. Родитель не учитывается повторно. Пропуски и отмены не означают выполнения.'),meta('Всего задач и подзадач: '+(project.task_count??project.tasks?.length??v.total))));
  }
  P.projectOrder = function(a,b,today=P.today()) {
    const rank=p=>p.archived_at?3:!P.active(p)?2:p.start_date&&p.start_date>today?1:0;
    return rank(a)-rank(b)||(rank(a)>=2?(b.closed_at||b.updated_at||'').localeCompare(a.closed_at||a.updated_at||''):(a.start_date||a.due_date||'9999').localeCompare(b.start_date||b.due_date||'9999'))||a.title.localeCompare(b.title,'ru');
  };
  P.projectDateLabel = project => project.start_date ? P.date(project.start_date)+(project.due_date&&project.due_date>=project.start_date?' — '+P.date(project.due_date):'') : '';
  function renderProjects(content) {
    content.append(el('div', { class: 'pl-section-head' }, el('div', {}, el('h2', {}, 'Проекты'), el('p', { class: 'pl-muted' }, 'Большие цели, разложенные на понятные шаги.')), button('+ Проект', () => P.navigate(P.base() + '?new=project'), 'pl-primary')));
    const filters = el('div', { class: 'pl-filters' });
    const status = P.field(filters, 'status', 'Статус проекта', 'select', '', [['', 'Все статусы'], ...['planned', 'in_progress', 'completed', 'cancelled'].map(k => [k, P.labels[k]])]);
    const archive = P.field(filters, 'archive', 'Показывать', 'select', 'active', [['active', 'Без архива'], ['archived', 'Архив']]);
    const grid = el('div', { class: 'pl-project-grid' });
    function draw() {
      const projects = P.projectCards(P.data.projects.filter(p => Boolean(p.archived_at) === (archive.value === 'archived') && (!status.value || p.status === status.value)));
      grid.replaceChildren(...projects.map(project => {
        const count = project.task_count ?? project.tasks?.length ?? 0;
        return P.paint(el('a', { class: 'pl-project-card', href: '#/plans/projects/' + encodeURIComponent(project.id) }, el('div', { class: 'pl-card-top' }, P.appearanceIcon(project,true), P.status(project)), el('h3', {}, project.title), project.description?el('p', { class: 'pl-card-description' }, project.description):null, project.start_date?el('p',{class:'pl-project-date'},(project.occurrence_id?'↻ Выполнение · ':'Начало · ')+P.projectDateLabel(project)):null, progress(project), el('div', { class: 'pl-card-bottom' }, project.repeat_series_id&&!project.occurrence_id?meta('↻ Повторение'):null,project.due_date ? el('span', { class: P.overdue(project) ? 'pl-overdue' : 'pl-muted' }, (P.overdue(project) ? 'Просрочен · ' : 'До ') + P.date(project.due_date)) : meta('Без крайнего срока'))),project);
      }));
      if (!projects.length) grid.append(P.empty('Здесь появятся ваши проекты', 'Создайте проект и добавляйте задачи в удобном темпе.'));
    }
    filters.addEventListener('change', draw); content.append(filters, grid); draw();
  }
  function renderProject(content, project) {
    content.append(el('a', { class: 'pl-back', href: '#/plans/projects' }, '← Все проекты'));
    const overview=el('section',{class:'pl-project-overview pl-project-compact'});
    const heading=el('div',{class:'pl-project-heading'},el('h2',{},P.appearanceIcon(project,true),project.title),P.status(project));
    const controls=el('div',{class:'pl-actions'},!project.archived_at?button('+ Задача в проект',()=>P.newTask(project.id),'pl-primary'):null,!project.archived_at?button('Добавить существующие',()=>P.addExistingTasks(project)):null,P.projectMenuButton(project));
    overview.append(el('div',{class:'pl-project-top'},heading,controls));
    const dates=el('div',{class:'pl-task-meta'},project.start_date?meta((project.occurrence_id?'↻ Выполнение: ':'Начало: ')+P.date(project.start_date)):null,project.due_date?el('span',{class:P.overdue(project)?'pl-overdue':'pl-muted'},'Крайний срок: '+P.date(project.due_date)):null,project.archived_at?meta('В архиве · только просмотр'):null);
    overview.append(dates);
    if(project.description){
      if(project.description.length>140)overview.append(el('details',{class:'pl-project-description'},el('summary',{},project.description.slice(0,110)+'… · Подробнее'),el('p',{class:'pl-description'},project.description)));
      else overview.append(el('p',{class:'pl-project-description'},project.description));
    }
    overview.append(progress(project));if(P.active(project)&&!project.preliminary&&!project.routines&&project.progress.total>0&&project.progress.completed===project.progress.total)overview.append(button("Завершить проект",()=>P.projectAction(project,"complete"),"pl-primary"));content.append(overview);P.projectRepeatPanel(content,project);
    if(!project.repeat_series_id&&!project.archived_at&&!project.occurrence_id)overview.append(button('Повторять проект',()=>P.projectTemplateFlow(project,true)));
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
