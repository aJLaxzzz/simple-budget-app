package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func previewInput() recurrence.PreviewRequest {
	return recurrence.PreviewRequest{Rule: recurrence.Rule{SchemaVersion: 1, Mode: recurrence.Schedule, Unit: recurrence.Days, Interval: 45, AllowedDays: recurrence.AllowedDays{Mode: recurrence.AnyDay}, Adjustment: recurrence.NoAdjustment}, Anchor: recurrence.Anchor{Date: "2026-01-01", Kind: recurrence.ScheduleAnchor}, Count: 5}
}
func syntheticRecurrenceYear(year int) CalendarYear {
	data := CalendarYear{Year: year, Source: "SYNTHETIC recurrence test fixture — NOT official RF data"}
	for day := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC); day.Year() == year; day = day.AddDate(0, 0, 1) {
		date := Date(day.Format("2006-01-02"))
		kind := "working"
		reason := "synthetic weekday"
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			kind = "nonworking"
			reason = "synthetic weekend"
		}
		if date == "2026-11-16" || date == "2026-12-31" {
			kind = "nonworking"
			reason = "SYNTHETIC state holiday for tests only"
		}
		if date == "2026-11-21" {
			kind = "shortened"
			reason = "SYNTHETIC transferred working Saturday"
		}
		data.Days = append(data.Days, CalendarDay{Date: date, Kind: kind, Reason: reason})
	}
	return data
}

// Compare all persisted columns, including revisions, settings, calendars and
// history. Counts alone would miss updates to existing records.
func recurrenceDBSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	snapshot := []any{}
	e := s.Transaction(ctx, func(tx *sql.Tx) error {
		rows, e := tx.Query("SELECT name,sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
		if e != nil {
			return e
		}
		names := []string{}
		for rows.Next() {
			var name, definition string
			if e = rows.Scan(&name, &definition); e != nil {
				rows.Close()
				return e
			}
			names = append(names, name)
			snapshot = append(snapshot, []string{name, definition})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, name := range names {
			rows, e := tx.Query(`SELECT * FROM "` + strings.ReplaceAll(name, `"`, `""`) + `" ORDER BY rowid`)
			if e != nil {
				return e
			}
			columns, e := rows.Columns()
			if e != nil {
				rows.Close()
				return e
			}
			for rows.Next() {
				cells := make([]any, len(columns))
				dest := make([]any, len(columns))
				for i := range cells {
					dest[i] = &cells[i]
				}
				if e = rows.Scan(dest...); e != nil {
					rows.Close()
					return e
				}
				raw, e := json.Marshal(cells)
				if e != nil {
					rows.Close()
					return e
				}
				snapshot = append(snapshot, json.RawMessage(raw))
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(snapshot)
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}
func TestRecurrencePreviewAPIReadOnlyAndProductionCalendar(t *testing.T) {
	store := testStore(t)
	h := NewHandler(NewService(store))
	task(t, h, map[string]any{"title": "Existing task", "planned_date": "2026-11-16"})
	year := syntheticRecurrenceYear(2026)
	if e := store.ImportCalendarYear(ctx, year); e != nil {
		t.Fatal(e)
	}
	snapshot := recurrenceDBSnapshot(t, store)
	var changes int64
	if e := store.db.QueryRow("SELECT total_changes()").Scan(&changes); e != nil {
		t.Fatal(e)
	}
	in := previewInput()
	result := value[recurrence.PreviewResult](t, apiCall(t, h, "POST", "/recurrence/preview", in, 200))
	want := []recurrence.Date{"2026-02-15", "2026-04-01", "2026-05-16", "2026-06-30", "2026-08-14"}
	if len(result.Items) != len(want) {
		t.Fatal(result)
	}
	for i, c := range result.Items {
		if c.BaseDate != want[i] || c.CycleIndex != int64(i+1) {
			t.Fatal(result)
		}
	}
	if result.Rule.Timezone != "Europe/Moscow" || len(result.Calendars) != 0 {
		t.Fatal(result)
	}
	again := value[recurrence.PreviewResult](t, apiCall(t, h, "POST", "/recurrence/preview", in, 200))
	if !reflect.DeepEqual(result, again) {
		t.Fatal("nondeterministic preview")
	}
	in.Rule.Interval = 1
	in.Rule.AllowedDays.Mode = recurrence.RussianWorkdays
	in.Rule.Adjustment = recurrence.Next
	in.Anchor.Date = "2026-11-15"
	in.Count = 1
	result = value[recurrence.PreviewResult](t, apiCall(t, h, "POST", "/recurrence/preview", in, 200))
	if result.Items[0].AdjustedDate != "2026-11-17" || len(result.Calendars) != 1 || result.Calendars[0].Source != year.Source || len(result.Calendars[0].Checksum) != 64 || result.Calendars[0].Revision != 1 {
		t.Fatal(result)
	}
	in.Anchor.Date = "2026-11-20"
	result = value[recurrence.PreviewResult](t, apiCall(t, h, "POST", "/recurrence/preview", in, 200))
	if result.Items[0].AdjustedDate != "2026-11-21" {
		t.Fatal("working Saturday ignored", result)
	}
	in.Anchor.Date = "2026-12-30"
	missing := apiCall(t, h, "POST", "/recurrence/preview", in, 422)
	if !strings.Contains(missing.Body.String(), `"code":"production_calendar_unavailable"`) || !strings.Contains(missing.Body.String(), `"year":2027`) {
		t.Fatal(missing.Body.String())
	}
	// An unconfirmed completion has no dates to evaluate, even with RF days.
	in.Rule.Mode = recurrence.Completion
	in.Anchor = recurrence.Anchor{Kind: recurrence.UnconfirmedAnchor}
	in.Count = 10
	result = value[recurrence.PreviewResult](t, apiCall(t, h, "POST", "/recurrence/preview", in, 200))
	if result.Status != "needs_anchor" || len(result.Items) != 0 {
		t.Fatal(result)
	}
	in.Rule.AllowedDays.Mode = recurrence.AnyDay
	in.Rule.Adjustment = recurrence.NoAdjustment
	in.Anchor = recurrence.Anchor{Date: "2026-11-16", Kind: recurrence.CompletedAnchor, CycleIndex: 8}
	result = value[recurrence.PreviewResult](t, apiCall(t, h, "POST", "/recurrence/preview", in, 200))
	if len(result.Items) != 1 || result.Items[0].CycleIndex != 9 || result.Status != "awaiting_completion" {
		t.Fatal(result)
	}
	if recurrenceDBSnapshot(t, store) != snapshot {
		t.Fatal("preview changed SQLite")
	}
	var after int64
	if e := store.db.QueryRow("SELECT total_changes()").Scan(&after); e != nil {
		t.Fatal(e)
	}
	if changes != after {
		t.Fatal("preview attempted a write")
	}
}
func TestRecurrenceMissingAndIncompleteCalendarAndUpdatedSnapshot(t *testing.T) {
	store := testStore(t)
	h := NewHandler(NewService(store))
	in := previewInput()
	in.Rule.Interval = 1
	in.Rule.AllowedDays.Mode = recurrence.RussianWorkdays
	in.Rule.Adjustment = recurrence.Next
	in.Anchor.Date = "2026-11-15"
	in.Count = 1
	apiCall(t, h, "POST", "/recurrence/preview", in, 422)
	year := syntheticRecurrenceYear(2026)
	if e := store.ImportCalendarYear(ctx, year); e != nil {
		t.Fatal(e)
	}
	first := value[recurrence.PreviewResult](t, apiCall(t, h, "POST", "/recurrence/preview", in, 200))
	for i := range year.Days {
		if year.Days[i].Date == "2026-11-16" {
			year.Days[i].Kind = "working"
			year.Days[i].Reason = "SYNTHETIC corrected calendar fixture"
		}
	}
	if e := store.ImportCalendarYear(ctx, year); e != nil {
		t.Fatal(e)
	}
	second := value[recurrence.PreviewResult](t, apiCall(t, h, "POST", "/recurrence/preview", in, 200))
	if first.Items[0].AdjustedDate == second.Items[0].AdjustedDate || second.Calendars[0].Revision != 2 || second.Calendars[0].Checksum == first.Calendars[0].Checksum {
		t.Fatal(first, second)
	}
	exec(t, store, "DELETE FROM production_calendar_days WHERE date='2026-05-04'")
	w := apiCall(t, h, "POST", "/recurrence/preview", in, 422)
	if !strings.Contains(w.Body.String(), `"year":2026`) {
		t.Fatal(w.Body.String())
	}
}
func TestRecurrenceHTTPValidation(t *testing.T) {
	h := NewHandler(NewService(testStore(t)))
	apiCall(t, h, "GET", "/recurrence/preview", nil, 405)
	apiCall(t, h, "POST", "/recurrence/preview/unknown", previewInput(), 404)
	raw, _ := json.Marshal(previewInput())
	tests := []struct {
		body   string
		status int
	}{
		{`null`, 400}, {`[]`, 400}, {`{`, 400}, {string(raw) + ` {}`, 400},
		{strings.Replace(string(raw), `"interval":45`, `"interval":1.5`, 1), 400},
		{strings.Replace(string(raw), `"interval":45`, `"interval":"45"`, 1), 400},
		{strings.Replace(string(raw), `"interval":45`, `"interval":0`, 1), 422},
		{strings.Replace(string(raw), `"interval":45`, `"interval":-45`, 1), 422},
		{strings.Replace(string(raw), `"interval":45`, `"interval":9223372036854775808`, 1), 400},
		{strings.Replace(string(raw), `"schema_version":1`, `"schema_version":99`, 1), 422},
		{strings.Replace(string(raw), `"count":5`, `"count":51`, 1), 422},
		{strings.Replace(string(raw), `"kind":"schedule"`, `"kind":"completion"`, 1), 422},
		{strings.Replace(string(raw), `"interval":45`, `"interval":45,"manual_date":"2026-02-01"`, 1), 400},
		{strings.Replace(string(raw), `"mode":"any"`, `"mode":"any","holiday_override":true`, 1), 400},
		{strings.Replace(string(raw), `"cycle_index":0`, `"cycle_index":0,"completed_at":"2026-01-01T01:00:00Z"`, 1), 400},
		{strings.Replace(string(raw), `"count":5`, `"count":5,"revision":2`, 1), 400},
		{`{"padding":"` + strings.Repeat("x", 129<<10) + `"}`, 400},
	}
	for i, tc := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/api/planner/recurrence/preview", strings.NewReader(tc.body)))
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"error"`) {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
