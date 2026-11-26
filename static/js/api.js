(function () {
  'use strict';

  let csrf = '', authReady;
  let revision = '', primaryRead = null, financeTurn = 0;
  // Keep the displayed snapshot's CAS revision until a fresh view is accepted.
  // A failed/cancelled refresh must not leave an otherwise valid draft with no If-Match.
  window.budgetFinanceView = path => { if (primaryRead !== path) revision = ''; primaryRead = path; financeTurn++; };
  window.budgetAcceptFinanceRead = reads => {
    const snapshot = reads.get(primaryRead);
    if (!window.budgetDraftDirty && snapshot?.financeRevision) revision = snapshot.financeRevision;
  };
  window.budgetForgetAuth = () => { authReady = null; csrf = ''; };
  const emit = (name, detail) => { if (window.dispatchEvent && typeof CustomEvent !== 'undefined') window.dispatchEvent(new CustomEvent(name, { detail })); };
  window.budgetAuth = async function (refresh) {
    if (refresh) authReady = null;
    if (!authReady) authReady = fetch('/api/auth/session').then(async r => {
      if (r.status === 401) { emit('budget-auth-required'); throw new Error('Требуется повторный вход'); }
      if (!r.ok) throw new Error('Не удалось проверить сессию');
      const session = await r.json(); csrf = session.csrf || ''; return session;
    }).catch(e => { authReady = null; throw e; });
    return authReady;
  };
  window.budgetAPI = async function (url, opts) {
    const views = window.budgetViews;
    const tx = views?.capture();
    const ownFinanceTurn = financeTurn;
    const finance = /^\/api\/(months|categories)(\/|$|\?)/.test(url);
    const write = opts && opts.method && !['GET', 'HEAD'].includes(opts.method);
    const writeRevision = revision;
    const internal = /\/materialize$|\/ensure-upcoming$/.test(url);
    if (write && !internal) views?.invalidate();
    if (!write) { views?.check(tx); const cached = views?.cached(tx, url); if (cached) {
      if (finance && !window.budgetDraftDirty && !revision && (!primaryRead || url === primaryRead)) revision = cached.financeRevision || '';
      return cached.value;
    } }
    // Unit consumers without a DOM remain pure API clients.
    if (typeof document !== 'undefined') await window.budgetAuth();
    window.budgetRequests = (window.budgetRequests || 0) + 1;
    try {
    var response;
    try {
      response = await fetch(url, {
        ...opts,
        headers: { 'Content-Type': 'application/json', ...(csrf ? {'X-CSRF-Token': csrf} : {}), ...(finance && write ? {'If-Match': writeRevision} : {}), ...(opts && opts.headers) }
      });
    } catch (error) {
      throw new Error('Нет связи с приложением. Сохранение не подтверждено; проверь данные после восстановления связи.');
    }
    if (response.status === 401) { authReady = null; emit('budget-auth-required'); }
    if (!response.ok) {
      var message = 'Ошибка сервера (' + response.status + '). Сохранение не подтверждено.';
      try {
        var body = await response.json();
        if (body && typeof body.error === 'string') message = body.error;
        else if (body && body.error && body.error.message) message = body.error.message;
      } catch (error) { /* The server may return a non-JSON error. */ }
      var failure = new Error(message);
      failure.status = response.status;
      failure.code = body && body.error && body.error.code;
      failure.current = body && body.error && body.error.current;
      throw failure;
    }
    if (response.headers && ownFinanceTurn === financeTurn && ((finance && write) || (!write && !window.budgetDraftDirty && !revision && (primaryRead ? url === primaryRead : finance)))) revision = response.headers.get('X-Budget-Revision') || revision;
    if (write) views?.written(internal);
    if (write && !internal) { window.budgetLastWrite = Date.now(); window.budgetDraftDirty = false; emit('budget-saved'); }
    if (response.status === 204) return null;
    try {
      const value = await response.json();
      if (!write) views?.read(tx, url, value, response.headers);
      return value;
    } catch (error) {
      if (error.superseded) throw error;
      throw new Error('Некорректный ответ сервера. Проверь данные перед повторным сохранением.');
    }
    } catch (error) { if (!write && !error.superseded) window.budgetReadFailures = (window.budgetReadFailures || 0) + 1; throw error; }
    finally { window.budgetRequests--; }
  };
})();
