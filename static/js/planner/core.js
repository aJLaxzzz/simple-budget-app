(function () {
  'use strict';
  const P = window.Planner = {};
  P.labels = { planned: 'Запланировано', in_progress: 'В работе', completed: 'Завершено', cancelled: 'Отменено', skipped: 'Пропущено', low: 'Низкий', normal: 'Обычный', high: 'Высокий' };
  P.active = value => value.status === 'planned' || value.status === 'in_progress';
  P.api = (path, method, data) => window.budgetAPI('/api/planner' + path, method ? { method, body: JSON.stringify(data) } : undefined).catch(error=>{const message=P.errorMessage(error);error.message=message;throw error;});
  P.el = function (tag, props = {}, ...children) {
    const node = document.createElement(tag);
    for (const [key, value] of Object.entries(props)) {
      if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
      else if (key === 'class') node.className = value;
      else if (key in node && !key.startsWith('aria')) node[key] = value;
      else node.setAttribute(key, value);
    }
    for (const child of children.flat(Infinity)) if (child != null) node.append(child.nodeType ? child : document.createTextNode(String(child)));
    return node;
  };
  P.button = (text, onclick, cls = '') => P.el('button', { type: 'button', class: 'pl-button ' + cls, onclick }, text);
  // Reuse the formatter, never the date: large lists call this for each task,
  // while an open tab must still observe the next Moscow calendar day.
  const moscowDateFormatter = new Intl.DateTimeFormat('en-GB', { timeZone: 'Europe/Moscow', year: 'numeric', month: '2-digit', day: '2-digit' });
  P.today = function (date = new Date()) {
    const parts = moscowDateFormatter.formatToParts(date);
    const value = type => parts.find(p => p.type === type).value;
    return `${value('year')}-${value('month')}-${value('day')}`;
  };
  P.date = value => value ? value.split('-').reverse().join('.') : 'Без даты';
  P.status = item => P.el('span', { class: 'pl-status pl-status-' + item.status }, P.labels[item.status] || item.status);
  P.overdue = item => !item.dynamic && item.due_date && item.due_date < P.today() && P.active(item);
  P.meta = text => P.el('span', { class: 'pl-muted' }, text);
  P.revisions = items => Object.fromEntries(items.map(item => [item.id, item.revision]));
  P.route = function (hash = window.location.hash) {
    const [path, query = ''] = hash.replace(/^#/, '').split('?');
    const parts = path.split('/').filter(Boolean);
    return { view: ['inbox', 'projects', 'tasks', 'calendar', 'series', 'templates', 'task-templates', 'attention', 'settings'].includes(parts[1]) ? parts[1] : 'calendar', id: ['projects','series','templates','task-templates'].includes(parts[1]) && parts[2] ? decodeURIComponent(parts[2]) : null, params: new URLSearchParams(query) };
  };
  P.base = () => window.location.hash.split('?')[0];
  const editorRoute=hash=>{const q=new URLSearchParams((hash||'').split('?')[1]);return q.has('task')||q.get('new')==='task'||q.get('new')==='project'||q.get('edit')==='project';};
  P.rememberEditorOrigin=hash=>{
    if(!editorRoute(hash)||editorRoute(window.location.hash))return;
    P.editorOrigin={url:window.location.hash,scrollY:window.scrollY,scrollX:window.scrollX,focus:document.activeElement};
  };
  P.navigate = hash => { P.rememberEditorOrigin(hash);if(hash!==window.location.hash)P.clearNotice(); window.location.hash = hash; };
  // Native links and programmatic navigation share the same return context.
  if(typeof document!=='undefined')document.addEventListener?.('click',event=>{const link=event.target.closest?.('a[href^="#/plans"]');if(link)P.rememberEditorOrigin(link.getAttribute('href'));},true);
  window.addEventListener('hashchange', () => { if(document.getElementById('planner-toast')?.dataset.route!==window.location.hash)P.clearNotice(); });
  P.panelURL = function (params = {}) {
    const query = new URLSearchParams(window.location.hash.split('?')[1] || '');
    ['task', 'new', 'edit', 'id', 'for_project', 'parent', 'for_date'].forEach(k => query.delete(k));
    Object.entries(params).forEach(([key, value]) => { if (value) query.set(key, value); });
    return P.base() + (query.size ? '?' + query : '');
  };
  // Tag only same-document entries we observe. Unknown/external history remains native.
  const historySession=String(Date.now())+Math.random();
  let historyEntry, historyTransition, restoringHash;
  const tagHistory=(index,hash=window.location.hash)=>{
    const entry={session:historySession,index,hash};
    window.history.replaceState({...window.history.state,plannerEntry:entry},'',hash||window.location.href);
    return entry;
  };
  if(window.history?.replaceState){
    historyEntry=tagHistory(0);
    window.addEventListener('hashchange',()=>{
      const state=window.history.state?.plannerEntry;
      const next=state?.session===historySession&&state.hash===window.location.hash?state:tagHistory(historyEntry.index+1);
      historyTransition={from:historyEntry,to:next};historyEntry=next;
    });
  }
  P.replaceRoute=hash=>{
    window.history.replaceState(window.history.state,'',hash);
    if(historyEntry){historyEntry=tagHistory(historyEntry.index,hash);if(historyTransition)historyTransition.to=historyEntry;}
  };
  P.historyEntry=()=>historyEntry;
  P.restoreEditorEntry=entry=>{
    if(restoringHash===entry.hash)return;
    if(entry?.session===historySession&&historyEntry?.session===historySession&&entry.index!==historyEntry.index){
      restoringHash=entry.hash;window.history.go(entry.index-historyEntry.index);
    }else {window.history.replaceState(window.history.state,'',entry.hash);historyEntry=tagHistory(entry.index,entry.hash);}
  };
  P.consumeHistoryRestore=()=>{
    if(restoringHash!==window.location.hash)return false;
    restoringHash=null;return true;
  };
  P.closeEditor = () => {
    const target=P.panelURL();
    P.editorReturn=P.editorOrigin;
    if(!window.history?.replaceState){P.navigate(target);return;}
    if(historyTransition&&historyTransition.to===historyEntry&&historyTransition.from.hash===target&&historyTransition.from.index===historyEntry.index-1)window.history.back();
    else {P.replaceRoute(target);window.dispatchEvent(new HashChangeEvent('hashchange'));}
  };
  P.openTask = id => P.navigate(P.panelURL({ task: id }));
  P.newTask = (project, parent, date) => P.navigate(P.panelURL({ new: 'task', for_project: project, parent, for_date: date }));
  P.clearNotice = () => { clearTimeout(P.toastTimer); document.getElementById('planner-toast')?.remove(); };
  P.notify = function (message, action) {
    let toast = document.getElementById('planner-toast');
    if (!toast) { toast = P.el('div', { id: 'planner-toast', class: 'pl-toast', role: 'status' }); }
    const dialog=[...document.querySelectorAll('dialog[open]')].at(-1);
    if(dialog){const footer=dialog.querySelector(':scope > .pl-drawer-footer');dialog.insertBefore(toast,footer);}else document.body.append(toast);
    toast.dataset.route=window.location.hash;
    toast.replaceChildren(P.el('span', {}, message));
    if(action)toast.append(P.button(action.label,action.run));
    clearTimeout(P.toastTimer);
    P.toastTimer = setTimeout(() => toast.remove(), 4000);
  };
  let fieldSequence = 0;
  P.field = function (form, name, label, type = 'text', value = '', options) {
    let input;
    if (type === 'select') input = P.el('select', { name }, options.map(([id, text]) => P.el('option', { value: id }, text)));
    else input = P.el(type === 'textarea' ? 'textarea' : 'input', { name, ...(type === 'textarea' ? { rows: 3 } : { type }) });
    input.value = value == null ? '' : value;
    input.id = 'planner-field-' + (++fieldSequence);
    const field = P.el('div', { class: 'pl-field' }, P.el('label', { htmlFor: input.id }, label), input);
    form.append(field);
    return input;
  };
  P.values = form => Object.fromEntries(new FormData(form));
  P.nullable = (data, keys) => { for (const key of keys) data[key] = data[key] || null; return data; };
  P.errorBox = () => P.el('div', { class: 'pl-error', role: 'alert', hidden: true });
  P.describe = function (record) {
    const labels = { color_id:'Цвет',icon_id:'Иконка',title: 'Название', description: 'Описание', status: 'Статус', project_id: 'Проект', parent_task_id: 'Родитель', priority: 'Приоритет', planned_date: 'Плановая дата', scheduled_time: 'Время', due_date: 'Крайний срок', start_date: 'Начало', date: 'Дата периода работы', start_time: 'С', end_time: 'До', completed_date: 'Выполнено', archived_at: 'Архивирован' };
    return P.el('dl', { class: 'pl-comparison' }, Object.entries(labels).filter(([key]) => key in record || (['color_id','icon_id'].includes(key)&&record.effective_color_id)).map(([key, label]) => {
      let value = record[key];
      if(key==='color_id')value=P.palette?.[value]?.[0]||'Наследовать';
      if(key==='icon_id')value=value?P.appearanceIcon({icon_id:value}):'Наследовать';
      if (key === 'project_id') value = P.data.projects.find(p => p.id === value)?.title || value;
      if (key === 'parent_task_id') value = P.data.tasks.find(t => t.id === value)?.title || value;
      return [P.el('dt', {}, label), P.el('dd', {}, P.labels[value] || value || '—')];
    }));
  };
  // A failed mutation never rebuilds the form. Rebase is an explicit two-step review.
  P.bindForm = function (form, submit, success, review) {
    const error = P.errorBox();
    const save = P.el('button', { type: 'submit', class: 'pl-button pl-primary' }, form.dataset.submit || 'Сохранить');
    form.append(error, P.el('div', { class: 'pl-form-footer' }, save));
    let busy = false, recoveryControls = null;
    form.addEventListener('submit', async event => {
      event.preventDefault();
      if (busy || save.disabled) return;
      busy = true;
      form.dataset.busy = 'true';
      if(recoveryControls)recoveryControls.forEach(([control,disabled])=>{control.disabled=disabled;});
      const controls = [...form.elements].map(control => [control, control.disabled]);
      // Read the payload before disabling inputs (FormData omits disabled fields).
      let pending;
      try { pending = submit(); } catch (e) { pending = Promise.reject(e); }
      controls.forEach(([control]) => { control.disabled = true; });
      error.hidden = true;
      try {
        const value = await pending;
        delete form.dataset.uncertain; recoveryControls=null;
        form.dataset.saved='true';
        await success(value);
        form.dispatchEvent(new Event('planner-saved'));
      } catch (e) {
        form.dispatchEvent(new Event('planner-failed'));
        error.replaceChildren(P.el('p', {}, e.status === 409 ? P.errorMessage(e) + ' Черновик сохранён. Сначала просмотрите актуальную запись.' : P.errorMessage(e)));
        error.hidden = false;
        if(e.uncertainCommand){form.dataset.uncertain='true';recoveryControls=controls;error.append(P.el('p',{},'Повторите сохранение: будет отправлена та же операция. До подтверждения поля временно заблокированы.'));}
        else {delete form.dataset.uncertain;recoveryControls=null;}
        if ((e.status === 409 || e.code === 'sessions_decision_required') && review) {
          save.dataset.conflict = 'true';
          const check = P.button('Посмотреть актуальную версию', async () => {
            check.disabled = true;
            try {
              const result = await review();
              const approve = P.button('Использовать эту версию и сохранить черновик', () => {
                result.accept();
                delete save.dataset.conflict;
                save.disabled = false;
                error.replaceChildren(P.el('p', {}, 'Актуальная версия принята. Проверьте черновик и нажмите «Сохранить».'));
              });
              error.replaceChildren(P.el('strong', {}, 'Сейчас на сервере'), P.describe(result.record), ...(result.related || []).map(record => P.describe(record)), approve);
              if (result.reload) error.append(P.button('Загрузить актуальные данные вместо черновика', result.reload));
            } catch (loadError) { error.append(P.el('p', {}, P.errorMessage(loadError))); check.disabled = false; }
          });
          error.append(check);
        }
      } finally {
        busy = false;
        delete form.dataset.busy;
        controls.forEach(([control, disabled]) => { control.disabled = form.dataset.uncertain ? control !== save : disabled; });
        if (save.dataset.conflict) save.disabled = true;
      }
    });
    return save;
  };
  P.trapFocus = function(dialog) {
    dialog.addEventListener('keydown',e=>{
      if(e.key!=='Tab')return;
      const controls=[...dialog.querySelectorAll('button,input,select,textarea,a[href],[tabindex]')].filter(n=>!n.disabled&&n.tabIndex>=0&&n.getClientRects().length);
      const first=controls[0],last=controls[controls.length-1];
      if(!first){e.preventDefault();dialog.focus();return;}
      if(e.shiftKey&&(document.activeElement===first||!dialog.contains(document.activeElement))){e.preventDefault();last.focus();}
      else if(!e.shiftKey&&(document.activeElement===last||!dialog.contains(document.activeElement))){e.preventDefault();first.focus();}
    });
  };
  P.dialog = function (title, host, options = {}) {
    if(host)return {dialog:host,close:host.closeEditor};
    const previous = document.activeElement;
    const headingID='planner-dialog-'+(++fieldSequence);
    const dialog = P.el('dialog', { class: 'pl-dialog planner', 'aria-labelledby':headingID });
    const close = async (force) => { if (force !== true && dialog.querySelector('[data-busy="true"]')) return; if(force!==true && dialog.plannerGuard && !await dialog.plannerGuard())return; dialog.close(); dialog.remove(); if(previous?.isConnected)previous.focus({preventScroll:true}); options.onClosed?.(force); };
    const x = P.button('×', close, 'pl-icon'); x.setAttribute('aria-label', 'Закрыть диалог');
    dialog.append(P.el('div', { class: 'pl-dialog-head' }, P.el('h2', {id:headingID}, title), x));
    dialog.addEventListener('cancel', e => { e.preventDefault(); close(); });
    P.trapFocus(dialog);
    document.body.append(dialog); if(!options.deferOpen)dialog.showModal();
    return { dialog, close };
  };
  // The same form and dialog survive changes of viewport. Only CSS changes layout.
  P.editorShell = function(title, options={}) {
    const h=P.dialog(title,null,{deferOpen:true,onClosed:force=>{if(force!==true&&editorRoute(window.location.hash))P.closeEditor();}});
    const dialog=h.dialog;
    dialog.classList.add('pl-drawer','pl-drawer-shell','pl-editor-shell');
    // Preserve task/action-dialog selectors used by existing commands.
    if(options.kind==='task')dialog.classList.remove('pl-dialog');
    if(options.label){dialog.removeAttribute('aria-labelledby');dialog.setAttribute('aria-label',options.label);}
    if(options.width)dialog.style.setProperty('--editor-width',options.width+'px');
    const head=dialog.querySelector('.pl-dialog-head');head.classList.add('pl-drawer-head');
    const heading=head.querySelector('h2');heading.tabIndex=-1;heading.autofocus=true;
    const x=head.querySelector('button');x.classList.add('pl-editor-close');x.setAttribute('aria-label','Закрыть редактор');
    x.replaceChildren(P.el('span',{class:'pl-editor-back','aria-hidden':'true'},'←'),P.el('span',{class:'pl-editor-x','aria-hidden':'true'},'×'));
    const body=P.el('div',{class:'pl-drawer-body'});dialog.append(body);
    const shell={...h,body,attach(form){
      if(!body.contains(form))body.append(form);
      form.id ||= 'planner-editor-form-'+(++fieldSequence);
      const footer=form.querySelector('.pl-form-footer');
      if(footer){footer.classList.add('pl-drawer-footer');footer.querySelectorAll('button').forEach(b=>b.setAttribute('form',form.id));dialog.append(footer);}
    },show(focus){
      if(!dialog.open)dialog.showModal();
      document.body.classList.add('planner-open');
      if(window.matchMedia('(max-width:767px)').matches)heading.focus({preventScroll:true});else (focus||heading).focus({preventScroll:true});
    }};
    dialog.editorShell=shell;dialog.closeEditor=h.close;
    return shell;
  };
  P.showPreview = function (section) {
    section.tabIndex=-1;
    requestAnimationFrame(()=>{if(section.isConnected){section.scrollIntoView({block:'nearest'});section.focus({preventScroll:true});}});
  };
  P.errorMessage = function(error) {
    const messages={completion_branch_conflict:'У этого повторения уже есть продолжение. Старое выполнение нельзя восстановить как второе активное. Можно отметить его выполненным только для истории.',production_calendar_missing:'Нет подтверждённого производственного календаря для выбранного года. Откройте настройки календаря.',revision_conflict:'Запись была изменена на другом устройстве.',preview_stale:'Данные изменились. Повторите предпросмотр перед применением.'};
    if(messages[error.code])return messages[error.code];
    const message=String(error.message||'');
    // Keep transport diagnostics in the console; never expose raw identifiers,
    // SQL, stack traces or untranslated server internals in a user message.
    if(/[А-Яа-яЁё]/.test(message)&&!/[a-z]+_[a-z]+|SQL|ReferenceError|SELECT |constraint failed/.test(message))return message;
    console.warn('Planner request failed', {code:error.code,status:error.status,message});
    return error.status===409?'Данные изменились. Черновик сохранён; проверьте актуальные данные.':'Не удалось выполнить действие. Проверьте соединение и повторите попытку.';
  };
  P.data = { projects: [], tasks: [] };
  P.drafts = new Map();
})();
