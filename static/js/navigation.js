(function () {
 'use strict';
 const P=window.Planner, tabs=document.getElementById('module-tabs'),toggle=document.getElementById('module-switch'),menu=document.getElementById('module-menu');
 const last={plans:'#/plans/calendar?mode=month&date='+P.today(),budget:'#/'};
 let current='plans', signature='', renderedHash;
 const close=()=>{menu.hidden=true;toggle.setAttribute('aria-expanded','false');};
 toggle.addEventListener('click',()=>{const open=menu.hidden;menu.hidden=!open;toggle.setAttribute('aria-expanded',String(open));if(open){menu.querySelectorAll('[data-module]').forEach(a=>a.href=last[a.dataset.module]);menu.querySelector('[data-module="'+current+'"]')?.focus();}});
 document.addEventListener('click',event=>{if(!event.target.closest('.app-module-switch'))close();});
 menu.addEventListener('click',event=>{if(event.target.closest('a'))close();});
 menu.addEventListener('keydown',event=>{if(event.key==='Escape'){event.preventDefault();close();toggle.focus();}if(['ArrowDown','ArrowUp'].includes(event.key)){event.preventDefault();const links=[...menu.querySelectorAll('a')],index=links.indexOf(document.activeElement);links[(index+(event.key==='ArrowDown'?1:links.length-1))%links.length].focus();}});
 window.AppNavigation={
  deferNavigation(){
   if(!renderedHash||renderedHash===location.hash)return false;
   const dirty=current==='budget'&&((window.budgetDraftDirty&&window.budgetDraftNode?.isConnected)||document.querySelector('#modal-root form'));
   if(dirty&&!window.confirm('Есть несохранённые изменения бюджета. Перейти на другую страницу и отменить их?')){
    window.history.replaceState(null,'',renderedHash);return true;
   }
   window.budgetDraftDirty=false;
   if(dirty)document.getElementById('modal-root').replaceChildren();
   return false;
  },
  render(module,route){
  current=module;last[module]=location.hash;if(renderedHash!==location.hash){close();renderedHash=location.hash;}
  const active=module==='plans'?P.navGroup(route):route.page==='month'?'home':route.page;
  const key=module+':'+active;
  if(key!==signature){
   signature=key;toggle.replaceChildren(document.createTextNode(module==='plans'?'Планы ':'Бюджет '),P.el('span',{'aria-hidden':'true'},'⌄'));
   const entries=module==='plans'?[['calendar','Календарь',P.calendarLink()],['tasks','Задачи','#/plans/tasks'],['projects','Проекты','#/plans/projects'],['attention','Внимание','#/plans/attention']]:[['home','Месяцы','#/'],['year','Аналитика за год','#/year'],['categories','Категории','#/categories']];
   tabs.classList.toggle('pl-main-tabs',module==='plans');tabs.setAttribute('aria-label',module==='plans'?'Разделы планировщика':'Разделы бюджета');
   tabs.replaceChildren(...entries.map(([id,title,href])=>P.el('a',{href,...(id===active?{'aria-current':'page',class:'is-active'}:{})},title)));
   document.getElementById('backup-button').hidden=module!=='budget';document.getElementById('budget-search').hidden=module!=='budget';
  }
  // Calendar remembers its current date/mode while links keep their original hashes.
  if(module==='plans'){const calendar=tabs.querySelector('a[href^="#/plans/calendar"]');if(calendar)calendar.href=P.calendarLink();return P.updateAttentionCount(tabs);}
 }};
})();
