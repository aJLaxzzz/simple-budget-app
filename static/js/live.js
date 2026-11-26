(function () {
  'use strict';
  const indicator = document.createElement('span'); indicator.id = 'sync-status'; indicator.setAttribute('role', 'status');

  document.body.append(indicator);
  const notice = document.createElement('div'); notice.hidden = true; notice.setAttribute('role', 'status');
  notice.className='budget-sync-notice';(document.querySelector('header') || document.body).after(notice);
  let stream, retry, connectTurn = 0, pending = false, running = false, failed = false, connected = false, delay = 1000, scope = 'all', lastActivity = Date.now(), generation, revision, nextReconcile = 0, reconcileDelay = 1000;
  let pointerDown = false, dragging = false, keyboardDown = false, interactionUntil = 0;
  const interacted = () => { window.budgetInteractionEpoch = (window.budgetInteractionEpoch || 0) + 1; };
  document.addEventListener('pointerdown', () => { interacted(); pointerDown = true; }, true);
  document.addEventListener('dragstart', () => { interacted(); dragging = true; }, true);
  const releasePointer = () => { pointerDown = false; interactionUntil = Date.now() + 250; };
  window.addEventListener('pointerup', releasePointer, true);
  window.addEventListener('pointercancel', releasePointer, true);
  const releaseDrag = () => { dragging = false; releasePointer(); };
  window.addEventListener('dragend', releaseDrag, true);
  window.addEventListener('drop', releaseDrag, true);
  document.addEventListener('focusin', () => { interactionUntil = Date.now() + 250; }, true);
  document.addEventListener('keydown', () => { interacted(); keyboardDown = true; }, true);
  window.addEventListener('keyup', () => { keyboardDown = false; interactionUntil = Date.now() + 250; }, true);
  window.addEventListener('blur', () => { keyboardDown = false; releaseDrag(); });
  const state = text => { indicator.textContent = text; };
  function placeNotice() {
    const dialogs = document.querySelectorAll('dialog[open]');
    const host = dialogs[dialogs.length-1] || document.querySelector('#modal-root .modal, .pl-drawer-shell');
    if(host){const heading=host.querySelector('.pl-drawer-head, .pl-dialog-head, .modal-header');if(heading)heading.after(notice);else host.prepend(notice);}
    else (document.querySelector('header') || document.body).after(notice);
  }
  function editing() { if(window.budgetDraftDirty && window.budgetDraftNode && !window.budgetDraftNode.isConnected)window.budgetDraftDirty=false;return window.budgetDraftDirty || !!document.querySelector('.pl-dialog, .pl-drawer-shell form, #modal-root form'); }
  function conflict() {
    if(!notice.hidden && notice.isConnected && notice.dataset.kind==='conflict')return;
    placeNotice();
    notice.dataset.kind='conflict';
    notice.replaceChildren(document.createTextNode('Данные изменились. Черновик сохранён на экране. '));
    const button = document.createElement('button');button.textContent = 'Обновить, отменив черновик';
    button.onclick = () => { if (!confirm('Отменить несохранённые изменения и загрузить актуальные данные?')) return; window.budgetDraftDirty = false;document.querySelectorAll('.pl-dialog,.pl-drawer-shell').forEach(x=>x.remove());const modal=document.getElementById('modal-root');if(modal)modal.replaceChildren();window.Planner?.drafts?.clear();notice.hidden=true;reconcile(true); };
    notice.append(button);notice.hidden = false;
  }
  async function reconcile(force) {
    if (!pending || running || failed || !connected || Date.now() < nextReconcile) return;
    if (window.budgetViews?.covers({generation, revision})) { pending=false;notice.hidden=true;state('Синхронизировано');return; }
    const plansView=location.hash.startsWith('#/plans');
    if(scope!=='all'&&((scope==='planner'&&!plansView)||(scope==='finance'&&plansView))){pending=false;state('Синхронизировано');return;}
    // Replacing calendar nodes during a native drag loses its drop target.
    if(pointerDown || dragging || keyboardDown || Date.now() < interactionUntil){state('Синхронизация');return;}
    if (window.budgetRequests > 0 || window.budgetViews?.busy) {state('Синхронизация');return;}
    if (!force && editing()) { state('Синхронизация'); if (editing()) conflict(); return; }
    running = true; pending = false;state('Синхронизация');
    const hash = location.hash, x = scrollX, y = scrollY, readFailures = window.budgetReadFailures || 0;
    const active = document.activeElement;
    const selectorFor = n => n.id ? '#' + CSS.escape(n.id) : n.classList.length ? n.tagName.toLowerCase()+Array.from(n.classList).map(c=>'.'+CSS.escape(c)).join('') : null;
    const focus = active?.closest('.pl-filters') && active.name ? {name: active.name, start: active.selectionStart, end: active.selectionEnd} : active?.closest('#root') && active.matches('button,a,summary,[tabindex]') ? {selector: selectorFor(active), text: active.textContent, href: active.getAttribute('href')} : null;
    const positions = Array.from(document.querySelectorAll('#root *, header *')).filter(n=>n.scrollTop||n.scrollLeft).map(n=>[selectorFor(n),n.scrollTop,n.scrollLeft]);
    const disclosures = Array.from(document.querySelectorAll('#root details')).map(n=>[selectorFor(n),n.querySelector('summary')?.textContent,n.open]);
    try {
      const plans = hash.startsWith('#/plans');
      if ((scope === 'all' || scope === 'planner' && plans || scope === 'finance' && !plans) && window.budgetRefreshView) {
        const applied = plans ? await window.Planner.refresh(true) : await window.budgetRefreshView(true);
        if (applied === false) return;
      }
      if ((window.budgetReadFailures || 0) !== readFailures) throw new Error('Сверка не завершена');
      if(hash===location.hash){
        disclosures.forEach(([selector,title,open])=>{if(selector)Array.from(document.querySelectorAll(selector)).find(n=>n.querySelector('summary')?.textContent===title)?.toggleAttribute('open',open);});
        if(focus){const n=focus.name?document.querySelector('.pl-filters [name="'+CSS.escape(focus.name)+'"]'):focus.selector&&Array.from(document.querySelectorAll(focus.selector)).find(n=>n.textContent===focus.text&&n.getAttribute('href')===focus.href);n?.focus({preventScroll:true});if(n&&typeof n.setSelectionRange==='function'&&typeof focus.start==='number')n.setSelectionRange(focus.start,focus.end);}
        scrollTo(x,y);positions.forEach(([selector,top,left])=>{const n=selector&&document.querySelector(selector);if(n){n.scrollTop=top;n.scrollLeft=left;}});
      }
      reconcileDelay=1000;
      if(!pending&&connected&&!failed){notice.hidden=true;state('Синхронизировано');}
    } catch(e){pending=true;nextReconcile=Date.now()+reconcileDelay;reconcileDelay=Math.min(reconcileDelay*2,30000);state(navigator.onLine?'Переподключение':'Нет соединения');}
    finally{running=false;}
  }
  window.addEventListener('budget-view-ready',()=>{if(pending && window.budgetViews?.covers({generation,revision})){pending=false;notice.hidden=true;if(connected&&!failed)state('Синхронизировано');}});
  window.addEventListener('budget-view-stale',()=>{pending=true;scope='all';});
  const dirty = target => {
    if(target.closest('.pl-filters')||target.matches('[type=search],#search-input'))return;
    if(target.closest('#modal-root,.pl-dialog,.pl-drawer-shell,.categories-page,form:not(.pl-filters)')||target.id==='income-input'){
      window.budgetEditEpoch=(window.budgetEditEpoch||0)+1;window.budgetDraftDirty=true;window.budgetDraftNode=target.closest('.categories-page')?document.getElementById('root'):target;
    }
  };
  document.addEventListener('input',e=>dirty(e.target));
  document.addEventListener('change',e=>dirty(e.target));
  document.addEventListener('click',e=>{if(e.target.closest('.categories-page button:not(#save-categories)'))dirty(e.target);},true);
  window.addEventListener('planner:saved',()=>{window.budgetDraftDirty=false;});
  window.addEventListener('budget-auth-required',()=>{
    window.budgetForgetAuth();failed=true;connectTurn++;connected=false;stream?.close();clearTimeout(retry);state('Требуется повторная авторизация');
    if(!editing()){location.assign('/login.html'+location.hash);return;}
    placeNotice();notice.dataset.kind='auth';notice.replaceChildren(document.createTextNode('Сессия завершена. Черновик остаётся здесь. '));const link=document.createElement('a');link.href='/login.html';link.target='_blank';link.rel='noopener';link.textContent='Войти в новой вкладке';notice.append(link);notice.hidden=false;
  });
  async function initialRender() {
    if(window.budgetStarted)return;
    window.budgetStarted=true;running=true;const failures=window.budgetReadFailures||0;
    state('Синхронизация');
    try{await window.budgetRefreshView();if((window.budgetReadFailures||0)!==failures)pending=true;else if(connected&&!pending&&!failed)state('Синхронизировано');}
    catch(e){pending=true;}
    finally{running=false;}
  }
  async function connect(){
    const turn=++connectTurn;clearTimeout(retry);
    if(failed||document.hidden||!navigator.onLine)return;
    state(navigator.onLine?'Переподключение':'Нет соединения');
    try{await window.budgetAuth(true);}catch(e){if(!failed&&turn===connectTurn)retry=setTimeout(connect,Math.min(delay*=2,30000));return;}
    if(turn!==connectTurn||failed||document.hidden||!navigator.onLine)return;
    connected=false;stream?.close();const source=new EventSource('/api/events');stream=source;
    stream.addEventListener('change',event=>{
      if(stream!==source)return;
      lastActivity=Date.now();delay=1000;
      try{const data=JSON.parse(event.data);const full=generation!==data.generation||(revision!==undefined&&data.revision>revision+1);generation=data.generation;revision=data.revision;if(full||data.scope==='all'||data.scope==='planner')window.Planner?.invalidateIconCatalog?.();scope=full||pending&&scope!==data.scope?'all':data.scope;connected=true;if(!window.budgetStarted){pending=false;initialRender();}else{pending=true;reconcile();}}catch(e){stream.close();retry=setTimeout(connect,1000);}
    });
    stream.addEventListener('heartbeat',()=>{lastActivity=Date.now();});
    stream.addEventListener('auth-required',()=>window.dispatchEvent(new Event('budget-auth-required')));
    stream.onerror=()=>{if(stream!==source)return;connected=false;source.close();pending=true;scope='all';state(navigator.onLine?'Переподключение':'Нет соединения');clearTimeout(retry);retry=setTimeout(connect,Math.min(delay*=2,30000));};
  }
  setInterval(()=>{if(!failed&&pending)reconcile();if(!failed&&stream&&Date.now()-lastActivity>35000){lastActivity=Date.now();stream.close();connect();}},1000);
  window.addEventListener('online',()=>{clearTimeout(retry);connect();});window.addEventListener('offline',()=>{connectTurn++;clearTimeout(retry);connected=false;stream?.close();state('Нет соединения');});
  document.addEventListener('visibilitychange',()=>{if(document.hidden){connectTurn++;clearTimeout(retry);connected=false;stream?.close();}else{clearTimeout(retry);connect();}});
  window.addEventListener('focus',()=>{if(failed){window.budgetAuth(true).then(()=>{failed=false;notice.hidden=true;connect();}).catch(()=>{});}});
  window.budgetSecurity=async function(host){
    const section=document.createElement('section');section.className='pl-security pl-settings-block';host.append(section);const title=document.createElement('h2');title.textContent='Безопасность';section.append(title);
    const session=await window.budgetAuth();if(!session.enabled){section.append('В локальном режиме пароль не настроен.');return;}
    const rows=await window.budgetAPI('/api/auth/sessions');
    const action=(label,payload,path)=>{const b=document.createElement('button');b.className='pl-button';b.type='button';b.textContent=label;b.onclick=async()=>{try{await window.budgetAPI(path||'/api/auth/revoke',{method:'POST',body:JSON.stringify(payload)});if(payload.all||path||payload.id===session.id){location.assign('/login.html');}else{section.remove();await window.budgetSecurity(host);}}catch(e){alert(e.message);}};return b;};
    for(const row of rows){const line=document.createElement('article');line.className='pl-session-card';line.textContent=(row.current?'Эта сессия':'Другая сессия')+' · вход '+new Date(row.created).toLocaleString('ru-RU')+' · активность '+new Date(row.active).toLocaleString('ru-RU')+' ';line.append(action('Завершить',{id:row.id}));section.append(line);}
    section.append(action('Завершить остальные',{others:true}),action('Завершить все',{all:true}),action('Выйти',{},'/api/auth/logout'));
    const note=document.createElement('p');note.textContent='Завершение сессии не отзывает сертификат устройства.';section.append(note);
  };
  connect();
  setTimeout(()=>{if(!failed&&!window.budgetStarted)initialRender().then(()=>{if(!connected)state('Переподключение');});},5000);
})();
