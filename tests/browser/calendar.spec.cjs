const { test, expect } = require('../browser-fixtures.cjs');
const fs = require('node:fs');
const path = require('node:path');
let counter = 0;
const unique = title => title + ' ' + Date.now() + '-' + ++counter;
async function create(request, type, data) {
  const response = await request.post('/api/planner/' + type, { data });
  expect(response.ok(), await response.text()).toBeTruthy(); return response.json();
}
const get = async (request, type, id) => (await request.get('/api/planner/' + type + '/' + id)).json();
const event = (page, type, id) => page.locator('[data-event-id="' + type + ':' + id + '"]');
const dialog = page => page.locator('dialog.pl-dialog');
async function open(page, mode = 'month', date = '2026-12-15', extra = '') {
  await page.goto('/#/plans/calendar?mode=' + mode + '&date=' + date + extra);
  await expect(page.locator('.pc-calendar')).toBeVisible();
}
async function menu(page, type, id) { await event(page, type, id).locator('.pc-event-menu').click(); }
async function move(page, type, id, date) {
  await menu(page, type, id);
  await dialog(page).getByRole('button', { name: 'Перенести', exact: true }).click();
  await dialog(page).getByLabel('Новая дата', { exact: true }).fill(date);
}
test.beforeEach(async ({ page }) => {
  page.errors = []; page.on('pageerror', error => page.errors.push(error.message));
  await page.route('https://**/*', route => route.abort());
});
test.afterEach(async ({ page }) => { expect(page.errors).toEqual([]); });
test.afterAll(() => {
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'data.json'), 'utf8')).toBe('{"months":[]}');
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'categories.json'), 'utf8')).toBe('{"expense":[],"income":[]}');
});

test('month grid, December/January navigation, bounded single API and keyboard days', async ({ page }) => {
  const requests = [];
  page.on('request', req => { if (req.url().includes('/api/')) requests.push(new URL(req.url()).pathname + new URL(req.url()).search); });
  await open(page);
  await expect(page.locator('.pc-month-day')).toHaveCount(35);
  // Protected image resources are assets; the calendar data still uses one bounded projection.
  expect(requests.filter(path=>!/^\/api\/planner\/icons\/[^/]+\/image$/.test(path))).toEqual(['/api/planner/attention?limit=1', '/api/planner/materialize', '/api/planner/calendar?from=2026-11-30&to=2027-01-03']);
  await page.getByRole('button', { name: 'Следующий период' }).click();
  await expect(page.locator('.pc-period h2')).toContainText('январь 2027');
  await expect(page.locator('.pc-month-day')).toHaveCount(35);
  await page.getByRole('button', { name: 'Предыдущий период' }).click();
  await expect(page.locator('.pc-period h2')).toContainText('декабрь 2026');
  await page.getByRole('button', { name: 'Открыть день 15.12.2026', exact: true }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('button', { name: 'Открыть день 16.12.2026', exact: true })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: 'День', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByLabel('Выбранный день')).toHaveValue('2026-12-16');
  await page.goBack(); await expect(page.locator('.pc-month')).toBeVisible();
  await page.getByLabel('Выбранный день').fill('2021-02-15');
  await expect(page.locator('.pc-month-day')).toHaveCount(28);
  await page.getByLabel('Выбранный день').fill('2026-11-15');
  await expect(page.locator('.pc-month-day')).toHaveCount(42);
});

test('tasks, child, several sessions, planning and deadline markers have no duplicate work', async ({ page, request }) => {
  const project = await create(request, 'projects', { title: unique('Календарный проект') });
  const task = await create(request, 'tasks', { title: unique('Встреча'), project_id: project.id, planned_date: '2026-12-10', scheduled_time: '09:00', due_date: '2026-12-12', priority: 'high' });
  const child = await create(request, 'tasks', { title: unique('Подготовка'), project_id: project.id, parent_task_id: task.id, planned_date: '2026-12-11' });
  const s1 = await create(request, 'sessions', { task_id: task.id, date: '2026-12-10', start_time: '09:00', end_time: '10:00' });
  const s2 = await create(request, 'sessions', { task_id: task.id, date: '2026-12-11', start_time: '09:00', end_time: '10:00' });
  const undated = await create(request, 'tasks', { title: unique('Входящая без даты') });
  await open(page, 'month', '2026-12-10', '&project=' + project.id);
  await expect(event(page, 'task', task.id)).toHaveCount(0);
  await expect(event(page, 'session', s1.id)).toHaveCount(1);
  await expect(event(page, 'session', s2.id)).toHaveCount(1);
  await expect(event(page, 'task', child.id)).toHaveCount(1);
  await expect(event(page, 'task', undated.id)).toHaveCount(0);
  await expect(event(page, 'deadline', task.id)).toHaveCount(1);
  await menu(page, 'deadline', task.id);
  await expect(dialog(page).getByRole('button', { name: 'Отметить выполненной', exact: true })).toHaveCount(0);
  await expect(dialog(page)).toContainText('Это срок задачи');
  await page.getByRole('button', { name: 'Закрыть диалог' }).click();
  await request.post('/api/planner/tasks/' + task.id + '/reschedule', { data: { expected_revision: 1, planned_date: '2026-12-09' } });
  await page.reload();
  await expect(event(page, 'planning', task.id)).toHaveCount(1);
  await menu(page, 'planning', task.id);
  await expect(dialog(page).getByRole('button', { name: 'Отметить выполненной', exact: true })).toHaveCount(0);
});

test('all-day and timed tasks, overlaps and week/day switching', async ({ page, request }) => {
  const project = await create(request, 'projects', { title: unique('Расписание') });
  const allDay = await create(request, 'tasks', { title: unique('Без времени'), project_id: project.id, planned_date: '2026-12-15' });
  const timed = await create(request, 'tasks', { title: unique('Время'), project_id: project.id, planned_date: '2026-12-15', scheduled_time: '10:00' });
  const work = await create(request, 'tasks', { title: unique('Работа'), project_id: project.id });
  const s1 = await create(request, 'sessions', { task_id: work.id, date: '2026-12-15', start_time: '10:00', end_time: '11:30' });
  const s2 = await create(request, 'sessions', { task_id: work.id, date: '2026-12-15', start_time: '10:00', end_time: '12:00' });
  await open(page, 'week', '2026-12-15', '&project=' + project.id);
  await expect(page.locator('.pc-time-column')).toHaveCount(7);
  await expect(page.locator('.pc-allday').locator('[data-event-id="task:' + allDay.id + '"]')).toBeVisible();
  // A week column is too narrow for three readable cards: all objects remain in its group.
  await page.locator('.pc-dense-group').click();
  await expect(page.locator('.pc-dense-list .pc-event')).toHaveCount(3);
  await expect(event(page, 'task', timed.id)).toBeVisible();
  for (const s of [s1, s2]) await expect(event(page, 'session', s.id)).toBeVisible();
  await event(page, 'task', timed.id).locator('.pc-event-title').click();
  const taskEditor=page.getByRole('dialog',{name:'Редактор задачи',exact:true});
  await expect(taskEditor.getByLabel('Название',{exact:true})).toHaveValue(timed.title);
  await taskEditor.getByRole('button',{name:'Закрыть редактор'}).click();
  await page.getByRole('button', { name: 'День', exact: true }).click();
  await expect(page.locator('.pc-time-column')).toHaveCount(1);
  await expect(event(page, 'task', timed.id)).toHaveClass(/pc-timed/);
  const bounds = await Promise.all([s1, s2].map(s => event(page, 'session', s.id).boundingBox()));
  expect(bounds[0].x).not.toBe(bounds[1].x);
  await expect(event(page, 'task', timed.id)).toContainText('10:00');
  await page.getByRole('button', { name: 'Месяц', exact: true }).click();
  await expect(page.locator('.pc-month')).toBeVisible();
});

test('more events opens the complete day and task opens existing editor', async ({ page, request }) => {
  const project = await create(request, 'projects', { title: unique('Много дел') });
  const tasks = [];
  for (let i = 0; i < 5; i++) tasks.push(await create(request, 'tasks', { title: unique('Дело ' + i), project_id: project.id, planned_date: '2026-12-20' }));
  await open(page, 'month', '2026-12-20', '&project=' + project.id);
  const cell = page.locator('.pc-month-day[data-date="2026-12-20"]');
  await expect(cell.locator('.pc-event')).toHaveCount(3);
  await cell.getByRole('button', { name: 'Ещё 2' }).click();
  await expect(page.locator('.pc-event')).toHaveCount(5);
  await event(page, 'task', tasks[0].id).locator('.pc-event-title').click();
  await expect(page.getByRole('dialog', { name: 'Редактор задачи', exact: true })).toBeVisible();
  await expect(page.getByLabel('Название', { exact: true })).toHaveValue(tasks[0].title);
  await page.getByRole('button', { name: 'Закрыть редактор' }).click();
  await expect(page.getByRole('button', { name: 'День', exact: true })).toHaveAttribute('aria-pressed', 'true');
});

test('menu move task keeps deadline and uses reschedule; editor date-only move too', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Перенос дела'), planned_date: '2026-12-15', due_date: '2026-12-31' });
  await open(page, 'day');
  await move(page, 'task', task.id, '2026-12-16');
  const saved = page.waitForRequest(req => req.method() === 'POST' && req.url().endsWith('/tasks/' + task.id + '/reschedule'));
  await dialog(page).locator('button[type=submit]').click();
  expect((await saved).postDataJSON()).toEqual({ expected_revision: 1, planned_date: '2026-12-16', scheduled_time: null });
  await expect(event(page, 'task', task.id)).toHaveCount(0);
  expect((await get(request, 'tasks', task.id)).due_date).toBe('2026-12-31');
  await open(page, 'day', '2026-12-16');
  await event(page, 'task', task.id).locator('.pc-event-title').click();
  await page.getByRole('dialog', { name: 'Редактор задачи', exact: true }).getByLabel('Плановая дата').fill('2026-12-17');
  const editorSave = page.waitForRequest(req => req.method() === 'POST' && req.url().endsWith('/reschedule'));
  await page.getByRole('dialog', { name: 'Редактор задачи', exact: true }).locator('button[type=submit]').click();
  expect((await editorSave).postDataJSON().planned_date).toBe('2026-12-17');
  await expect.poll(async () => (await get(request, 'tasks', task.id)).planned_date).toBe('2026-12-17');
});

test('desktop drag task and session preserves task dates and session interval', async ({ page, request }) => {
  const project = await create(request, 'projects', { title: unique('Перетаскивание') });
  const task = await create(request, 'tasks', { title: unique('Перетащить'), project_id: project.id, planned_date: '2026-12-15', due_date: '2026-12-30' });
  const session = await create(request, 'sessions', { task_id: task.id, date: '2026-12-20', start_time: '10:00', end_time: '11:00' });
  await open(page, 'month', '2026-12-15', '&project=' + project.id);
  await event(page, 'planning', task.id).dragTo(page.locator('.pc-month-day[data-date="2026-12-16"]'), { targetPosition: { x: 10, y: 10 } });
  await expect(event(page, 'planning', task.id)).toHaveAttribute('data-event-date', '2026-12-16');
  const before = await get(request, 'tasks', task.id);
  await event(page, 'session', session.id).dragTo(page.locator('.pc-month-day[data-date="2026-12-21"]'), { targetPosition: { x: 10, y: 10 } });
  await expect(event(page, 'session', session.id)).toHaveAttribute('data-event-date', '2026-12-21');
  expect(await get(request, 'tasks', task.id)).toEqual(before);
  const after = await get(request, 'sessions', session.id);
  expect(after.start_time).toBe('10:00'); expect(after.end_time).toBe('11:00');
  expect(before.due_date).toBe('2026-12-30');
});

test('failed drag leaves event in original place and preserves requested date', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Ошибка переноса'), planned_date: '2026-12-23' });
  await open(page, 'month', '2026-12-23');
  await page.route('**/tasks/' + task.id + '/reschedule', route => route.fulfill({ status: 500, json: { error: { message: 'Не удалось сохранить перенос' } } }));
  await event(page, 'task', task.id).dragTo(page.locator('.pc-month-day[data-date="2026-12-24"]'), { targetPosition: { x: 10, y: 10 } });
  await expect(dialog(page).getByRole('alert')).toContainText('Не удалось сохранить');
  await expect(dialog(page).getByLabel('Новая дата')).toHaveValue('2026-12-24');
  await expect(event(page, 'task', task.id)).toHaveAttribute('data-event-date', '2026-12-23');
  expect((await get(request, 'tasks', task.id)).planned_date).toBe('2026-12-23');
});

test('move conflict requires explicit latest-version review', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Версии'), planned_date: '2026-12-22' });
  await open(page, 'day', '2026-12-22');
  await move(page, 'task', task.id, '2026-12-23');
  await request.post('/api/planner/tasks/' + task.id + '/reschedule', { data: { expected_revision: 1, planned_date: '2026-12-24', scheduled_time: '09:15' } });
  await dialog(page).locator('button[type=submit]').click();
  await expect(dialog(page).getByRole('alert')).toContainText('Запись была изменена на другом устройстве.');
  await expect(dialog(page).getByLabel('Новая дата')).toHaveValue('2026-12-23');
  expect((await get(request, 'tasks', task.id)).planned_date).toBe('2026-12-24');
  await dialog(page).getByRole('button', { name: 'Посмотреть актуальную версию' }).click();
  await expect(dialog(page).getByRole('alert')).toContainText('2026-12-24');
  await dialog(page).getByRole('button', { name: 'Использовать эту версию и сохранить черновик' }).click();
  await dialog(page).locator('button[type=submit]').click();
  await expect(dialog(page)).toHaveCount(0);
  expect((await get(request, 'tasks', task.id)).planned_date).toBe('2026-12-23');
  expect((await get(request, 'tasks', task.id)).scheduled_time).toBe('09:15');
});

test('project and terminal filters, project link and state preserved across planner navigation', async ({ page, request }) => {
  const project = await create(request, 'projects', { title: unique('Фильтрация') });
  const a = await create(request, 'tasks', { title: unique('Готово'), project_id: project.id, planned_date: '2026-12-25' });
  const b = await create(request, 'tasks', { title: unique('Отменено'), project_id: project.id, planned_date: '2026-12-25' });
  const c = await create(request, 'tasks', { title: unique('Другое'), planned_date: '2026-12-25' });
  await request.post('/api/planner/tasks/' + a.id + '/complete', { data: { expected_revision: 1, completed_date: '2026-01-01' } });
  await request.post('/api/planner/tasks/' + b.id + '/cancel', { data: { expected_revision: 1 } });
  await open(page, 'day', '2026-12-25');
  await expect(event(page, 'task', a.id)).toHaveCount(0);
  await page.getByLabel('Завершённые', { exact: true }).check();
  await expect(event(page, 'task', a.id)).toBeVisible();
  await page.getByLabel('Отменённые', { exact: true }).check();
  await expect(event(page, 'task', b.id)).toBeVisible();
  await page.getByLabel('Проект', { exact: true }).selectOption(project.id);
  await expect(event(page, 'task', c.id)).toHaveCount(0);
  await menu(page, 'task', a.id);
  await dialog(page).getByRole('button', { name: 'Перейти к проекту' }).click();
  await expect(page.getByRole('heading', { name: project.title, exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Календарь', exact: true }).click();
  await expect(page.getByLabel('Выбранный день')).toHaveValue('2026-12-25');
  await expect(page.getByLabel('Проект', { exact: true })).toHaveValue(project.id);
  await expect(page.getByRole('button', { name: 'День', exact: true })).toHaveAttribute('aria-pressed', 'true');
});

test('quick create on selected day and complete parent using existing session choices', async ({ page, request }) => {
  const project = await create(request, 'projects', { title: unique('Быстрые действия') });
  await open(page, 'day', '2026-12-18', '&project=' + project.id);
  await page.getByRole('button', { name: '+ Задача на день', exact: true }).click();
  await expect(page.getByLabel('Плановая дата', { exact: true })).toHaveValue('2026-12-18');
  const title = unique('Новая из календаря');
  await page.getByLabel('Название', { exact: true }).fill(title);
  await page.getByRole('dialog').locator('button[type=submit]').click();
  await expect(page.getByRole('dialog', { name: 'Редактор задачи', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Закрыть редактор' }).click();
  const data = await (await request.get('/api/planner/tasks?project_id=' + project.id)).json();
  const parent = data.items.find(t => t.title === title);
  const child = await create(request, 'tasks', { title: unique('Дочерняя'), project_id: project.id, parent_task_id: parent.id });
  const session = await create(request, 'sessions', { task_id: child.id, date: '2029-01-01' });
  await menu(page, 'task', parent.id);
  await dialog(page).getByRole('button', { name: 'Отметить выполненной', exact: true }).click();
  await dialog(page).getByLabel('Открытые подзадачи: 1').selectOption('complete');
  await dialog(page).getByLabel('Фактическая дата выполнения').fill('2026-01-01');
  await dialog(page).getByRole('checkbox').check();
  await dialog(page).locator('button[type=submit]').click();
  await expect(event(page, 'task', parent.id)).toHaveCount(0);
  expect((await get(request, 'tasks', child.id)).status).toBe('completed');
  expect((await get(request, 'sessions', session.id)).status).toBe('cancelled');
});

test('session quick completion leaves its task open', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Период работы дня') });
  const session = await create(request, 'sessions', { task_id: task.id, date: '2026-12-28' });
  await open(page, 'day', '2026-12-28');
  await menu(page, 'session', session.id);
  await dialog(page).getByRole('button', { name: 'Завершить период', exact: true }).click();
  await dialog(page).getByLabel('Фактическая дата выполнения').fill('2026-01-01');
  await dialog(page).locator('button[type=submit]').click();
  await expect(event(page, 'session', session.id)).toHaveCount(0);
  expect((await get(request, 'tasks', task.id)).status).toBe('planned');
});

test('1440px and 390px layouts, responsive defaults, mobile date move and editor', async ({ page, request }, info) => {
  const project = await create(request, 'projects', { title: 'Личные планы · ' + unique('Декабрь') });
  const task = await create(request, 'tasks', { title: 'Подготовить материалы к встрече', project_id: project.id, planned_date: '2026-12-15' });
  await create(request, 'tasks', { title: 'Обсудить следующие шаги', project_id: project.id, planned_date: '2026-12-16', scheduled_time: '11:00' });
  await page.goto('/#/plans/calendar');
  await expect(page.getByRole('button', { name: 'Месяц', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await open(page, 'month', '2026-12-15', '&project=' + project.id);
  await page.screenshot({ path: info.outputPath('calendar-1440-month.png'), fullPage: true });
  await page.getByRole('button', { name: 'Неделя', exact: true }).click();
  await expect(page.locator('.pc-time-column')).toHaveCount(7);
  await page.screenshot({ path: info.outputPath('calendar-1440-week.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/#/plans/calendar');
  await expect(page.getByRole('button', { name: 'Неделя', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await open(page, 'week', '2026-12-15', '&project=' + project.id);
  await expect(page.locator('.pc-week-strip .pc-day-button')).toHaveCount(7);
  await expect(page.locator('.pc-time-column')).toHaveCount(1);
  expect(await event(page, 'task', task.id).getAttribute('draggable')).toBeNull();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.screenshot({ path: info.outputPath('calendar-390-week.png'), fullPage: true });
  await move(page, 'task', task.id, '2026-12-16');
  await dialog(page).locator('button[type=submit]').click();
  await page.locator('.pc-week-strip').getByRole('button', { name: 'ср, 16', exact: true }).click();
  await event(page, 'task', task.id).locator('.pc-event-title').click();
  await expect(page.getByRole('dialog', { name: 'Редактор задачи', exact: true })).toBeVisible();
  expect((await page.getByRole('dialog', { name: 'Редактор задачи', exact: true }).boundingBox()).width).toBeCloseTo(390, 0);
});

test('Moscow today, responsive resize and keyboard mobile week navigation', async ({ page }) => {
  await page.clock.setFixedTime(new Date('2026-12-14T22:30:00Z'));
  await open(page, 'week', '2026-12-15');
  await expect(page.locator('.pc-day-button[aria-current="date"]')).toHaveAttribute('data-day', '2026-12-15');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('.pc-week-strip')).toBeVisible();
  await expect(page.locator('.pc-time-column')).toHaveCount(1);
  await page.locator('.pc-week-strip [data-day="2026-12-15"]').focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('.pc-week-strip [data-day="2026-12-16"]')).toBeFocused();
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('.pc-week-strip [data-day="2026-12-17"]')).toBeFocused();
  await page.getByRole('button', { name: 'Сегодня', exact: true }).click();
  await expect(page.locator('.pc-week-strip [data-day="2026-12-15"]')).toHaveAttribute('aria-pressed', 'true');
});

test('calendar load error can retry, and session transfer can change time without task mutation', async ({ page, request }) => {
  const task = await create(request, 'tasks', { title: unique('Сбой загрузки'), planned_date: '2026-12-01', due_date: '2026-12-31' });
  const session = await create(request, 'sessions', { task_id: task.id, date: '2026-12-02', start_time: '10:00', end_time: '11:00' });
  await page.route('**/api/planner/calendar?*', route => route.fulfill({ status: 503, json: { error: { message: 'Временно недоступно' } } }));
  await page.goto('/#/plans/calendar?mode=day&date=2026-12-02');
  await expect(page.getByRole('heading', { name: 'Не удалось загрузить планы' })).toBeVisible();
  await page.unroute('**/api/planner/calendar?*');
  await page.getByRole('button', { name: 'Попробовать снова' }).click();
  await move(page, 'session', session.id, '2026-12-03');
  await dialog(page).getByLabel('Начало · Москва').fill('13:00');
  await dialog(page).getByLabel('Окончание · Москва').fill('14:00');
  await dialog(page).locator('button[type=submit]').click();
  await expect(event(page, 'session', session.id)).toHaveCount(0);
  expect(await get(request, 'tasks', task.id)).toEqual(task);
  expect((await get(request, 'sessions', session.id)).start_time).toBe('13:00');
});

test('a rejected broad range can be narrowed without editing the URL', async ({ page }) => {
  await page.route('**/api/planner/calendar?*', route => {
    const url = new URL(route.request().url());
    if (url.searchParams.get('from') !== url.searchParams.get('to')) return route.fulfill({ status: 422, json: { error: { message: 'В диапазоне более 5000 событий; выберите меньший период' } } });
    return route.continue();
  });
  await page.goto('/#/plans/calendar?mode=month&date=2026-12-15');
  await expect(page.getByText('В диапазоне более 5000 событий; выберите меньший период')).toBeVisible();
  await page.getByLabel('Выбрать дату', { exact: true }).fill('2026-12-14');
  await page.getByRole('button', { name: 'Открыть выбранный день' }).click();
  await expect(page.locator('.pc-calendar')).toBeVisible();
  await expect(page.getByLabel('Выбранный день', { exact: true })).toHaveValue('2026-12-14');
});
