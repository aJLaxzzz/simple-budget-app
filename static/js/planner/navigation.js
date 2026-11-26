(function (P) {
  'use strict';
  const { el, button } = P;
  P.navGroup = route => ['projects','templates'].includes(route.view) || (route.view === 'series' && (route.params.get('source') || route.params.get('filter')) === 'project') ? 'projects' : ['tasks','inbox','series','task-templates'].includes(route.view) ? 'tasks' : route.view;
  P.navigation = function (page, route) {
    const group = P.navGroup(route);
    const ready = window.AppNavigation.render('plans',route);
    if(group==='tasks'&&route.view==='tasks')page.append(el('h1',{class:'pl-mobile-screen-title'},'Задачи'));
    if (group==='tasks' || group==='projects') {
      const project = group==='projects';
      const entries = project ? [['#/plans/projects','Все проекты','all'],['#/plans/projects?view=regular','Регулярные проекты','regular']] : [['#/plans/tasks','Все задачи','all'],['#/plans/tasks?view=regular','Регулярные','regular'],['#/plans/tasks?view=undated','Без даты','undated']];
      const current = ['templates','task-templates'].includes(route.view)?'templates':route.view==='series'?'regular':route.params.get('view')||'all';
      page.append(el('nav', {class:'pl-subnav','aria-label':project?'Представления проектов':'Представления задач'}, entries.map(([href,title,key]) => el('a',{href,...(current===key && route.view!=='inbox'?{'aria-current':'page',class:'is-active'}:{})},title))));
    }
    return ready;
  };
  P.compactTask = async function (route = P.route()) {
    const date = route.view==='calendar' ? route.params.get('date') || P.today() : '';
    const project = route.view==='projects' && route.id ? route.id : route.view==='calendar' && route.params.get('project')!=='none' ? route.params.get('project') || '' : '';
    const key = 'compact:'+project+':'+date;
    const {dialog,close} = P.dialog('Новая задача');
    const form = el('form', {'aria-label':'Быстрое создание',class:'pl-compact-create'});
    const draft = P.drafts.get(key) || {title:'',project_id:project,planned_date:date};
    const title = P.field(form,'title','Название','text',draft.title); title.required=true;
    const extras = el('details', {open:Boolean(project||date)}, el('summary',{},'Дата и проект · необязательно')); form.append(extras);
    const when = P.field(extras,'planned_date','Плановая дата','date',draft.planned_date);
    const choice = P.field(extras,'project_id','Проект','select',draft.project_id,[['','Без проекта'],...(project&&!P.data.projects.some(p=>p.id===project)?[[project,'Текущий проект']]:[]),...P.data.projects.filter(p=>!p.archived_at).map(p=>[p.id,p.title])]);
    form.addEventListener('input',()=>P.drafts.set(key,P.values(form)));
    const send=P.commandSender('/tasks');
    P.bindForm(form,()=>send({title:title.value.trim(),planned_date:when.value||null,project_id:choice.value||null}),async saved=>{
      P.drafts.delete(key);close(true);P.notify('Задача «'+saved.title+'» создана',{label:'Открыть',run:()=>P.openTask(saved.id)});
      if(!['tasks','inbox','calendar'].includes(route.view) && !(route.view==='projects'&&route.id)){P.navigate('#/plans/tasks');return;}
      // Show new work even when a closed-status/search filter would conceal it.
      if(route.view==='tasks'||route.view==='inbox') {
        const q=new URLSearchParams(route.params);['q','status','project','task'].forEach(k=>q.delete(k));
        if(q.get('view')==='regular' || (q.get('view')==='undated' && saved.planned_date))q.delete('view');
        P.replaceRoute(P.base()+(q.size?'?'+q:''));
      }
      await P.refresh();
      const row=[...document.querySelectorAll('[data-task-id]')].find(n=>n.dataset.taskId===saved.id);
      const group=row?.closest('details');if(group)group.open=true;
    });
    dialog.append(form);title.focus();
    // Load selectable projects when opened from screens without a project list.
    try {
      const result=await P.api('/projects');if(!dialog.isConnected)return;
      const selected=choice.value || draft.project_id;
      choice.replaceChildren(el('option',{value:''},'Без проекта'),...result.items.map(p=>el('option',{value:p.id},p.title)));
      choice.value=selected || '';
    } catch(e) { form.querySelector('[role=alert]').replaceChildren(el('p',{},e.message)); form.querySelector('[role=alert]').hidden=false; }
  };
})(window.Planner);
