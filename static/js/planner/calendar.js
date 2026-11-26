(function (P) {
  'use strict';
  const { el, button, meta } = P, C = P.calendarModel;
  const weekdays = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс'];
  const kind = { task: 'Задача', planning: 'Плановая дата', session: 'Период работы', deadline: 'Крайний срок' };
  const mobile = () => window.matchMedia('(max-width: 700px)').matches;
  const compactWeek = () => window.matchMedia('(max-width: 900px)').matches;
  const layoutKey = () => mobile()+':'+compactWeek()+':'+window.innerWidth;
  const canMove = item => ['task', 'planning', 'session'].includes(item.type) && P.active(item) && !C.completed(item) && !item.project_archived;
  let officialDays = new Map(), renderedLayout = layoutKey(), resizeTimer;
  const expandedDays=new Set();
  let saved = '', dragged = null, focusDay = false, filterOpen = false;
  try { saved = sessionStorage.getItem('planner-calendar') || ''; } catch (_) { /* storage is optional */ }
  P.calendarLink = () => {
    if (!saved) return '#/plans/calendar';
    const params = new URLSearchParams(saved.split('?')[1]);
    params.set('completed', P.showCompletedCalendar() ? '1' : '0');params.set('cancelled',P.showCancelledCalendar()?'1':'0');
    return '#/plans/calendar?' + params;
  };
  P.showCompletedCalendar = () => { try { return localStorage.getItem('planner-calendar-completed') !== 'off'; } catch (_) { return true; } };
  P.setCompletedCalendar = value => { try { localStorage.setItem('planner-calendar-completed', value ? 'on' : 'off'); } catch (_) {} };
  P.showCancelledCalendar = () => {try{return localStorage.getItem('planner-calendar-cancelled')==='on';}catch(_){return false;}};
  P.setCancelledCalendar = value => {try{localStorage.setItem('planner-calendar-cancelled',value?'on':'off');}catch(_){}};
  function state(route) {
    const params = route.params;
    return { mode: ['month', 'week', 'day'].includes(params.get('mode')) ? params.get('mode') : mobile() ? 'week' : 'month', date: C.valid(params.get('date')) ? params.get('date') : P.today(), project: params.get('project') || '', completed: params.has('completed') ? params.get('completed') === '1' : P.showCompletedCalendar(), cancelled: params.has('cancelled')?params.get('cancelled') === '1':P.showCancelledCalendar() };
  }
  function url(value) {
    const params = new URLSearchParams({ mode: value.mode, date: value.date });
    if (value.project) params.set('project', value.project);
    params.set('completed', value.completed ? '1' : '0');
    params.set('cancelled', value.cancelled?'1':'0');
    return '#/plans/calendar?' + params;
  }
  function remember(value) {
    P.setCompletedCalendar(value.completed);P.setCancelledCalendar(value.cancelled);
    saved = url(value);
    try { sessionStorage.setItem('planner-calendar', saved); } catch (_) { /* URL remains authoritative */ }
  }
  function go(value, patch) { if ('completed' in patch) P.setCompletedCalendar(patch.completed); focusDay = document.activeElement?.classList.contains('pc-day-button'); P.navigate(url({ ...value, ...patch })); }
  function actions(item) { P.objectMenu(item); }
  function eventCard(item, compact = false) {
    const card = el('div', { class: 'pc-event pc-kind-' + item.type + (P.active(item) ? '' : ' pc-closed') + (C.completed(item) ? ' pc-completed' : '')+(C.cancelled(item)?' pc-cancelled':''), 'data-event-id': item.type + ':' + item.id, 'data-event-date': item.date });
    card.budgetIdentity = JSON.stringify([item, compact, mobile()]);
    const label = [(item.dynamic||item.project_pending)&&P.active(item)?'Предварительно запланировано':null,kind[item.type], item.start_time ? item.start_time + (item.end_time ? '–' + item.end_time : '') : null, item.title, P.labels[C.completed(item) ? 'completed' : item.status], item.project_title].filter(Boolean).join(' · ');
    const open = button((C.completed(item) ? '✓ ' : C.cancelled(item) ? '⊘ ' : item.status === 'in_progress' ? '• ' : '') + (item.priority === 'high' ? '↑ ' : '') + (item.parent_task_id ? '↳ ' : '') + (item.start_time ? item.start_time + ' ' : '') + (item.type === 'deadline' ? '⚑ ' : item.type === 'planning' ? '◇ ' : item.type === 'session' ? '◷ ' : '') + ((item.dynamic||item.project_pending)&&P.active(item) ? '≈ ' : '') + (item.series_id ? '↻ ' : ''), () => P.openTask(item.task_id), 'pc-event-title');
    open.append(el('span',{class:'pc-event-name'},item.title));
    P.paint(card,item);open.prepend(P.appearanceIcon(item));
    open.setAttribute('aria-label', label);
    const menu = button('⋯', () => actions(item), 'pc-event-menu'); menu.setAttribute('aria-label', 'Действия: ' + label);
    card.append(el('div', { class: 'pc-event-line' }, open, menu));if((item.dynamic||item.project_pending)&&P.active(item))card.append(el('span',{class:'pc-event-hint'},'Предварительно · зависит от предыдущего'));
    if (!compact) {
      const details=el('div',{class:'pc-event-meta'});
      if(item.type!=='task')details.append(el('span',{},kind[item.type]));
      if(item.project_title)details.append(el('span',{},item.project_title));
      if(item.priority!=='normal')details.append(el('span',{},P.labels[item.priority]));
      if(item.status!=='planned')details.append(P.status(item));
      if(details.childElementCount)card.append(details);
    }
    else { card.title = label + ' · ' + P.labels[item.priority]; card.append(el('span', { class: 'pc-event-hint' }, (item.priority === 'high' ? '↑ ' : item.priority === 'low' ? '↓ ' : '') + (item.project_title || 'Без проекта') + ' · ' + P.labels[item.status])); }
    if (!mobile() && canMove(item)) {
      card.draggable = true;
      card.addEventListener('dragstart', event => { dragged = item; event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('application/x-planner-event', item.type + ':' + item.id); card.classList.add('pc-dragging'); });
      card.addEventListener('dragend', () => { dragged = null; card.classList.remove('pc-dragging'); document.querySelectorAll('.pc-drop-active').forEach(n => n.classList.remove('pc-drop-active')); });
    }
    return card;
  }
  function dropTarget(node, date, time) {
    node.addEventListener('dragover', event => { if (!dragged) return; event.preventDefault(); event.dataTransfer.dropEffect = 'move'; node.classList.add('pc-drop-active'); });
    node.addEventListener('dragleave', event => { if (!node.contains(event.relatedTarget)) node.classList.remove('pc-drop-active'); });
    node.addEventListener('drop', event => {
      if (!dragged) return;
      event.preventDefault(); event.stopPropagation(); node.classList.remove('pc-drop-active');
      const item = dragged; dragged = null;
      P.calendarDrop(item, date, time);
    });
  }
  P.calendarReschedule = (...args) => P.reschedule(...args);
  function dayButton(day, value, onclick) {
    const b = button(C.caption(day, { weekday: 'short', day: 'numeric' }), onclick, 'pc-day-button' + (day === P.today() ? ' pc-today' : '') + (day === value.date ? ' pc-selected' : ''));
    b.dataset.day = day;
    P.decorateCalendarDay(b,officialDays.get(day));
    if (day === P.today()) b.setAttribute('aria-current', 'date');
    return b;
  }
  function month(container, value, items, range) {
    const grid = el('div', { class: 'pc-month', 'aria-label': 'Месяц, понедельник — воскресенье' });
    weekdays.forEach(name => grid.append(el('div', { class: 'pc-weekday' }, name)));
    for (const day of C.days(range.from, range.to)) {
      const events = items.filter(item => item.date === day).sort(C.compareGroupedEvents);
      const cell = el('section', { class: 'pc-month-day' + (day.slice(0, 7) !== value.date.slice(0, 7) ? ' pc-outside' : '') + (day === value.date ? ' pc-selected-cell' : ''), 'data-date': day, 'aria-label': P.date(day) });
      const heading = dayButton(day, value, () => go(value, { date: day, mode: 'day' }));
      heading.textContent = String(Number(day.slice(8))); heading.setAttribute('aria-label', 'Открыть день ' + P.date(day));
      P.decorateCalendarDay(cell,officialDays.get(day));
      cell.append(heading);
      const completed=C.completedCount(events);
      if(completed) cell.append(el('span',{class:'pc-completed-count',title:'Выполнено задач: '+completed},'✓ '+completed));
      events.slice(0, 3).forEach(item => cell.append(eventCard(item, true)));
      if (events.length > 3) cell.append(button('Ещё ' + (events.length - 3), () => {expandedDays.add(day);go(value, { date: day, mode: 'day' });}, 'pc-more'));
      cell.addEventListener('click', event => { if (event.target === cell) go(value, { date: day, mode: 'day' }); });
      cell.addEventListener('keydown', event => {
        if (event.target !== heading) return;
        const step = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[event.key];
        if (step) { event.preventDefault(); const next = C.addDays(day, step); const target = [...grid.querySelectorAll('[data-date]')].find(n => n.dataset.date === next); if (target) target.querySelector('.pc-day-button').focus(); else go(value, { date: next }); }
      });
      dropTarget(cell, day); grid.append(cell);
    }
    container.append(grid);
  }
  function agenda(container, value, items, days) {
    const columns = days.length;
    const head = el('div', { class: 'pc-schedule-head', style: '--pc-days:' + columns }, el('span', { class: 'pc-time-label' }, 'МСК'));
    days.forEach(day => {const heading=dayButton(day,value,()=>go(value,{date:day,mode:'day'})), count=C.completedCount(items.filter(i=>i.date===day));if(count)heading.append(el('span',{class:'pc-completed-count'},' ✓ '+count));head.append(heading);});
    if(value.mode!=='day')container.append(head);
    else {const count=C.completedCount(items);if(count)container.append(el('p',{class:'pc-completed-count'},'Выполнено задач: '+count));}
    const agendaBody=el('div',{class:'pc-agenda'+(columns===1?' pc-agenda-single':'')});container.append(agendaBody);
    const allDay = el('div', { class: 'pc-allday', style: '--pc-days:' + columns }, el('span', { class: 'pc-time-label' }, 'Без времени'));
    days.forEach(day => {
      const cell = el('div', { class: 'pc-allday-cell', 'data-allday': day });
      const events = items.filter(item => item.date === day && !item.start_time).sort(C.compareGroupedEvents);
      const count=el('div',{class:'pc-allday-count'});cell.append(count);
      const list=el('div',{class:'pc-allday-items',tabindex:0,'aria-label':'Задачи без времени · '+P.date(day)});
      let expanded=expandedDays.has(day);const more=button('',()=>{expanded=!expanded;if(expanded)expandedDays.add(day);else expandedDays.delete(day);draw();},'pc-more pc-allday-more');
      function draw(){
        list.replaceChildren(...(expanded?events:events.slice(0,4)).map(item=>eventCard(item,true)));
        list.classList.toggle('pc-allday-expanded',expanded&&events.length>4);list.tabIndex=expanded&&events.length>4?0:-1;
        count.textContent=(columns===1?'Без времени · ':'')+events.length+' '+(events.length%10===1&&events.length%100!==11?'задача':events.length%10>=2&&events.length%10<=4&&(events.length%100<12||events.length%100>14)?'задачи':'задач')+(!expanded&&events.length>4?' · скрыто '+(events.length-4):'');
        more.textContent=expanded?'Свернуть список':'Показать ещё '+(events.length-4);more.setAttribute('aria-expanded',String(expanded));more.hidden=events.length<=4;
        hint.hidden=!expanded||events.length<=4;
      }
      const hint=el('p',{class:'pc-scroll-hint'},'Все задачи ниже · прокрутите список');
      draw();cell.append(list,more,hint);
      P.decorateCalendarDay(cell,officialDays.get(day));
      dropTarget(cell, day, null); allDay.append(cell);
    });
    agendaBody.append(allDay);
    const timed=el('section',{class:'pc-timed-panel','aria-label':'Временное расписание'});agendaBody.append(timed);
    const scroll = el('div', { class: 'pc-time-scroll', tabindex: 0, 'aria-label': 'Расписание по времени. Прокрутите для просмотра суток.' });
    const grid = el('div', { class: 'pc-time-grid', style: '--pc-days:' + columns });
    const ruler = el('div', { class: 'pc-ruler' });
    for (let hour = 0; hour < 24; hour++) ruler.append(el('span', { style: 'top:' + hour * 72 + 'px' }, C.time(hour * 60)));
    grid.append(ruler);
    days.forEach(day => {
      const column = el('div', { class: 'pc-time-column', 'data-time-day': day });
      P.decorateCalendarDay(column,officialDays.get(day));
      for (let hour = 0; hour < 24; hour++) {
        const slot = el('div', { class: 'pc-time-slot', style: 'top:' + hour * 72 + 'px', 'data-time': C.time(hour * 60), 'aria-hidden': 'true' });
        dropTarget(slot, day, C.time(hour * 60)); column.append(slot);
      }
      const width=(timed.getBoundingClientRect().width-(mobile()?49:64))/columns;
      const groups=C.overlapGroups(items.filter(item=>item.date===day&&item.start_time),width);
      for(const group of groups){
        if(group.grouped){
          const count=group.entries.length, last=count%10, teens=count%100>=11&&count%100<=14;
          const noun=teens?'дел':last===1?'дело':last>=2&&last<=4?'дела':'дел';
          const summary=button(C.time(group.start)+' · '+count+' '+noun,()=>{
            const {dialog,close}=P.dialog('Одновременные события · '+P.date(day));
            dialog.classList.add('pc-overlap-dialog');
            dialog.append(meta('Выберите конкретное дело; его перенос не изменит даты остальных. Время каждого события указано в списке.'));
            const list=el('div',{class:'pc-dense-list'});dialog.append(list);
            for(const entry of group.entries){
              const item=entry.item,card=eventCard(item);
              card.querySelector('.pc-event-title').onclick=()=>{close(true);P.openTask(item.task_id);};
              card.querySelector('.pc-event-menu').onclick=()=>{close(true);actions(item);};
              const footer=el('div',{class:'pc-group-footer'},meta((item.start_time||'')+(item.end_time?'–'+item.end_time:'')));
              if(canMove(item))footer.append(button('Перенести',()=>{close(true);P.reschedule(item);},'pc-group-move'));
              card.append(footer);
              list.append(card);
            }
          },'pc-dense-group');
          summary.style.top=group.start*1.2+'px';
          summary.style.height=Math.max(44,(group.end-group.start)*1.2-2)+'px';
          column.append(summary);
        }else for(const entry of group.entries){
          const card=eventCard(entry.item,true);card.classList.add('pc-timed');
          card.style.top=entry.start*1.2+'px';card.style.height=Math.max(24,(entry.end-entry.start)*1.2-2)+'px';
          card.style.left=`calc(${100*entry.column/entry.columns}% + 2px)`;card.style.width=`calc(${100/entry.columns}% - 4px)`;
          column.append(card);
        }
      }
      grid.append(column);
    });
    scroll.append(grid);
    const controls=el('div',{class:'pc-time-controls'},el('strong',{},'По времени'));
    const jump=P.field(controls,'jump_time','Перейти ко времени','time','');
    const seek=minutes=>{scroll.scrollTop=Math.max(0,minutes-30)*1.2;};
    jump.addEventListener('change',()=>{if(jump.value)seek(C.minutes(jump.value));});
    timed.append(controls,scroll);
    const earliest = items.filter(item => days.includes(item.date) && item.start_time).map(item => C.minutes(item.start_time));
    const first=earliest.length?Math.min(...earliest):480;jump.value=C.time(first);
    controls.append(button('Первое событие',()=>{jump.value=C.time(first);seek(first);}));seek(first);
  }
  // Explicit protected POST before the read-only calendar GET. Resume bounded
  // catch-up pages; never show a partial horizon as a fully loaded calendar.
  P.materializeCalendar = async function (through, isCurrent) {
    let after = '', pending = false;
    const errors = new Map();
    for (let batch = 0; batch < 100; batch++) {
      if (!isCurrent()) return null;
      let result;
      try { result = await P.api('/materialize', 'POST', { through, after_series_id: after }); }
      catch (error) {
        // A generation bound must not hide ordinary tasks in a distant year.
        if (error.status === 422 && error.code === 'materialization_range_limit') return [error.message];
        throw error;
      }
      for (const item of result.results) if (item.error) errors.set(item.series_id, item.error.message);
      pending = pending || result.pending;
      after = result.next_after_series_id || '';
      if (after) continue;
      if (pending) { pending = false; continue; }
      return [...errors.values()];
    }
    throw new Error('Восстановлена часть регулярных задач. Нажмите «Попробовать снова», чтобы продолжить загрузку большого периода.');
  };
  P.renderCalendar = async function (container, route, isCurrent) {
    renderedLayout = layoutKey();
    const value = state(route), range = C.range(value.mode, value.date);
    remember(value);
    const tab = document.querySelector('.pl-tabs a[aria-current="page"]');
    if (tab) tab.href = saved;
    // Canonical URL also preserves responsive defaults when opening the editor.
    const current = new URLSearchParams(route.params); current.set('mode', value.mode); current.set('date', value.date);
    window.history.replaceState(null, '', '#/plans/calendar?' + current);
    window.budgetViews.rekey();
    let data, recurrenceErrors;
    try {
      recurrenceErrors = await P.materializeCalendar(range.to, isCurrent);
      if (!isCurrent()) return false;
      data = await P.api('/calendar?from=' + range.from + '&to=' + range.to);
    } catch (error) {
      if (error.superseded) throw error;
      if (!isCurrent()) return false;
      const recovery = el('div', { class: 'pl-empty' }, el('h2', {}, 'Не удалось загрузить планы'), el('p', {}, error.message));
      const controls = el('div', { class: 'pc-filters' });
      const date = P.field(controls, 'recovery_date', 'Выбрать дату', 'date', value.date);
      controls.append(button('Открыть выбранный день', () => { if (C.valid(date.value)) go(value, { mode: 'day', date: date.value }); }), button('Попробовать снова', P.refresh));
      recovery.append(controls); container.replaceChildren(recovery);
      return false;
    }
    if (!isCurrent()) return false;
    officialDays = new Map((data.calendar_days||[]).map(d=>[d.date,d]));
    P.data = { projects: data.projects, tasks: [] };
    container.replaceChildren();
    if (recurrenceErrors?.length) container.append(el('div', { class: 'pl-note', role: 'alert' }, 'Часть регулярных задач не создана: ' + recurrenceErrors.join('; ')));
    const title = value.mode === 'month' ? C.caption(value.date, { month: 'long', year: 'numeric' }) : value.mode === 'week' ? P.date(range.from) + ' — ' + P.date(range.to) : C.caption(value.date, { day: 'numeric', month: 'long', year: 'numeric' });
    const previous = button('‹', () => go(value, { date: value.mode === 'month' ? C.addMonths(value.date, -1) : C.addDays(value.date, value.mode === 'week' ? -7 : -1) }), 'pc-arrow'); previous.setAttribute('aria-label', 'Предыдущий период');
    const next = button('›', () => go(value, { date: value.mode === 'month' ? C.addMonths(value.date, 1) : C.addDays(value.date, value.mode === 'week' ? 7 : 1) }), 'pc-arrow'); next.setAttribute('aria-label', 'Следующий период');
    const modes = el('div', { class: 'pc-modes', role: 'group', 'aria-label': 'Вид календаря' }, [['month', 'Месяц'], ['week', 'Неделя'], ['day', 'День']].map(([mode, name]) => { const b = button(name, () => go(value, { mode }), mode === value.mode ? 'pl-primary' : ''); b.setAttribute('aria-pressed', String(mode === value.mode)); return b; }));
    const create = button('+ Задача на день', () => P.newTask(value.project !== 'none' && !value.project.startsWith('series:') ? value.project : null, null, value.date), 'pl-primary pc-create');
    create.setAttribute('aria-label', '+ Задача на день');
    if (data.projects.find(p => p.id === value.project)?.archived_at) { create.disabled = true; create.title = 'Архивный проект: только просмотр'; }
    container.append(el('div', { class: 'pc-toolbar' }, el('div', { class: 'pc-period' }, previous, el('h2', {}, title), next), el('div', { class: 'pl-actions' }, button('Сегодня', () => go(value, { date: P.today(),mode:'month' })), modes, create)));
    const filters = el('div', { class: 'pc-filters' });
    const project = P.field(filters, 'calendar_project', 'Проект', 'select', value.project, [['', 'Все проекты'], ['none', 'Без проекта'], ...P.projectFilterOptions(data.projects,value.project)]);
    const datePicker = P.field(filters, 'calendar_date', 'Выбранный день', 'date', value.date);
    datePicker.addEventListener('change', () => { if (C.valid(datePicker.value)) go(value, { date: datePicker.value }); });
    project.addEventListener('change', () => go(value, { project: project.value }));
    const filterPanel = el('details', { class: 'pc-filter-panel', open: !mobile() || filterOpen }, el('summary', {}, 'Фильтры' + (value.project ? ' · ' + (data.projects.find(p => p.id === value.project)?.title || 'Без проекта') : '')), filters);
    filterPanel.addEventListener('toggle', () => { if (mobile() && filterPanel.isConnected) filterOpen = filterPanel.open; });
    container.append(filterPanel);
    const items = data.items.filter(item => C.visible(item, {...value,project:''}) && P.matchesProject(item.project_id,value.project,data.projects)).sort(C.compareEvents);
    const chips=el('div',{class:'pc-active-filters','aria-label':'Активные фильтры'});
    if(value.project){const name=value.project==='none'?'Без проекта':data.projects.find(p=>p.id===value.project||'series:'+p.repeat_series_id===value.project)?.title||'Недоступный проект';chips.append(button('Проект: '+name+' ×',()=>go(value,{project:''}),'pc-filter-chip'));}
    if(!value.completed)chips.append(el('span',{class:'pl-muted'},'Выполненные скрыты · настройка планировщика'));
    if(value.cancelled)chips.append(el('span',{class:'pl-muted'},'Отменённые показаны'));
    const countText='Показано '+items.length+' из '+data.items.length+' событий';
    filterPanel.querySelector('summary').textContent='Фильтры · '+countText;
    chips.append(el('span',{class:'pl-muted pc-filter-count'},countText));
    if(value.project)chips.append(button('Сбросить фильтр проекта',()=>go(value,{project:''}),'pc-filter-reset'));

    container.append(chips);
    P.calendarDayInfo(container,data,value.date);
    const schedule = el('div', { class: 'pc-calendar' }); container.append(schedule);
    if (value.mode === 'month') month(schedule, value, items, range);
    else {
      if (value.mode === 'week' && compactWeek()) {
        const strip = el('div', { class: 'pc-week-strip', 'aria-label': 'Дни недели' });
        C.days(range.from, range.to).forEach(day => {
          const b = dayButton(day, value, () => go(value, { date: day })); b.setAttribute('aria-pressed', String(day === value.date));
          b.addEventListener('keydown', event => { if (['ArrowLeft', 'ArrowRight'].includes(event.key)) { event.preventDefault(); go(value, { date: C.addDays(day, event.key === 'ArrowLeft' ? -1 : 1) }); } });
          strip.append(b);
        });
        schedule.append(strip);
      }
      const days = value.mode === 'week' && !compactWeek() ? C.days(range.from, range.to) : [value.date];
      if (!items.some(item => days.includes(item.date))) schedule.append(el('p', { class: 'pc-empty', role: 'status' }, 'На выбранные дни ничего не запланировано. Можно добавить задачу.'));
      agenda(schedule, value, items, days);
    }
    container.budgetAfterMount = () => {
      if (focusDay) { focusDay = false; const b = [...container.querySelectorAll('.pc-day-button')].find(n => n.dataset.day === value.date); b?.focus(); }
    };
    container.append(el('p', { class: 'pc-legend' }, '◷ Период работы · ◇ Плановая дата · ⚑ Крайний срок. Все даты и время — по Москве. Перенос доступен через ⋯' + (!mobile() ? ' или перетаскиванием.' : '.')));
  };
  function resizeCalendar() {
    // Ignore transient viewport changes (window resizing / full-page capture).
    // Only a settled change of layout needs another range projection.
    clearTimeout(resizeTimer);
    resizeTimer=setTimeout(()=>{
      if(renderedLayout!==layoutKey() && P.route().view === 'calendar' && !document.querySelector('.pl-drawer, .pl-dialog')) P.refresh();
    },200);
  }
  window.addEventListener('resize',resizeCalendar);
  for(const query of ['(max-width: 700px)','(max-width: 900px)'])window.matchMedia(query).addEventListener('change',resizeCalendar);
})(window.Planner);
