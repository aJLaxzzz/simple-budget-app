(function (P) {
  'use strict';
  const names = { overdue: 'Просрочен крайний срок', plan_missed: 'Не выполнено по плану', today: 'Сегодня', tomorrow: 'Завтра', week: 'На этой неделе', month: 'В ближайшие 30 дней', later: 'Позже', undated: 'Без даты', completed: 'Завершённые', cancelled: 'Отменённые', skipped: 'Пропущенные' };
  P.taskSection = function(task,today=P.today()) {
    const C=P.calendarModel, tomorrow=C.addDays(today,1), weekEnd=C.addDays(C.weekStart(today),6);
    const date=task.planned_date||task.due_date;
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
    return key;
  };
  P.compareTaskDates = (a,b) => (a.planned_date||a.due_date||'').localeCompare(b.planned_date||b.due_date||'') || (a.scheduled_time||'').localeCompare(b.scheduled_time||'') || a.title.localeCompare(b.title,'ru') || a.id.localeCompare(b.id);
  // Presentation of civil dates only; recurrence and lifecycle decisions stay on the server.
  P.taskGroups = function (tasks, today = P.today()) {
    const groups = new Map(Object.entries(names).map(([key, title]) => [key, { key, title, tasks: [] }]));
    const seen = new Set();
    for (const task of tasks) {
      if (seen.has(task.id)) continue;
      seen.add(task.id);
      const key=P.taskSection(task,today);
      groups.get(key)?.tasks.push(task);
    }
    for (const group of groups.values()) group.tasks.sort(P.compareTaskDates);
    return [...groups.values()].filter(g => g.tasks.length);
  };
  // Series keys are explicit; legacy concrete project IDs still match one instance.
  P.projectFilterOptions=function(projects,selected=''){
    const seen=new Set(),out=[];
    for(const p of projects.filter(p=>!p.archived_at)){const key=p.repeat_series_id?'series:'+p.repeat_series_id:p.id;if(seen.has(key))continue;seen.add(key);out.push([key,p.title+(p.repeat_series_id?' · ↻':p.archived_at?' · архив':'')]);}
    const concrete=projects.find(p=>p.id===selected);
    if(concrete?.repeat_series_id)out.push([concrete.id,concrete.title+' · только это выполнение']);
    return out;
  };
  P.matchesProject=function(projectID,selected,projects=P.data.projects){
    if(!selected||selected==='all')return true;
    if(selected==='none')return !projectID;
    if(selected.startsWith('series:'))return projects.some(p=>p.id===projectID&&p.repeat_series_id===selected.slice(7));
    return projectID===selected;
  };
  P.limitUpcoming=function(tasks,all=false,today=P.today(),limit=P.repeatLimit||3){
    if(all)return tasks;
    const counts=new Map(),allowed=new Set();
    for(const t of [...tasks].sort((a,b)=>Number(Boolean(a.dynamic))-Number(Boolean(b.dynamic))||(a.planned_date||'9999').localeCompare(b.planned_date||'9999')||(a.cycle_index??0)-(b.cycle_index??0)||a.id.localeCompare(b.id))){
      if(!t.series_id||!P.active(t)||!t.planned_date||(!t.dynamic&&(t.planned_date<today||(t.due_date&&t.due_date<today)))){allowed.add(t.id);continue;}
      const count=counts.get(t.series_id)||0;if(count<limit){allowed.add(t.id);counts.set(t.series_id,count+1);}
    }return tasks.filter(t=>allowed.has(t.id));
  };
  P.sortProjectTasks=function(tasks,mode='planned'){
 const date=t=>mode==='due'?(t.due_date||'9999-12-31'):(t.planned_date||t.due_date||'9999-12-31');
 const priorities={high:0,normal:1,low:2};
 return [...tasks].sort((a,b)=> (mode==='manual'?(a.sort_order||0)-(b.sort_order||0):mode==='title'?a.title.localeCompare(b.title,'ru'):mode==='priority'?priorities[a.priority]-priorities[b.priority]:mode==='created'?(a.created_at||'').localeCompare(b.created_at||''):date(a).localeCompare(date(b))||(a.scheduled_time||'').localeCompare(b.scheduled_time||'')) || (a.created_at||'').localeCompare(b.created_at||'') || a.id.localeCompare(b.id));
 };
  let saved={};try{saved=JSON.parse(localStorage.getItem('planner.group-expansion')||'{}');}catch(_){}
 const expanded = new Map(Object.entries(saved));
  P.groupProjects = () => {try{return localStorage.getItem('planner.project-groups')!=='off';}catch(_){return true;}};
  const projectExpanded=new Set();
  P.projectTaskGroups=function(tasks,allTasks,section,today=P.today(),projects=P.data?.projects||[]) {
    const groups=new Map(),out=[];
    const byID=new Map(projects.map(p=>[p.id,p]));
    const owner=t=>byID.get(t.project_id)?.repeat_series_id?'series:'+byID.get(t.project_id).repeat_series_id:'project:'+t.project_id;
    const key=t=>JSON.stringify([section,owner(t),t.planned_date||t.due_date||'undated']);
    for(const task of tasks){
      if(!task.project_id){out.push({task});continue;}
      const id=key(task);let group=groups.get(id);
      if(!group){group={key:id,projectID:task.project_id,seriesID:byID.get(task.project_id)?.repeat_series_id,projectIDs:new Set(),date:task.planned_date||task.due_date, tasks:[],members:[]};groups.set(id,group);out.push(group);}
      group.tasks.push(task);group.projectIDs.add(task.project_id);
    }
    const terminal=['completed','cancelled','skipped'].includes(section);
    for(const task of allTasks){
      const group=groups.get(key(task));
      if(group && P.taskSection(terminal?task:{...task,status:'planned'},today)===section)group.members.push(task);
    }
    for(const group of groups.values()){
      // Visible rows may be a filtered subset. Completion comes from all real
      // statuses on this project/date/section, not from the active-row count.
      const members=new Map(group.members.map(t=>[t.id,t]));group.tasks.forEach(t=>members.set(t.id,t));
      group.members=[...members.values()];group.completed=group.members.filter(t=>t.status==='completed').length;
      const times=group.members.map(t=>t.scheduled_time).filter(Boolean).sort();
      group.time=times.length?(times[0]===times.at(-1)?times[0]:times[0]+'–'+times.at(-1)):'';
    }
    for(const group of groups.values()){group.tasks.sort(P.compareTaskDates);group.sortTask=[...group.members.filter(P.active)].sort(P.compareTaskDates)[0]||group.tasks[0];}
    return out.sort((a,b)=>P.compareTaskDates(a.task||a.sortTask,b.task||b.sortTask));
  };
  P.groupedTasks = function (tasks, searching=false, membershipTasks=P.data.tasks) {
    const root = P.el('div', {class:'pl-groups'});
    const projects = new Map(P.data.projects.map(p => [p.id, p]));
    const parents = new Map(P.data.tasks.map(t => [t.id, t.title]));
    const row=(task,multiple=false)=>P.taskRow(task,false,[multiple?'Выполнение · '+P.projectDateLabel(projects.get(task.project_id)||{}):projects.get(task.project_id)?.title,task.parent_task_id&&('Подзадача · '+(task.parent_title||parents.get(task.parent_task_id)||'Открыть родителя'))].filter(Boolean).join(' · '));
    for (const group of P.taskGroups(tasks)) {
      const section = P.el('details', {class:'pl-task-group pl-group-'+group.key, 'data-group':group.key, open:searching || (expanded.get(group.key) ?? group.key!=='later')});
      section.append(P.el('summary', {}, P.el('span', {}, group.title), P.el('span', {class:'pl-group-count'}, String(group.tasks.length))));
      section.addEventListener('toggle', () => { if (section.isConnected && !searching) {expanded.set(group.key, section.open);try{localStorage.setItem('planner.group-expansion',JSON.stringify(Object.fromEntries(expanded)));}catch(_){}} });
      const entries=P.groupProjects()?P.projectTaskGroups(group.tasks,membershipTasks,group.key):group.tasks.map(task=>({task}));
      const list = P.el('div', {class:'pl-task-list'+(entries.some(entry=>entry.key)?' pl-grouped-list':'')});
      for (const entry of entries) {
        if(entry.task){list.append(row(entry.task));continue;}
        const project=projects.get(entry.projectID)||{title:'Проект'};
        const card=P.paint(P.el('details',{class:'pl-project-task-group','data-project-group':entry.key,'data-project-id':entry.projectIDs.size===1?entry.projectID:'','data-series-id':entry.seriesID||'',open:projectExpanded.has(entry.key)}),project);
        card.budgetIdentity=JSON.stringify([[...entry.projectIDs].map(id=>projects.get(id)),entry.tasks,entry.members]);
        const summary=P.el('summary',{},P.appearanceIcon(project,true),P.el('span',{class:'pl-project-group-copy'},P.el('strong',{},project.title),P.el('span',{class:'pl-muted'},[P.date(entry.date),entry.time,entry.projectIDs.size>1?'Выполнений: '+entry.projectIDs.size:project.repeat_series_id&&project.start_date?'Выполнение · '+P.projectDateLabel(project):''].filter(Boolean).join(' · '))),P.el('span',{class:'pl-project-group-progress'},entry.completed+' / '+entry.members.length,P.el('span',{class:'pl-muted'},' выполнено')));
        summary.setAttribute('aria-label',project.title+' · '+P.date(entry.date)+' · выполнено '+entry.completed+' из '+entry.members.length);
        const progress=P.el('progress',{max:entry.members.length,value:entry.completed,'aria-label':'Выполнение задач группы'});
        summary.append(progress);card.append(summary,P.el('div',{class:'pl-project-group-tasks'},entry.tasks.map(t=>row(t,entry.projectIDs.size>1))));
        card.addEventListener('toggle',()=>{if(card.isConnected){if(card.open)projectExpanded.add(entry.key);else projectExpanded.delete(entry.key);}});
        list.append(card);
      }
      section.append(list); root.append(section);
    }
    return root;
  };

})(window.Planner);
