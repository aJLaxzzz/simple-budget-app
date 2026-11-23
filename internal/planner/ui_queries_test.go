package planner

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestCalendarImportVerifiedPreviewReplayAndRestore(t *testing.T) {
	st := testStore(t)
	svc := NewService(st)
	h := NewHandler(svc)
	doc := value[CalendarDocument](t, apiCall(t, h, "GET", "/production-calendar/bundled/2026", nil, 200))
	year, _, e := validateCalendarDocument(doc)
	if e != nil {
		t.Fatal(e)
	}
	working := 0
	for _, d := range year.Days {
		if d.Kind != "nonworking" {
			working++
		}
	}
	if working != 247 {
		t.Fatalf("working days %d", working)
	}
	preview := value[struct {
		Token    string `json:"preview_token"`
		Revision int64  `json:"expected_revision"`
	}](t, apiCall(t, h, "POST", "/production-calendar/preview", CalendarImportInput{Calendar: doc}, 200))
	if count(t, st, "production_calendar_years") != 0 {
		t.Fatal("preview mutated store")
	}
	in := CalendarImportInput{Calendar: doc, SourceConfirmed: true, ExpectedRevision: preview.Revision, PreviewToken: preview.Token, RequestID: "verified-import"}
	apiCall(t, h, "POST", "/production-calendar/import", in, 200)
	apiCall(t, h, "POST", "/production-calendar/import", in, 200)
	if count(t, st, "calendar_imports") != 1 {
		t.Fatal("duplicate import")
	}
	in.RequestID = "stale-import"
	apiCall(t, h, "POST", "/production-calendar/import", in, 409)
	for _, date := range []Date{"2026-01-09", "2026-03-09", "2026-05-11", "2026-12-31"} {
		v, e := st.IsWorkingDay(ctx, date, RussianProductionCalendar)
		if e != nil || v {
			t.Fatalf("%s %v %v", date, v, e)
		}
	}
	if _, e = st.IsWorkingDay(ctx, "2027-01-01", RussianProductionCalendar); e == nil {
		t.Fatal("missing year fallback")
	}
	bad := doc
	bad.Days = bad.Days[:364]
	apiCall(t, h, "POST", "/production-calendar/preview", CalendarImportInput{Calendar: bad}, 422)
	bad = doc
	bad.Checksum = "bad"
	apiCall(t, h, "POST", "/production-calendar/preview", CalendarImportInput{Calendar: bad}, 422)
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if e = st.Export(ctx, path); e != nil {
		t.Fatal(e)
	}
	if e = st.Restore(ctx, path); e != nil {
		t.Fatal(e)
	}
	if count(t, st, "calendar_imports") != 1 {
		t.Fatal("import provenance lost")
	}
}
func TestAttentionMoscowOverdueAndHistoricalIncident(t *testing.T) {
	st := testStore(t)
	svc := NewService(st)
	svc.now = func() time.Time { return time.Date(2026, 11, 17, 22, 0, 0, 0, time.UTC) }
	h := NewHandler(svc)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Yesterday", "planned_date": "2026-11-17"}, 201)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Today", "planned_date": "2026-11-18"}, 201)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Undated"}, 201)
	result := value[struct {
		Count int              `json:"count"`
		Items []map[string]any `json:"items"`
	}](t, apiCall(t, h, "GET", "/attention", nil, 200))
	if result.Count != 1 || len(result.Items) != 1 || result.Items[0]["title"] != "Yesterday" {
		t.Fatalf("attention %+v", result)
	}
	apiCall(t, h, "GET", "/attention?tab=bad", nil, 422)
	apiCall(t, h, "GET", "/attention?limit=101", nil, 422)
	// Creation and summaries retain compatibility with existing series tests.
	v := createSeries(t, h, CreateSeriesInput{RequestID: "attention-series", Task: &TaskTemplate{Title: "Recurring", Priority: "normal"}, FirstDate: "2026-11-18", Rule: seriesInput("unused", "2026-11-18", "schedule", 45).Rule})
	o := v.Occurrences[0]
	c := value[CommandResult](t, apiCall(t, h, "GET", "/occurrences/"+o.ID, nil, 200))
	in := OccurrenceCommand{RequestID: "skip-attention", ExpectedRevision: c.Occurrence.Revision, ExpectedTaskRevision: c.Occurrence.Task.Revision, ExpectedSeriesRevision: c.Series.Revision}
	apiCall(t, h, "POST", "/occurrences/"+o.ID+"/skip", in, 200)
	raw := apiCall(t, h, "GET", "/attention?tab=skipped", nil, 200)
	var items struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(raw.Body.Bytes(), &items)
	if len(items.Items) != 1 {
		t.Fatal(raw.Body.String())
	}
	c = value[CommandResult](t, apiCall(t, h, "GET", "/occurrences/"+o.ID, nil, 200))
	in = OccurrenceCommand{RequestID: "review-attention", ExpectedRevision: c.Occurrence.Revision, ExpectedTaskRevision: c.Occurrence.Task.Revision, ExpectedSeriesRevision: c.Series.Revision}
	apiCall(t, h, "POST", "/occurrences/"+o.ID+"/review", in, 200)
	raw = apiCall(t, h, "GET", "/attention?tab=processed", nil, 200)
	json.Unmarshal(raw.Body.Bytes(), &items)
	if len(items.Items) != 1 {
		t.Fatal(raw.Body.String())
	}
	raw = apiCall(t, h, "GET", "/attention?tab=skipped", nil, 200)
	json.Unmarshal(raw.Body.Bytes(), &items)
	if len(items.Items) != 1 {
		t.Fatal("review erased skipped")
	}
}

func TestSeriesEndDateAndNestedProjectGuard(t *testing.T) {
	_, svc, h := seriesFixture(t)
	in := seriesInput("limited", "2026-11-16", "schedule", 1)
	end := "2026-11-18"
	in.EndDate = &end
	v := createSeries(t, h, in)
	if len(v.Occurrences) != 3 {
		t.Fatalf("end boundary: %d", len(v.Occurrences))
	}
	drainSeries(t, svc, MaterializeInput{SeriesID: v.ID, Through: "2027-01-01"})
	v, e := svc.Series(ctx, v.ID, 100, 0)
	if e != nil || len(v.Occurrences) != 3 {
		t.Fatalf("extension exceeded end: %v %+v", e, v)
	}
	p := value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": "Independent series"}, 201))
	nested := seriesInput("nested-end", "2026-11-16", "schedule", 14)
	nested.Task.ProjectID = &p.ID
	createSeries(t, h, nested)
	apiCall(t, h, "POST", "/projects/"+p.ID+"/complete", map[string]any{"expected_revision": p.Revision}, 409)
	apiCall(t, h, "POST", "/projects/"+p.ID+"/archive", map[string]any{"expected_revision": p.Revision}, 409)
}

func TestSeriesCreationPreviewUsesFirstCycleWithoutWrites(t *testing.T) {
	st, _, h := seriesFixture(t)
	before := count(t, st, "tasks")
	in := seriesInput("preview", "2026-11-21", "completion", 45)
	preview := value[struct {
		Items []struct {
			Date string `json:"adjusted_date"`
		} `json:"items"`
		Status string `json:"status"`
	}](t, apiCall(t, h, "POST", "/series/preview", SeriesCreationPreview{Rule: in.Rule, FirstDate: in.FirstDate, Count: 6}, 200))
	if len(preview.Items) != 1 || preview.Items[0].Date != "2026-11-21" || preview.Status != "awaiting_completion" {
		t.Fatalf("preview: %+v", preview)
	}
	if count(t, st, "tasks") != before || count(t, st, "series") != 0 || count(t, st, "history_events") != 0 {
		t.Fatal("preview wrote data")
	}
	apiCall(t, h, "POST", "/series/preview", map[string]any{"rule": in.Rule, "first_date": in.FirstDate, "count": 11}, 422)
}
func TestCalendarImportRollbackAndOfficialMetadataValidation(t *testing.T) {
	st := testStore(t)
	svc := NewService(st)
	h := NewHandler(svc)
	var doc CalendarDocument
	if e := json.Unmarshal(verified2026, &doc); e != nil {
		t.Fatal(e)
	}
	bad := doc
	bad.Source = "https://government.ru.evil.test/calendar"
	apiCall(t, h, "POST", "/production-calendar/preview", CalendarImportInput{Calendar: bad}, 422)
	bad = doc
	bad.PublishedAt = "2026-02-30"
	apiCall(t, h, "POST", "/production-calendar/preview", CalendarImportInput{Calendar: bad}, 422)
	p := value[struct {
		Token string `json:"preview_token"`
	}](t, apiCall(t, h, "POST", "/production-calendar/preview", CalendarImportInput{Calendar: doc}, 200))
	in := CalendarImportInput{Calendar: doc, SourceConfirmed: true, PreviewToken: p.Token, RequestID: "rollback-import"}
	exec(t, st, `CREATE TRIGGER reject_calendar_audit BEFORE INSERT ON calendar_imports BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if _, e := svc.ImportProductionCalendar(ctx, in); e == nil {
		t.Fatal("expected failure")
	}
	if count(t, st, "production_calendar_years") != 0 || count(t, st, "production_calendar_days") != 0 || count(t, st, "planner_commands") != 0 {
		t.Fatal("partially imported year")
	}
}

func TestAttentionSnoozeRevisionReplayAndMoscowWakeup(t *testing.T) {
	st, svc, h := seriesFixture(t)
	task := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Напомнить позже", "planned_date": "2026-11-15"}, 201))
	settings := value[AttentionSettings](t, apiCall(t, h, "GET", "/attention/"+task.ID+"?type=task", nil, 200))
	if settings.Revision != 0 {
		t.Fatal(settings)
	}
	in := SnoozeInput{Type: "task", ExpectedRevision: 0, Until: "2026-11-17", RequestID: "snooze-task"}
	apiCall(t, h, "POST", "/attention/"+task.ID, in, 200)
	apiCall(t, h, "POST", "/attention/"+task.ID, in, 200)
	if count(t, st, "attention_state") != 1 {
		t.Fatal("duplicate state")
	}
	result := value[struct {
		Count int `json:"count"`
	}](t, apiCall(t, h, "GET", "/attention", nil, 200))
	if result.Count != 0 {
		t.Fatal("snoozed still counted")
	}
	in.RequestID = "stale-snooze"
	apiCall(t, h, "POST", "/attention/"+task.ID, in, 409)
	svc.now = func() time.Time { return time.Date(2026, 11, 16, 21, 0, 0, 0, time.UTC) }
	result = value[struct {
		Count int `json:"count"`
	}](t, apiCall(t, h, "GET", "/attention", nil, 200))
	if result.Count != 1 {
		t.Fatal("did not wake at Moscow midnight")
	}
	latest := value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+task.ID, nil, 200))
	if latest.Status != "planned" || latest.Revision != task.Revision {
		t.Fatal("snooze changed task")
	}
	settings = value[AttentionSettings](t, apiCall(t, h, "GET", "/attention/"+task.ID+"?type=task", nil, 200))
	in.ExpectedRevision = settings.Revision
	in.Until = "2026-11-18"
	in.RequestID = "rollback-snooze"
	exec(t, st, `CREATE TRIGGER reject_snooze_history BEFORE INSERT ON history_events BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if _, e := svc.SnoozeAttention(ctx, task.ID, in); e == nil {
		t.Fatal("expected history failure")
	}
	after := value[AttentionSettings](t, apiCall(t, h, "GET", "/attention/"+task.ID+"?type=task", nil, 200))
	if after.Revision != settings.Revision || *after.SnoozedUntil != *settings.SnoozedUntil {
		t.Fatal("partial snooze")
	}
}
