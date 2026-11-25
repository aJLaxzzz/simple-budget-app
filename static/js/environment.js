(function () {
  'use strict';
  // The port label is immediate, including while the backend reconnects.
  // The server also identifies acceptance when used in an isolated local test.
  function mark() {
    if (document.getElementById('stg-banner')) return;
    const label = document.createElement('div');
    label.id = 'stg-banner'; label.setAttribute('role', 'note');
    label.textContent = 'STG · Тестовое окружение · Только синтетические данные';
    label.style.cssText = 'position:sticky;top:0;z-index:10000;padding:8px 12px;background:#facc15;color:#291900;text-align:center;font:600 14px system-ui;';
    document.body.prepend(label); document.title = '[STG] ' + document.title;
  }
  if (location.port === '8443') mark();
  fetch('/api/environment', { cache: 'no-store' }).then(r => r.ok ? r.json() : null)
    .then(info => { if (info && info.staging) mark(); }).catch(() => {});
})();
