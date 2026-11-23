const { test, expect } = require('../browser-fixtures.cjs');
const { randomUUID } = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
async function post(request, route, data) {
  const r = await request.post('/api/planner/' + route, { data });
  expect(r.ok(), await r.text()).toBeTruthy(); return r.json();
}
const rule = mode => ({ schema_version: 1, mode, unit: 'days', interval: 45, allowed_days: { mode: 'any' }, adjustment: 'none' });
test.afterAll(() => {
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'data.json'), 'utf8')).toBe('{"months":[]}');
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'categories.json'), 'utf8')).toBe('{"expense":[],"income":[]}');
});
test('future calendar materializes before GET and preserves existing task edits', async ({ page, request }) => {
  await page.route('https://**/*', route => route.abort());
  const project = await post(request, 'projects', { title: 'Recurring calendar ' + randomUUID() });
  const series = await post(request, 'series', { request_id: randomUUID(), task: { title: 'Haircut', project_id: project.id }, first_date: '2026-11-16', rule: rule('schedule') });
  const first = series.occurrences[0].task;
  await post(request, 'tasks/' + first.id + '/reschedule', { expected_revision: first.revision, planned_date: '2027-02-10' });
  const before = await (await request.get('/api/planner/calendar?from=2027-02-01&to=2027-02-28')).json();
  expect(before.items.some(i => i.series_id === series.id && i.cycle_index === 2)).toBeFalsy();
  const requests = [];
  page.on('request', req => { if (/\/api\/planner\/(materialize|calendar)/.test(req.url())) requests.push([req.method(), new URL(req.url()).pathname]); });
  await page.goto('/#/plans/calendar?mode=month&date=2027-02-15&project=' + project.id);
  await expect(page.locator('.pc-calendar')).toBeVisible();
  expect(requests[0]).toEqual(['POST', '/api/planner/materialize']);
  expect(requests.at(-1)).toEqual(['GET', '/api/planner/calendar']);
  let current = await (await request.get('/api/planner/series/' + series.id)).json();
  const second = current.occurrences.find(o => o.cycle_index === 2);
  expect(second.base_date).toBe('2027-02-14');
  await expect(page.locator('[data-event-id="task:' + second.task.id + '"]')).toHaveCount(1);
  expect(current.occurrences[0].task.planned_date).toBe('2027-02-10');
  const ids = current.occurrences.map(o => o.id);
  await page.reload(); await expect(page.locator('.pc-calendar')).toBeVisible();
  current = await (await request.get('/api/planner/series/' + series.id)).json();
  expect(current.occurrences.map(o => o.id)).toEqual(ids);
});
test('existing completion dialog creates exactly one successor via real API', async ({ page, request }) => {
  await page.route('https://**/*', route => route.abort());
  const series = await post(request, 'series', { request_id: randomUUID(), task: { title: 'Completion recurrence ' + randomUUID() }, first_date: '2026-11-16', rule: rule('completion') });
  const first = series.occurrences[0].task;
  await page.goto('/#/plans/calendar?mode=day&date=2026-11-16');
  await expect(page.locator('.pc-calendar')).toBeVisible();
  await page.locator('[data-event-id="task:' + first.id + '"] .pc-event-menu').click();
  await page.locator('dialog').getByRole('button', { name: 'Отметить выполненной', exact: true }).click();
  const dialog = page.locator('dialog');
  await dialog.locator('input[type="date"]').fill('2026-11-16');
  await dialog.locator('button[type="submit"]').click();
  await expect(dialog).toHaveCount(0);
  let current = await (await request.get('/api/planner/series/' + series.id)).json();
  expect(current.occurrences).toHaveLength(2);
  expect(current.occurrences[1].base_date).toBe('2026-12-31');
  const duplicate = await request.post('/api/planner/tasks/' + first.id + '/complete', { data: { expected_revision: first.revision, completed_date: '2026-11-16' } });
  expect(duplicate.status()).toBe(409);
  await post(request, 'materialize', {});
  current = await (await request.get('/api/planner/series/' + series.id)).json();
  expect(current.occurrences).toHaveLength(2);
});
test('materialization bound preserves viewing ordinary tasks in a distant year', async ({ page, request }) => {
  await page.route('https://**/*', route => route.abort());
  const task = await post(request, 'tasks', { title: 'Far future ' + randomUUID(), planned_date: '2099-01-15' });
  await page.goto('/#/plans/calendar?mode=day&date=2099-01-15');
  await expect(page.locator('.pc-calendar')).toBeVisible();
  await expect(page.locator('[data-event-id="task:' + task.id + '"]')).toBeVisible();
  await expect(page.getByRole('alert')).toContainText('двумя годами');
});
