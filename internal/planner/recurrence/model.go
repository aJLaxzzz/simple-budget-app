// Package recurrence calculates civil dates. It has no HTTP, SQL, clock, or
// mutation dependencies. Calendar implementations must provide a stable snapshot.
package recurrence

import (
	"context"
	"fmt"
)

type Date string // YYYY-MM-DD in the application's civil calendar, not an instant.
type Mode string
type Unit string
type DayMode string
type Adjustment string
type AnchorKind string

const (
	Schedule          Mode       = "schedule"
	Completion        Mode       = "completion"
	Days              Unit       = "days"
	Weeks             Unit       = "weeks"
	Months            Unit       = "months"
	Years             Unit       = "years"
	AnyDay            DayMode    = "any"
	Weekdays          DayMode    = "weekdays"
	Weekends          DayMode    = "weekends"
	SelectedDays      DayMode    = "selected"
	RussianWorkdays   DayMode    = "ru_production"
	RussianWeekdays   DayMode    = "ru_weekdays"
	Next              Adjustment = "next"
	Previous          Adjustment = "previous"
	Nearest           Adjustment = "nearest"
	NoAdjustment      Adjustment = "none"
	ScheduleAnchor    AnchorKind = "schedule"
	CompletedAnchor   AnchorKind = "completion"
	ExplicitAnchor    AnchorKind = "reanchor"
	UnconfirmedAnchor AnchorKind = "unconfirmed"
	Timezone                     = "Europe/Moscow"
	MaxPreviewCount              = 50
	MaxPreviewYears              = 100
	MaxAdjustmentDays            = 366
)

type AllowedDays struct {
	Mode     DayMode `json:"mode"`
	Weekdays []int   `json:"weekdays,omitempty"` // ISO: Monday=1, Sunday=7.
}
type Rule struct {
	SchemaVersion int         `json:"schema_version"`
	Mode          Mode        `json:"mode"`
	Unit          Unit        `json:"unit"`
	Interval      int64       `json:"interval"`
	AllowedDays   AllowedDays `json:"allowed_days"`
	Adjustment    Adjustment  `json:"adjustment"`
	Timezone      string      `json:"timezone"`
}
type Anchor struct {
	Date       Date       `json:"date,omitempty"`
	CycleIndex int64      `json:"cycle_index"`
	Kind       AnchorKind `json:"kind"`
}
type PreviewRequest struct {
	Rule       Rule   `json:"rule"`
	Anchor     Anchor `json:"anchor"`
	Count      int    `json:"count"`
	StartCycle *int64 `json:"start_cycle,omitempty"`
}
type Candidate struct {
	CycleIndex   int64       `json:"cycle_index"`
	AnchorDate   Date        `json:"anchor_date"`
	BaseDate     Date        `json:"base_date"`
	AdjustedDate Date        `json:"adjusted_date"`
	AllowedDays  AllowedDays `json:"allowed_days"`
	Adjustment   Adjustment  `json:"adjustment"`
	ShiftDays    int         `json:"shift_days"`
	ReasonCode   string      `json:"reason_code"`
	Reason       string      `json:"reason"`
	Conditional  bool        `json:"conditional"`
}
type PreviewResult struct {
	Rule           Rule           `json:"rule"`
	Anchor         Anchor         `json:"anchor"`
	RequestedCount int            `json:"requested_count"`
	Items          []Candidate    `json:"items"`
	Status         string         `json:"status"` // ready, needs_anchor, awaiting_completion
	Reason         string         `json:"reason,omitempty"`
	Calendars      []CalendarInfo `json:"calendars"`
}
type CalendarInfo struct {
	Year     int    `json:"year"`
	Source   string `json:"source"`
	Checksum string `json:"checksum"`
	Revision int64  `json:"revision"`
}
type ProductionDay struct {
	Kind     string
	Reason   string
	Calendar CalendarInfo
}
type ProductionCalendar interface {
	Day(context.Context, Date) (ProductionDay, error)
}

// Error is a domain error; HTTP status mapping belongs to the adapter.
type Error struct{ Code, Message string }

func (e *Error) Error() string           { return e.Message }
func failure(code, message string) error { return &Error{Code: code, Message: message} }

type MissingCalendarError struct{ Year int }

func (e *MissingCalendarError) Error() string {
	return fmt.Sprintf("Нет полного производственного календаря РФ за %d год", e.Year)
}
