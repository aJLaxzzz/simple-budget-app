package recurrence

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

var testContext = context.Background()

func rule(unit Unit, interval int64) Rule {
	return Rule{SchemaVersion: 1, Mode: Schedule, Unit: unit, Interval: interval, AllowedDays: AllowedDays{Mode: AnyDay}, Adjustment: NoAdjustment}
}
func preview(t *testing.T, r Rule, date Date, count int) PreviewResult {
	t.Helper()
	result, e := Preview(testContext, PreviewRequest{Rule: r, Anchor: Anchor{Date: date, Kind: ScheduleAnchor}, Count: count}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return result
}
func dates(items []Candidate) []Date {
	out := []Date{}
	for _, item := range items {
		out = append(out, item.BaseDate)
	}
	return out
}
func TestBaseIntervalsAndCivilBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		unit     Unit
		interval int64
		anchor   Date
		want     []Date
	}{
		{"45 days is not a month", Days, 45, "2026-01-01", []Date{"2026-02-15", "2026-04-01", "2026-05-16", "2026-06-30", "2026-08-14"}},
		{"two weeks across year", Weeks, 2, "2026-12-20", []Date{"2027-01-03", "2027-01-17", "2027-01-31"}},
		{"31 January", Months, 1, "2026-01-31", []Date{"2026-02-28", "2026-03-31", "2026-04-30", "2026-05-31"}},
		{"30 January", Months, 1, "2026-01-30", []Date{"2026-02-28", "2026-03-30", "2026-04-30"}},
		{"29 January", Months, 1, "2026-01-29", []Date{"2026-02-28", "2026-03-29"}},
		{"leap February", Months, 1, "2024-01-31", []Date{"2024-02-29", "2024-03-31"}},
		{"two calendar months", Months, 2, "2026-12-31", []Date{"2027-02-28", "2027-04-30", "2027-06-30"}},
		{"annual February 29", Years, 1, "2024-02-29", []Date{"2025-02-28", "2026-02-28", "2027-02-28", "2028-02-29"}},
		{"century is not leap", Years, 4, "2096-02-29", []Date{"2100-02-28", "2104-02-29"}},
		{"400-year leap rule", Years, 4, "1996-02-29", []Date{"2000-02-29", "2004-02-29"}},
		{"leap days", Days, 1, "2024-02-28", []Date{"2024-02-29", "2024-03-01"}},
		{"non-leap days", Days, 1, "2026-02-28", []Date{"2026-03-01", "2026-03-02"}},
		{"year boundary", Days, 1, "2026-12-31", []Date{"2027-01-01", "2027-01-02"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := preview(t, rule(tc.unit, tc.interval), tc.anchor, len(tc.want))
			if !reflect.DeepEqual(dates(got.Items), tc.want) {
				t.Fatalf("got %v want %v", dates(got.Items), tc.want)
			}
			for i, item := range got.Items {
				if item.CycleIndex != int64(i+1) || item.AdjustedDate != item.BaseDate || item.Conditional {
					t.Fatal(item)
				}
			}
		})
	}
}
func TestDayPoliciesAndNearestTie(t *testing.T) {
	cases := []struct {
		base   Date
		days   AllowedDays
		policy Adjustment
		want   Date
		shift  int
	}{
		{"2026-11-21", AllowedDays{Mode: Weekdays}, Next, "2026-11-23", 2},
		{"2026-11-22", AllowedDays{Mode: Weekdays}, Previous, "2026-11-20", -2},
		{"2026-11-21", AllowedDays{Mode: Weekdays}, Nearest, "2026-11-20", -1},
		{"2026-11-22", AllowedDays{Mode: Weekdays}, Nearest, "2026-11-23", 1},
		{"2026-11-18", AllowedDays{Mode: SelectedDays, Weekdays: []int{1, 5}}, Nearest, "2026-11-20", 2},
		{"2026-11-18", AllowedDays{Mode: Weekends}, Nearest, "2026-11-21", 3},
		{"2026-11-20", AllowedDays{Mode: SelectedDays, Weekdays: []int{3, 7}}, Next, "2026-11-22", 2},
		{"2026-11-20", AllowedDays{Mode: SelectedDays, Weekdays: []int{3, 7}}, Previous, "2026-11-18", -2},
		{"2026-11-16", AllowedDays{Mode: SelectedDays, Weekdays: []int{2, 4, 7}}, Nearest, "2026-11-17", 1},
		{"2026-11-21", AllowedDays{Mode: Weekends}, Previous, "2026-11-21", 0},
		{"2026-11-16", AllowedDays{Mode: Weekdays}, Next, "2026-11-16", 0},
		{"2026-12-31", AllowedDays{Mode: SelectedDays, Weekdays: []int{1}}, Next, "2027-01-04", 4},
	}
	for _, tc := range cases {
		got, e := AdjustDate(testContext, tc.base, tc.days, tc.policy, nil)
		if e != nil {
			t.Fatal(e)
		}
		if got.Date != tc.want || got.ShiftDays != tc.shift || got.Reason == "" {
			t.Fatalf("%+v => %+v", tc, got)
		}
	}
}
func TestNoDriftAndNoDateDeduplication(t *testing.T) {
	r := rule(Days, 1)
	r.AllowedDays.Mode = Weekdays
	r.Adjustment = Next
	got := preview(t, r, "2026-11-20", 4)
	want := []Date{"2026-11-23", "2026-11-23", "2026-11-23", "2026-11-24"}
	for i, c := range got.Items {
		if c.AdjustedDate != want[i] || c.CycleIndex != int64(i+1) {
			t.Fatal(got)
		}
	}
	// Materialization may display a manual date. It cannot feed that value back
	// into the schedule: CalculateCycle consumes the immutable origin only.
	got.Items[0].AdjustedDate = "2026-12-25"
	origin := Anchor{Date: "2026-11-20", Kind: ScheduleAnchor}
	next, e := CalculateCycle(testContext, r, origin, 2, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(next, got.Items[1]) {
		t.Fatal("manual move changed the schedule")
	}
	// Jumping to a cycle has the same meaning as calculating a longer prefix.
	start := int64(3)
	tail, e := Preview(testContext, PreviewRequest{Rule: r, Anchor: origin, Count: 2, StartCycle: &start}, nil)
	if e != nil || !reflect.DeepEqual(tail.Items, got.Items[2:]) {
		t.Fatal(tail, e)
	}
}
func TestCompletionRequiresConfirmedAnchor(t *testing.T) {
	r := rule(Days, 45)
	r.Mode = Completion
	r.AllowedDays.Mode = Weekdays
	r.Adjustment = Next
	in := PreviewRequest{Rule: r, Anchor: Anchor{Date: "2026-01-01", Kind: CompletedAnchor}, Count: 10}
	got, e := Preview(testContext, in, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Items) != 1 || got.Status != "awaiting_completion" || got.Items[0].BaseDate != "2026-02-15" || got.Items[0].AdjustedDate != "2026-02-16" || got.Items[0].Conditional {
		t.Fatal(got)
	}
	in.Anchor = Anchor{Date: "2026-02-20", CycleIndex: 1, Kind: CompletedAnchor}
	next, e := Preview(testContext, in, nil)
	if e != nil {
		t.Fatal(e)
	}
	if next.Items[0].BaseDate != "2026-04-06" || next.Items[0].CycleIndex != 2 {
		t.Fatal(next)
	}
	in.Anchor = Anchor{Kind: UnconfirmedAnchor, CycleIndex: 1}
	blocked, e := Preview(testContext, in, nil)
	if e != nil || len(blocked.Items) != 0 || blocked.Status != "needs_anchor" {
		t.Fatal(blocked, e)
	}
	if _, e = CalculateCycle(testContext, r, in.Anchor, 2, nil); e == nil {
		t.Fatal("unconfirmed cycle calculated")
	}
	in.Anchor = Anchor{Date: "2026-03-01", Kind: ExplicitAnchor, CycleIndex: 1}
	resumed, e := Preview(testContext, in, nil)
	if e != nil || resumed.Items[0].BaseDate != "2026-04-15" {
		t.Fatal(resumed, e)
	}
	// Actual completion is an intentional NEW origin, not February clamping drift.
	r.Unit = Months
	r.Interval = 1
	in.Rule = r
	in.Anchor = Anchor{Date: "2026-02-28", Kind: CompletedAnchor, CycleIndex: 7}
	actual, e := Preview(testContext, in, nil)
	if e != nil || actual.Items[0].BaseDate != "2026-03-28" {
		t.Fatal(actual, e)
	}
	cycle := int64(10)
	in.StartCycle = &cycle
	if _, e = Preview(testContext, in, nil); e == nil {
		t.Fatal("completion cycles skipped without facts")
	}
}
func TestValidationAndLimits(t *testing.T) {
	base := rule(Days, 1)
	changes := []func(*Rule){
		func(r *Rule) { r.Interval = 0 }, func(r *Rule) { r.Interval = -1 }, func(r *Rule) { r.Unit = "hours" }, func(r *Rule) { r.SchemaVersion = 2 }, func(r *Rule) { r.Mode = "manual" }, func(r *Rule) { r.Timezone = "America/New_York" }, func(r *Rule) { r.Adjustment = "ignore" },
		func(r *Rule) { r.AllowedDays.Mode = Weekdays },
		func(r *Rule) { r.AllowedDays.Weekdays = []int{1} },
		func(r *Rule) { r.AllowedDays = AllowedDays{Mode: SelectedDays}; r.Adjustment = Next },
		func(r *Rule) {
			r.AllowedDays = AllowedDays{Mode: SelectedDays, Weekdays: []int{1, 1}}
			r.Adjustment = Next
		},
		func(r *Rule) {
			r.AllowedDays = AllowedDays{Mode: SelectedDays, Weekdays: []int{0, 8}}
			r.Adjustment = Next
		},
		func(r *Rule) { r.AllowedDays.Mode = "holidays" },
	}
	for i, change := range changes {
		r := base
		change(&r)
		if e := ValidateRule(r); e == nil {
			t.Fatalf("accepted invalid rule %d: %+v", i, r)
		}
	}
	for _, date := range []Date{"2026-02-30", "1900-02-29", "0000-01-01", "2026-1-01", "2026-01-01T00:00:00Z", ""} {
		if _, e := BaseDate(base, Anchor{Date: date, Kind: ScheduleAnchor}, 1); e == nil {
			t.Fatal(date)
		}
	}
	for _, count := range []int{0, -1, 51} {
		if _, e := Preview(testContext, PreviewRequest{Rule: base, Anchor: Anchor{Date: "2026-01-01", Kind: ScheduleAnchor}, Count: count}, nil); e == nil {
			t.Fatal(count)
		}
	}
	for _, unit := range []Unit{Days, Weeks, Months, Years} {
		r := rule(unit, math.MaxInt64)
		if _, e := BaseDate(r, Anchor{Date: "2026-01-01", Kind: ScheduleAnchor}, math.MaxInt64); e == nil {
			t.Fatal("overflow", unit)
		}
	}
	if _, e := BaseDate(base, Anchor{Date: "9999-12-31", Kind: ScheduleAnchor}, 1); e == nil {
		t.Fatal("date range overflow")
	}
	if _, e := AdjustDate(testContext, "0001-01-01", AllowedDays{Mode: Weekends}, Previous, nil); e == nil {
		t.Fatal("date range underflow")
	}
	in := PreviewRequest{Rule: rule(Years, 101), Anchor: Anchor{Date: "2026-01-01", Kind: ScheduleAnchor}, Count: 1}
	if _, e := Preview(testContext, in, nil); e == nil {
		t.Fatal("preview horizon")
	}
	in.Rule = base
	in.Anchor.CycleIndex = math.MaxInt64
	if _, e := Preview(testContext, in, nil); e == nil {
		t.Fatal("cycle overflow")
	}
	in.Anchor = Anchor{Kind: UnconfirmedAnchor, Date: "2026-01-01"}
	in.Rule.Mode = Completion
	if _, e := Preview(testContext, in, nil); e == nil {
		t.Fatal("unconfirmed date silently accepted")
	}
	in.Anchor = Anchor{Kind: ScheduleAnchor, Date: "2026-01-01"}
	if _, e := Preview(testContext, in, nil); e == nil {
		t.Fatal("scheduled date treated as completed")
	}
}
func TestDeterminismNormalizationAndTimezone(t *testing.T) {
	r := rule(Months, 1)
	r.AllowedDays = AllowedDays{Mode: SelectedDays, Weekdays: []int{7, 1, 4}}
	r.Adjustment = Nearest
	input := PreviewRequest{Rule: r, Anchor: Anchor{Date: "2026-01-31", Kind: ScheduleAnchor, CycleIndex: 100}, Count: 10}
	expected, e := Preview(testContext, input, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(r.AllowedDays.Weekdays, []int{7, 1, 4}) {
		t.Fatal("rule mutated")
	}
	var group sync.WaitGroup
	for i := 0; i < 30; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			actual, e := Preview(testContext, input, nil)
			if e != nil || !reflect.DeepEqual(expected, actual) {
				t.Error("nondeterministic", e)
			}
		}()
	}
	group.Wait()
	old := time.Local
	defer func() { time.Local = old }()
	for _, name := range []string{"Europe/Moscow", "America/Los_Angeles", "Pacific/Auckland"} {
		loc, e := time.LoadLocation(name)
		if e != nil {
			t.Fatal(e)
		}
		time.Local = loc
		actual, e := Preview(testContext, input, nil)
		if e != nil || !reflect.DeepEqual(expected, actual) {
			t.Fatal(name, actual, e)
		}
	}
	cancelCtx, cancel := context.WithCancel(testContext)
	cancel()
	if _, e := Preview(cancelCtx, input, nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

type testCalendar func(context.Context, Date) (ProductionDay, error)

func (f testCalendar) Day(ctx context.Context, d Date) (ProductionDay, error) { return f(ctx, d) }
func syntheticCalendar(date Date) (ProductionDay, error) {
	t, _ := parse(date)
	if t.Year() != 2026 {
		return ProductionDay{}, &MissingCalendarError{Year: t.Year()}
	}
	weekday := t.Weekday()
	kind := "working"
	reason := "SYNTHETIC weekday fixture, not an official calendar"
	if weekday == time.Saturday || weekday == time.Sunday {
		kind = "nonworking"
	}
	// Deliberately fictional holiday and transferred Saturday. No production data.
	if date == "2026-11-16" {
		kind = "nonworking"
		reason = "SYNTHETIC state-holiday fixture"
	}
	if date == "2026-11-21" {
		kind = "shortened"
		reason = "SYNTHETIC transferred working Saturday"
	}
	return ProductionDay{Kind: kind, Reason: reason, Calendar: CalendarInfo{Year: 2026, Source: "SYNTHETIC TEST ONLY", Checksum: "test-checksum", Revision: 1}}, nil
}
func TestProductionCalendarAndYearBoundary(t *testing.T) {
	cal := testCalendar(func(_ context.Context, d Date) (ProductionDay, error) { return syntheticCalendar(d) })
	r := rule(Days, 1)
	r.AllowedDays.Mode = RussianWorkdays
	r.Adjustment = Next
	in := PreviewRequest{Rule: r, Anchor: Anchor{Date: "2026-11-15", Kind: ScheduleAnchor}, Count: 1}
	result, e := Preview(testContext, in, cal)
	if e != nil {
		t.Fatal(e)
	}
	if result.Items[0].AdjustedDate != "2026-11-17" || result.Items[0].ReasonCode != "ru_nonworking" || len(result.Calendars) != 1 {
		t.Fatal(result)
	}
	ordinary, e := AdjustDate(testContext, "2026-11-16", AllowedDays{Mode: Weekdays}, Next, nil)
	if e != nil || ordinary.Date != "2026-11-16" {
		t.Fatal(ordinary, e)
	}
	saturday, e := AdjustDate(testContext, "2026-11-21", r.AllowedDays, Next, cal)
	if e != nil || saturday.Date != "2026-11-21" || saturday.ShiftDays != 0 {
		t.Fatal(saturday, e)
	}
	_, e = AdjustDate(testContext, "2027-01-01", r.AllowedDays, Next, cal)
	var missing *MissingCalendarError
	if !errors.As(e, &missing) || missing.Year != 2027 {
		t.Fatal(e)
	}
	r.AllowedDays.Mode = RussianWorkdays
	r.Adjustment = Previous
	// The anchor can be available while a required previous year's date is not.
	noWork := testCalendar(func(_ context.Context, d Date) (ProductionDay, error) {
		day, e := syntheticCalendar(d)
		day.Kind = "nonworking"
		return day, e
	})
	_, e = AdjustDate(testContext, "2026-01-01", r.AllowedDays, Previous, noWork)
	if !errors.As(e, &missing) || missing.Year != 2025 {
		t.Fatal(e)
	}
	if _, e = Preview(testContext, in, nil); !errors.As(e, &missing) || missing.Year != 2026 {
		t.Fatal(e)
	}
}
func TestAdjustmentBoundAndUnstableCalendar(t *testing.T) {
	blocked := testCalendar(func(_ context.Context, d Date) (ProductionDay, error) {
		v, _ := parse(d)
		return ProductionDay{Kind: "nonworking", Calendar: CalendarInfo{Year: v.Year(), Source: "SYNTHETIC", Checksum: "test", Revision: 1}}, nil
	})
	_, e := AdjustDate(testContext, "2026-06-01", AllowedDays{Mode: RussianWorkdays}, Nearest, blocked)
	var domain *Error
	if !errors.As(e, &domain) || domain.Code != "recurrence_adjustment_limit" {
		t.Fatal(e)
	}
	calls := int64(0)
	changing := testCalendar(func(_ context.Context, d Date) (ProductionDay, error) {
		calls++
		day, _ := syntheticCalendar(d)
		day.Calendar.Revision = calls
		return day, nil
	})
	r := rule(Days, 1)
	r.AllowedDays.Mode = RussianWorkdays
	r.Adjustment = Next
	_, e = Preview(testContext, PreviewRequest{Rule: r, Anchor: Anchor{Kind: ScheduleAnchor, Date: "2026-11-15"}, Count: 2}, changing)
	if !errors.As(e, &domain) || domain.Code != "production_calendar_changed" {
		t.Fatal(e)
	}
}
func TestVersionWindowsUseBaseDatesAndStableCycles(t *testing.T) {
	input := []VersionWindow{
		{SeriesID: "s", RuleVersionID: "v2", EffectiveFrom: "2026-11-23", FirstCycle: 3},
		{SeriesID: "s", RuleVersionID: "v1", EffectiveFrom: "2026-11-20", FirstCycle: 0},
	}
	resolved, e := ResolveVersionWindows(input)
	if e != nil {
		t.Fatal(e)
	}
	if input[1].EffectiveUntil != nil || input[1].UntilCycle != nil {
		t.Fatal("immutable version changed")
	}
	if *resolved[0].EffectiveUntil != "2026-11-23" || *resolved[0].UntilCycle != 3 {
		t.Fatal(resolved)
	}
	candidate := Candidate{CycleIndex: 2, BaseDate: "2026-11-22", AdjustedDate: "2026-11-23"}
	if !resolved[0].Contains(candidate) || resolved[1].Contains(candidate) {
		t.Fatal("adjusted date chose version")
	}
	overlap := Date("2026-11-24")
	bad := append([]VersionWindow(nil), input...)
	bad[1].EffectiveUntil = &overlap
	if _, e = ResolveVersionWindows(bad); e == nil {
		t.Fatal("overlapping dates")
	}
	end := int64(4)
	bad = append([]VersionWindow(nil), input...)
	bad[1].UntilCycle = &end
	if _, e = ResolveVersionWindows(bad); e == nil {
		t.Fatal("overlapping cycles")
	}
	bad = append([]VersionWindow(nil), input...)
	bad[0].FirstCycle = 0
	if _, e = ResolveVersionWindows(bad); e == nil {
		t.Fatal("unstable cycle numbering")
	}
	bad = append([]VersionWindow(nil), input...)
	bad[0].EffectiveFrom = bad[1].EffectiveFrom
	if _, e = ResolveVersionWindows(bad); e == nil {
		t.Fatal("same version start")
	}
}
