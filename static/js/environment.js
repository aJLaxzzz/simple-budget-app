(function () {
  'use strict';
  // The port label is immediate, including while the backend reconnects.
  // The server also identifies acceptance when used in an isolated local test.
  function mark() {
    if (document.getElementById('stg-banner')) return;
    const label = document.createElement('span');
    label.id = 'stg-banner'; label.setAttribute('role', 'note');
    label.textContent = 'STG'; label.title = 'Тестовое окружение · Только синтетические данные'; label.setAttribute('aria-label',label.title);
    label.style.cssText = 'display:inline-flex;align-items:center;flex-shrink:0;padding:3px 7px;margin:0 8px;border:1px solid #b99536;border-radius:6px;background:#302817;color:#f5d274;font:700 12px system-ui;line-height:18px;';
    (document.querySelector('#environment-marker,.header h1,main h1')||document.querySelector('.header')||document.body).append(label); document.title = '[STG] ' + document.title;
  }
  if (location.port === '8443') mark();
  fetch('/api/environment', { cache: 'no-store' }).then(r => r.ok ? r.json() : null)
    .then(info => { if (info && info.staging) mark(); }).catch(() => {});
})();
