(function(P){
 'use strict';const {el,button,meta}=P;
 P.templateContext=route=>({date:route.view==='calendar'?route.params.get('date')||P.today():'',project:route.view==='projects'&&route.id?route.id:route.view==='calendar'&&route.params.get('project')!=='none'?route.params.get('project')||'':''});
 P.taskBlueprint=t=>({title:t.title,description:t.description||'',priority:t.priority||'normal',color_id:t.color_id||null,icon_id:t.icon_id||null});
 P.taskTemplateEditor=async function(template,source){
  const key='task-template:'+(template?.id||source?.id||'new');const initial=P.drafts.get(key)||P.taskBlueprint(template?.snapshot.tasks[0]||(source?{...source,color_id:source.color_id||source.effective_color_id,icon_id:source.icon_id||source.effective_icon_id}:{}));
  const {dialog,close}=P.dialog(template?'Изменить шаблон задачи':'Сохранить как шаблон');dialog.classList.add('pl-management-drawer');const form=el('form',{'aria-label':'Шаблон задачи'});
  form.append(meta('Сохраняются название, описание, приоритет и оформление. Новая задача начнётся без выполнения, истории и старых дат. Подзадачи и периоды работы не копируются.'));
  const appearance=P.appearanceField(form,initial,false);const title=P.field(form,'title','Название шаблона','text',initial.title||'');title.required=true;
  const extras=el('details',{},el('summary',{},'Описание и приоритет'));form.append(extras);const description=P.field(extras,'description','Описание','textarea',initial.description);const priority=P.field(extras,'priority','Приоритет','select',initial.priority,['low','normal','high'].map(k=>[k,P.labels[k]]));
  const read=()=>({...appearance(),title:title.value,description:description.value,priority:priority.value});form.addEventListener('input',()=>P.drafts.set(key,read()));
  const send=P.commandSender(template?'/task-templates/'+template.id+'/versions':'/task-templates');
  P.bindForm(form,()=>send({snapshot:read(),...(template?{expected_revision:template.revision}:source?{source_task_id:source.id,expected_revision:source.revision}:{})}),async result=>{P.drafts.delete(key);close(true);P.notify('Шаблон задачи сохранён',{label:'Открыть шаблон',run:()=>P.navigate('#/plans/task-templates/'+result.template.id)});await P.refresh();},template||source?async()=>{const current=await P.api(template?'/task-templates/'+template.id:'/tasks/'+source.id);return {record:current.snapshot?.tasks[0]||current,accept(){if(template)template=current;else source=current;}};}:undefined);
  dialog.append(form);P.guardForm(form,key,initial,dialog);title.focus();
 };
 P.instantiateTaskTemplate=async function(t,context={}){
  const {dialog,close}=P.dialog('Создать задачу из шаблона');const form=el('form',{'aria-label':'Задача из шаблона'});form.append(el('h3',{},P.appearanceIcon(t.snapshot.tasks[0]),t.snapshot.title));
  const date=P.field(form,'planned_date','Плановая дата','date',context.date||'');const project=P.field(form,'project_id','Проект','select',context.project||'',[['','Без проекта'],...(context.project?[[context.project,'Текущий проект']]:[])]);
  const send=P.commandSender('/task-templates/'+t.id+'/instantiate');P.bindForm(form,()=>send({expected_revision:t.revision,planned_date:date.value||null,project_id:project.value||null}),async result=>{close(true);P.openTask(result.task.id);P.notify('Создана задача «'+result.task.title+'»');await P.refresh();},async()=>{const latest=await P.api('/task-templates/'+t.id);return {record:latest.snapshot.tasks[0],accept(){t=latest;}};});dialog.append(form);
  try{const projects=await P.api('/projects');if(!dialog.isConnected)return;const chosen=project.value;project.replaceChildren(el('option',{value:''},'Без проекта'),...projects.items.map(p=>el('option',{value:p.id},p.title)));project.value=chosen;}catch(e){P.notify(e.message);}
 };
 async function archive(t){const {dialog,close}=P.dialog('Архивировать шаблон задачи?');dialog.append(meta('Созданные задачи и история сохранятся.'));const form=el('form',{'data-submit':'Архивировать шаблон'});const send=P.commandSender('/task-templates/'+t.id+'/archive');P.bindForm(form,()=>send({expected_revision:t.revision}),async()=>{close(true);await P.refresh();});dialog.append(form);}
 P.renderTaskTemplates=async function(host,route){
  host.replaceChildren(el('div',{class:'pl-section-head'},el('div',{},el('h2',{},'Шаблоны задач'),meta('Заготовки для новых задач. Повторение настраивается отдельно.')),button('+ Шаблон задачи',()=>P.taskTemplateEditor(), 'pl-primary')));
  if(route.id){const t=await P.api('/task-templates/'+route.id);const b=t.snapshot.tasks[0];host.append(el('a',{href:'#/plans/task-templates'},'← Все шаблоны задач'),el('h2',{},P.appearanceIcon(b),t.snapshot.title),el('p',{},b.description),meta(t.archived?'В архиве':'Сохранённый шаблон'));if(!t.archived)host.append(el('div',{class:'pl-actions'},button('Создать задачу',()=>P.instantiateTaskTemplate(t),'pl-primary'),button('Изменить шаблон',()=>P.taskTemplateEditor(t)),button('Архивировать шаблон',()=>archive(t))));const history=P.disclosure('История шаблона');host.append(history);P.renderHistory(history,'template',t.id);return;}
  const filters=el('div',{class:'pl-filters'});const state=P.field(filters,'archived','Показывать','select','false',[['false','Текущие'],['true','Архив']]);const list=el('div',{class:'pl-project-grid'});let offset=0,busy=false,turn=0;
  const more=button('Ещё шаблоны',load);async function load(){if(busy)return;busy=true;more.disabled=true;const own=turn;try{const result=await P.api('/task-templates?archived='+state.value+'&offset='+offset);if(own!==turn)return;for(const t of result.items){const b=t.snapshot.tasks[0];list.append(P.paint(el('article',{class:'pl-project-card pl-task-template-card'},el('a',{href:'#/plans/task-templates/'+t.id},el('h3',{},P.appearanceIcon(b),t.snapshot.title)),el('p',{},b.description),null,!t.archived?button('Создать задачу',()=>P.instantiateTaskTemplate(t)):null),b));}offset+=result.items.length;more.hidden=result.items.length<50;if(!offset)list.append(P.empty('Сохраните полезную заготовку','Откройте задачу и выберите «Сохранить как шаблон».'));}catch(e){P.notify(e.message);}finally{busy=false;more.disabled=false;if(own!==turn)load();}}
  state.addEventListener('change',()=>{turn++;offset=0;list.replaceChildren();load();});host.append(filters,list,more);await load();
 };
 P.chooseTemplate=async function(route=P.route()){
  const context=P.templateContext(route);const {dialog,close}=P.dialog('Создать из шаблона');const form=el('div',{class:'pl-template-chooser'});const kind=P.field(form,'kind','Что создать','select','task',[['task','Задачу'],['project','Проект']]);const search=P.field(form,'search','Поиск шаблона','search','');const list=el('div',{class:'pl-menu-items'});const more=button('Ещё шаблоны',()=>load(false));let items=[],offset=0,token=0;
  const draw=()=>{list.replaceChildren(...items.filter(t=>t.snapshot.title.toLocaleLowerCase('ru').includes(search.value.toLocaleLowerCase('ru'))).map(t=>button(t.snapshot.title,()=>{close(true);if(kind.value==='task')P.instantiateTaskTemplate(t,context);else P.instantiateProjectTemplate(t,context);})));if(!items.length)list.append(meta('Пока нет шаблонов. Их можно сохранить из карточки задачи или проекта.'));};
  async function load(reset=false){const own=++token;more.disabled=true;if(reset){items=[];offset=0;list.replaceChildren(meta('Загружаем шаблоны…'));}try{const result=await P.api((kind.value==='task'?'/task-templates':'/templates')+'?offset='+offset+'&limit=50');if(own!==token)return;items.push(...result.items);offset+=result.items.length;more.hidden=result.items.length<50;draw();}catch(e){list.replaceChildren(el('p',{role:'alert'},e.message),button('Повторить',()=>load(true)));}finally{if(own===token)more.disabled=false;}}
  kind.addEventListener('change',()=>load(true));search.addEventListener('input',draw);form.append(list,more);dialog.append(form);await load(true);
 };
 P.projectTemplateFlow=async function(project,repeat=false){
  if(P.guardActive&&!await P.guardActive())return;
  try{
   project=await P.api('/projects/'+project.id);let anchor=project.start_date||P.today();
   const initial=await P.api('/projects/'+project.id+'/template-preview','POST',{expected_revision:project.revision,anchor_date:anchor});
   let builder,end,input,applyForm,applyArea,mainForm,send=P.commandSender('/projects/'+project.id+'/save-template');let selected;
   const key='project-source:'+project.id+':'+repeat;
   P.editProjectTemplate(null,{key,title:repeat?'Повторять проект':'Сохранить проект как шаблон',snapshot:initial.snapshot,
    intro:'Исходный проект останется самостоятельным: его задачи, даты и история сохранятся. '+(repeat?'Новые проекты начнутся с выбранной даты следующего повторения. ':'')+'В заготовку войдут задачи и подзадачи с оформлением. Выполнение и периоды работы не копируются.',
    mount(form,dialog){mainForm=form;form.dataset.submit='Посмотреть результат';const base=P.field(form,'anchor_date','Относительные даты рассчитаны от','date',anchor);base.readOnly=true;
     if(repeat){builder=P.ruleBuilder(form,{project:true,firstDate:P.calendarModel.addDays(P.today(),14)});builder.first.previousElementSibling.textContent='Дата следующего проекта';end=P.field(form,'end_date','Создавать проекты до (необязательно)','date','');}
     const invalidate=()=>{selected=null;if(applyArea)applyArea.hidden=true;};form.addEventListener('input',invalidate);form.addEventListener('change',invalidate);
    },
    async submit(snapshot){project=await P.api('/projects/'+project.id);input={expected_revision:project.revision,anchor_date:anchor,snapshot,...(repeat?{rule:builder.read(),first_date:builder.first.value,...(end.value?{end_date:end.value}:{})}:{})};return P.api('/projects/'+project.id+'/template-preview','POST',input);},
    done(result,form,dialog,close){delete form.dataset.saved;selected={...input,preview_token:result.preview_token};applyArea?.remove();applyArea=el('section',{class:'pl-source-preview'},el('h3',{},repeat?'Будущие проекты':'Состав шаблона'),meta('Исходный проект не изменится. Новые задачи будут открытыми.'));
     const compositions=result.items.length?result.items:[{project:{tasks:result.snapshot.tasks}}];
     for(const item of compositions){const details=el('details',{open:compositions.length===1},el('summary',{},item.candidate?P.date(item.candidate.adjusted_date)+' · '+item.project.tasks.length+' задач':result.snapshot.tasks.length+' задач'));details.append(el('ul',{},item.project.tasks.map(t=>el('li',{},t.title+(t.planned_date?' · '+P.date(t.planned_date):'')+(t.due_date?' · крайний срок '+P.date(t.due_date):'')))));applyArea.append(details);}
     applyForm=el('form',{'aria-label':'Подтверждение шаблона'});applyForm.dataset.submit=repeat?'Включить повторение':'Сохранить шаблон';
     let locked;P.bindForm(applyForm,()=>{if(!selected)throw new Error('Повторите предпросмотр.');locked||=[...mainForm.elements].map(c=>[c,c.disabled]);locked.forEach(([c])=>c.disabled=true);return send(selected);},async out=>{P.drafts.delete(key);close(true);P.notify(repeat?'Повторение проекта включено':'Шаблон проекта сохранён');await P.refresh();});
     applyForm.addEventListener('planner-failed',()=>{queueMicrotask(()=>{if(!applyForm.dataset.uncertain){mainForm.closest('.pl-preview-settings').open=true;locked?.forEach(([c,disabled])=>c.disabled=disabled);locked=null;selected=null;applyForm.querySelector('button[type=submit]').disabled=true;applyArea.append(meta('Черновик сохранён. Нажмите «Посмотреть результат» ещё раз.'));}});});
     let settings=form.closest('.pl-preview-settings');if(!settings){settings=el('details',{class:'pl-preview-settings'},el('summary',{},'1. Настройка'));form.before(settings);settings.append(form);}settings.open=false;applyArea.append(applyForm,button('Изменить настройки',()=>{settings.open=true;P.showPreview(settings);}));dialog.append(applyArea);P.showPreview(applyArea);
    }
   });
  }catch(e){P.notify(e.message);}
 };
 P.taskProperties=async function(host,record){
  const box=el('section',{class:'pl-entity-properties','aria-label':'Повторение и шаблон'});host.append(box);
  if(record.series_id){let action='changeRule';const change=button('Изменить повторение',()=>P.runAction(record,action));box.append(change);try{const s=await P.api('/series/'+record.series_id+'?limit=1');if(!box.isConnected)return;if(s.state==='archived'){action='series';change.textContent='История повторения';}box.prepend(meta(P.ruleText(s.rule.rule)+(s.state==='archived'?' · Будущие повторения прекращены':'')));}catch(e){box.append(meta(e.message));}}
  else if(['planned','in_progress','completed'].includes(record.status)&&!P.data.tasks.some(t=>t.parent_task_id===record.id))box.append(button('Сделать регулярной',()=>P.runAction(record,'repeat')));
  if(record.saved_template_id)box.append(el('a',{href:'#/plans/task-templates/'+record.saved_template_id},'Сохранено как шаблон'));
  else box.append(button('Сохранить как шаблон',()=>P.runAction(record,'saveTemplate')));
  if(record.source_template_id)box.append(el('a',{href:'#/plans/task-templates/'+record.source_template_id},'Создано из шаблона'));
 };
})(window.Planner);
