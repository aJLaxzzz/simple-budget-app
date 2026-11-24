package planner

import (
	"budget/internal/planner/recurrence"
	"encoding/json"
)

const materializeCyclesPerBatch = 100
const materializeSeriesPerBatch = 20

type TaskTemplate struct {
	Appearance
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Priority      string  `json:"priority"`
	ProjectID     *string `json:"project_id,omitempty"`
	ParentTaskID  *string `json:"parent_task_id,omitempty"`
	ScheduledTime *string `json:"scheduled_time,omitempty"`
}
type CreateSeriesInput struct {
	FirstDueDate      *string         `json:"first_due_date,omitempty"`
	FirstStatus       string          `json:"first_status,omitempty"`
	EndDate           *string         `json:"end_date,omitempty"`
	SourceType        string          `json:"source_type,omitempty"`
	TemplateID        string          `json:"template_id,omitempty"`
	TemplateVersionID string          `json:"template_version_id,omitempty"`
	RequestID         string          `json:"request_id"`
	Task              *TaskTemplate   `json:"task,omitempty"`
	ExistingTaskID    string          `json:"existing_task_id,omitempty"`
	ExpectedRevision  int64           `json:"expected_revision,omitempty"`
	FirstDate         string          `json:"first_date"`
	Rule              recurrence.Rule `json:"rule"`
}
type StoredTaskRule struct {
	Rule      recurrence.Rule `json:"rule"`
	FirstDate recurrence.Date `json:"first_date"`
	// For schedule this immutable origin retains the desired month/day.
	Anchor     recurrence.Anchor `json:"anchor"`
	FirstCycle int64             `json:"first_cycle"`
}
type SeriesDTO struct {
	SourceProjectID  *string `json:"source_project_id,omitempty"`
	CalculationError *string `json:"calculation_error,omitempty"`
	EffectiveAppearance
	Upcoming          []TaskOccurrenceDTO      `json:"upcoming,omitempty"`
	EndDate           *string                  `json:"end_date,omitempty"`
	TemplateID        *string                  `json:"template_id,omitempty"`
	NextDate          *string                  `json:"next_date,omitempty"`
	LastCompletedDate *string                  `json:"last_completed_date,omitempty"`
	UnresolvedCount   int                      `json:"unresolved_count"`
	ProjectTemplate   *ProjectTemplateSnapshot `json:"project_template,omitempty"`
	TemplateVersionID *string                  `json:"template_version_id,omitempty"`
	AnchorKind        recurrence.AnchorKind    `json:"anchor_kind"`
	Pauses            []PauseDTO               `json:"pauses,omitempty"`
	Versions          []VersionScopeDTO        `json:"versions,omitempty"`
	RecordMeta
	SourceType          string              `json:"source_type"`
	AnchorMode          string              `json:"anchor_mode"`
	State               string              `json:"state"`
	RuleVersionID       string              `json:"rule_version_id"`
	Rule                StoredTaskRule      `json:"rule"`
	Template            TaskTemplate        `json:"template"`
	NextCycle           int64               `json:"next_cycle"`
	AnchorDate          *string             `json:"anchor_date"`
	AnchorCycle         int64               `json:"anchor_cycle"`
	WaitingOccurrenceID *string             `json:"waiting_occurrence_id"`
	HorizonDate         string              `json:"horizon_date"`
	Occurrences         []TaskOccurrenceDTO `json:"occurrences,omitempty"`
	HasMore             bool                `json:"has_more"`
}
type TaskOccurrenceDTO struct {
	SourceType            string      `json:"source_type,omitempty"`
	Project               *ProjectDTO `json:"project,omitempty"`
	Compact               bool        `json:"compact,omitempty"`
	ProjectStatus         string      `json:"project_status,omitempty"`
	TemplateVersionID     *string     `json:"template_version_id,omitempty"`
	OriginalRuleVersionID string      `json:"original_rule_version_id"`
	OriginalBaseDate      string      `json:"original_base_date"`
	OriginalAdjustedDate  string      `json:"original_adjusted_date"`
	ManualOverride        bool        `json:"manual_override"`
	RecordMeta
	SeriesID      string  `json:"series_id"`
	RuleVersionID string  `json:"rule_version_id"`
	CycleIndex    int64   `json:"cycle_index"`
	BaseDate      string  `json:"base_date"`
	AdjustedDate  string  `json:"adjusted_date"`
	ManualDate    *string `json:"manual_date"`
	Task          TaskDTO `json:"task"`
}
type MaterializeInput struct {
	Through       string `json:"through,omitempty"`
	SeriesID      string `json:"series_id,omitempty"`
	AfterSeriesID string `json:"after_series_id,omitempty"`
}
type SeriesMaterialization struct {
	SeriesID string    `json:"series_id"`
	Created  int       `json:"created"`
	Pending  bool      `json:"pending"`
	Error    *APIError `json:"error,omitempty"`
}
type MaterializeResult struct {
	Through           string                  `json:"through"`
	Results           []SeriesMaterialization `json:"results"`
	NextAfterSeriesID string                  `json:"next_after_series_id,omitempty"`
	Pending           bool                    `json:"pending"`
}

// Task clients keep their existing payload; a project cycle has no dummy task.
func (o TaskOccurrenceDTO) MarshalJSON() ([]byte, error) {
	type alias TaskOccurrenceDTO
	var task *TaskDTO
	if o.SourceType != "project" {
		task = &o.Task
	}
	return json.Marshal(struct {
		*alias
		Task *TaskDTO `json:"task,omitempty"`
	}{(*alias)(&o), task})
}
