(function (P) {
  'use strict';
  const names = { overdue: 'Просрочен крайний срок', plan_missed: 'Не выполнено по плану', today: 'Сегодня', tomorrow: 'Завтра', week: 'На этой неделе', month: 'Позже в этом месяце', later: 'Позже', undated: 'Без даты', completed: 'Завершённые', cancelled: 'Отменённые', skipped: 'Пропущенные' };
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
      else if (task.due_date && task.due_date < today) key = 'overdue';
      else if (task.planned_date && task.planned_date < today) key = 'plan_missed';
      else if (!date) key = 'undated';
      else if (date === today) key = 'today';
      else if (date === tomorrow) key = 'tomorrow';
      else if (date <= weekEnd) key = 'week';
      else if (date.slice(0, 7) === today.slice(0, 7)) key = 'month';
      else key = 'later';
      groups.get(key)?.tasks.push(task);
    }
    for (const group of groups.values()) group.tasks.sort((a,b) => (a.planned_date || a.due_date || '').localeCompare(b.planned_date || b.due_date || '') || a.title.localeCompare(b.title,'ru') || a.id.localeCompare(b.id));
    return [...groups.values()].filter(g => g.tasks.length);
  };
  const expanded = new Map();
  P.groupedTasks = function (tasks) {
    const root = P.el('div', {class:'pl-groups'});
    const projects = new Map(P.data.projects.map(p => [p.id, p.title]));
    const parents = new Map(P.data.tasks.map(t => [t.id, t.title]));
    for (const group of P.taskGroups(tasks)) {
      const section = P.el('details', {class:'pl-task-group pl-group-'+group.key, 'data-group':group.key, open:expanded.get(group.key) ?? true});
      section.append(P.el('summary', {}, P.el('span', {}, group.title), P.el('span', {class:'pl-group-count'}, String(group.tasks.length))));
      section.addEventListener('toggle', () => { if (section.isConnected) expanded.set(group.key, section.open); });
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
