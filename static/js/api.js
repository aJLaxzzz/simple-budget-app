(function () {
  'use strict';

  window.budgetAPI = async function (url, opts) {
    var response;
    try {
      response = await fetch(url, {
        ...opts,
        headers: { 'Content-Type': 'application/json', ...(opts && opts.headers) }
      });
    } catch (error) {
      throw new Error('Нет связи с приложением. Сохранение не подтверждено; проверь данные после восстановления связи.');
    }
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
    if (response.status === 204) return null;
    try {
      return await response.json();
    } catch (error) {
      throw new Error('Некорректный ответ сервера. Проверь данные перед повторным сохранением.');
    }
  };
})();
