(function(P){
 'use strict';const {el,button,meta}=P;
 P.updateAttentionCount=async nav=>{try{const result=await P.api('/attention?limit=1');if(!nav.isConnected)return;const link=nav.querySelector('[href="#/plans/attention"]');if(link){link.replaceChildren(document.createTextNode('Внимание'),el('span',{class:'pl-attention-count','aria-label':'Необработанных проблем: '+result.count},String(result.count)));}}catch{/* The view exposes retry and details; navigation stays usable. */}};
 P.renderAttention=async function(host,route){
   const tab=route.params.get('tab')||'needs_decision';const series=route.params.get('series_id');
   host.replaceChildren(el('div',{class:'pl-section-head'},el('div',{},el('h2',{},'Требуют внимания'),meta('Пропущенное не исчезает. Выберите, что делать дальше.'))));
   host.append(el('nav',{class:'pl-tabs','aria-label':'Фильтры внимания'},[['needs_decision','Требуют решения'],['overdue','Просроченные'],['skipped','Пропущенные'],['processed','Обработанные']].map(([key,title])=>el('a',{href:'#/plans/attention?tab='+key+(series?'&series_id='+series:''),class:key===tab?'is-active':''},title))));
   if(series)host.append(el('a',{href:'#/plans/attention?tab='+tab},'Показать все повторения'));
   const projects=await P.api('/projects');const projectNames=new Map(projects.items.map(p=>[p.id,p.title]));
   const list=el('div',{class:'pl-attention-list'});const groups=new Map();let offset=0;const more=button('Ещё',load);host.append(list,more);
   async function load(){more.disabled=true;try{const result=await P.api('/attention?tab='+tab+'&offset='+offset+'&limit=50'+(series?'&series_id='+series:''));for(const item of result.items){const groupKey=item.source==='pause'?item.series_id:null;let target=list;if(groupKey){if(!groups.has(groupKey)){const details=el('details',{class:'pl-attention-group'},el('summary',{},item.title+' · пропуски во время паузы'));groups.set(groupKey,details);list.append(details);}target=groups.get(groupKey);}target.append(row(item,projectNames));}offset+=result.items.length;more.hidden=result.items.length<50;if(!offset)list.append(P.empty('Всё спокойно','В этом списке пока нет выполнений, требующих просмотра.'));}catch(e){list.append(el('p',{role:'alert'},e.message));}finally{more.disabled=false;}}
   await load();
 };
 function row(item,projectNames){
   const card=el('article',{class:'pl-occurrence'},el('div',{},el('h3',{},item.title),meta((['task','project','series'].includes(item.kind)?(item.reason||'Требует решения'):item.kind==='skipped'?'Пропущено':'Отменено')+' · '+P.date(item.planned_date)),(['task','project'].includes(item.kind)?null:meta(item.reason||'')),item.snoozed_until>P.today()?meta('Напоминание отложено до '+P.date(item.snoozed_until)):null,item.disposition==='processed'?meta('Обработано · история сохранена'):null));
   if(item.project_id&&item.kind!=='project')card.firstChild.append(el('a',{class:'pl-attention-context',href:'#/plans/projects/'+item.project_id},'Проект: '+(projectNames.get(item.project_id)||item.title)));
   if(item.series_id)card.firstChild.append(el('a',{class:'pl-attention-context',href:'#/plans/series/'+item.series_id},'↻ Настройки повторения'));
   const actions=el('div',{class:'pl-actions'}), entries=[];
   const add=(label,run)=>entries.push({label,run});
   if(item.kind==='series'){actions.append(button(item.source==='anchor'?'Выбрать дату продолжения':'Открыть повторение',async()=>{try{if(item.source==='anchor')P.seriesChange(await P.api('/series/'+item.series_id),'reanchor');else P.navigate('#/plans/series/'+item.series_id);}catch(e){P.notify(e.message);}},'pl-primary'));}
   else if(item.occurrence_id){
     const allowed=P.occurrenceActions(item.status), primary=allowed.includes('reschedule')?'reschedule':allowed.includes('restore')?'restore':allowed[0];
     if(primary)actions.append(button(P.occurrenceLabels[primary],()=>P.openOccurrence(item.occurrence_id,primary),'pl-primary'));
     for(const action of allowed.filter(a=>a!==primary))add(P.occurrenceLabels[action],()=>P.openOccurrence(item.occurrence_id,action));
   }else if(item.task_id){
     actions.append(button('Перенести',async()=>{try{P.runAction(await P.api('/tasks/'+item.task_id),'reschedule');}catch(e){P.notify(P.errorMessage(e));}},'pl-primary'));
     for(const action of ['complete','cancel'])add(P.occurrenceLabels[action],async()=>{try{P.taskAction(await P.api('/tasks/'+item.task_id),action);}catch(e){P.notify(P.errorMessage(e));}});
   }else if(item.project_id){
     actions.append(button('Изменить крайний срок',()=>P.navigate('#/plans/projects/'+item.project_id+'?edit=project&id='+item.project_id),'pl-primary'));
     for(const action of ['complete','cancel'])add(action==='complete'?'Завершить проект':'Отменить проект',async()=>{try{P.projectAction(await P.api('/projects/'+item.project_id),action);}catch(e){P.notify(P.errorMessage(e));}});
   }
   if(item.task_id)add('Открыть задачу',()=>P.openTask(item.task_id));else if(item.project_id)add('Открыть проект',()=>P.navigate('#/plans/projects/'+item.project_id));
   if(item.series_id)add('Настройки повторения',()=>P.navigate('#/plans/series/'+item.series_id));
   if(item.disposition==='needs_decision'&&item.kind!=='series')add('Отложить напоминание',()=>snooze(item));
   const more=button('···',()=>{const {dialog,close}=P.dialog('Действия с проблемой');dialog.classList.add('pl-action-menu');dialog.append(el('p',{class:'pl-menu-title'},item.title),el('div',{class:'pl-menu-items'},entries.map(({label,run})=>button(label,()=>{close(true);run();}))));});more.setAttribute('aria-label','Другие действия');if(entries.length)actions.append(more);
   card.append(actions);return card;
 }
 async function snooze(item){
   const kind=['task','project'].includes(item.kind)?item.kind:'occurrence',id=kind==='task'?item.task_id:kind==='project'?item.project_id:item.occurrence_id;
   const {dialog,close}=P.dialog('Отложить напоминание');const form=el('form',{'aria-label':'Отложить напоминание','data-submit':'Отложить напоминание'});dialog.append(form);
   let settings;try{settings=await P.api('/attention/'+id+'?type='+kind);}catch(e){form.append(el('p',{role:'alert'},e.message));return;}
   form.append(el('h3',{},item.title),meta('Статус и история сохранятся. Начиная с выбранной даты проблема снова потребует внимания.'));
   const date=P.field(form,'snoozed_until','Напомнить начиная с','date',settings.snoozed_until||'');date.required=true;date.min=P.today();
   const send=P.commandSender('/attention/'+id);
   P.bindForm(form,()=>send({type:kind,expected_revision:settings.revision,snoozed_until:date.value}),async()=>{close(true);P.notify('«'+item.title+'»: напоминание отложено до '+P.date(date.value));await P.refresh();},async()=>{const latest=await P.api('/attention/'+id+'?type='+kind);return {record:latest,accept(){settings=latest;}};});
 }

})(window.Planner);
