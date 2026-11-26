(function(P){
 'use strict';
 P.showCalendarDays=()=>{try{return localStorage.getItem('planner-calendar-days')!=='off';}catch(_){return true;}};
 P.setCalendarDays=value=>{try{localStorage.setItem('planner-calendar-days',value?'on':'off');}catch(_){P.notify('Браузер не разрешает сохранять настройки.');}};
 const kinds=new Set(['working','weekend','holiday','transferred_day_off','working_weekend','nonworking']);
 P.decorateCalendarDay=(node,day)=>{
  if(!P.showCalendarDays()||!day||!kinds.has(day.kind))return;
  node.classList.add('pc-official-'+day.kind);node.dataset.calendarKind=day.kind;
  node.title=[day.label,day.reason].filter(Boolean).join(' · ');
 };
 P.calendarDayInfo=(host,data,selected)=>{
  if(!P.showCalendarDays())return;
  const {el}=P;
  const legend=el('div',{class:'pc-day-legend','aria-label':'Обозначения производственного календаря'});
  for(const [kind,label]of [['weekend','Выходной'],['holiday','Праздник'],['transferred_day_off','Перенос выходного'],['working_weekend','Рабочие сб/вс']])legend.append(el('span',{class:'pc-key pc-official-'+kind},label));
  host.append(el('details',{class:'pc-day-legend-panel',open:!window.matchMedia('(max-width:700px)').matches},el('summary',{},'Обозначения календаря'),legend));
  const day=(data.calendar_days||[]).find(d=>d.date===selected);
  if(day)host.append(el('p',{class:'pc-day-description',title:day.reason},P.date(selected)+' · '+day.label));
  if(data.calendar_missing_years?.length)host.append(el('p',{class:'pc-calendar-missing',role:'status'},'Нет официального календаря РФ: '+data.calendar_missing_years.join(', ')+'. Подсветка недоступна. ',el('a',{href:'#/plans/settings?section=calendar'},'Настройки календаря')));
 };
})(window.Planner);
