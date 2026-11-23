package recurrence

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func FuzzBaseDates(f *testing.F) {
	f.Add(uint16(2024), uint8(1), uint8(31), uint8(2), uint16(1), uint16(10))
	f.Add(uint16(2096), uint8(2), uint8(29), uint8(3), uint16(4), uint16(2))
	f.Fuzz(func(t *testing.T, year uint16, month, day, unit uint8, interval, cycle uint16) {
		y := 1900 + int(year)%200
		m := time.Month(1 + month%12)
		d := 1 + int(day)%31
		origin := clamped(y, m, d)
		index := int64(1 + cycle%100)
		n := int64(1 + interval%20)
		units := []Unit{Days, Weeks, Months, Years}
		r := rule(units[unit%4], n)
		anchor := Anchor{Kind: ScheduleAnchor, Date: Date(origin.Format("2006-01-02")), CycleIndex: 51}
		got, e := BaseDate(r, anchor, 51+index)
		if e != nil {
			t.Fatal(e)
		}
		parsed, e := parse(got)
		if e != nil || !parsed.After(origin) {
			t.Fatal(got, e)
		}
		again, e := BaseDate(r, anchor, 51+index)
		if e != nil || got != again {
			t.Fatal("not deterministic")
		}
		switch r.Unit {
		case Days, Weeks:
			expected := n * index
			if r.Unit == Weeks {
				expected *= 7
			}
			// UTC duration is an independent test oracle, not production arithmetic.
			if int64(parsed.Sub(origin).Hours()/24) != expected {
				t.Fatal("day interval", got)
			}
		case Months:
			difference := int64(parsed.Year()-origin.Year())*12 + int64(parsed.Month()-origin.Month())
			if difference != n*index {
				t.Fatal("month interval", got)
			}
			wantDay := origin.Day()
			last := time.Date(parsed.Year(), parsed.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
			if wantDay > last {
				wantDay = last
			}
			if parsed.Day() != wantDay {
				t.Fatal("lost desired day", got)
			}
		case Years:
			if int64(parsed.Year()-origin.Year()) != n*index || parsed.Month() != origin.Month() {
				t.Fatal("year interval", got)
			}
			if origin.Month() != time.February || origin.Day() != 29 {
				if parsed.Day() != origin.Day() {
					t.Fatal("day changed", got)
				}
			} else if parsed.Day() != 28 && parsed.Day() != 29 {
				t.Fatal("leap clamp", got)
			}
		}
	})
}
func FuzzSelectedDaysAdjustment(f *testing.F) {
	f.Add(uint8(6), uint8(17), uint8(0b0010101), uint8(2))
	f.Fuzz(func(t *testing.T, month, day, mask, policy uint8) {
		base := Date(fmt.Sprintf("2026-%02d-%02d", 1+month%12, 1+day%28))
		selected := []int{}
		for i := 0; i < 7; i++ {
			if mask&(1<<i) != 0 {
				selected = append(selected, i+1)
			}
		}
		if len(selected) == 0 {
			selected = []int{1}
		}
		days := AllowedDays{Mode: SelectedDays, Weekdays: selected}
		policies := []Adjustment{Next, Previous, Nearest}
		p := policies[policy%3]
		adjusted, e := AdjustDate(testContext, base, days, p, nil)
		if e != nil {
			t.Fatal(e)
		}
		allowed, _, _, e := permitted(testContext, adjusted.Date, days, nil)
		if e != nil || !allowed {
			t.Fatal(adjusted, e)
		}
		if adjusted.ShiftDays > 6 || adjusted.ShiftDays < -6 {
			t.Fatal("unbounded weekday search")
		}
		twice, e := AdjustDate(testContext, adjusted.Date, days, p, nil)
		if e != nil || twice.Date != adjusted.Date || twice.ShiftDays != 0 {
			t.Fatal("not idempotent")
		}
		again, e := AdjustDate(testContext, base, days, p, nil)
		if e != nil || !reflect.DeepEqual(again, adjusted) {
			t.Fatal("not deterministic")
		}
		// Independently enumerate all legal choices in a whole week on either side.
		expected := 0
		found := false
		for distance := 0; distance <= 7 && !found; distance++ {
			for _, sign := range []int{1, -1} {
				if p == Next && sign < 0 || p == Previous && sign > 0 {
					continue
				}
				candidate, _ := addDays(base, sign*distance)
				ok, _, _, _ := permitted(testContext, candidate, days, nil)
				if ok {
					expected = sign * distance
					found = true
					break
				}
			}
		}
		if adjusted.ShiftDays != expected {
			t.Fatal("not closest", adjusted, expected)
		}
	})
}
