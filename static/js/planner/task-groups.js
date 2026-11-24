(function (P) {
  'use strict';
  const names = { overdue: 'Просрочен крайний срок', plan_missed: 'Не выполнено по плану', today: 'Сегодня', tomorrow: 'Завтра', week: 'На этой неделе', month: 'В ближайшие 30 дней', later: 'Позже', undated: 'Без даты', completed: 'Завершённые', cancelled: 'Отменённые', skipped: 'Пропущенные' };
  // Presentation of civil dates only; recurrence and lifecycle decisions stay on the server.
  P.taskGroups = function (tasks, today = P.today()) {
    const C = P.calendarModel, tomorrow = C.addDays(today, 1), weekEnd = C.addDays(C.weekStart(today), 6);
    const groups = new Map(Object.entries(names).map(([key, title]) => [key, { key, title, tasks: [] }]));
    const seen = new Set();
    for (const task of tasks) {
      if (seen.has(task.id)) continue;
      seen.add(task.id);
      const date = task.planned_date || task.due_date;
      let key;
      if (!P.active(task)) key = task.status;
      else if (task.dynamic && date && date<today) key='later';
      else if (!task.dynamic && task.due_date && task.due_date < today) key = 'overdue';
      else if (task.planned_date && task.planned_date < today) key = 'plan_missed';
      else if (!date) key = 'undated';
      else if (date === today) key = 'today';
      else if (date === tomorrow) key = 'tomorrow';
      else if (date <= weekEnd) key = 'week';
      else if (date <= C.addDays(today,30)) key = 'month';
      else key = 'later';
      groups.get(key)?.tasks.push(task);
    }
    for (const group of groups.values()) group.tasks.sort((a,b) => (a.planned_date || a.due_date || '').localeCompare(b.planned_date || b.due_date || '') || a.title.localeCompare(b.title,'ru') || a.id.localeCompare(b.id));
    return [...groups.values()].filter(g => g.tasks.length);
  };
  P.limitUpcoming=function(tasks,all=false,today=P.today()){
    if(all)return tasks;
    const counts=new Map(),allowed=new Set();
    for(const t of [...tasks].sort((a,b)=>Number(Boolean(a.dynamic))-Number(Boolean(b.dynamic))||(a.planned_date||'9999').localeCompare(b.planned_date||'9999')||(a.cycle_index??0)-(b.cycle_index??0)||a.id.localeCompare(b.id))){
      if(!t.series_id||!P.active(t)||!t.planned_date||(!t.dynamic&&(t.planned_date<today||(t.due_date&&t.due_date<today)))){allowed.add(t.id);continue;}
      const count=counts.get(t.series_id)||0;if(count<3){allowed.add(t.id);counts.set(t.series_id,count+1);}
    }return tasks.filter(t=>allowed.has(t.id));
  };
  P.sortProjectTasks=function(tasks,mode='planned'){
 const date=t=>mode==='due'?(t.due_date||'9999-12-31'):(t.planned_date||t.due_date||'9999-12-31');
 const priorities={high:0,normal:1,low:2};
 return [...tasks].sort((a,b)=> (mode==='manual'?(a.sort_order||0)-(b.sort_order||0):mode==='title'?a.title.localeCompare(b.title,'ru'):mode==='priority'?priorities[a.priority]-priorities[b.priority]:mode==='created'?(a.created_at||'').localeCompare(b.created_at||''):date(a).localeCompare(date(b))||(a.scheduled_time||'99:99').localeCompare(b.scheduled_time||'99:99')) || (a.created_at||'').localeCompare(b.created_at||'') || a.id.localeCompare(b.id));
 };
  let saved={};try{saved=JSON.parse(localStorage.getItem('planner.group-expansion')||'{}');}catch(_){}
 const expanded = new Map(Object.entries(saved));
  P.groupedTasks = function (tasks, searching=false) {
    const root = P.el('div', {class:'pl-groups'});
    const projects = new Map(P.data.projects.map(p => [p.id, p.title]));
    const parents = new Map(P.data.tasks.map(t => [t.id, t.title]));
    for (const group of P.taskGroups(tasks)) {
      const section = P.el('details', {class:'pl-task-group pl-group-'+group.key, 'data-group':group.key, open:searching || (expanded.get(group.key) ?? group.key!=='later')});
      section.append(P.el('summary', {}, P.el('span', {}, group.title), P.el('span', {class:'pl-group-count'}, String(group.tasks.length))));
      section.addEventListener('toggle', () => { if (section.isConnected && !searching) {expanded.set(group.key, section.open);try{localStorage.setItem('planner.group-expansion',JSON.stringify(Object.fromEntries(expanded)));}catch(_){}} });
      const list = P.el('div', {class:'pl-task-list'});
      for (const task of group.tasks) {
        const context = [projects.get(task.project_id), task.parent_task_id && ('Подзадача · '+(task.parent_title || parents.get(task.parent_task_id) || 'Открыть родителя'))].filter(Boolean).join(' · ');
        list.append(P.taskRow(task, false, context));
      }
      section.append(list); root.append(section);
    }
    return root;
  };
})(window.Planner);
