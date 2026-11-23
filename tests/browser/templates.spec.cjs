const { test, expect } = require('../browser-fixtures.cjs');
const { randomUUID } = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const today = new Intl.DateTimeFormat('en-CA', { timeZone: 'Europe/Moscow', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date());
const day = offset => { const d = new Date(today + 'T12:00:00Z'); d.setUTCDate(d.getUTCDate() + offset); return d.toISOString().slice(0, 10); };
async function post(request, route, data, status = 200) {
  const response = await request.post('/api/planner/' + route, { data });
  expect(response.status(), await response.text()).toBe(status); return response.json();
}
const rule = { schema_version: 1, mode: 'schedule', unit: 'days', interval: 14, allowed_days: { mode: 'any' }, adjustment: 'none' };
const snapshot = () => ({ title: 'Уборка ' + randomUUID(), description: 'Шаблон', tasks: [
  { key: 'root', title: 'Комнаты', planned_offset: 0, due_offset: 2 },
  { key: 'child', parent_key: 'root', title: 'Кухня', planned_offset: 1 },
  { key: 'undated', title: 'Без даты' }
] });
async function seriesCommand(request, id, body) {
  const current = await (await request.get('/api/planner/series/' + id)).json();
  const input = { ...body, expected_revision: current.revision };
  const preview = await post(request, `series/${id}/preview`, input);
  return post(request, `series/${id}/${body.action.replaceAll('_', '-')}`, { ...input, request_id: randomUUID(), preview_token: preview.preview_token });
}
test.afterAll(() => {
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'data.json'), 'utf8')).toBe('{"months":[]}');
  expect(fs.readFileSync(path.join(process.env.PLANNER_TEST_DIR, 'categories.json'), 'utf8')).toBe('{"expense":[],"income":[]}');
});
test('project template produces independent projects and real dated tasks in the existing calendar', async ({ page, request }) => {
  await page.route('https://**/*', route => route.abort());
  const { template } = await post(request, 'templates', { request_id: randomUUID(), snapshot: snapshot() }, 201);
  const v = await post(request, 'series', { request_id: randomUUID(), source_type: 'project', template_id: template.id, expected_revision: template.revision, first_date: today, rule }, 201);
  expect(v.occurrences.length).toBeGreaterThan(1);
  const first = v.occurrences[0].project, second = v.occurrences[1].project;
  expect(first.id).not.toBe(second.id);
  const root = first.tasks.find(t => t.title === 'Комнаты'), child = first.tasks.find(t => t.title === 'Кухня');
  expect(child.parent_task_id).toBe(root.id);
  expect(child.planned_date).toBe(day(1));
  await page.goto(`/#/plans/calendar?mode=day&date=${today}&project=${first.id}`);
  await expect(page.locator(`[data-event-id="task:${root.id}"]`)).toHaveCount(1);
  const cal = await (await request.get(`/api/planner/calendar?from=${today}&to=${day(3)}`)).json();
  expect(cal.items.filter(x => x.project_id === first.id).some(x => x.title === 'Без даты')).toBeFalsy();
  await post(request, `tasks/${child.id}/reschedule`, { expected_revision: child.revision, planned_date: day(8) });
  const freshTemplate = await (await request.get(`/api/planner/templates/${template.id}`)).json();
  expect(freshTemplate.snapshot.tasks.find(t => t.key === 'child').planned_offset).toBe(1);
  const other = await (await request.get(`/api/planner/projects/${second.id}`)).json();
  expect(other.tasks.find(t => t.title === 'Кухня').planned_date).toBe(day(15));
});
test('project pause, restoration, template version preview and command replay through protected API', async ({ request }) => {
  const { template } = await post(request, 'templates', { request_id: randomUUID(), snapshot: snapshot() }, 201);
  const v = await post(request, 'series', { request_id: randomUUID(), source_type: 'project', template_id: template.id, expected_revision: template.revision, first_date: today, rule }, 201);
  await seriesCommand(request, v.id, { action: 'pause', start_date: day(14), end_date: day(28) });
  let current = await (await request.get(`/api/planner/series/${v.id}`)).json();
  const paused = current.occurrences[1];
  expect(paused.project.status).toBe('skipped');
  const input = { request_id: randomUUID(), expected_revision: paused.revision, expected_project_revision: paused.project.revision, expected_series_revision: current.revision, tasks_decision: 'restore_closed', related_revisions: Object.fromEntries(paused.project.tasks.map(t => [t.id, t.revision])) };
  const restored = await post(request, `occurrences/${paused.id}/restore`, input);
  const replay = await post(request, `occurrences/${paused.id}/restore`, input);
  expect(replay).toEqual(restored); expect(restored.occurrence.project.id).toBe(paused.project.id);
  const incidents = await (await request.get(`/api/planner/incidents?series_id=${v.id}&kind=skipped`)).json();
  expect(incidents.items).toHaveLength(2);
  expect(incidents.items.find(i => i.occurrence_id === paused.id).resolution).toBe('restored');
  const updated = structuredClone(template.snapshot); updated.tasks.push({ key: 'extra', title: 'Окна', planned_offset: 2 });
  const next = await post(request, `templates/${template.id}/versions`, { request_id: randomUUID(), expected_revision: template.revision, snapshot: updated }, 201);
  current = await (await request.get(`/api/planner/series/${v.id}`)).json();
  const preview = await post(request, `series/${v.id}/preview`, { action: 'change_template', expected_revision: current.revision, template_version_id: next.template.version_id, from_cycle: 0 });
  expect(preview.rows.find(row => row.occurrence_id === paused.id).disposition).toBe('manual_exception');
  expect(preview.proposed_template.tasks).toHaveLength(4);
});
