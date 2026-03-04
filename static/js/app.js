(function () {
  'use strict';

  const API = '/api';
  const MONTH_NAMES = ['', 'Январь', 'Февраль', 'Март', 'Апрель', 'Май', 'Июнь', 'Июль', 'Август', 'Сентябрь', 'Октябрь', 'Ноябрь', 'Декабрь'];
  const CHART_COLORS = ['#58a6ff', '#3fb950', '#d29922', '#f85149', '#a371f7', '#79c0ff', '#ff7b72', '#56d4dd', '#ffa657'];

  const root = document.getElementById('root');
  const modalRoot = document.getElementById('modal-root');

  var monthSortExpenses = 'desc';
  var monthSortIncomes = 'desc';

  function parseHash() {
    const hash = (window.location.hash || '#/').slice(1);
    const parts = hash.split('/').filter(Boolean);
    if (parts[0] === 'month' && parts[1]) return { page: 'month', monthId: parts[1] };
    if (parts[0] === 'year') return { page: 'year', year: parts[1] ? parseInt(parts[1], 10) : new Date().getFullYear() };
    if (parts[0] === 'categories') return { page: 'categories' };
    return { page: 'home' };
  }

  function navigateTo(path) {
    window.location.hash = path;
  }

  function api(path, opts) {
    return fetch(API + path, {
      headers: { 'Content-Type': 'application/json' },
      ...opts
    }).then(function (r) {
      if (r.status === 204) return null;
      return r.json();
    });
  }

  function totalIncome(month) {
    var sum = Number(month.income) || 0;
    (month.incomes || []).forEach(function (e) { sum += e.amount; });
    return sum;
  }

  // ——— Home ———
  function renderHome() {
    root.innerHTML = '<div class="home"><p class="muted">Загрузка...</p></div>';
    Promise.all([api('/now'), api('/months')]).then(function (results) {
      var now = results[0] || {};
      var months = results[1] || [];
      var newMonthCreated = null;

      if (now.month && now.year) {
        var monthId = 'month-' + now.year + '-' + (now.month < 10 ? '0' + now.month : now.month);
        var exists = months.some(function (m) { return m.id === monthId; });
        if (!exists) {
          return api('/months', { method: 'POST', body: JSON.stringify({ month: now.month, year: now.year }) })
            .then(function (created) {
              if (created) {
                newMonthCreated = MONTH_NAMES[created.month] + ' ' + created.year;
                months = [].concat(months, created).sort(function (a, b) {
                  return a.year !== b.year ? a.year - b.year : a.month - b.month;
                });
              }
              renderHomeContent(months, newMonthCreated);
            });
        }
      }
      renderHomeContent(months, newMonthCreated);
    });
  }

  function renderHomeContent(months, newMonthCreated, selectedYear) {
      // Сортировка: сначала самый свежий год, в нём самый свежий месяц
      months = months.slice().sort(function (a, b) {
        return a.year !== b.year ? b.year - a.year : b.month - a.month;
      });
      var years = [];
      var seen = {};
      months.forEach(function (m) {
        if (!seen[m.year]) { seen[m.year] = true; years.push(m.year); }
      });
      years.sort(function (a, b) { return b - a; });
      selectedYear = selectedYear != null ? selectedYear : (years[0] || new Date().getFullYear());
      var monthsInYear = months.filter(function (m) { return m.year === selectedYear; });

      root.innerHTML = '<div class="home">' +
        (newMonthCreated ? '<div class="toast-new-month" id="toast-new-month">Создан новый месяц: ' + newMonthCreated + '</div>' : '') +
        '<p class="home-greeting">Здарова, Тимка!</p>' +
        '<h1>Месячные бюджеты</h1>' +
        '<p class="subtitle">Выбери месяц или создай новый лист. Неверный месяц можно удалить целиком.</p>' +
        '<form class="create-month-form" id="create-month-form">' +
          '<h2>Создать новый месяц</h2>' +
          '<div class="form-row">' +
            '<label><span>Месяц</span><select id="new-month" required><option value="">Выбери</option>' +
            MONTH_NAMES.slice(1).map(function (name, i) {
              return '<option value="' + (i + 1) + '">' + name + '</option>';
            }).join('') + '</select></label>' +
            '<label><span>Год</span><input type="number" id="new-year" min="2020" max="2030" value="' + new Date().getFullYear() + '" /></label>' +
            '<button type="submit">Создать</button>' +
          '</div>' +
        '</form>' +
        (years.length > 0
          ? '<section class="months-list">' +
              '<div class="year-tabs" role="tablist">' +
                years.map(function (y) {
                  return '<button type="button" class="year-tab' + (y === selectedYear ? ' active' : '') + '" data-year="' + y + '" role="tab">' + y + '</button>';
                }).join('') +
              '</div>' +
              '<h2>Месяцы ' + selectedYear + '</h2>' +
              '<ul id="home-months-list">' +
                monthsInYear.map(function (m) {
                  var totalExp = (m.expenses || []).reduce(function (s, e) { return s + e.amount; }, 0);
                  var totalInc = totalIncome(m);
                  return '<li class="month-card-wrap">' +
                    '<a href="#/month/' + m.id + '" class="month-card">' +
                      '<span class="month-title">' + MONTH_NAMES[m.month] + ' ' + m.year + '</span>' +
                      '<span class="month-meta">Расходы: ' + totalExp.toLocaleString('ru-RU') + ' ₽' +
                      (totalInc > 0 ? ' · Доход: ' + totalInc.toLocaleString('ru-RU') + ' ₽' : '') + '</span>' +
                    '</a>' +
                    '<button type="button" class="btn-delete btn-delete-month" title="Удалить месяц" data-month-id="' + m.id + '">×</button>' +
                    '</li>';
                }).join('') +
              '</ul>' +
            '</section>'
          : '<section class="months-list"><h2>Все месяцы</h2><p class="muted">Пока нет ни одного месяца. Создай первый выше.</p></section>') +
        '</div>';

      if (newMonthCreated) {
        setTimeout(function () {
          var el = document.getElementById('toast-new-month');
          if (el) el.classList.add('toast-hide');
        }, 5000);
      }

      document.getElementById('create-month-form').onsubmit = function (e) {
        e.preventDefault();
        var month = parseInt(document.getElementById('new-month').value, 10);
        var year = parseInt(document.getElementById('new-year').value, 10);
        if (!month || month < 1 || month > 12) return;
        api('/months', { method: 'POST', body: JSON.stringify({ month: month, year: year }) })
          .then(function (created) {
            if (created) navigateTo('#/month/' + created.id);
          });
      };

      root.querySelectorAll('.btn-delete-month').forEach(function (btn) {
        btn.onclick = function (e) {
          e.preventDefault();
          e.stopPropagation();
          var id = btn.getAttribute('data-month-id');
          if (!id || !confirm('Удалить весь месяц и все данные в нём? Это нельзя отменить.')) return;
          api('/months/' + id, { method: 'DELETE' }).then(function () { renderHome(); });
        };
      });

      root.querySelectorAll('.year-tab').forEach(function (btn) {
        btn.onclick = function () {
          var y = parseInt(btn.getAttribute('data-year'), 10);
          if (y && years.indexOf(y) !== -1) renderHomeContent(months, newMonthCreated, y);
        };
      });
    }

  var chartInstances = [];

  function destroyCharts() {
    chartInstances.forEach(function (c) { if (c && c.destroy) c.destroy(); });
    chartInstances = [];
  }

  function renderMonthPage(monthId) {
    root.innerHTML = '<div class="month-page"><p class="muted">Загрузка...</p></div>';
    Promise.all([
      api('/months/' + monthId),
      api('/categories')
    ]).then(function (results) {
      var month = results[0];
      var cats = results[1] || {};
      var expenseCats = cats.expense || [];
      var incomeCats = cats.income || [];
      if (!month) {
        root.innerHTML = '<div class="month-page"><p class="muted">Месяц не найден.</p><a href="#/">На главную</a></div>';
        return;
      }

      destroyCharts();

      var expenses = month.expenses || [];
      var incomes = month.incomes || [];
      expenses.forEach(function (e, i) { e._idx = i; });
      incomes.forEach(function (e, i) { e._idx = i; });
      var totalExpenses = expenses.reduce(function (s, e) { return s + e.amount; }, 0);
      var income = totalIncome(month);
      var balance = income - totalExpenses;

      var categoryMap = {};
      expenseCats.forEach(function (c) { categoryMap[c.id] = c.name; });
      var byCategory = {};
      var byCategoryId = {};
      expenses.forEach(function (e) {
        var name = categoryMap[e.category] || e.category;
        byCategory[name] = (byCategory[name] || 0) + e.amount;
        byCategoryId[e.category] = (byCategoryId[e.category] || 0) + e.amount;
      });
      var categoryAnalytics = Object.keys(byCategoryId).map(function (id) {
        var name = categoryMap[id] || id;
        var sum = byCategoryId[id];
        return {
          id: id,
          name: name,
          sum: Math.round(sum),
          percent: totalExpenses > 0 ? Math.round((sum / totalExpenses) * 100) : 0
        };
      }).sort(function (a, b) { return b.sum - a.sum; });

      var categoryColorById = {};
      categoryAnalytics.forEach(function (a, i) { categoryColorById[a.id] = CHART_COLORS[i % CHART_COLORS.length]; });

      var bySubKey = {};
      expenses.forEach(function (e) {
        var subName = e.subcategory || '—';
        var key = e.category + '\t' + subName;
        if (!bySubKey[key]) {
          bySubKey[key] = { categoryId: e.category, categoryName: categoryMap[e.category] || e.category, subcategoryName: subName, sum: 0 };
        }
        bySubKey[key].sum += e.amount;
      });
      var subcategoryAnalytics = Object.keys(bySubKey).map(function (k) {
        var o = bySubKey[k];
        return { categoryId: o.categoryId, categoryName: o.categoryName, subcategoryName: o.subcategoryName, sum: Math.round(o.sum) };
      }).sort(function (a, b) { return b.sum - a.sum; });

      var byDay = {};
      expenses.forEach(function (e) {
        byDay[e.date] = (byDay[e.date] || 0) + e.amount;
      });
      var barLabels = Object.keys(byDay).sort();
      var barData = barLabels.map(function (d) { return byDay[d]; });

      var sortedExpenses = expenses.slice().sort(function (a, b) {
        return monthSortExpenses === 'desc' ? b._idx - a._idx : a._idx - b._idx;
      });
      var sortedIncomes = incomes.slice().sort(function (a, b) {
        return monthSortIncomes === 'desc' ? b._idx - a._idx : a._idx - b._idx;
      });
      var incomeCatMap = {};
      incomeCats.forEach(function (c) { incomeCatMap[c.id] = c.name; });

      var daysInMonth = new Date(month.year, month.month, 0).getDate();
      var dayOptions = [];
      for (var d = 1; d <= daysInMonth; d++) dayOptions.push('<option value="' + d + '">' + d + '</option>');

      root.innerHTML =
        '<div class="month-page">' +
          '<nav class="breadcrumb">' +
            '<a href="#/">Месяцы</a><span class="sep">/</span>' +
            '<span>' + MONTH_NAMES[month.month] + ' ' + month.year + '</span>' +
            '<button type="button" class="btn-delete-month-inline" id="btn-delete-month">Удалить месяц</button>' +
          '</nav>' +
          '<div class="summary-cards">' +
            '<div class="card income-card"><span class="card-label">Доход за месяц</span>' +
            '<span class="card-value" id="income-value" title="Нажми, чтобы изменить">' + income.toLocaleString('ru-RU') + ' ₽</span>' +
            '<div class="income-edit" id="income-edit" style="display:none">' +
              '<input type="number" id="income-input" />' +
              '<button type="button" id="income-save">Сохранить</button>' +
              '<button type="button" class="secondary" id="income-cancel">Отмена</button>' +
            '</div></div>' +
            '<div class="card expense-card"><span class="card-label">Расходы</span><span class="card-value red">' + totalExpenses.toLocaleString('ru-RU') + ' ₽</span></div>' +
            '<div class="card balance-card"><span class="card-label">Остаток</span><span class="card-value ' + (balance >= 0 ? 'green' : 'red') + '">' + balance.toLocaleString('ru-RU') + ' ₽</span></div>' +
          '</div>' +
          '<div class="toolbar">' +
            '<button type="button" class="btn-primary" id="btn-add-expense">+ Добавить расход</button> ' +
            '<button type="button" class="btn-primary btn-income" id="btn-add-income">+ Добавить доход</button>' +
          '</div>' +
          '<section class="section"><h2>Доходы за месяц</h2>' +
          '<div class="sort-row"><label>Порядок: </label><select id="sort-incomes">' +
            '<option value="asc"' + (monthSortIncomes === 'asc' ? ' selected' : '') + '>Сначала старые</option>' +
            '<option value="desc"' + (monthSortIncomes === 'desc' ? ' selected' : '') + '>Сначала новые</option>' +
          '</select></div>' +
          '<div class="table-wrap"><table class="expenses-table"><thead><tr><th>Дата</th><th>День</th><th>Категория</th><th>Сумма</th><th>Описание</th><th></th></tr></thead><tbody>' +
          (sortedIncomes.length === 0
            ? '<tr><td colspan="6" class="empty">Нет записей о доходах. Нажми «Добавить доход».</td></tr>'
            : sortedIncomes.map(function (e) {
                return '<tr data-income-id="' + e.id + '">' +
                  '<td>' + e.date + '</td><td>' + (e.dayOfWeek || '') + '</td>' +
                  '<td>' + (incomeCatMap[e.category] || e.category) + '</td>' +
                  '<td class="amount">' + e.amount.toLocaleString('ru-RU') + ' ₽</td>' +
                  '<td class="desc">' + (e.description || '—') + '</td>' +
                  '<td><button type="button" class="btn-delete" title="Удалить">×</button></td></tr>';
              }).join('')) +
          '</tbody></table></div></section>' +
          '<section class="section"><h2>Все траты за месяц</h2>' +
          '<div class="sort-row"><label>Порядок: </label><select id="sort-expenses">' +
            '<option value="asc"' + (monthSortExpenses === 'asc' ? ' selected' : '') + '>Сначала старые</option>' +
            '<option value="desc"' + (monthSortExpenses === 'desc' ? ' selected' : '') + '>Сначала новые</option>' +
          '</select></div>' +
          '<div class="table-wrap"><table class="expenses-table"><thead><tr><th>Дата</th><th>День</th><th>Категория</th><th>Подкатегория</th><th>Сумма</th><th>Описание</th><th></th></tr></thead><tbody>' +
          (sortedExpenses.length === 0
            ? '<tr><td colspan="7" class="empty">Пока нет расходов. Нажми «Добавить расход».</td></tr>'
            : sortedExpenses.map(function (e) {
                return '<tr data-expense-id="' + e.id + '">' +
                  '<td>' + e.date + '</td><td>' + (e.dayOfWeek || '') + '</td>' +
                  '<td>' + (categoryMap[e.category] || e.category) + '</td><td>' + (e.subcategory || '—') + '</td>' +
                  '<td class="amount">' + e.amount.toLocaleString('ru-RU') + ' ₽</td>' +
                  '<td class="desc">' + (e.description || '—') + '</td>' +
                  '<td><button type="button" class="btn-delete" title="Удалить">×</button></td></tr>';
              }).join('')) +
          '</tbody></table></div></section>' +
          '<section class="section"><h2>Список категорий расходов</h2><div class="analytics-grid">' +
          '<div class="analytics-list">' +
          (categoryAnalytics.length === 0
            ? '<p class="muted">Нет данных</p>'
            : '<ul>' + categoryAnalytics.map(function (a) {
                return '<li><span class="cat-name">' + a.name + '</span><span class="cat-sum">' + a.sum.toLocaleString('ru-RU') + ' ₽</span><span class="cat-pct">' + a.percent + '%</span></li>';
              }).join('') + '</ul>') +
          '</div>' +
          '<div class="chart-pie"><div class="chart-container"><canvas id="chart-pie"></canvas></div></div>' +
          '</div></section>' +
          '<section class="section"><h2>Расходы по дням</h2><div class="chart-bar"><div class="chart-container"><canvas id="chart-bar"></canvas></div></div></section>' +
          (subcategoryAnalytics.length > 0 ? '<section class="section"><h2>Топ подкатегорий</h2><div class="chart-bar"><div class="chart-container" style="height:220px"><canvas id="chart-hbar"></canvas></div></div></section>' : '') +
          (barLabels.length > 0 ? '<section class="section"><h2>Динамика расходов по дням</h2><div class="chart-bar"><div class="chart-container" style="height:200px"><canvas id="chart-line"></canvas></div></div></section>' : '') +
          '<p class="year-link"><a href="#/year/' + month.year + '">Аналитика за ' + month.year + ' год →</a></p>' +
        '</div>';

      document.getElementById('btn-delete-month').onclick = function () {
        if (!confirm('Удалить весь месяц и все данные? Это нельзя отменить.')) return;
        api('/months/' + monthId, { method: 'DELETE' }).then(function () { navigateTo('#/'); });
      };

      var incomeVal = document.getElementById('income-value');
      var incomeEdit = document.getElementById('income-edit');
      var incomeInput = document.getElementById('income-input');
      incomeVal.onclick = function () {
        incomeVal.style.display = 'none';
        incomeEdit.style.display = 'flex';
        incomeInput.value = month.income || 0;
        incomeInput.focus();
      };
      document.getElementById('income-save').onclick = function () {
        var v = parseFloat(incomeInput.value) || 0;
        api('/months/' + monthId + '/income', { method: 'PATCH', body: JSON.stringify({ income: v }) })
          .then(function () { renderMonthPage(monthId); });
      };
      document.getElementById('income-cancel').onclick = function () {
        incomeEdit.style.display = 'none';
        incomeVal.style.display = '';
      };

      root.querySelectorAll('tr[data-expense-id] .btn-delete').forEach(function (btn) {
        btn.onclick = function () {
          var row = btn.closest('tr');
          var expId = row && row.dataset.expenseId;
          if (!expId || !confirm('Удалить запись?')) return;
          api('/months/' + monthId + '/expenses/' + expId, { method: 'DELETE' })
            .then(function () { renderMonthPage(monthId); });
        };
      });
      root.querySelectorAll('tr[data-income-id] .btn-delete').forEach(function (btn) {
        btn.onclick = function () {
          var row = btn.closest('tr');
          var incId = row && row.dataset.incomeId;
          if (!incId || !confirm('Удалить запись?')) return;
          api('/months/' + monthId + '/incomes/' + incId, { method: 'DELETE' })
            .then(function () { renderMonthPage(monthId); });
        };
      });

      document.getElementById('btn-add-expense').onclick = function () {
        openAddExpenseModal(monthId, month, expenseCats, function () { renderMonthPage(monthId); });
      };
      document.getElementById('btn-add-income').onclick = function () {
        openAddIncomeModal(monthId, month, incomeCats, function () { renderMonthPage(monthId); });
      };

      document.getElementById('sort-expenses').onchange = function () {
        monthSortExpenses = this.value;
        renderMonthPage(monthId);
      };
      document.getElementById('sort-incomes').onchange = function () {
        monthSortIncomes = this.value;
        renderMonthPage(monthId);
      };

      if (categoryAnalytics.length > 0 && typeof Chart !== 'undefined') {
        var pieCtx = document.getElementById('chart-pie');
        if (pieCtx) {
          chartInstances.push(new Chart(pieCtx.getContext('2d'), {
            type: 'pie',
            data: {
              labels: categoryAnalytics.map(function (a) { return a.name + ' ' + a.percent + '%'; }),
              datasets: [{
                data: categoryAnalytics.map(function (a) { return a.sum; }),
                backgroundColor: categoryAnalytics.map(function (a) { return categoryColorById[a.id] || CHART_COLORS[0]; })
              }]
            },
            options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { labels: { color: '#e8ecf1' } } } }
          }));
        }
      }

      if (barLabels.length > 0 && typeof Chart !== 'undefined') {
        var barCtx = document.getElementById('chart-bar');
        if (barCtx) {
          chartInstances.push(new Chart(barCtx.getContext('2d'), {
            type: 'bar',
            data: {
              labels: barLabels.map(function (d) { return d.slice(5); }),
              datasets: [{ label: 'Сумма', data: barData, backgroundColor: 'rgba(88, 166, 255, 0.8)' }]
            },
            options: { responsive: true, maintainAspectRatio: false, scales: { y: { ticks: { color: '#8b949e' } }, x: { ticks: { color: '#8b949e' } } }, plugins: { legend: { labels: { color: '#e8ecf1' } } } }
          }));
        }
      }

      if (subcategoryAnalytics.length > 0 && typeof Chart !== 'undefined') {
        var hbarCtx = document.getElementById('chart-hbar');
        if (hbarCtx) {
          chartInstances.push(new Chart(hbarCtx.getContext('2d'), {
            type: 'bar',
            data: {
              labels: subcategoryAnalytics.map(function (a) { return a.subcategoryName + ' (' + a.categoryName + ')'; }),
              datasets: [{
                label: '₽',
                data: subcategoryAnalytics.map(function (a) { return a.sum; }),
                backgroundColor: subcategoryAnalytics.map(function (a) { return categoryColorById[a.categoryId] || CHART_COLORS[0]; })
              }]
            },
            options: {
              indexAxis: 'y',
              responsive: true,
              maintainAspectRatio: false,
              scales: { x: { ticks: { color: '#8b949e' } }, y: { ticks: { color: '#8b949e' } } },
              plugins: { legend: { display: false } }
            }
          }));
        }
      }

      if (barLabels.length > 0 && typeof Chart !== 'undefined') {
        var lineCtx = document.getElementById('chart-line');
        if (lineCtx) {
          chartInstances.push(new Chart(lineCtx.getContext('2d'), {
            type: 'line',
            data: {
              labels: barLabels.map(function (d) { return d.slice(5); }),
              datasets: [{ label: 'Расход за день', data: barData, borderColor: '#58a6ff', backgroundColor: 'rgba(88, 166, 255, 0.2)', fill: true, tension: 0.3 }]
            },
            options: { responsive: true, maintainAspectRatio: false, scales: { y: { ticks: { color: '#8b949e' } }, x: { ticks: { color: '#8b949e' } } }, plugins: { legend: { labels: { color: '#e8ecf1' } } } }
          }));
        }
      }
    });
  }

  function openAddExpenseModal(monthId, month, categories, onAdded) {
    var daysInMonth = new Date(month.year, month.month, 0).getDate();
    var today = new Date().getDate();
    if (today > daysInMonth) today = daysInMonth;
    var modalEl = document.createElement('div');
    modalEl.className = 'modal-overlay';
    var dayOpts = '';
    for (var d = 1; d <= daysInMonth; d++) dayOpts += '<option value="' + d + '"' + (d === today ? ' selected' : '') + '>' + d + '</option>';
    modalEl.innerHTML =
      '<div class="modal">' +
        '<div class="modal-header"><h2>Добавить расход</h2><button type="button" class="modal-close" aria-label="Закрыть">×</button></div>' +
        '<form class="modal-form" id="expense-form">' +
          '<label><span>День месяца (1–' + daysInMonth + ')</span><select id="exp-day" required>' + dayOpts + '</select></label>' +
          '<label><span>Категория</span><select id="exp-category" required><option value="">Выбери категорию</option>' +
          categories.map(function (c) { return '<option value="' + c.id + '">' + c.name + '</option>'; }).join('') + '</select></label>' +
          '<label id="exp-subcat-wrap" style="display:none"><span>Подкатегория</span><select id="exp-subcategory"><option value="">—</option></select></label>' +
          '<label><span>Сумма (₽)</span><input type="text" id="exp-amount" inputmode="decimal" placeholder="0" required /></label>' +
          '<label><span>Описание</span><input type="text" id="exp-desc" placeholder="Необязательно" /></label>' +
          '<div class="modal-actions"><button type="button" class="btn-secondary" id="exp-cancel">Отмена</button><button type="submit" class="btn-primary">Добавить</button></div>' +
        '</form>' +
      '</div>';
    modalRoot.appendChild(modalEl);

    var catSelect = modalEl.querySelector('#exp-category');
    var subcatWrap = modalEl.querySelector('#exp-subcat-wrap');
    var subcatSelect = modalEl.querySelector('#exp-subcategory');

    function updateSubcategories() {
      var id = catSelect.value;
      var cat = categories.find(function (c) { return c.id === id; });
      subcatSelect.innerHTML = '<option value="">—</option>';
      if (cat && cat.subcategories && cat.subcategories.length) {
        subcatWrap.style.display = 'flex';
        cat.subcategories.forEach(function (s) {
          subcatSelect.appendChild(new Option(s, s));
        });
      } else {
        subcatWrap.style.display = 'none';
      }
    }
    catSelect.onchange = updateSubcategories;

    function closeModal() { modalEl.remove(); }

    modalEl.querySelector('.modal-close').onclick = closeModal;
    modalEl.querySelector('#exp-cancel').onclick = closeModal;
    modalEl.querySelector('#expense-form').onsubmit = function (e) {
      e.preventDefault();
      var amount = parseFloat(modalEl.querySelector('#exp-amount').value.replace(/,/, '.').replace(/\s/g, '')) || 0;
      var day = parseInt(modalEl.querySelector('#exp-day').value, 10);
      if (!catSelect.value || amount <= 0) return;
      api('/months/' + monthId + '/expenses', {
        method: 'POST',
        body: JSON.stringify({
          category: catSelect.value,
          subcategory: subcatSelect.value || '',
          amount: amount,
          description: (modalEl.querySelector('#exp-desc').value || '').trim(),
          day: day
        })
      }).then(function () { closeModal(); if (onAdded) onAdded(); });
    };
    modalEl.onclick = function (e) { if (e.target === modalEl) closeModal(); };
    modalEl.querySelector('.modal').onclick = function (e) { e.stopPropagation(); };
  }

  function openAddIncomeModal(monthId, month, incomeCats, onAdded) {
    var daysInMonth = new Date(month.year, month.month, 0).getDate();
    var today = new Date().getDate();
    if (today > daysInMonth) today = daysInMonth;
    var modalEl = document.createElement('div');
    modalEl.className = 'modal-overlay';
    var dayOpts = '';
    for (var d = 1; d <= daysInMonth; d++) dayOpts += '<option value="' + d + '"' + (d === today ? ' selected' : '') + '>' + d + '</option>';
    modalEl.innerHTML =
      '<div class="modal">' +
        '<div class="modal-header"><h2>Добавить доход</h2><button type="button" class="modal-close" aria-label="Закрыть">×</button></div>' +
        '<form class="modal-form" id="income-form">' +
          '<label><span>День месяца (1–' + daysInMonth + ')</span><select id="inc-day" required>' + dayOpts + '</select></label>' +
          '<label><span>Категория</span><select id="inc-category" required><option value="">Выбери категорию</option>' +
          incomeCats.map(function (c) { return '<option value="' + c.id + '">' + c.name + '</option>'; }).join('') + '</select></label>' +
          '<label><span>Сумма (₽)</span><input type="text" id="inc-amount" inputmode="decimal" placeholder="0" required /></label>' +
          '<label><span>Описание</span><input type="text" id="inc-desc" placeholder="Необязательно" /></label>' +
          '<div class="modal-actions"><button type="button" class="btn-secondary" id="inc-cancel">Отмена</button><button type="submit" class="btn-primary">Добавить</button></div>' +
        '</form>' +
      '</div>';
    modalRoot.appendChild(modalEl);

    function closeModal() { modalEl.remove(); }

    modalEl.querySelector('.modal-close').onclick = closeModal;
    modalEl.querySelector('#inc-cancel').onclick = closeModal;
    modalEl.querySelector('#income-form').onsubmit = function (e) {
      e.preventDefault();
      var amount = parseFloat(modalEl.querySelector('#inc-amount').value.replace(/,/, '.').replace(/\s/g, '')) || 0;
      var day = parseInt(modalEl.querySelector('#inc-day').value, 10);
      var cat = modalEl.querySelector('#inc-category').value;
      if (!cat || amount <= 0) return;
      api('/months/' + monthId + '/incomes', {
        method: 'POST',
        body: JSON.stringify({
          category: cat,
          amount: amount,
          description: (modalEl.querySelector('#inc-desc').value || '').trim(),
          day: day
        })
      }).then(function () { closeModal(); if (onAdded) onAdded(); });
    };
    modalEl.onclick = function (e) { if (e.target === modalEl) closeModal(); };
    modalEl.querySelector('.modal').onclick = function (e) { e.stopPropagation(); };
  }

  function renderYearPage(year) {
    if (!year) year = new Date().getFullYear();
    root.innerHTML = '<div class="year-page"><p class="muted">Загрузка...</p></div>';
    api('/years/' + year).then(function (data) {
      if (!data) {
        root.innerHTML = '<div class="year-page"><p class="muted">Нет данных за этот год.</p><a href="#/">На главную</a></div>';
        return;
      }
      destroyCharts();

      var totalIncome = data.totalIncome || 0;
      var totalExpenses = data.totalExpenses || 0;
      var balance = data.balance || 0;
      var byCategory = data.byCategory || [];
      var byMonthExpenses = data.byMonthExpenses || {};
      var byMonthIncome = data.byMonthIncome || {};
      var byMonthBalance = data.byMonthBalance || {};
      var monthLabels = ['Янв','Фев','Мар','Апр','Май','Июн','Июл','Авг','Сен','Окт','Ноя','Дек'];
      var monthLabelsFull = ['Январь','Февраль','Март','Апрель','Май','Июнь','Июль','Август','Сентябрь','Октябрь','Ноябрь','Декабрь'];
      var months = data.months || [];
      var yearList = [];
      var curYear = new Date().getFullYear();
      for (var y = curYear; y >= curYear - 5; y--) yearList.push(y);

      root.innerHTML =
        '<div class="year-page">' +
          '<nav class="breadcrumb">' +
            '<a href="#/">Месяцы</a><span class="sep">/</span>' +
            '<span>Аналитика за год</span>' +
          '</nav>' +
          '<div class="year-select">' +
            '<label>Год:</label><select id="year-select">' +
            yearList.map(function (y) { return '<option value="' + y + '"' + (y === year ? ' selected' : '') + '>' + y + '</option>'; }).join('') +
            '</select>' +
          '</div>' +
          '<div class="summary-cards">' +
            '<div class="card income-card"><span class="card-label">Доход за год</span><span class="card-value">' + totalIncome.toLocaleString('ru-RU') + ' ₽</span></div>' +
            '<div class="card expense-card"><span class="card-label">Расходы за год</span><span class="card-value red">' + totalExpenses.toLocaleString('ru-RU') + ' ₽</span></div>' +
            '<div class="card balance-card"><span class="card-label">Накоплено за год</span><span class="card-value ' + (balance >= 0 ? 'green' : 'red') + '">' + balance.toLocaleString('ru-RU') + ' ₽</span></div>' +
          '</div>' +
          '<section class="section section-by-month-balance"><h2>Остаток по месяцам</h2>' +
          (function () {
            var keys = Object.keys(byMonthBalance).sort();
            if (keys.length === 0) return '<p class="muted">Нет данных по месяцам</p>';
            return '<ul class="analytics-list-ul by-month-balance-list">' + keys.map(function (key) {
              var parts = key.split('-');
              var m = parseInt(parts[1], 10);
              var name = monthLabelsFull[m - 1] || key;
              var val = byMonthBalance[key];
              return '<li><span class="cat-name">' + name + '</span><span class="cat-sum ' + (val >= 0 ? 'green' : 'red') + '">' + val.toLocaleString('ru-RU') + ' ₽</span></li>';
            }).join('') + '</ul>';
          })() +
          '</section>' +
          '<section class="section"><h2>Расходы по категориям за год</h2>' +
          (byCategory.length === 0 ? '<p class="muted">Нет данных</p>' :
            '<ul class="analytics-list-ul">' + byCategory.map(function (a) {
              return '<li><span class="cat-name">' + a.name + '</span><span class="cat-sum">' + a.sum.toLocaleString('ru-RU') + ' ₽</span><span class="cat-pct">' + Math.round(a.percent) + '%</span></li>';
            }).join('') + '</ul>') +
          '</div></section>' +
          '<section class="section"><h2>Доходы и расходы по месяцам</h2><div class="chart-bar"><div class="chart-container" style="height:280px"><canvas id="chart-year-bars"></canvas></div></div></section>' +
          (byCategory.length > 0 ? '<section class="section"><h2>Структура расходов за год</h2><div class="chart-pie"><div class="chart-container" style="height:260px"><canvas id="chart-year-pie"></canvas></div></div></section>' : '') +
        '</div>';

      document.getElementById('year-select').onchange = function () {
        navigateTo('#/year/' + this.value);
      };

      var labels = [];
      var incData = [];
      var expData = [];
      for (var m = 1; m <= 12; m++) {
        var key = year + '-' + (m < 10 ? '0' + m : m);
        labels.push(monthLabels[m - 1]);
        incData.push(byMonthIncome[key] || 0);
        expData.push(byMonthExpenses[key] || 0);
      }

      if (typeof Chart !== 'undefined') {
        var yearBarCtx = document.getElementById('chart-year-bars');
        if (yearBarCtx) {
          chartInstances.push(new Chart(yearBarCtx.getContext('2d'), {
            type: 'bar',
            data: {
              labels: labels,
              datasets: [
                { label: 'Доход', data: incData, backgroundColor: 'rgba(63, 185, 80, 0.8)' },
                { label: 'Расход', data: expData, backgroundColor: 'rgba(248, 81, 73, 0.8)' }
              ]
            },
            options: {
              responsive: true,
              maintainAspectRatio: false,
              scales: { y: { ticks: { color: '#8b949e' } }, x: { ticks: { color: '#8b949e' } } },
              plugins: { legend: { labels: { color: '#e8ecf1' } } }
            }
          }));
        }

        if (byCategory.length > 0) {
          var yearPieCtx = document.getElementById('chart-year-pie');
          if (yearPieCtx) {
            chartInstances.push(new Chart(yearPieCtx.getContext('2d'), {
              type: 'pie',
              data: {
                labels: byCategory.map(function (a) { return a.name + ' ' + Math.round(a.percent) + '%'; }),
                datasets: [{ data: byCategory.map(function (a) { return a.sum; }), backgroundColor: CHART_COLORS }]
              },
              options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { labels: { color: '#e8ecf1' } } } }
            }));
          }
        }
      }
    });
  }

  function renderCategoriesPage() {
    root.innerHTML = '<div class="categories-page"><p class="muted">Загрузка...</p></div>';
    api('/categories').then(function (data) {
      var expense = (data && data.expense) ? data.expense.slice() : [];
      var income = (data && data.income) ? data.income.slice() : [];

      function collectFromDOM() {
        if (!root.querySelector('.categories-expense')) return;
        expense = [];
        root.querySelectorAll('.categories-expense .category-row').forEach(function (row) {
          var idEl = row.querySelector('.cat-id');
          var nameEl = row.querySelector('.cat-name');
          var id = idEl ? idEl.value.trim() : '';
          var name = (nameEl && nameEl.value) ? nameEl.value.trim() : '';
          var sub = [];
          row.querySelectorAll('.subcategory-item input').forEach(function (inp) {
            var v = inp.value.trim();
            if (v) sub.push(v);
          });
          expense.push({ id: id, name: name || 'Без названия', subcategories: sub });
        });
        income = [];
        root.querySelectorAll('.categories-income .category-row').forEach(function (row) {
          var idEl = row.querySelector('.cat-id');
          var nameEl = row.querySelector('.cat-name');
          var id = idEl ? idEl.value.trim() : '';
          var name = (nameEl && nameEl.value) ? nameEl.value.trim() : '';
          income.push({ id: id, name: name || 'Без названия' });
        });
      }

      function addExpenseCat() {
        collectFromDOM();
        expense.push({ id: '', name: 'Новая категория', subcategories: [] });
        render();
      }
      function addIncomeCat() {
        collectFromDOM();
        income.push({ id: '', name: 'Новый доход', subcategories: [] });
        render();
      }
      function removeExpenseCat(i) {
        collectFromDOM();
        expense.splice(i, 1);
        render();
      }
      function removeIncomeCat(i) {
        collectFromDOM();
        income.splice(i, 1);
        render();
      }
      function addSubcat(i) {
        collectFromDOM();
        if (!expense[i].subcategories) expense[i].subcategories = [];
        expense[i].subcategories.push('');
        render();
      }
      function removeSubcat(catIdx, subIdx) {
        collectFromDOM();
        expense[catIdx].subcategories.splice(subIdx, 1);
        render();
      }

      function collectAndSave() {
        var exp = [];
        root.querySelectorAll('.categories-expense .category-row').forEach(function (row) {
          var idEl = row.querySelector('.cat-id');
          var nameEl = row.querySelector('.cat-name');
          var id = idEl ? idEl.value.trim() : '';
          var name = (nameEl && nameEl.value) ? nameEl.value.trim() : '';
          if (!name) return;
          var sub = [];
          row.querySelectorAll('.subcategory-item input').forEach(function (inp) {
            var v = inp.value.trim();
            if (v) sub.push(v);
          });
          exp.push({ id: id, name: name, subcategories: sub });
        });
        var inc = [];
        root.querySelectorAll('.categories-income .category-row').forEach(function (row) {
          var idEl = row.querySelector('.cat-id');
          var nameEl = row.querySelector('.cat-name');
          var id = idEl ? idEl.value.trim() : '';
          var name = (nameEl && nameEl.value) ? nameEl.value.trim() : '';
          if (!name) return;
          inc.push({ id: id, name: name });
        });
        if (exp.length === 0 || inc.length === 0) {
          alert('Должна быть хотя бы одна категория расходов и одна доходов.');
          return;
        }
        api('/categories', { method: 'PUT', body: JSON.stringify({ expense: exp, income: inc }) })
          .then(function () {
            root.querySelector('.categories-saved').classList.add('visible');
            setTimeout(function () { root.querySelector('.categories-saved').classList.remove('visible'); }, 2000);
          })
          .catch(function () { alert('Не удалось сохранить.'); });
      }

      function render() {
        root.innerHTML =
          '<div class="categories-page">' +
            '<nav class="breadcrumb"><a href="#/">Месяцы</a><span class="sep">/</span><span>Категории</span></nav>' +
            '<p class="categories-intro">Здесь можно добавлять, удалять и редактировать категории расходов и доходов. Они сразу появятся в формах добавления трат и доходов.</p>' +
            '<span class="categories-saved" aria-live="polite">Сохранено</span>' +
            '<section class="section categories-expense">' +
              '<h2>Категории расходов</h2>' +
              '<p class="muted">У каждой категории может быть подкатегории (например: Еда → Продукты, Кафе).</p>' +
              expense.map(function (c, i) {
                var subcats = (c.subcategories || []).map(function (s, j) {
                  return '<div class="subcategory-item">' +
                    '<input type="text" value="' + (s || '').replace(/"/g, '&quot;') + '" placeholder="Подкатегория" />' +
                    '<button type="button" class="btn-delete-small" data-cat="' + i + '" data-sub="' + j + '">×</button>' +
                    '</div>';
                }).join('');
                return '<div class="category-row">' +
                  '<input type="hidden" class="cat-id" value="' + (c.id || '').replace(/"/g, '&quot;') + '" />' +
                  '<div class="category-main">' +
                    '<input type="text" class="cat-name" value="' + (c.name || '').replace(/"/g, '&quot;') + '" placeholder="Название категории" />' +
                    '<button type="button" class="btn-delete-cat" data-type="exp" data-idx="' + i + '">Удалить</button>' +
                  '</div>' +
                  '<div class="subcategories-list">' +
                    subcats +
                    '<button type="button" class="btn-add-sub" data-idx="' + i + '">+ Подкатегория</button>' +
                  '</div>' +
                  '</div>';
              }).join('') +
              '<button type="button" class="btn-add-cat" id="add-expense-cat">+ Категория расхода</button>' +
            '</section>' +
            '<section class="section categories-income">' +
              '<h2>Категории доходов</h2>' +
              income.map(function (c, i) {
                return '<div class="category-row">' +
                  '<input type="hidden" class="cat-id" value="' + (c.id || '').replace(/"/g, '&quot;') + '" />' +
                  '<div class="category-main">' +
                    '<input type="text" class="cat-name" value="' + (c.name || '').replace(/"/g, '&quot;') + '" placeholder="Название" />' +
                    '<button type="button" class="btn-delete-cat" data-type="inc" data-idx="' + i + '">Удалить</button>' +
                  '</div>' +
                  '</div>';
              }).join('') +
              '<button type="button" class="btn-add-cat" id="add-income-cat">+ Категория дохода</button>' +
            '</section>' +
            '<div class="categories-actions"><button type="button" class="btn-primary" id="save-categories">Сохранить категории</button></div>' +
          '</div>';

        document.getElementById('add-expense-cat').onclick = addExpenseCat;
        document.getElementById('add-income-cat').onclick = addIncomeCat;
        document.getElementById('save-categories').onclick = collectAndSave;

        root.querySelectorAll('.btn-delete-cat[data-type="exp"]').forEach(function (btn) {
          btn.onclick = function () { removeExpenseCat(parseInt(btn.getAttribute('data-idx'), 10)); };
        });
        root.querySelectorAll('.btn-delete-cat[data-type="inc"]').forEach(function (btn) {
          btn.onclick = function () { removeIncomeCat(parseInt(btn.getAttribute('data-idx'), 10)); };
        });
        root.querySelectorAll('.btn-add-sub').forEach(function (btn) {
          btn.onclick = function () { addSubcat(parseInt(btn.getAttribute('data-idx'), 10)); };
        });
        root.querySelectorAll('.btn-delete-small').forEach(function (btn) {
          btn.onclick = function () {
            removeSubcat(parseInt(btn.getAttribute('data-cat'), 10), parseInt(btn.getAttribute('data-sub'), 10));
          };
        });
      }

      render();
    });
  }

  function render() {
    var route = parseHash();
    if (route.page === 'month') {
      renderMonthPage(route.monthId);
    } else if (route.page === 'year') {
      renderYearPage(route.year);
    } else if (route.page === 'categories') {
      renderCategoriesPage();
    } else {
      renderHome();
    }
  }

  window.addEventListener('hashchange', render);
  render();

  function initSearch() {
    var input = document.getElementById('search-input');
    var resultsEl = document.getElementById('search-results');
    if (!input || !resultsEl) return;
    var debounceTimer;
    input.addEventListener('input', function () {
      clearTimeout(debounceTimer);
      var q = input.value.trim();
      if (q.length < 2) {
        resultsEl.innerHTML = '';
        resultsEl.setAttribute('aria-hidden', 'true');
        resultsEl.classList.remove('search-results-visible');
        return;
      }
      debounceTimer = setTimeout(function () {
        var url = window.location.origin + API + '/search?q=' + encodeURIComponent(q);
        fetch(url)
          .then(function (r) {
            if (!r.ok) return Promise.reject(new Error('HTTP ' + r.status));
            return r.json();
          })
          .then(function (list) {
            if (input.value.trim() !== q) return;
            if (!Array.isArray(list)) list = [];
            if (list.length === 0) {
              resultsEl.innerHTML = '<div class="search-results-empty">Ничего не найдено</div>';
            } else {
              resultsEl.innerHTML = list.map(function (item) {
                var label = MONTH_NAMES[item.month] + ' ' + item.year;
                var typeLabel = item.type === 'expense' ? 'Расход' : 'Доход';
                var desc = (item.description || '').slice(0, 40);
                if ((item.description || '').length > 40) desc += '…';
                var sub = item.subcategory ? ' · ' + item.subcategory : '';
                return '<a href="#/month/' + item.monthId + '" class="search-result-item">' +
                  '<span class="search-result-meta">' + label + ' · ' + typeLabel + '</span>' +
                  '<span class="search-result-desc">' + (item.category || '') + sub + (desc ? ' — ' + desc : '') + '</span>' +
                  '<span class="search-result-amount ' + (item.type === 'expense' ? 'red' : 'green') + '">' + (item.amount != null ? item.amount.toLocaleString('ru-RU') : '') + ' ₽</span>' +
                  '</a>';
              }).join('');
            }
            resultsEl.setAttribute('aria-hidden', 'false');
            resultsEl.classList.add('search-results-visible');
          })
          .catch(function () {
            if (input.value.trim() !== q) return;
            resultsEl.innerHTML = '<div class="search-results-empty">Ошибка поиска. Проверьте, что приложение запущено на localhost:3001.</div>';
            resultsEl.classList.add('search-results-visible');
          });
      }, 250);
    });
    input.addEventListener('focus', function () {
      if (resultsEl.innerHTML) resultsEl.classList.add('search-results-visible');
    });
    document.addEventListener('click', function (e) {
      if (!input.contains(e.target) && !resultsEl.contains(e.target)) {
        resultsEl.classList.remove('search-results-visible');
      }
    });
  }
  initSearch();

  // Кнопка бэкапа
  function initHeaderActions() {
    var backupBtn = document.getElementById('backup-button');
    if (backupBtn) {
      backupBtn.addEventListener('click', function () {
        var url = window.location.origin + API + '/backup';
        fetch(url)
          .then(function (r) {
            if (!r.ok) throw new Error('HTTP ' + r.status);
            return r.blob();
          })
          .then(function (blob) {
            var a = document.createElement('a');
            var objectUrl = URL.createObjectURL(blob);
            a.href = objectUrl;
            a.download = 'budget-backup.json';
            document.body.appendChild(a);
            a.click();
            a.remove();
            setTimeout(function () { URL.revokeObjectURL(objectUrl); }, 2000);
          })
          .catch(function () {
            alert('Не удалось скачать бэкап. Проверь, что приложение запущено.');
          });
      });
    }
  }
  initHeaderActions();

  // При закрытии вкладки — сохраняем данные и завершаем сервер (без открытия новой вкладки/перехода)
  var shutdownUrl = window.location.origin + API + '/shutdown';
  function sendShutdown() {
    navigator.sendBeacon(shutdownUrl);
  }
  window.addEventListener('pagehide', sendShutdown);
  window.addEventListener('beforeunload', sendShutdown);
})();
