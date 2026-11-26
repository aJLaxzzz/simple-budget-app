(function () {
  'use strict';
  const P = window.Planner;
  const tabs = document.getElementById('module-tabs'), toggle = document.getElementById('module-switch'), menu = document.getElementById('module-menu');
  const last = {plans:'#/plans/calendar?mode=month&date='+P.today(), budget:'#/'};
  const icons = {calendar:'calendar', tasks:'clipboard-list', projects:'folder', attention:'bell'};
  const icon = name => P.el('span', {class:'app-icon', 'aria-hidden':'true', style:'--app-icon:url("/api/planner/icons/lucide:'+name+'/image")'});
  let current = 'plans', signature = '', renderedHash;
  const close = () => { menu.hidden = true; toggle.setAttribute('aria-expanded','false'); };
  for (const link of menu.querySelectorAll('[data-module]')) {
    const module = link.dataset.module;
    link.replaceChildren(icon(module==='plans'?'calendar':'wallet'), P.el('span',{},module==='plans'?'Планы':'Бюджет'), P.el('span',{class:'app-module-check','aria-hidden':'true'},'✓'));
  }
  document.querySelector('.app-settings').replaceChildren(icon('settings'));
  toggle.addEventListener('click', () => {
    const open = menu.hidden; menu.hidden = !open; toggle.setAttribute('aria-expanded',String(open));
    if (open) {
      menu.querySelectorAll('[data-module]').forEach(a => a.href = last[a.dataset.module]);
      menu.querySelector('[data-module="'+current+'"]')?.focus({preventScroll:true});
    }
  });
  document.addEventListener('click', event => { if (!event.target.closest('.app-module-switch')) close(); });
  menu.addEventListener('click', event => { if (event.target.closest('a')) close(); });
  document.addEventListener('keydown', event => {
    if (menu.hidden) return;
    if (event.key==='Escape') { event.preventDefault(); close(); toggle.focus({preventScroll:true}); }
    if (menu.contains(event.target) && ['ArrowDown','ArrowUp','Home','End'].includes(event.key)) {
      event.preventDefault(); const links=[...menu.querySelectorAll('a')], index=links.indexOf(document.activeElement);
      const next=event.key==='Home'?0:event.key==='End'?links.length-1:(index+(event.key==='ArrowDown'?1:links.length-1))%links.length;
      links[next].focus();
    }
  });
  window.AppNavigation = {
    deferNavigation() {
      if (!renderedHash || renderedHash===location.hash) return false;
      const dirty=current==='budget'&&((window.budgetDraftDirty&&window.budgetDraftNode?.isConnected)||document.querySelector('#modal-root form'));
      if (dirty&&!window.confirm('Есть несохранённые изменения бюджета. Перейти на другую страницу и отменить их?')) {
        window.history.replaceState(window.history.state,'',renderedHash); return true;
      }
      window.budgetDraftDirty=false;
      if (dirty) document.getElementById('modal-root').replaceChildren();
      return false;
    },
    render(module, route) {
      current=module; last[module]=location.hash; document.body.dataset.module=module;
      if (renderedHash!==location.hash) { close(); renderedHash=location.hash; }
      const active=module==='plans'?P.navGroup(route):route.page==='month'?'home':route.page;
      const key=module+':'+active;
      if (key!==signature) {
        signature=key;
        toggle.replaceChildren(document.createTextNode(module==='plans'?'Планы ':'Бюджет '),P.el('span',{'aria-hidden':'true',class:'app-chevron'},'⌄'));
        for (const link of menu.querySelectorAll('[data-module]')) {
          if (link.dataset.module===module) link.setAttribute('aria-current','true'); else link.removeAttribute('aria-current');
        }
        const entries=module==='plans'?
          [['calendar','Календарь',P.calendarLink()],['tasks','Задачи','#/plans/tasks'],['projects','Проекты','#/plans/projects'],['attention','Внимание','#/plans/attention']]:
          [['home','Месяцы','#/'],['year','Аналитика за год','#/year'],['categories','Категории','#/categories']];
        tabs.classList.toggle('pl-main-tabs',module==='plans');
        tabs.setAttribute('aria-label',module==='plans'?'Разделы планировщика':'Разделы бюджета');
        tabs.replaceChildren(...entries.map(([id,title,href]) => P.el('a',{href,...(id===active?{'aria-current':'page',class:'is-active'}:{})},
          module==='plans'?P.el('span',{class:'app-tab-icon'},icon(icons[id])):null,P.el('span',{class:'app-tab-label'},title))));
        document.getElementById('backup-button').hidden=module!=='budget';
        document.getElementById('budget-search').hidden=module!=='budget';
      }
      if (module==='plans') {
        const calendar=tabs.querySelector('a[href^="#/plans/calendar"]'); if(calendar) calendar.href=P.calendarLink();
        return P.updateAttentionCount(tabs);
      }
    }
  };
})();
