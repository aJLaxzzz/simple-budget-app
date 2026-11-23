package planner

import (
	"budget/internal/planner/recurrence"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandedCatalogIntegrityCompatibilityAndRestore(t *testing.T) {
	st, _, h := seriesFixture(t)
	icons := value[[]IconDTO](t, apiCall(t, h, "GET", "/icons", nil, 200))
	if len(icons) < 250 {
		t.Fatal(len(icons))
	}
	seen := map[string]bool{}
	categories := map[string]bool{}
	for _, i := range icons {
		if seen[i.ID] || len(i.Synonyms) == 0 || i.Name == "" {
			t.Fatal(i)
		}
		seen[i.ID] = true
		categories[i.Category] = true
		data, e := builtinIcons.ReadFile("icons/" + strings.TrimPrefix(i.ID, "lucide:") + ".svg")
		if e != nil {
			t.Fatal(e)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != i.Checksum {
			t.Fatal(i.ID)
		}
		apiCall(t, h, "GET", "/icons/"+i.ID+"/image", nil, 200)
	}
	if len(categories) != 20 {
		t.Fatal(categories)
	}
	// Migration 008 remains the source of truth for all legacy references/bytes.
	raw := appearanceSQL
	var e error
	legacy := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "INSERT INTO planner_icons") {
			continue
		}
		parts := strings.Split(line, "'")
		id, sum := parts[1], parts[11]
		entry, ok := iconCatalog[id]
		if !ok || entry.Checksum != sum {
			t.Fatalf("legacy changed: %s %s", id, sum)
		}
		legacy++
	}
	if legacy != 66 {
		t.Fatal(legacy)
	}
	// Restore of a stage-4 catalog re-seeds missing builtins without changing tasks.
	exec(t, st, `DELETE FROM planner_icons WHERE id='lucide:broom'`)
	snapshot := filepath.Join(t.TempDir(), "before.db")
	if e = st.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e = st.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	task(t, h, map[string]any{"title": "Clean", "icon_id": "lucide:broom"})
	if e = st.Transaction(ctx, func(tx *sql.Tx) error { return seedBuiltinIcons(ctx, tx) }); e != nil {
		t.Fatal(e)
	}
	if count(t, st, "planner_icons") != len(iconCatalog) {
		t.Fatal("duplicate seed")
	}
}

func TestCalendarDisplayImportedFactsAndMissingYear(t *testing.T) {
	st, s, h := seriesFixture(t)
	before := count(t, st, "history_events")
	missing := projection(t, h, "2026-12-28", "2027-01-03")
	if len(missing.CalendarDays) != 0 || len(missing.CalendarMissingYears) != 2 {
		t.Fatal(missing)
	}
	if count(t, st, "production_calendar_years") != 0 || count(t, st, "history_events") != before {
		t.Fatal("GET wrote data")
	}
	importBundled(t, s, verified2026, "display26")
	importBundled(t, s, verified2027, "display27")
	expected := map[string]string{"2026-12-31": "transferred_day_off", "2027-01-01": "holiday", "2027-02-20": "working_weekend", "2027-02-21": "weekend", "2027-02-22": "transferred_day_off", "2027-02-23": "holiday", "2027-02-24": "working", "2027-11-05": "transferred_day_off", "2027-12-31": "transferred_day_off"}
	for date, kind := range expected {
		p := projection(t, h, date, date)
		if len(p.CalendarMissingYears) != 0 || len(p.CalendarDays) != 1 || p.CalendarDays[0].Kind != kind || p.CalendarDays[0].Reason == "" {
			t.Fatalf("%s %+v", date, p)
		}
		if date == "2027-02-20" && !p.CalendarDays[0].Shortened {
			t.Fatal(p)
		}
	}
	tsk := task(t, h, map[string]any{"title": "Holiday task", "planned_date": "2027-01-01", "icon_id": "lucide:mop", "color_id": "teal"})
	p := projection(t, h, "2026-12-28", "2027-01-03")
	if len(p.CalendarDays) != 7 || len(p.Items) != 1 || p.Items[0].TaskID != tsk.ID || p.Items[0].EffectiveIconID != "lucide:mop" {
		t.Fatal(p)
	}
	// Legacy/unconfirmed imports must not be advertised as official facts.
	if e := st.ImportCalendarYear(ctx, syntheticRecurrenceYear(2027)); e != nil {
		t.Fatal(e)
	}
	p = projection(t, h, "2027-02-20", "2027-02-20")
	if len(p.CalendarDays) != 0 || len(p.CalendarMissingYears) != 1 {
		t.Fatal(p)
	}
}

func TestCustomIconThumbnailPreservesOriginal(t *testing.T) {
	_, _, h := seriesFixture(t)
	img := image.NewNRGBA(image.Rect(0, 0, 1024, 512))
	var raw bytes.Buffer
	png.Encode(&raw, img)
	i := value[IconDTO](t, apiCall(t, h, "POST", "/icons", map[string]any{"name": "Thumbnail", "data": raw.Bytes()}, 201))
	thumb := apiCall(t, h, "GET", "/icons/"+i.ID+"/image?size=64", nil, 200)
	cfg, e := png.DecodeConfig(thumb.Body)
	if e != nil || cfg.Width != 64 || cfg.Height != 32 {
		t.Fatal(cfg, e)
	}
	orig := apiCall(t, h, "GET", "/icons/"+i.ID+"/image", nil, 200)
	cfg, e = png.DecodeConfig(orig.Body)
	if e != nil || cfg.Width != 1024 {
		t.Fatal(cfg, e)
	}
	apiCall(t, h, "GET", "/icons/"+i.ID+"/image?size=1024", nil, 422)
	catalog := apiCall(t, h, "GET", "/icons", nil, 200)
	var items []IconDTO
	json.Unmarshal(catalog.Body.Bytes(), &items)
	for _, v := range items {
		if v.ID == i.ID && v.Revision != 1 {
			t.Fatal("thumbnail mutated revision")
		}
	}
}

func TestNewCatalogRecurrenceSnapshots(t *testing.T) {
	_, s, h := seriesFixture(t)
	in := seriesInput("new icons", "2026-11-16", recurrence.Schedule, 7)
	in.Task.Appearance = Appearance{IconID: ptr("lucide:robot-vacuum"), ColorID: ptr("teal")}
	series := createSeries(t, h, in)
	first := series.Occurrences[0].Task
	apiCall(t, h, "PATCH", "/tasks/"+first.ID, map[string]any{"expected_revision": first.Revision, "icon_id": "lucide:brush-cleaning"}, 200)
	drainSeries(t, s, MaterializeInput{Through: "2027-02-01"})
	current := getSeriesTest(t, s, series.ID)
	for i, o := range current.Occurrences {
		want := "lucide:robot-vacuum"
		if i == 0 {
			want = "lucide:brush-cleaning"
		}
		if o.Task.EffectiveIconID != want {
			t.Fatal(o)
		}
	}
	snapshot := cleaningSnapshot()
	snapshot.Appearance = Appearance{IconID: ptr("lucide:broom-sparkles"), ColorID: ptr("green")}
	snapshot.Tasks[0].IconID = ptr("lucide:mop-sparkles")
	template := createTemplateTest(t, s, snapshot)
	projects := projectSeriesTest(t, s, template, "2026-11-16", 14)
	for _, o := range projects.Occurrences {
		if o.Project.EffectiveIconID != "lucide:broom-sparkles" {
			t.Fatal(o)
		}
		if taskNamed(t, *o.Project, snapshot.Tasks[0].Title).EffectiveIconID != "lucide:mop-sparkles" {
			t.Fatal(o.Project)
		}
	}
}
