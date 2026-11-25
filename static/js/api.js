(function () {
  'use strict';

  let csrf = '', authReady;
  let revision = '', primaryRead = null, financeTurn = 0;
  window.budgetFinanceView = path => { primaryRead = path; revision = ''; financeTurn++; };
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
    const ownFinanceTurn = financeTurn;
    const finance = /^\/api\/(months|categories)(\/|$|\?)/.test(url);
    const write = opts && opts.method && !['GET', 'HEAD'].includes(opts.method);
    // Unit consumers without a DOM remain pure API clients.
    if (typeof document !== 'undefined') await window.budgetAuth();
    window.budgetRequests = (window.budgetRequests || 0) + 1;
    try {
    var response;
    try {
      response = await fetch(url, {
        ...opts,
        headers: { 'Content-Type': 'application/json', ...(csrf ? {'X-CSRF-Token': csrf} : {}), ...(finance && write ? {'If-Match': revision} : {}), ...(opts && opts.headers) }
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
    if (write && !/\/materialize$|\/ensure-upcoming$/.test(url)) { window.budgetLastWrite = Date.now(); window.budgetDraftDirty = false; emit('budget-saved'); }
    if (response.status === 204) return null;
    try {
      return await response.json();
    } catch (error) {
      throw new Error('Некорректный ответ сервера. Проверь данные перед повторным сохранением.');
    }
    } catch (error) { if (!write) window.budgetReadFailures = (window.budgetReadFailures || 0) + 1; throw error; }
    finally { window.budgetRequests--; }
  };
})();
