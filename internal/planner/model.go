package planner

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type ID string
type Date string // YYYY-MM-DD civil date, never a device-dependent timestamp.
type Metadata struct {
	Revision             int64
	CreatedAt, UpdatedAt time.Time
}
type Project struct {
	ID                              ID
	Title, Description, Status      string
	StartDate, DueDate              *Date
	OccurrenceID, TemplateVersionID *ID
	ClosedAt                        *time.Time
	ArchivedAt                      *time.Time
	Metadata
}
type Task struct {
	ID                                    ID
	ProjectID, ParentTaskID, OccurrenceID *ID
	Title, Description, Priority, Status  string
	PlannedDate, DueDate, CompletedDate   *Date
	ScheduledTime                         *string
	CompletedAt                           *time.Time
	Metadata
}
type Session struct {
	ID, TaskID         ID
	Date               Date
	StartTime, EndTime *string
	Timezone, Status   string
	CompletedAt        *time.Time
	CompletedDate      *Date
	Metadata
}
type Series struct {
	ID                            ID
	SourceType, AnchorMode, State string
	Metadata
}
type RuleVersion struct {
	ID, SeriesID                 ID
	Version                      int
	EffectiveFrom                Date
	EffectiveUntil               *Date
	RuleJSON, SourceSnapshotJSON string
	TemplateVersionID            *ID
	CreatedAt                    time.Time
}
type Occurrence struct {
	ID, SeriesID, RuleVersionID ID
	CycleIndex                  int
	BaseDate, AdjustedDate      Date
	ManualDate                  *Date
	Outcome, Reason             *string
	Metadata
}
type Pause struct {
	ID, SeriesID ID
	StartDate    Date
	EndDate      *Date
	Reason       string
	Metadata
}
type Template struct {
	ID       ID
	Title    string
	Archived bool
	Metadata
}
type TemplateVersion struct {
	ID, TemplateID ID
	Version        int
	SnapshotJSON   string
	CreatedAt      time.Time
}
type HistoryEvent struct {
	ID                                                   ID
	TaskID, ProjectID, SessionID, SeriesID, OccurrenceID *ID
	Actor, Action, PayloadJSON                           string
	OperationID                                          *ID
	CreatedAt                                            time.Time
}
type AttentionState struct {
	ID                                        ID
	TaskID, ProjectID, OccurrenceID, SeriesID *ID
	Disposition                               string
	ViewedAt                                  *time.Time
	SnoozedUntil                              *Date
	Metadata
}

func NewID() (ID, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return ID(hex.EncodeToString(b[:])), nil
}
