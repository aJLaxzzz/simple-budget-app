(function(P) {
  'use strict';
  const {el, button, meta} = P;
  P.commandSender = function(path) {
    let pending;
    return async data => {
      // A lost response must be retried with the identical command, even if a form was edited.
      if (!pending) pending = {...data, request_id: crypto.randomUUID()};
      try { const result = await P.api(path, 'POST', pending); pending = null; return result; }
      catch (error) { if (error.status && error.status < 500) pending = null; else error.uncertainCommand=true; throw error; }
    };
  };
  P.seriesTitle = s => s.project_template?.title || s.template?.title || 'Регулярное дело';
  P.ruleText = rule => {
    const units = {days:'дн.', weeks:'нед.', months:'календарных мес.', years:'лет'};
    const days = {any:'любые дни', weekdays:'понедельник–пятница', weekends:'суббота–воскресенье', selected:'выбранные дни недели', ru_production:'рабочие дни РФ'};
    return `Каждые ${rule.interval} ${units[rule.unit]} ${rule.mode === 'completion' ? 'после выполнения' : 'по расписанию'} · ${days[rule.allowed_days.mode]}`;
  };
  P.ruleBuilder = function(host, options = {}) {
    const initial = options.rule || {unit:'weeks',interval:1,mode:'schedule',allowed_days:{mode:'any'},adjustment:'none'};
    const block = el('section', {class:'pl-rule-builder'}); host.append(block);
    const preset = P.field(block,'preset','Повторять','select','custom', [['daily','Каждый день'],['weekly','Каждую неделю'],['monthly','Каждый месяц'],['yearly','Каждый год'],...(!options.project ? [['completion','Через N дней после выполнения']] : []),['custom','Настроить самостоятельно']]);
    const advanced = el('details', {open:true}, el('summary',{},'Настройки повторения'));
    const grid = el('div',{class:'pl-form-grid'}); advanced.append(grid); block.append(advanced);
    const interval = P.field(grid,'interval','Каждые','number',initial.interval); interval.min=1; interval.max=10000; interval.required=true;
    const unit = P.field(grid,'unit','Интервал','select',initial.unit,[['days','Календарные дни'],['weeks','Недели'],['months','Календарные месяцы'],['years','Годы']]);
    const mode = P.field(grid,'mode','Отсчёт','select',initial.mode,[['schedule','От расписания'],...(!options.project ? [['completion','От фактического выполнения']] : [])]);
    if(options.changing){mode.disabled=true;block.append(meta('Режим отсчёта существующего повторения сохраняется. Изменяются интервал и допустимые дни.'));}
    const allowed = P.field(grid,'allowed','Допустимые дни','select',initial.allowed_days.mode,[['any','Любые'],['weekdays','Понедельник–пятница'],['weekends','Суббота–воскресенье'],['selected','Выбранные дни недели'],['ru_production','Рабочие дни РФ']]);
    const weekdays=el('div',{class:'pl-weekday-choices'});
    const dayInputs=['Пн','Вт','Ср','Чт','Пт','Сб','Вс'].map((label,i)=>{const input=el('input',{type:'checkbox',name:'weekday_'+(i+1),value:i+1,checked:initial.allowed_days.weekdays?.includes(i+1)});weekdays.append(el('label',{},input,label));return input;});grid.append(weekdays);
    const adjustment=P.field(grid,'adjustment','Если день не подходит','select',initial.adjustment,[['none','Без переноса'],['next','Следующий допустимый'],['previous','Предыдущий допустимый'],['nearest','Ближайший допустимый']]);
    const first=P.field(block,'first_date',options.changing ? 'Дата отсчёта выбранного выполнения' : 'Первая дата выполнения','date',options.firstDate||P.today());first.required=true;
    if(options.changing&&initial.mode==='completion'){first.parentElement.hidden=true;first.required=false;}
    const preview=el('div',{class:'pl-recurrence-preview','aria-live':'polite'});block.append(preview);
    const read=()=>({schema_version:1,unit:unit.value,interval:Number(interval.value),mode:mode.value,allowed_days:{mode:allowed.value,...(allowed.value==='selected'?{weekdays:dayInputs.filter(i=>i.checked).map(i=>Number(i.value))}:{})},adjustment:adjustment.value,timezone:'Europe/Moscow'});
    let turn=0,timer;
    async function show() {
      const own=++turn;
      preview.replaceChildren(meta('Рассчитываем даты…'));
      try {
        const rule=read();
        const anchor=options.completedDate && rule.mode==='completion' ? {date:options.completedDate,cycle_index:0,kind:options.anchorKind||'completion'} : {date:first.value,cycle_index:0,kind:rule.mode==='schedule'?'schedule':'unconfirmed'};
        const result=options.changing ? await P.api('/recurrence/preview','POST',{rule,anchor,count:6,...(rule.mode==='schedule'?{start_cycle:0}:{})}) : await P.api('/series/preview','POST',{rule,first_date:first.value,count:6,...(options.taskID?{existing_task_id:options.taskID}:{})});
        if(own!==turn)return;
        preview.replaceChildren(el('h4',{},'Предпросмотр следующих дат'));
        if(rule.mode==='schedule')preview.append(meta('Первое выполнение и последующие интервалы с учётом допустимых дней.'));
        if(result.reason)preview.append(el('p',{class:'pl-info'},result.reason));
        for(const row of result.items||[])preview.append(el('div',{class:'pl-preview-row'},el('span',{},P.date(row.base_date)+' → '+P.date(row.adjusted_date)),meta(row.reason||'Без переноса'),row.conditional?meta('Условный прогноз'):null));
      }catch(error){if(own!==turn)return;preview.replaceChildren(el('p',{role:'alert'},error.message),el('a',{href:'#/plans/settings'},'Настройки производственного календаря'));}
    }
    function sync(){weekdays.hidden=allowed.value!=='selected';if(allowed.value==='any')adjustment.value='none';else if(adjustment.value==='none')adjustment.value='next';adjustment.querySelector('[value="none"]').disabled=allowed.value!=='any';clearTimeout(timer);timer=setTimeout(show,250);}
    preset.addEventListener('change',()=>{const values={daily:['days',1,'schedule'],weekly:['weeks',1,'schedule'],monthly:['months',1,'schedule'],yearly:['years',1,'schedule'],completion:['days',45,'completion']}[preset.value];if(values){[unit.value,interval.value,mode.value]=values;if(options.changing)mode.value=initial.mode;advanced.open=preset.value==='completion';}else advanced.open=true;sync();});
    grid.addEventListener('change',sync);first.addEventListener('change',sync);sync();
    return {read,first,preview,refresh:show};
  };
  P.createRecurring = function(task, template) {
    const {dialog,close}=P.dialog(template?'Повторять проект':task?'Повторять задачу':'Новое регулярное дело');
    dialog.classList.add('pl-management-drawer');
    const form=el('form',{'aria-label':'Регулярное дело'});
    if(!task&&!template){P.field(form,'title','Название','text','').required=true;P.field(form,'description','Описание','textarea','');P.field(form,'priority','Приоритет','select','normal',['low','normal','high'].map(k=>[k,P.labels[k]]));}
    else form.append(el('h3',{},task?.title||template.snapshot.title));
    const builder=P.ruleBuilder(form,{project:!!template,firstDate:task?.planned_date||P.today(),completedDate:task?.completed_date,taskID:task?.id});
    if(task?.planned_date){builder.first.readOnly=true;form.append(meta('Первое выполнение использует плановую дату исходной задачи. Перенести её можно отдельно в карточке.'));}
    const end=P.field(form,'end_date','Создавать выполнения до (необязательно)','date','');
    if(task)form.append(meta('Исходная задача станет первым выполнением. Её копия не создаётся.'));
    const send=P.commandSender('/series');
    P.bindForm(form,()=>{const values=P.values(form);return send({rule:builder.read(),first_date:builder.first.value,...(end.value?{end_date:end.value}:{}),...(task?{existing_task_id:task.id,expected_revision:task.revision}:template?{source_type:'project',template_id:template.id,template_version_id:template.version_id,expected_revision:template.revision}:{task:{title:values.title,description:values.description,priority:values.priority}})});},async result=>{close(true);P.navigate('#/plans/series/'+result.id);P.notify('«'+P.seriesTitle(result)+'»: повторение включено');},task?async()=>{const latest=await P.api('/tasks/'+task.id);return {record:latest,accept(){task=latest;}};}:undefined);
    dialog.append(form);
  };
})(window.Planner);
