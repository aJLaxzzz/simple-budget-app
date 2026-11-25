(function () {
  'use strict';
  const form = document.getElementById('login-form'), password = document.getElementById('password');
  document.getElementById('show-password').onclick = function () {
    const show = password.type === 'password'; password.type = show ? 'text' : 'password';
    this.textContent = show ? 'Скрыть пароль' : 'Показать пароль'; this.setAttribute('aria-pressed', String(show));
  };
  fetch('/api/auth/session').then(r => r.ok ? r.json() : null).then(s => { if (s && s.enabled) location.replace('/' + location.hash); }).catch(() => {});
  form.onsubmit = async function (event) {
    event.preventDefault(); const button = form.querySelector('[type=submit]'), error = document.getElementById('error');
    button.disabled = true; error.textContent = '';
    try {
      const response = await fetch('/api/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username: form.elements.username.value, password: password.value, remember: form.elements.remember.checked }) });
      const body = await response.json(); if (!response.ok) throw new Error(body.error || 'Не удалось войти');
      location.replace('/' + location.hash);
    } catch (e) { error.textContent = e.message || 'Нет связи с сервером'; button.disabled = false; }
  };
})();
