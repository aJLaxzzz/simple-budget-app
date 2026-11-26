(function (P) {
  'use strict';
  // UTC fields are used only as a civil-date calculator, never as device-local time.
  const make = value => { const [y, m, d] = value.split('-').map(Number); const date = new Date(0); date.setUTCFullYear(y, m - 1, d); date.setUTCHours(12, 0, 0, 0); return date; };
  const iso = date => [String(date.getUTCFullYear()).padStart(4, '0'), String(date.getUTCMonth() + 1).padStart(2, '0'), String(date.getUTCDate()).padStart(2, '0')].join('-');
  const C = P.calendarModel = {};
  C.valid = value => /^\d{4}-\d{2}-\d{2}$/.test(value || '') && value.slice(0, 4) !== '0000' && iso(make(value)) === value;
  C.addDays = (value, days) => { const date = make(value); date.setUTCDate(date.getUTCDate() + days); return iso(date); };
  C.addMonths = (value, months) => {
    const date = make(value), day = date.getUTCDate(); date.setUTCDate(1); date.setUTCMonth(date.getUTCMonth() + months);
    const last = new Date(date); last.setUTCMonth(last.getUTCMonth() + 1, 0);
    date.setUTCDate(Math.min(day, last.getUTCDate())); return iso(date);
  };
  C.weekStart = value => C.addDays(value, -((make(value).getUTCDay() + 6) % 7));
  C.days = (from, to) => { const list = []; for (let day = from; day <= to; day = C.addDays(day, 1)) list.push(day); return list; };
  C.range = (mode, value) => {
    if (mode === 'day') return { from: value, to: value };
    if (mode === 'week') { const from = C.weekStart(value); return { from, to: C.addDays(from, 6) }; }
    const first = value.slice(0, 7) + '-01', last = C.addDays(C.addMonths(first, 1), -1);
    return { from: C.weekStart(first), to: C.addDays(C.weekStart(last), 6) };
  };
  C.caption = (value, options) => new Intl.DateTimeFormat('ru-RU', { timeZone: 'UTC', ...options }).format(make(value));
  C.minutes = value => { const [hours, minutes] = value.split(':').map(Number); return hours * 60 + minutes; };
  C.time = minutes => String(Math.floor(minutes / 60)).padStart(2, '0') + ':' + String(minutes % 60).padStart(2, '0');
  C.completed = item => !['cancelled','skipped'].includes(item.status) && !['cancelled','skipped'].includes(item.task_status) && (item.status === 'completed' || item.task_status === 'completed');
  C.completedCount = items => new Set(items.filter(item => C.completed(item) && (item.task_status ? item.task_status === 'completed' : item.type !== 'session')).map(item => item.task_id || item.id)).size;
  C.visible = (item, filters) => item.status !== 'skipped' && item.task_status !== 'skipped' && (!filters.project || item.project_id === filters.project || (filters.project === 'none' && !item.project_id)) && (filters.completed || (item.status !== 'completed' && item.task_status !== 'completed')) && (filters.cancelled || (item.status !== 'cancelled' && item.task_status !== 'cancelled'));
  C.cancelled = item => item.status === 'cancelled' || item.task_status === 'cancelled';
  C.statusRank = item => C.cancelled(item)?2:C.completed(item)?1:0;
  C.compareGroupedEvents = (a,b) => a.date.localeCompare(b.date) || C.statusRank(a)-C.statusRank(b) || C.compareEvents(a,b);
  C.compareEvents = (a,b) => a.date.localeCompare(b.date) || (a.start_time||'').localeCompare(b.start_time||'') || a.title.localeCompare(b.title,'ru') || a.id.localeCompare(b.id);
  C.color = id => { let hash = 0; for (const char of id || 'inbox') hash = (hash * 31 + char.charCodeAt(0)) >>> 0; return hash % 6; };
  // Column packing is presentation only. Tasks have no duration; their 40-minute
  // display footprint is not persisted. Sessions retain their actual interval.
  C.layout = items => {
    const entries = items.map(item => ({ item, start: C.minutes(item.start_time), end: Math.min(1440, Math.max(item.end_time ? C.minutes(item.end_time) : 0, C.minutes(item.start_time) + 40)) })).sort((a, b) => a.start - b.start || b.end - a.end || a.item.id.localeCompare(b.item.id));
    let group = [], ends = [], until = -1;
    const finish = () => { for (const entry of group) entry.columns = ends.length; group = []; ends = []; };
    for (const entry of entries) {
      if (entry.start >= until) finish();
      let column = ends.findIndex(end => end <= entry.start);
      if (column < 0) column = ends.length;
      ends[column] = entry.end; entry.column = column; group.push(entry); until = Math.max(until, entry.end);
    }
    finish(); return entries;
  };
  // Connected overlap components are visual only; an event belongs to exactly one group.
  C.overlapGroups = (items, width) => {
    const limit = Math.max(1, Math.min(3, Math.floor(width / 180)));
    const groups = []; let current;
    for (const entry of C.layout(items)) {
      if (!current || entry.start >= current.end) { current = {entries: [], start: entry.start, end: entry.end}; groups.push(current); }
      current.entries.push(entry); current.end = Math.max(current.end, entry.end);
    }
    return groups.map(group => ({...group, grouped: group.entries.some(entry => entry.columns > limit)}));
  };
})(window.Planner);
