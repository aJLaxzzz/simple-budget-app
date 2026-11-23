const { test, expect } = require('../browser-fixtures.cjs');
const { randomUUID } = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const firstDate = new Intl.DateTimeFormat('en-CA', { timeZone: 'Europe/Moscow', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date());
function dateAfter(days) {
  const date = new Date(firstDate + 'T12:00:00Z');
  date.setUTCDate(date.getUTCDate() + days);
  return date.toISOString().slice(0, 10);
}
async function post(request, route, data, status = 200) {
  const r = await request.post('/api/planner/' + route, { data });
  expect(r.status(), await r.text()).toBe(status); return r.json();
}
const getSeries = async (request, id) => (await request.get('/api/planner/series/' + id)).json();
async function command(request, id, data) {
  const v = await getSeries(request, id);
  const input = { ...data, expected_revision: v.revision };
  const preview = await post(request, 'series/' + id + '/preview', input);
  expect(preview.calendar_errors).toEqual([]);
  return post(request, 'series/' + id + '/' + data.action.replaceAll('_', '-'), { ...input, preview_token: preview.preview_token, request_id: randomUUID() });
}
async function occurrence(request, seriesID, id, action, data = {}) {
  const v = await getSeries(request, seriesID), o = v.occurrences.find(o => o.id === id);
  return post(request, 'occurrences/' + id + '/' + action, { request_id: randomUUID(), expected_revision: o.revision, expected_task_revision: o.task.revision, expected_series_revision: v.revision, ...data });
}
const rule = { schema_version: 1, mode: 'schedule', unit: 'days', interval: 7, allowed_days: { mode: 'any' }, adjustment: 'none' };
test.afterAll(() => {
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'data.json'), 'utf8')).toBe('{"months":[]}');
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'categories.json'), 'utf8')).toBe('{"expense":[],"income":[]}');
});
test('pause hides skipped work, review retains history, restoration reuses calendar task ID', async ({ page, request }) => {
  await page.route('https://**/*', route => route.abort());
  const project = await post(request, 'projects', { title: 'Pause integration ' + randomUUID() }, 201);
  const v = await post(request, 'series', { request_id: randomUUID(), task: { title: 'Paused task', project_id: project.id }, first_date: firstDate, rule }, 201);
  const o = v.occurrences[1];
  await command(request, v.id, { action: 'pause', start_date: dateAfter(7), end_date: dateAfter(14), reason: 'Integration pause' });
  await page.goto('/#/plans/calendar?mode=day&date=' + dateAfter(7) + '&project=' + project.id);
  await expect(page.locator('.pc-calendar')).toBeVisible();
  await expect(page.locator('[data-event-id="task:' + o.task.id + '"]')).toHaveCount(0);
  let facts = await (await request.get('/api/planner/incidents?series_id=' + v.id + '&disposition=needs_decision')).json();
  expect(facts.items).toHaveLength(2);
  await occurrence(request, v.id, o.id, 'review');
  facts = await (await request.get('/api/planner/incidents?series_id=' + v.id + '&kind=skipped')).json();
  expect(facts.items).toHaveLength(2);
  expect(facts.items.find(i => i.occurrence_id === o.id).resolution).toBe('acknowledged');
  const restored = await occurrence(request, v.id, o.id, 'reschedule', { planned_date: dateAfter(8) });
  expect(restored.occurrence.task.id).toBe(o.task.id);
  await page.goto('/#/plans/calendar?mode=day&date=' + dateAfter(8) + '&project=' + project.id);
  await expect(page.locator('[data-event-id="task:' + o.task.id + '"]')).toHaveCount(1);
  const history = await (await request.get('/api/planner/history?object_type=occurrence&object_id=' + o.id)).json();
  expect(history.items.some(e => e.action === 'incident_registered')).toBeTruthy();
});
test('rule preview preserves a manual date and rejects an intervening edit', async ({ request }) => {
  const v = await post(request, 'series', { request_id: randomUUID(), task: { title: 'Version integration' }, first_date: firstDate, rule }, 201);
  await occurrence(request, v.id, v.occurrences[1].id, 'reschedule', { planned_date: dateAfter(39) });
  let current = await getSeries(request, v.id);
  let input = { action: 'change_rule', expected_revision: current.revision, rule: { ...rule, interval: 14 }, from_cycle: 1, first_date: dateAfter(8) };
  let preview = await post(request, 'series/' + v.id + '/preview', input);
  expect(preview.rows.find(r => r.occurrence_id === v.occurrences[1].id).disposition).toBe('manual_exception');
  const edited = current.occurrences[2].task;
  const edit = await request.patch('/api/planner/tasks/' + edited.id, { data: { expected_revision: edited.revision, description: 'Keep user content' } });
  expect(edit.ok()).toBeTruthy();
  await post(request, 'series/' + v.id + '/change-rule', { ...input, preview_token: preview.preview_token, request_id: randomUUID() }, 409);
  preview = await post(request, 'series/' + v.id + '/preview', input);
  const body = { ...input, preview_token: preview.preview_token, request_id: randomUUID() };
  const result = await post(request, 'series/' + v.id + '/change-rule', body);
  const replay = await post(request, 'series/' + v.id + '/change-rule', body);
  expect(replay).toEqual(result);
  current = await getSeries(request, v.id);
  expect(current.occurrences[1].task.planned_date).toBe(dateAfter(39));
  expect(current.occurrences[2].task.description).toBe('Keep user content');
  expect(current.occurrences[2].id).toBe(v.occurrences[2].id);
  expect(current.versions).toHaveLength(2);
});
