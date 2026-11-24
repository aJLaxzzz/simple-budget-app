(function(P){
 'use strict';
 const {el,button,meta}=P;
 const countNames={tasks:'Задачи и подзадачи',sessions:'Периоды работы',projects:'Проекты',occurrences:'Регулярные выполнения',series:'Повторения',history_events:'События истории',occurrence_incidents:'Инциденты',entity_template_links:'Связи с шаблонами',template_versions:'Сохранённые варианты шаблона'};
 P.unarchive=async function(kind,item){
  if(P.guardActive&&!await P.guardActive())return;
  const {dialog,close}=P.dialog('Вернуть из архива');
  dialog.append(el('p',{},item.title||item.snapshot?.title),meta('Данные и история сохранятся. Статус выполнения не изменится: отменённый проект останется отменённым.'));
  const form=el('form',{'data-submit':'Вернуть из архива'}),send=P.commandSender('/'+kind+'/'+item.id+'/unarchive');
  P.bindForm(form,()=>send({expected_revision:item.revision}),async()=>{close(true);P.notify('«'+(item.title||item.snapshot?.title)+'»: возвращено из архива');await P.refresh();},async()=>{const latest=await P.api('/'+kind+'/'+item.id);return {record:latest,accept(){item=latest;}};});dialog.append(form);
 };
 P.deleteForever=async function(kind,item){
  if(P.guardActive&&!await P.guardActive())return;
  const previousGuard=P.guardActive,previousNavigation=P.deferNavigation;
  const {dialog,close}=P.dialog('Удалить навсегда');dialog.classList.add('pl-delete-dialog');
  let navigationGuard;
  dialog.addEventListener('close',()=>{if(P.guardActive===dialog.plannerGuard)P.guardActive=previousGuard;if(P.deferNavigation===navigationGuard)P.deferNavigation=previousNavigation;});
  const form=el('form',{'aria-label':'Окончательное удаление','data-submit':'Удалить навсегда'});
  dialog.append(form);
  let selectedKind=kind,selectedID=item.id,preview=null,turn=0;
  let occurrence=item.occurrence_id||(kind==='occurrences'?item.id:null),series=item.series_id;
  const scope=occurrence?P.field(form,'scope','Область удаления','select','occurrence',[['occurrence','Только это выполнение'],['series','Всё повторение навсегда']]):null;
  const details=el('div',{'aria-live':'polite'});form.append(details);
  const executionDate=item.planned_date||item.start_date||item.manual_date||item.adjusted_date||item.date;
  const backup=el('details',{},el('summary',{},'Перед первым удалением — резервный снимок'),meta('Создайте проверенный экспорт SQLite через SQLite .backup (инструкция в README, раздел «Окончательное удаление»). Не копируйте работающую planner.db без WAL. Снимок храните вне каталога приложения. Удаление через интерфейс необратимо.'));form.append(backup);
  const reload=button('Обновить состав удаления',()=>load());form.append(reload);
  let send;
  P.bindForm(form,()=>{
   if(!preview||preview.blockers.length)throw new Error('Проверьте состав удаления и зависимости.');
   return send({expected_revision:preview.revision,preview_token:preview.preview_token,confirm:true});
  },async()=>{close(true);const target=kind==='templates'?'templates':kind==='task-templates'?'task-templates':kind==='projects'?'projects':kind==='series'?'tasks?view=regular':'tasks';P.navigate('#/plans/'+target);await P.refresh();P.notify('«'+preview.title+'»: удалено навсегда');});
  const submit=form.querySelector('[type=submit]');submit.classList.add('pl-danger');submit.classList.remove('pl-primary');submit.disabled=true;
  async function load(){
   const own=++turn;preview=null;submit.disabled=true;reload.disabled=true;details.replaceChildren(meta('Проверяем зависимости…'));
   try{
    if(scope){if(!series){const context=await P.api('/occurrences/'+occurrence);series=context.series?.id||context.occurrence?.series_id||context.series_id;}
     selectedKind=scope.value==='series'?'series':'occurrences';selectedID=scope.value==='series'?series:occurrence;
    }
    if(!selectedID)throw new Error('Не удалось определить повторение. Откройте его настройки.');
    const result=await P.api('/'+selectedKind+'/'+selectedID+'/delete-preview');if(own!==turn||!dialog.isConnected)return;preview=result;send=P.commandSender('/'+selectedKind+'/'+selectedID+'/delete');
    details.replaceChildren(el('h3',{},preview.title),...(selectedKind==='occurrences'&&executionDate?[meta('Дата выполнения: '+P.date(executionDate))]:[]),el('p',{},selectedKind==='series'?'Область: всё повторение и все его выполнения':selectedKind==='occurrences'?'Область: только выбранное выполнение':'Область: выбранный объект и перечисленные связи'),el('dl',{class:'pl-delete-counts'},Object.entries(countNames).filter(([key])=>preview.counts[key]>0||['tasks','sessions'].includes(key)).flatMap(([key,label])=>[el('dt',{},label),el('dd',{},String(preview.counts[key]||0))])),...preview.consequences.map(text=>el('p',{class:'pl-note'},text)),...preview.blockers.map(text=>el('p',{role:'alert'},text)));
    for(const dependency of preview.dependencies||[])details.append(button('Открыть повторение «'+dependency.title+'»',()=>{close(true);P.navigate('#/plans/'+dependency.kind+'/'+encodeURIComponent(dependency.id));}));
    if(preview.blockers.length)details.append(el('p',{class:'pl-muted'},'Ничего не удалено. Обработайте указанные зависимости и повторите проверку.'));
    submit.disabled=preview.blockers.length>0;
    // Guard pending destructive commands, without treating scope selection as an edit.
    P.guardForm(form,'delete:'+selectedKind+':'+selectedID,null,dialog,{previewOnly:true});navigationGuard=P.deferNavigation;
   }catch(e){details.replaceChildren(el('p',{role:'alert'},P.errorMessage(e)));}finally{reload.disabled=false;}
  }
  scope?.addEventListener('change',load);await load();
 };
 P.lifecycleMenuButton=(kind,item)=>{const b=button('···',()=>{
  const {dialog,close}=P.dialog('Действия с объектом');const run=fn=>()=>{close(true);fn();};
  if(item.archived||item.archived_at)dialog.append(button('Вернуть из архива',run(()=>P.unarchive(kind,item))));
  dialog.append(button('Удалить навсегда',run(()=>P.deleteForever(kind,item)),'pl-danger'));
 },'pl-icon');b.setAttribute('aria-label','Дополнительные действия');return b;};
})(window.Planner);
