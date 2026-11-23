(function(P){
 'use strict';const {el,button,meta}=P;
 P.renderCalendarSettings=async function(host){
   const appearance=P.route().params.get('section')==='appearance';
   host.replaceChildren(el('nav',{class:'pl-tabs pl-settings-nav','aria-label':'Подразделы настроек'},[['calendar','Производственный календарь'],['appearance','Оформление']].map(([key,title])=>el('a',{href:'#/plans/settings?section='+key,class:(appearance?key==='appearance':key==='calendar')?'is-active':''},title))));
   if(appearance){const icons=el('section',{class:'pl-my-icons'});host.append(icons);await P.renderMyIcons(icons);return;}
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
 function importDialog(calendar){
   const {dialog,close}=P.dialog('Проверить календарь '+calendar.year);const form=el('form',{'aria-label':'Импорт календаря','data-submit':'Импортировать календарь'});const area=el('div');let preview;const send=P.commandSender('/production-calendar/import');
   form.append(el('p',{},'Версия '+calendar.version+' · публикация '+P.date(calendar.published_at)),meta(calendar.source||''));
   const consent=el('input',{type:'checkbox',required:true});form.append(el('label',{class:'pl-checkbox'},consent,'Данные сверены с официальным источником; подтверждаю импорт полного года.'));
   const inspect=button('Проверить файл и изменения',async()=>{inspect.disabled=true;try{preview=await P.api('/production-calendar/preview','POST',{calendar});area.replaceChildren(el('p',{},'Изменятся дней: '+preview.changed_days.length),el('p',{class:'pl-info'},preview.message),el('details',{},el('summary',{},'Изменения по дням'),el('ul',{},preview.changed_days.map(d=>el('li',{},P.date(d.date)+' · '+(d.before||'нет данных')+' → '+d.after)))));}catch(e){area.replaceChildren(el('p',{role:'alert'},e.message));}finally{inspect.disabled=false;}});form.append(inspect,area);
   P.bindForm(form,()=>{if(!preview)throw Error('Сначала проверьте файл и изменения.');return send({calendar,expected_revision:preview.expected_revision,preview_token:preview.preview_token,source_confirmed:consent.checked});},async()=>{close(true);P.notify('Календарь импортирован. Существующие выполнения сохранены.');await P.refresh();},async()=>({record:{message:'Повторите проверку файла, чтобы увидеть актуальные изменения.'},accept(){preview=null;inspect.click();}}));dialog.append(form);
 }
})(window.Planner);
