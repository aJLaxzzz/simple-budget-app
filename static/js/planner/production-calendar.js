(function(P){
 'use strict';const {el,button,meta}=P;
 P.renderCalendarSettings=async function(host){
   const section=P.route().params.get('section')||'planner';
   host.classList.add('pl-settings');
   host.replaceChildren(el('nav',{class:'pl-tabs pl-settings-nav','aria-label':'Подразделы настроек'},[['planner','Планировщик'],['calendar','Производственный календарь'],['appearance','Оформление'],['backups','Резервные копии'],['security','Безопасность']].map(([key,title])=>el('a',{href:'#/plans/settings?section='+key,class:key===section?'is-active':''},title))));
   if(section==='templates'){host.append(button('Создать из шаблона',()=>P.chooseTemplate(P.route())));return;}
   if(section==='security'){await window.budgetSecurity(host);return;}
   if(section==='backups'){await renderBackups(host);return;}
   if(section==='appearance'){const icons=el('section',{class:'pl-my-icons pl-settings-block'});host.append(icons);await P.renderMyIcons(icons);return;}
   if(section==='planner'){
     const block=(title,description)=>{const box=el('section',{class:'pl-settings-block'},el('h2',{},title),meta(description));host.append(box);return box;};
     const repeat=block('Повторения','Ближайшие выполнения регулярных задач и проектов');
     const limit=await P.api('/settings/project-repeat-limit');
     const form=el('form',{'data-submit':'Сохранить лимит','aria-label':'Ближайшие выполнения'});
     const count=P.field(form,'project_repeat_limit','Количество ближайших выполнений','number',limit.limit);count.min=1;count.max=20;count.required=true;
     form.append(meta('От 1 до 20. Текущие и будущие открытые проекты входят в лимит, просроченные показаны дополнительно. У задач это фильтр списка. «Все выполнения» показывает уже доступные задачи, не создавая неограниченное число новых. Уменьшение лимита не удаляет созданные экземпляры.'));
     P.bindForm(form,()=>P.api('/settings/project-repeat-limit','PUT',{limit:Number(count.value),expected_revision:limit.revision}),async result=>{P.repeatLimit=result.limit;P.notify('Лимит сохранён');await P.refresh();});repeat.append(form);
     const checkbox=(box,label,checked,save)=>{const input=el('input',{type:'checkbox',checked});input.addEventListener('change',()=>save(input.checked));box.append(el('label',{class:'pl-checkbox'},input,label));};
     const tasks=block('Список задач','Группы разделяются по экземпляру проекта и дате');
     checkbox(tasks,'Группировать по проектам',P.groupProjects(),value=>{try{localStorage.setItem('planner.project-groups',value?'on':'off');}catch(_){}});
     const calendar=block('Календарь','История остаётся на запланированных датах');
     checkbox(calendar,'Показывать выполненные задачи',P.showCompletedCalendar(),P.setCompletedCalendar);
     checkbox(calendar,'Показывать отменённые задачи',P.showCancelledCalendar(),P.setCancelledCalendar);
     const templates=el('details',{class:'pl-settings-block'},el('summary',{},'Ранее сохранённые шаблоны'),el('div',{class:'pl-actions'},el('a',{class:'pl-button',href:'#/plans/task-templates'},'Шаблоны задач'),el('a',{class:'pl-button',href:'#/plans/templates'},'Шаблоны проектов'),el('a',{class:'pl-button',href:'#/plans/settings?section=templates'},'Создать из шаблона')));host.append(templates);return;
   }
   host.append(el('h2',{},'Производственный календарь РФ'));
   const highlight=el('input',{type:'checkbox',checked:P.showCalendarDays()});highlight.addEventListener('change',()=>P.setCalendarDays(highlight.checked));host.append(el('label',{class:'pl-checkbox pc-highlight-setting'},highlight,'Показывать праздники и выходные'));
   const data=await P.api('/production-calendar');const grid=el('div',{class:'pl-project-grid pl-calendar-years'});
   for(const year of data.items)grid.append(el('article',{class:'pl-project-card'},el('h3',{},String(year.year)),meta(year.complete?'Полный год · '+year.days+' дней':'Неполные данные'),meta('Обновлено: '+new Intl.DateTimeFormat('ru-RU',{timeZone:'Europe/Moscow',dateStyle:'medium'}).format(new Date(year.updated_at))),meta('Версия: '+(year.version||'Внешний импорт')+' · публикация '+P.date(year.published_at)),el('a',{class:'pl-calendar-source',href:year.source.startsWith('https://')?year.source:'#/plans/settings',target:'_blank',rel:'noopener noreferrer'},'Официальный источник'),el('details',{},el('summary',{},'Контрольная сумма'),el('code',{class:'pl-calendar-source'},year.checksum))));
   if(!data.items.length)grid.append(P.empty('Календарь ещё не загружен','Повторения по рабочим дням РФ потребуют полного года. Обычные будни доступны независимо.'));
   host.append(grid,el('p',{class:'pl-info'},'Федеральная пятидневная рабочая неделя. Региональные праздники и индивидуальные графики не учитываются.'),el('h3',{},'Загрузить проверенные данные'));
   for(const year of data.bundled_years)host.append(button('Проверить и импортировать '+year,async()=>{try{importDialog(await P.api('/production-calendar/bundled/'+year));}catch(e){P.notify(e.message);}}));
   const file=P.field(host,'calendar_file','Или импортировать подготовленный JSON-файл','file','');file.accept='.json,application/json';
   file.addEventListener('change',async()=>{try{if(!file.files[0])return;if(file.files[0].size>120*1024)throw Error('Файл должен быть не больше 120 KiB.');importDialog(JSON.parse(await file.files[0].text()));}catch(e){P.notify('Не удалось прочитать календарь: '+e.message);}finally{file.value='';}});
   host.append(el('p',{class:'pl-info'},'Подготовлены проверенные календари 2026 и 2027 годов. Каждый год импортируется только после подтверждения. Для остальных лет нужен полный файл, сверенный с официальными публикациями.'),el('p',{class:'pl-info'},'После обновления откройте нужную серию → «Изменить правило» → «Посмотреть изменения». Даты существующих выполнений изменятся только после отдельного подтверждения; ручные переносы и закрытая история защищены.'));
 };
 P.backupViewModel=function(data,staging=false){
   const copy=data.latest;const ready=Boolean(copy&&/^[0-9]{8}T[0-9]{6}Z-[a-f0-9]{16}$/.test(copy.manifest?.backup_id));
   return {ready,automatic:data.automatic==='enabled'?'Включено':staging?'STG: ежедневные копии намеренно отключены; сохраняются копии перед обновлением':data.automatic==='unknown'?'Состояние требует проверки':'Автоматическое копирование ещё не установлено',warning:data.warning||(data.stale&&!staging?'Нет актуальной резервной копии':''),base:ready?'/api/backups/'+copy.manifest.backup_id:''};
 };
 async function renderBackups(host){
   const panel=el('section',{class:'pl-backups','aria-label':'Резервные копии'});host.append(panel);panel.append(el('h2',{},'Резервные копии'),meta('Скачиваются готовые проверенные копии. Новая копия при нажатии не создаётся.'));
   const content=el('div',{'aria-live':'polite'},meta('Проверяем наличие копий…'));panel.append(content);
   try{
     const data=await window.budgetAPI('/api/backups/status'),environment=await window.budgetAPI('/api/environment'),view=P.backupViewModel(data,environment.staging===true);content.replaceChildren();
     const format=value=>new Intl.DateTimeFormat('ru-RU',{timeZone:'Europe/Moscow',dateStyle:'medium',timeStyle:'short'}).format(new Date(value));
     if(view.warning)content.append(el('p',{class:'pl-info',role:'status'},view.warning));
     if(view.ready){content.append(el('p',{},'Последняя успешная копия: '+format(data.latest.manifest.created_at)+' · МСК'),meta((data.latest.size<1048576?(data.latest.size/1024).toFixed(1)+' КиБ':(data.latest.size/1048576).toFixed(1)+' МиБ')+' · Проверена '+format(data.latest.verified_at)));}
     content.append(el('p',{},'Ежедневных копий: '+data.daily_count+' · хранится до 7 обычных копий'),meta(view.automatic));
     if(data.automatic==='enabled'&&data.next_at)content.append(meta('Следующая копия: '+format(data.next_at)+' · МСК'));
     const actions=el('div',{class:'pl-backup-actions'});for(const [kind,title]of [['planner','Скачать резервную копию планировщика'],['full','Скачать полный бэкап']])actions.append(view.ready?el('a',{class:'pl-button',href:view.base+'/'+kind,download:''},title):el('button',{type:'button',class:'pl-button',disabled:true},title));content.append(actions);
   }catch(error){content.replaceChildren(el('p',{role:'alert'},'Не удалось проверить резервные копии. Попробуйте обновить сведения.'));}
   panel.append(el('div',{class:'pl-backup-actions'},button('Обновить сведения',()=>{panel.remove();renderBackups(host);}),el('a',{class:'pl-button',href:'/api/backup',download:'budget-backup.json'},'Скачать финансовый JSON')),el('p',{class:'pl-meta'},'Полный архив содержит личные данные. Сохраните внешнюю копию в защищённом месте. Восстановление выполняется административной командой.'));
 }
 function importDialog(calendar){
   const {dialog,close}=P.dialog('Проверить календарь '+calendar.year);const form=el('form',{'aria-label':'Импорт календаря','data-submit':'Импортировать календарь'});const area=el('div');let preview;const send=P.commandSender('/production-calendar/import');
   form.append(el('p',{},'Версия '+calendar.version+' · публикация '+P.date(calendar.published_at)),meta(calendar.source||''));
   const consent=el('input',{type:'checkbox',required:true});form.append(el('label',{class:'pl-checkbox'},consent,'Данные сверены с официальным источником; подтверждаю импорт полного года.'));
   const inspect=button('Проверить файл и изменения',async()=>{inspect.disabled=true;try{preview=await P.api('/production-calendar/preview','POST',{calendar});area.replaceChildren(el('p',{},'Изменятся дней: '+preview.changed_days.length),el('p',{class:'pl-info'},preview.message),el('details',{},el('summary',{},'Изменения по дням'),el('ul',{},preview.changed_days.map(d=>el('li',{},P.date(d.date)+' · '+(d.before||'нет данных')+' → '+d.after)))));}catch(e){area.replaceChildren(el('p',{role:'alert'},e.message));}finally{inspect.disabled=false;}});form.append(inspect,area);
   P.bindForm(form,()=>{if(!preview)throw Error('Сначала проверьте файл и изменения.');return send({calendar,expected_revision:preview.expected_revision,preview_token:preview.preview_token,source_confirmed:consent.checked});},async()=>{close(true);P.notify('Календарь импортирован. Существующие выполнения сохранены.');await P.refresh();},async()=>({record:{message:'Повторите проверку файла, чтобы увидеть актуальные изменения.'},accept(){preview=null;inspect.click();}}));dialog.append(form);
 }
})(window.Planner);
