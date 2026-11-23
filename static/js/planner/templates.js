(function(P){
 'use strict';const {el,button,meta}=P;
 P.renderTemplates=async function(host,route){
   host.replaceChildren(el('div',{class:'pl-section-head'},el('div',{},el('h2',{},'Шаблоны проектов'),meta('Сохраните привычный порядок действий. Каждый проект начнёт собственную историю.')),button('+ Шаблон',()=>editTemplate(), 'pl-primary')));
   if(route.id){const template=await P.api('/templates/'+route.id);host.append(el('a',{href:'#/plans/templates'},'← Все шаблоны'));return show(host,template);}
   const filters=el('div',{class:'pl-filters'});const archive=P.field(filters,'archive','Показывать','select','false',[['false','Текущие'],['true','Архив']]);const list=el('div',{class:'pl-project-grid'});let offset=0;
   const more=button('Ещё шаблоны',load);
   async function load(){more.disabled=true;try{const result=await P.api('/templates?limit=50&offset='+offset+'&archived='+archive.value);for(const t of result.items)list.append(el('a',{class:'pl-project-card',href:'#/plans/templates/'+t.id},el('h3',{},t.snapshot.title),el('p',{},t.snapshot.description),meta(t.snapshot.tasks.length+' задач')));offset+=result.items.length;more.hidden=result.items.length<50;if(!offset)list.append(P.empty('Начните с привычного процесса','Уборка, подготовка отчёта или планирование поездки — сохраните шаги в шаблоне.'));}catch(e){P.notify(e.message);}finally{more.disabled=false;}}
   archive.addEventListener('change',()=>{offset=0;list.replaceChildren();load();});host.append(filters,list,more);await load();
 };
 async function show(host,t){
   host.append(el('h2',{},t.snapshot.title),el('p',{class:'pl-description'},t.snapshot.description),meta('Сохранённый шаблон'));
   const actions=el('div',{class:'pl-actions'});
   if(!t.archived){actions.append(button('Редактировать шаблон',()=>editTemplate(t)),button('Создать проект',()=>instantiate(t),'pl-primary'),button('Повторять проект',()=>P.createRecurring(null,t)),button('Архивировать шаблон',()=>archive(t)));}
   host.append(actions,treePreview(t.snapshot));
   const linked=(await P.seriesList()).filter(s=>s.template_id===t.id);
   host.append(el('h3',{},'Повторения из шаблона'),linked.length?el('div',{class:'pl-actions'},linked.map(s=>el('a',{href:'#/plans/series/'+s.id,class:'pl-button'},P.seriesTitle(s)))):meta('Повторение пока не настроено.'));
   const projects=await P.api('/templates/'+t.id+'/projects');host.append(el('h3',{},'Созданные проекты'),el('div',{class:'pl-actions'},(projects.items||[]).map(p=>el('a',{href:'#/plans/projects/'+p.id,class:'pl-button'},p.title+' · '+P.date(p.start_date)))));
   const history=P.disclosure('История шаблона');host.append(history);P.renderHistory(history,'template',t.id);
 }
 function offsetText(n){return n==null?'Без даты':n===0?'В день начала':n<0?'За '+(-n)+' дн. до начала':'Через '+n+' дн. после начала';}
 function treePreview(snapshot){return el('div',{class:'pl-template-tree'},snapshot.tasks.map(t=>el('div',{class:'pl-template-item'+(t.parent_key?' pl-child':'')},el('strong',{},t.title),meta('План: '+offsetText(t.planned_offset)+' · Срок: '+offsetText(t.due_offset)))));}
 function offsetField(host,name,label,value){
   const group=el('div',{class:'pl-offset-field'});host.append(group);
   const mode=P.field(group,name+'_mode',label,'select',value==null?'none':value===0?'same':value<0?'before':'after',[['none','Без даты'],['same','В день начала проекта'],['before','За N дней до начала'],['after','Через N дней после начала']]);
   const number=P.field(group,name+'_days','Количество дней','number',Math.abs(value||1));number.min=1;number.max=3650;
   const sync=()=>{number.parentElement.hidden=!['before','after'].includes(mode.value);number.required=!number.parentElement.hidden;};mode.addEventListener('change',sync);sync();
   return ()=>mode.value==='none'?null:mode.value==='same'?0:Number(number.value)*(mode.value==='before'?-1:1);
 }
 function editTemplate(template, workflow={}){
   const key=workflow.key||'template:'+(template?.id||'new');const snapshot=structuredClone(P.drafts.get(key)||workflow.snapshot||template?.snapshot||{title:'',description:'',tasks:[]});
   const {dialog,close}=P.dialog(workflow.title||(template?'Изменить шаблон проекта':'Новый шаблон'));dialog.classList.add('pl-wide-dialog','pl-management-drawer');const form=el('form',{'aria-label':'Шаблон проекта'});
   workflow.intro && form.append(meta(workflow.intro));
   const appearance=P.appearanceField(form,snapshot,true);
   const title=P.field(form,'title','Название шаблона','text',snapshot.title);title.required=true;const description=P.field(form,'description','Описание проекта','textarea',snapshot.description);
   const due=offsetField(form,'project_due','Крайний срок проекта',snapshot.due_offset);
   const tree=el('div',{class:'pl-template-tree'});const taskReaders=new Map();
   function capture(){Object.assign(snapshot,appearance());snapshot.title=title.value;snapshot.description=description.value;snapshot.due_offset=due();for(const t of snapshot.tasks){const read=taskReaders.get(t.key);if(read)Object.assign(t,read());}P.drafts.set(key,structuredClone(snapshot));}
   function draw(){taskReaders.clear();tree.replaceChildren();for(const t of snapshot.tasks){
     const row=el(workflow.submit?'details':'section',{class:'pl-template-edit'+(t.parent_key?' pl-child':'')});if(workflow.submit)row.append(el('summary',{},t.title+' · '+offsetText(t.planned_offset)));const head=el('div',{class:'pl-actions'});
     const appearance=P.appearanceField(row,t,false,()=>({parentAppearance:snapshot.tasks.find(x=>x.key===t.parent_key),projectAppearance:snapshot}),t.key+'_');
     const name=P.field(row,'task_'+t.key,'Название '+(t.parent_key?'подзадачи':'задачи'),'text',t.title);name.required=true;
     const desc=P.field(row,'description_'+t.key,'Описание','textarea',t.description||'');const priority=P.field(row,'priority_'+t.key,'Приоритет','select',t.priority||'normal',['low','normal','high'].map(k=>[k,P.labels[k]]));
     const dates=el('div',{class:'pl-form-grid'});row.append(dates);const plan=offsetField(dates,'plan_'+t.key,'Плановая дата',t.planned_offset);const deadline=offsetField(dates,'due_'+t.key,'Крайний срок',t.due_offset);
     const time=P.field(row,'time_'+t.key,'Время (необязательно) · Москва','time',t.scheduled_time||'');
     taskReaders.set(t.key,()=>({...appearance(),title:name.value,description:desc.value,priority:priority.value,planned_offset:plan(),due_offset:deadline(),...(time.value?{scheduled_time:time.value}:{scheduled_time:null})}));
     if(!t.parent_key)head.append(button('+ Подзадача',()=>{capture();const at=snapshot.tasks.findIndex(x=>x.key===t.key);snapshot.tasks.splice(at+1,0,{key:crypto.randomUUID(),parent_key:t.key,title:'',priority:'normal'});draw();}));
     for(const [label,direction]of [['↑',-1],['↓',1]])head.append(button(label,()=>{capture();const siblings=snapshot.tasks.filter(x=>x.parent_key===t.parent_key);const i=siblings.findIndex(x=>x.key===t.key);const other=siblings[i+direction];if(!other)return;const group=x=>snapshot.tasks.filter(y=>y.key===x.key||y.parent_key===x.key);const moving=group(t);snapshot.tasks=snapshot.tasks.filter(x=>!moving.includes(x));const otherGroup=group(other);const at=snapshot.tasks.indexOf(direction<0?other:otherGroup.at(-1));snapshot.tasks.splice(at+(direction>0?1:0),0,...moving);draw();}));
     head.append(button('Удалить',()=>{capture();snapshot.tasks=snapshot.tasks.filter(x=>x.key!==t.key&&x.parent_key!==t.key);draw();}));if(workflow.submit)row.insertBefore(head,row.children[1]);else row.prepend(head);tree.append(row);
   }form.dispatchEvent(new Event('input',{bubbles:true}));}
   form.append(el('h3',{},'Задачи и подзадачи'),tree,button('+ Задача шаблона',()=>{capture();snapshot.tasks.push({key:crypto.randomUUID(),title:'',priority:'normal'});draw();}));draw();
   form.append(el('p',{class:'pl-info'},'Сохраняется обновлённый шаблон. Существующие проекты не меняются. Чтобы обновить будущие проекты, откройте настройки повторения и посмотрите изменения.'));
   form.addEventListener('input',capture);
   workflow.mount?.(form,dialog,close);
   const send=P.commandSender(template?'/templates/'+template.id+'/versions':'/templates');
   P.bindForm(form,()=>{capture();const clean=structuredClone(snapshot);for(const task of clean.tasks)if(!task.scheduled_time)delete task.scheduled_time;if(workflow.submit)return workflow.submit(clean);return send({snapshot:clean,...(template?{expected_revision:template.revision}:{})});},async result=>{if(workflow.done)return workflow.done(result,form,dialog,close);P.drafts.delete(key);close(true);P.navigate('#/plans/templates/'+result.template.id);P.notify('Шаблон «'+result.template.snapshot.title+'» сохранён');await P.refresh();},template?async()=>{const latest=await P.api('/templates/'+template.id);return {record:{title:latest.snapshot.title,description:latest.snapshot.description,revision:latest.revision},related:latest.snapshot.tasks,accept(){template=latest;}};}:undefined);
   dialog.append(form);P.guardForm(form,key,template||{},dialog,{previewOnly:!!workflow.submit});
 }
 function instantiate(t,context={}){
   const key='instantiate-project:'+t.id;
   const initial={start_date:context.date||P.today()};
   const draft=P.drafts.get(key)||initial;
   const {dialog,close}=P.dialog('Создать проект из шаблона');
   const form=el('form',{'aria-label':'Создание проекта из шаблона','data-submit':'Создать проект'});
   const start=P.field(form,'start_date','Начало проекта','date',draft.start_date);start.required=true;
   const preview=el('section',{'aria-live':'polite'});
   const send=P.commandSender('/templates/'+t.id+'/instantiate');
   const capture=()=>{P.drafts.set(key,P.values(form));preview.replaceChildren();};
   form.addEventListener('input',capture);form.addEventListener('change',capture);
   const review=button('Посмотреть даты задач',async()=>{
     if(!form.reportValidity())return;
     review.disabled=true;const requested=start.value;
     try{const result=await P.api('/templates/'+t.id+'/preview','POST',{start_date:requested,expected_revision:t.revision});
       if(!dialog.isConnected||start.value!==requested)return;
       preview.replaceChildren(el('h3',{},'Задачи нового проекта'),...result.items.map(i=>el('ul',{},i.project.tasks.map(x=>el('li',{},x.title+' · '+P.date(x.planned_date)+(x.due_date?' · крайний срок '+P.date(x.due_date):''))))));
       P.showPreview(preview);
     }catch(e){preview.replaceChildren(el('p',{role:'alert'},P.errorMessage(e)));}finally{review.disabled=false;}
   });form.append(review,preview);
   P.bindForm(form,()=>send({start_date:start.value,expected_revision:t.revision}),async result=>{
     P.drafts.delete(key);close(true);P.navigate('#/plans/projects/'+result.project.id);
     P.notify('Проект «'+result.project.title+'» создан на '+P.date(result.project.start_date));
   },async()=>{const latest=await P.api('/templates/'+t.id);return {record:{title:latest.snapshot.title},accept(){t=latest;preview.replaceChildren();}};});
   dialog.append(form);P.guardForm(form,key,initial,dialog);start.focus();
 }

 function archive(t){const {dialog,close}=P.dialog('Архивировать шаблон?');dialog.append(meta('Проекты и повторения сохранятся. Архивирование шаблона не прекращает настроенные повторения.'));const form=el('form',{'data-submit':'Архивировать шаблон'});const send=P.commandSender('/templates/'+t.id+'/archive');P.bindForm(form,()=>send({expected_revision:t.revision}),async()=>{close(true);await P.refresh();});dialog.append(form);}
P.editProjectTemplate=editTemplate;P.instantiateProjectTemplate=instantiate;
})(window.Planner);
