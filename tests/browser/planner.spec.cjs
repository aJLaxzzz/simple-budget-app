const { test, expect, revealAction } = require('../browser-fixtures.cjs');
const fs = require('node:fs');
const path = require('node:path');
let number = 0;
const unique = label => `${label} ${Date.now()}-${++number}`;
async function create(request, type, data) {
  const response = await request.post('/api/planner/' + type, { data });
  expect(response.ok(), await response.text()).toBeTruthy(); return response.json();
}
const get = async (request, type, id) => (await request.get('/api/planner/' + type + '/' + id)).json();
async function openTask(page, task) {
  await page.goto('/#/plans/tasks?task=' + task.id);
  await expect(page.getByRole('dialog', { name: 'Редактор задачи', exact: true })).toBeVisible();
}
const editor = page => page.getByRole('dialog', { name: 'Редактор задачи', exact: true });
const actionDialog = page => page.locator('dialog.pl-dialog');
async function expand(page,title){if(['Периоды работы','История'].includes(title)){await editor(page).getByRole('tab',{name:title,exact:true}).click();return;}const d=editor(page).locator('details.pl-disclosure').filter({has:page.locator(':scope > summary > span').filter({hasText:new RegExp('^'+title+'$')})});if(!await d.getAttribute('open').then(v=>v!==null))await d.locator(':scope > summary').click();}
async function taskMenu(page,label){await editor(page).locator('.pl-editor-actions').getByRole('button',{name:'Действия с задачей',exact:true}).click();await actionDialog(page).getByRole('button',{name:label,exact:true}).click();}

test.beforeEach(async ({ page }) => {
  page.unhandledErrors = [];
  page.on('pageerror', error => page.unhandledErrors.push(error.message));
  await page.route('https://**/*', route => route.abort());
});
test.afterEach(async ({ page }) => { expect(page.unhandledErrors).toEqual([]); });
test.afterAll(() => {
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'data.json'), 'utf8')).toBe('{"months":[]}');
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'categories.json'), 'utf8')).toBe('{"expense":[],"income":[]}');
});

test('plans navigation, reload, back/forward and no finance requests', async ({ page }) => {
  const financial = [];
  page.on('request', req => { if (new URL(req.url()).pathname.startsWith('/api/') && !req.url().includes('/api/planner/') && !req.url().includes('/api/auth/') && !req.url().includes('/api/events')) financial.push(req.url()); });
  await page.goto('/#/plans/inbox');
  await expect(page.getByRole('heading', { name: 'Входящие', exact: true })).toBeVisible();
  await page.getByRole('navigation', { name: 'Разделы планировщика' }).getByRole('link', { name: 'Проекты' }).click();
  await expect(page.getByRole('heading', { name: 'Проекты', exact: true })).toBeVisible();
  await page.reload(); await expect(page.getByRole('heading', { name: 'Проекты', exact: true })).toBeVisible();
  await page.goBack(); await expect(page.getByRole('heading', { name: 'Входящие', exact: true })).toBeVisible();
  await page.goForward(); await expect(page.getByRole('heading', { name: 'Проекты', exact: true })).toBeVisible();
  expect(financial).toEqual([]);
});

test('quick create safely renders HTML and prevents duplicate submission', async ({ page, request }) => {
  const title = unique('<img src=x onerror="window.injected=true">');
  await page.goto('/#/plans/inbox');
  await page.getByRole('textbox', { name: 'Название новой задачи' }).fill(title);
  let posts = 0;
  await page.route('**/api/planner/tasks', async route => {
    if (route.request().method() === 'POST') { posts++; await new Promise(r => setTimeout(r, 200)); }
    await route.continue();
  });
  await page.getByRole('button', { name: 'Добавить', exact: true }).dblclick();
  await expect(page.getByRole('button', { name: title, exact: true })).toBeVisible();
  expect(posts).toBe(1);
  expect(await page.evaluate(() => window.injected)).toBeUndefined();
  expect(await page.locator('.planner img').count()).toBe(0);
  const tasks = await (await request.get('/api/planner/tasks')).json();
  expect(tasks.items.filter(t => t.title === title)).toHaveLength(1);
});

test('create project, open card, create task and subtask, use server progress', async ({ page, request }) => {
  const title = unique('Переезд');
  await page.goto('/#/plans/projects');
  await page.getByRole('button', { name: '+ Проект', exact: true }).click();
  await page.getByLabel('Название проекта', { exact: true }).fill(title);
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible();
  await page.getByRole('textbox', { name: 'Название новой задачи' }).fill('Документы');
  await page.getByRole('button', { name: 'Добавить', exact: true }).click();
  await page.getByRole('button', { name: 'Добавить подзадачу: Документы', exact: true }).click();
  await page.getByLabel('Название', { exact: true }).fill('Проверить договор');
  await page.getByRole('dialog').locator('button[type=submit]').click();
  await expect(editor(page)).toHaveCount(0);
  await page.getByRole('button',{name:'Проверить договор',exact:true}).click();
  await expect(editor(page).getByLabel('Родительская задача', { exact: true })).toHaveValue(/.+/);
  await page.getByRole('button', { name: 'Закрыть редактор' }).click();
  await expect(page.locator('.pl-child').getByRole('button', { name: 'Проверить договор', exact: true })).toBeVisible();
  await expect(page.getByText('Выполнено 0 из 1', { exact: true })).toBeVisible();
  await page.getByRole('link', { name: '← Все проекты' }).click();
  await page.locator('.pl-project-card').filter({ hasText: title }).click();
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible();
  const projects = await (await request.get('/api/planner/projects')).json();
  const project = projects.items.find(p => p.title === title);
  expect(project.progress.total).toBe(1);
});

test('inbox schedule and move keep task, filters and draft navigation', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Разобрать входящие') });
  const project = await create(request, 'projects', { title: unique('Дом') });
  await page.goto('/#/plans/inbox?task=' + task.id);
  await editor(page).getByLabel('Плановая дата', { exact: true }).fill('2026-12-20');
  await expand(page,'Проект и подзадача');
  await editor(page).getByLabel('Проект', { exact: true }).selectOption(project.id);
  await editor(page).locator('button[type=submit]').click();
  await expect(editor(page)).toHaveCount(0);
  expect((await get(request, 'tasks', task.id)).planned_date).toBe('2026-12-20');
  await expect(page.getByRole('button', { name: task.title, exact: true })).toHaveCount(0);
  expect((await get(request, 'tasks', task.id)).project_id).toBe(project.id);
  await page.goto('/#/plans/tasks');
  await page.getByLabel('Поиск по названию').fill(task.title);
  await page.getByLabel('Проект', { exact: true }).selectOption(project.id);
  await page.getByRole('button', { name: task.title, exact: true }).click();
  await expand(page,'Параметры');
  await editor(page).getByLabel('Описание').fill('Несохранённая мысль');
  await page.getByRole('button', { name: 'Закрыть редактор' }).click();
  await page.getByRole('button',{name:'Продолжить редактирование',exact:true}).click();
  await expect(editor(page).getByLabel('Описание')).toHaveValue('Несохранённая мысль');
  await page.getByRole('button',{name:'Закрыть редактор'}).click();
  await page.getByRole('button',{name:'Сохранить и продолжить',exact:true}).click();
  await expect(editor(page)).toHaveCount(0);
  await expect(page.getByLabel('Поиск по названию')).toHaveValue(task.title);
  await page.getByRole('button', { name: task.title, exact: true }).click();
  await expect(editor(page).getByLabel('Описание')).toHaveValue('Несохранённая мысль');
});

test('completion actual Moscow date, cancellation, restore and lazy history', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Прочитать') });
  await openTask(page, task);
  const historyRequests = [];
  page.on('request', req => { if (req.url().includes('/history?')) historyRequests.push(req.url()); });
  await editor(page).getByRole('button', {name:'Действия с задачей',exact:true}).click();
 await revealAction(page,'Выполнить другой датой');await page.getByRole('button',{name:'Выполнить другой датой',exact:true}).click();
  expect(await actionDialog(page).getByLabel('Фактическая дата выполнения').inputValue()).toBe(await page.evaluate(() => window.Planner.today()));
  await actionDialog(page).getByLabel('Фактическая дата выполнения').fill('2026-01-15');
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(editor(page).getByRole('button', { name: 'Восстановить задачу', exact: true })).toBeVisible();
  expect((await get(request, 'tasks', task.id)).completed_date).toBe('2026-01-15');
  expect(historyRequests).toHaveLength(0);
  await expand(page,'История');

  await expect(page.locator('.pl-history details')).toHaveCount(2);
  await editor(page).getByRole('tab', { name: 'Основное', exact: true }).click();
  await editor(page).getByRole('button', { name: 'Восстановить задачу', exact: true }).click();
  await actionDialog(page).locator('button[type=submit]').click();
  await taskMenu(page,'Отменить задачу');
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(editor(page).getByRole('button', { name: 'Восстановить задачу', exact: true })).toBeVisible();
  expect((await get(request, 'tasks', task.id)).status).toBe('cancelled');
});

test('parent completes children with explicit session cancellation and revisions', async ({ page, request }) => {
  const project = await create(request, 'projects', { title: unique('Составной проект') });
  const parent = await create(request, 'tasks', { title: unique('Родитель'), project_id: project.id });
  const child = await create(request, 'tasks', { title: unique('Дочерняя'), project_id: project.id, parent_task_id: parent.id });
  const first = await create(request, 'sessions', { task_id: parent.id, date: '2027-01-01' });
  const second = await create(request, 'sessions', { task_id: child.id, date: '2027-01-02' });
  await openTask(page, parent);
  await editor(page).getByRole('button', { name: 'Завершить задачу', exact: true }).click();
  const dialog = actionDialog(page);
  await dialog.getByLabel('Открытые подзадачи: 1').selectOption('complete');
  await expect(dialog.getByText('Активные периоды работы: 2', { exact: true })).toBeVisible();
  await dialog.locator('button[type=submit]').click();
  expect((await get(request, 'tasks', parent.id)).status).toBe('planned');
  await dialog.getByRole('checkbox').check();
  const mutation = page.waitForRequest(req => req.method() === 'POST' && req.url().endsWith('/tasks/' + parent.id + '/complete'));
  await dialog.locator('button[type=submit]').click();
  const body = (await mutation).postDataJSON();
  expect(body.expected_revision).toBe(parent.revision);
  expect(body.related_revisions[child.id]).toBe(child.revision);
  expect(body.sessions_decision).toBe('cancel');
  expect(body.session_revisions).toEqual({ [first.id]: first.revision, [second.id]: second.revision });
  await expect(editor(page).getByRole('button', { name: 'Восстановить задачу', exact: true })).toBeVisible();
  expect((await get(request, 'tasks', child.id)).status).toBe('completed');
  expect((await get(request, 'sessions', second.id)).status).toBe('cancelled');
});

test('keep children open leaves their sessions active', async ({ page, request }) => {
  const project = await create(request, 'projects', { title: unique('Открытые дети') });
  const parent = await create(request, 'tasks', { title: unique('Родитель'), project_id: project.id });
  const child = await create(request, 'tasks', { title: unique('Ребёнок'), project_id: project.id, parent_task_id: parent.id });
  const session = await create(request, 'sessions', { task_id: child.id, date: '2027-01-01' });
  await openTask(page, parent);
  await editor(page).getByRole('button', { name: 'Завершить задачу', exact: true }).click();
  await actionDialog(page).getByLabel('Открытые подзадачи: 1').selectOption('keep_open');
  await expect(actionDialog(page).getByRole('checkbox')).toHaveCount(0);
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(editor(page).getByText('Родитель завершён, подзадачи оставлены открытыми.', { exact: false })).toBeVisible();
  expect((await get(request, 'tasks', child.id)).status).toBe('planned');
  expect((await get(request, 'sessions', session.id)).status).toBe('planned');
});

test('409 retains draft and requires viewing latest before reapply', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Конфликт') });
  await openTask(page, task);
  await editor(page).getByLabel('Название', { exact: true }).fill('Мой черновик');
  await request.patch('/api/planner/tasks/' + task.id, { data: { expected_revision: 1, title: 'Из другого окна', description: 'Новое описание коллеги' } });
  await editor(page).locator('button[type=submit]').click();
  await expect(editor(page).getByRole('alert')).toContainText('Запись была изменена на другом устройстве.');
  await expect(editor(page).getByLabel('Название', { exact: true })).toHaveValue('Мой черновик');
  await expect(editor(page).locator('button[type=submit]')).toBeDisabled();
  expect((await get(request, 'tasks', task.id)).title).toBe('Из другого окна');
  await editor(page).getByRole('button', { name: 'Посмотреть актуальную версию' }).click();
  await expect(editor(page).getByRole('alert')).toContainText('Из другого окна');
  await editor(page).getByRole('button', { name: 'Использовать эту версию и сохранить черновик' }).click();
  await editor(page).locator('button[type=submit]').click();
  await expect(editor(page).getByRole('alert')).toBeHidden();
  expect((await get(request, 'tasks', task.id)).title).toBe('Мой черновик');
  expect((await get(request, 'tasks', task.id)).description).toBe('Новое описание коллеги');
});

test('network and server save errors retain form and never claim success', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Ошибка сети') });
  await openTask(page, task);
  await expand(page,'Параметры');
  await editor(page).getByLabel('Описание').fill('Важные заметки');
  await page.route('**/api/planner/tasks/' + task.id, route => route.request().method() === 'PATCH' ? route.abort() : route.continue());
  await editor(page).locator('button[type=submit]').click();
  await expect(editor(page).getByRole('alert')).toContainText('Сохранение не подтверждено');
  await expect(editor(page).getByLabel('Описание')).toHaveValue('Важные заметки');
  await page.unroute('**/api/planner/tasks/' + task.id);
  await page.route('**/api/planner/tasks/' + task.id, route => route.request().method() === 'PATCH' ? route.fulfill({ status: 500, json: { error: { code: 'internal_error', message: 'Не удалось сохранить' } } }) : route.continue());
  await editor(page).locator('button[type=submit]').click();
  await expect(editor(page).getByRole('alert')).toContainText('Не удалось сохранить');
  await expect(editor(page).getByLabel('Описание')).toHaveValue('Важные заметки');
  expect((await get(request, 'tasks', task.id)).description).toBe('');
});

test('sessions add, reschedule, complete without changing task', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Периоды работы'), planned_date: '2026-11-01', due_date: '2026-12-31' });
  await openTask(page, task);
  await expand(page,'Периоды работы');
  await editor(page).getByRole('button', { name: '+ Период работы', exact: true }).click();
  await actionDialog(page).getByLabel('Дата периода работы', { exact: true }).fill('2026-11-15');
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(editor(page).locator('.pl-session')).toHaveCount(1);
  await editor(page).getByRole('button', { name: 'Перенести / изменить' }).click();
  await actionDialog(page).getByLabel('Новая дата', { exact: true }).fill('2026-11-16');
  await actionDialog(page).getByLabel('Начало · Москва').fill('10:00');
  await actionDialog(page).getByLabel('Окончание · Москва').fill('11:00');
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(editor(page).locator('.pl-session')).toContainText('10:00–11:00');
  await editor(page).getByRole('button', { name: 'Выполнить период работы' }).click();
  await actionDialog(page).getByLabel('Фактическая дата выполнения').fill('2026-11-15');
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(editor(page).locator('.pl-session')).toContainText('Выполнено 15.11.2026');
  expect(await get(request, 'tasks', task.id)).toEqual(task);
});

test('desktop and narrow layout, drawer focus, Escape and no overflow', async ({ page, request }, testInfo) => {
  const project = await create(request, 'projects', { title: unique('Личный проект'), description: 'Спокойно двигаться к важным целям — по одному шагу.' });
  const task = await create(request, 'tasks', { title: 'Продумать следующий шаг', project_id: project.id, priority: 'high' });
  await page.goto('/#/plans/projects');
  await expect(page.locator('.pl-project-card').filter({ hasText: project.title })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('projects-desktop.png'), fullPage: true });
  await openTask(page, task);
  expect((await editor(page).boundingBox()).width).toBeCloseTo(560, 0);
  await page.screenshot({ path: testInfo.outputPath('editor-desktop.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  expect((await editor(page).boundingBox()).width).toBeCloseTo(390, 0);
  expect(await editor(page).evaluate(n => n.scrollWidth <= n.clientWidth)).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('editor-narrow.png') });
  await page.keyboard.press('Escape'); await expect(editor(page)).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
});

test('financial categories still load and plans does not mutate JSON', async ({ page }) => {
  await page.goto('/#/categories');
  await expect(page.getByRole('heading', { name: 'Категории расходов', exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Планы', exact: true }).click();
  await expect(page.locator('.pc-calendar')).toBeVisible();
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'data.json'), 'utf8')).toBe('{"months":[]}');
});

test('moving a parent moves the whole tree with all expected revisions', async ({ page, request }) => {
  const first = await create(request, 'projects', { title: unique('Первый проект') });
  const second = await create(request, 'projects', { title: unique('Второй проект') });
  const parent = await create(request, 'tasks', { title: unique('Дерево'), project_id: first.id });
  const child = await create(request, 'tasks', { title: unique('Лист'), project_id: first.id, parent_task_id: parent.id });
  await openTask(page, parent);
  await expand(page,'Проект и подзадача');
  await editor(page).getByLabel('Проект', { exact: true }).selectOption(second.id);
  const mutation = page.waitForRequest(req => req.method() === 'PATCH' && req.url().endsWith('/tasks/' + parent.id));
  await editor(page).locator('button[type=submit]').click();
  expect((await mutation).postDataJSON().related_revisions[child.id]).toBe(child.revision);
  await expect(editor(page)).toHaveCount(0);
  expect((await get(request, 'tasks', parent.id)).project_id).toBe(second.id);
  await expect.poll(async () => (await get(request, 'tasks', child.id)).project_id).toBe(second.id);
  await page.goto('/#/plans/projects/' + second.id);
  await expect(page.locator('[data-task-id="' + child.id + '"]')).toHaveCount(1);
  await expect(page.locator('[data-task-id="' + child.id + '"]')).toHaveClass(/pl-child/);
});

test('cancel task explicitly closes active sessions; restore preserves history', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Отмена периодов работы') });
  const session = await create(request, 'sessions', { task_id: task.id, date: '2027-03-01' });
  await openTask(page, task);
  await taskMenu(page,'Отменить задачу');
  await actionDialog(page).getByRole('checkbox').check();
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(editor(page).getByRole('button', { name: 'Восстановить задачу', exact: true })).toBeVisible();
  expect((await get(request, 'sessions', session.id)).status).toBe('cancelled');
  await editor(page).getByRole('button', { name: 'Восстановить задачу', exact: true }).click();
  await actionDialog(page).locator('button[type=submit]').click();
  await expand(page,'Периоды работы');
  await expect(editor(page).getByRole('button', { name: 'Восстановить период работы', exact: true })).toBeVisible();
  expect((await get(request, 'sessions', session.id)).status).toBe('cancelled');
});

test('project edit, completion and archive leave task status intact', async ({ page, request }) => {
  const project = await create(request, 'projects', { title: unique('Проект для архива') });
  const task = await create(request, 'tasks', { title: unique('Незавершённая'), project_id: project.id });
  await page.goto('/#/plans/projects/' + project.id);
  await page.getByRole('button',{name:'Действия с проектом',exact:true}).click();
  await page.getByRole('button', { name: 'Редактировать проект' }).click();
  await actionDialog(page).getByLabel('Описание').fill('Сохранённое описание');
  await actionDialog(page).locator('summary').filter({hasText:'Даты и статус'}).click();
  await actionDialog(page).getByLabel('Статус', { exact: true }).selectOption('in_progress');
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(page.locator('.pl-project-overview')).toContainText('Сохранённое описание');
  await page.getByRole('button',{name:'Действия с проектом',exact:true}).click();
  await page.getByRole('button', { name: 'Завершить проект', exact: true }).click();
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(page.locator('.pl-project-overview .pl-status')).toHaveText('Завершено');
  expect((await get(request, 'tasks', task.id)).status).toBe('planned');
  await page.getByRole('button',{name:'Действия с проектом',exact:true}).click();
  await page.getByRole('button', { name: 'В архив', exact: true }).click();
  await actionDialog(page).locator('button[type=submit]').click();
  await expect(page.getByText('В архиве · только просмотр')).toBeVisible();
  await page.getByRole('link', { name: '← Все проекты' }).click();
  await page.getByLabel('Показывать', { exact: true }).selectOption('archived');
  await expect(page.locator('.pl-project-card').filter({ hasText: project.title })).toBeVisible();
});

test('history paginates without duplicate events', async ({ page, request }) => {
  let task = await create(request, 'tasks', { title: unique('История') });
  for (let i = 0; i < 12; i++) {
    const response = await request.patch('/api/planner/tasks/' + task.id, { data: { expected_revision: task.revision, description: 'Изменение ' + i } });
    expect(response.ok()).toBeTruthy(); task = await response.json();
  }
  await openTask(page, task);
  await expand(page,'История');

  await expect(editor(page).locator('.pl-history details')).toHaveCount(10);
  await editor(page).getByRole('button', { name: 'Показать ещё' }).click();
  await expect(editor(page).locator('.pl-history details')).toHaveCount(13);
  await expect(editor(page).getByRole('button', { name: 'Показать ещё' })).toBeHidden();
});

test('load error can retry and empty inbox has a clear next action', async ({ page }) => {
  await page.route('**/api/planner/tasks?*', route => route.fulfill({ status: 503, json: { error: { message: 'Временно недоступно' } } }));
  await page.goto('/#/plans/inbox');
  await expect(page.getByRole('heading', { name: 'Не удалось загрузить планы' })).toBeVisible();
  await page.unroute('**/api/planner/tasks?*');
  await page.route('**/api/planner/tasks?*', route => route.fulfill({ json: { items: [] } }));
  await page.getByRole('button', { name: 'Попробовать снова' }).click();
  await expect(page.getByRole('heading', { name: 'Входящие разобраны' })).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'Название новой задачи' })).toBeVisible();
});
