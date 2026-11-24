package planner

import (
	"encoding/json"
	"fmt"
)

type RecordMeta struct {
	ID        string `json:"id"`
	Revision  int64  `json:"revision"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
type TemplateLinks struct {
	SavedTemplateID  *string `json:"saved_template_id,omitempty"`
	SourceTemplateID *string `json:"source_template_id,omitempty"`
}
type RoutineSummary struct {
	Count     int      `json:"count"`
	NextDate  *string  `json:"next_date,omitempty"`
	OpenDates []string `json:"open_dates"`
	Ordinary  Progress `json:"ordinary"`
}
type ProjectDTO struct {
	ManagedRepeat    bool            `json:"managed_repeat,omitempty"`
	Routines         *RoutineSummary `json:"routines,omitempty"`
	ProjectMode      string          `json:"project_mode"`
	CurrentProjectID *string         `json:"current_project_id,omitempty"`
	NeedsDecision    bool            `json:"needs_decision"`
	Preliminary      bool            `json:"preliminary"`
	TemplateLinks
	RepeatSeriesID *string `json:"repeat_series_id,omitempty"`
	EffectiveAppearance
	Appearance
	TaskCount         int     `json:"task_count"`
	OccurrenceID      *string `json:"occurrence_id,omitempty"`
	TemplateVersionID *string `json:"template_version_id,omitempty"`
	RecordMeta
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	StartDate   *string   `json:"start_date"`
	DueDate     *string   `json:"due_date"`
	ClosedAt    *string   `json:"closed_at"`
	ArchivedAt  *string   `json:"archived_at"`
	Progress    Progress  `json:"progress"`
	Tasks       []TaskDTO `json:"tasks"`
}
type Progress struct {
	Total     int     `json:"total"`
	Completed int     `json:"completed"`
	Cancelled int     `json:"cancelled"`
	Skipped   int     `json:"skipped"`
	Percent   float64 `json:"percent"`
	OpenTasks int     `json:"open_tasks"`
}
type TaskDTO struct {
	SortOrder      int  `json:"sort_order"`
	ProjectPending bool `json:"project_pending"`
	Dynamic        bool `json:"dynamic"`
	TemplateLinks
	EffectiveAppearance
	Appearance
	ParentTitle   *string `json:"parent_title,omitempty"`
	OccurrenceID  *string `json:"occurrence_id,omitempty"`
	SeriesID      *string `json:"series_id,omitempty"`
	RuleVersionID *string `json:"rule_version_id,omitempty"`
	CycleIndex    *int64  `json:"cycle_index,omitempty"`
	RecordMeta
	ProjectID                 *string `json:"project_id"`
	ParentTaskID              *string `json:"parent_task_id"`
	Title                     string  `json:"title"`
	Description               string  `json:"description"`
	Priority                  string  `json:"priority"`
	Status                    string  `json:"status"`
	PlannedDate               *string `json:"planned_date"`
	ScheduledTime             *string `json:"scheduled_time"`
	DueDate                   *string `json:"due_date"`
	CompletedDate             *string `json:"completed_date"`
	CompletedAt               *string `json:"completed_at"`
	OpenChildren              int     `json:"open_children"`
	CompletedWithOpenChildren bool    `json:"completed_with_open_children"`
}

// Field distinguishes omitted fields from explicit null (clear a date/link).
type Field[T any] struct {
	Set   bool
	Value *T
}

func (f *Field[T]) UnmarshalJSON(b []byte) error { f.Set = true; return json.Unmarshal(b, &f.Value) }

type Mutation struct {
	ExpectedRevision int64            `json:"expected_revision"`
	RelatedRevisions map[string]int64 `json:"related_revisions"`
}
type ProjectInput struct {
	ColorID   Field[string] `json:"color_id"`
	IconID    Field[string] `json:"icon_id"`
	RequestID string        `json:"request_id,omitempty"`
	Mutation
	Title       Field[string] `json:"title"`
	Description Field[string] `json:"description"`
	Status      Field[string] `json:"status"`
	StartDate   Field[string] `json:"start_date"`
	DueDate     Field[string] `json:"due_date"`
}
type TaskInput struct {
	ColorID          Field[string]    `json:"color_id"`
	IconID           Field[string]    `json:"icon_id"`
	RequestID        string           `json:"request_id,omitempty"`
	SessionsDecision string           `json:"sessions_decision"`
	SessionRevisions map[string]int64 `json:"session_revisions"`
	Mutation
	Title            Field[string] `json:"title"`
	Description      Field[string] `json:"description"`
	Priority         Field[string] `json:"priority"`
	Status           Field[string] `json:"status"`
	ProjectID        Field[string] `json:"project_id"`
	ParentTaskID     Field[string] `json:"parent_task_id"`
	PlannedDate      Field[string] `json:"planned_date"`
	ScheduledTime    Field[string] `json:"scheduled_time"`
	DueDate          Field[string] `json:"due_date"`
	CompletedDate    *string       `json:"completed_date"`
	ChildrenDecision string        `json:"children_decision"`
}
type HistoryDTO struct {
	ID          string          `json:"id"`
	ObjectType  string          `json:"object_type"`
	ObjectID    string          `json:"object_id"`
	Action      string          `json:"action"`
	Actor       string          `json:"actor"`
	CreatedAt   string          `json:"created_at"`
	OperationID string          `json:"operation_id"`
	Changes     json.RawMessage `json:"changes"`
}
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Current any    `json:"current,omitempty"`
}

func (e *APIError) Error() string { return e.Message }
func invalid(message string) error {
	return &APIError{Status: 422, Code: "validation_error", Message: message}
}
func notFound(kind, id string) error {
	return &APIError{Status: 404, Code: "not_found", Message: fmt.Sprintf("%s %s не найден", kind, id)}
}
func checkRevision(expected int64, current RecordMeta) error {
	if expected <= 0 {
		return invalid("Требуется положительная expected_revision (для дочерних записей — related_revisions)")
	}
	if expected != current.Revision {
		return &APIError{Status: 409, Code: "revision_conflict", Message: "Запись изменена другим запросом; перечитайте её перед сохранением", Current: current}
	}
	return nil
}

type SessionDTO struct {
	RecordMeta
	TaskID        string  `json:"task_id"`
	Date          string  `json:"date"`
	StartTime     *string `json:"start_time"`
	EndTime       *string `json:"end_time"`
	Timezone      string  `json:"timezone"`
	Status        string  `json:"status"`
	CompletedDate *string `json:"completed_date"`
	CompletedAt   *string `json:"completed_at"`
}
type SessionInput struct {
	RequestID        string        `json:"request_id,omitempty"`
	ExpectedRevision int64         `json:"expected_revision"`
	TaskID           string        `json:"task_id"`
	Date             Field[string] `json:"date"`
	StartTime        Field[string] `json:"start_time"`
	EndTime          Field[string] `json:"end_time"`
	Timezone         Field[string] `json:"timezone"`
	CompletedDate    *string       `json:"completed_date"`
}
