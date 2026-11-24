package planner

import (
	"budget/internal/planner/recurrence"
	"encoding/json"
)

type SeriesCommand struct {
	TemplateVersionID    string           `json:"template_version_id,omitempty"`
	TasksDateDecision    string           `json:"tasks_date_decision,omitempty"`
	RemovedTasksDecision string           `json:"removed_tasks_decision,omitempty"`
	RequestID            string           `json:"request_id,omitempty"`
	ExpectedRevision     int64            `json:"expected_revision"`
	PreviewToken         string           `json:"preview_token,omitempty"`
	Action               string           `json:"action"`
	Rule                 *recurrence.Rule `json:"rule,omitempty"`
	FromCycle            *int64           `json:"from_cycle,omitempty"`
	FirstDate            string           `json:"first_date,omitempty"`
	StartDate            string           `json:"start_date,omitempty"`
	EndDate              string           `json:"end_date,omitempty"`
	PauseID              string           `json:"pause_id,omitempty"`
	AnchorDate           string           `json:"anchor_date,omitempty"`
	Reason               string           `json:"reason,omitempty"`
}
type PauseDTO struct {
	RecordMeta
	SeriesID  string  `json:"series_id"`
	StartDate string  `json:"start_date"`
	EndDate   *string `json:"end_date"`
	Reason    string  `json:"reason"`
	ResumedOn *string `json:"resumed_on"`
}
type VersionScopeDTO struct {
	TemplateVersionID *string                  `json:"template_version_id,omitempty"`
	ProjectTemplate   *ProjectTemplateSnapshot `json:"project_template,omitempty"`
	VersionID         string                   `json:"version_id"`
	FromCycle         int64                    `json:"from_cycle"`
	UntilCycle        *int64                   `json:"until_cycle"`
	Rule              StoredTaskRule           `json:"rule"`
}
type SeriesChangeRow struct {
	TemplateVersionID  *string                   `json:"template_version_id,omitempty"`
	Project            *ProjectDTO               `json:"project,omitempty"`
	ProposedProject    *ProjectComposition       `json:"proposed_project,omitempty"`
	OccurrenceID       string                    `json:"occurrence_id,omitempty"`
	TaskID             string                    `json:"task_id,omitempty"`
	CycleIndex         int64                     `json:"cycle_index"`
	OccurrenceRevision int64                     `json:"occurrence_revision"`
	TaskRevision       int64                     `json:"task_revision"`
	BeforeDate         *string                   `json:"before_date"`
	AfterDate          *string                   `json:"after_date"`
	BaseDate           string                    `json:"base_date"`
	AdjustedDate       string                    `json:"adjusted_date"`
	Disposition        string                    `json:"disposition"`
	Manual             bool                      `json:"manual"`
	Reason             string                    `json:"reason,omitempty"`
	PauseConflict      bool                      `json:"pause_conflict"`
	Candidate          *recurrence.Candidate     `json:"candidate,omitempty"`
	Calendars          []recurrence.CalendarInfo `json:"calendars,omitempty"`
	Sessions           []SessionDTO              `json:"sessions"`
}
type SeriesChangePreview struct {
	ProposedTemplate      *ProjectTemplateSnapshot `json:"proposed_template,omitempty"`
	CurrentTemplate       *ProjectTemplateSnapshot `json:"current_template,omitempty"`
	UnchangedOutsideScope int                      `json:"unchanged_outside_scope"`
	SeriesID              string                   `json:"series_id"`
	Revision              int64                    `json:"revision"`
	Action                string                   `json:"action"`
	CurrentRule           StoredTaskRule           `json:"current_rule"`
	ProposedRule          *StoredTaskRule          `json:"proposed_rule,omitempty"`
	Rows                  []SeriesChangeRow        `json:"rows"`
	Pauses                []PauseDTO               `json:"pauses"`
	Conflicts             []string                 `json:"conflicts"`
	CalendarErrors        []*APIError              `json:"calendar_errors"`
	PreviewToken          string                   `json:"preview_token"`
}
type OccurrenceCommand struct {
	CompletionRequestID     string           `json:"completion_request_id,omitempty"`
	DynamicDecision         string           `json:"dynamic_decision,omitempty"`
	ScheduledTime           Field[string]    `json:"scheduled_time"`
	ExpectedProjectRevision int64            `json:"expected_project_revision,omitempty"`
	TasksDecision           string           `json:"tasks_decision,omitempty"`
	TasksDateDecision       string           `json:"tasks_date_decision,omitempty"`
	RequestID               string           `json:"request_id"`
	ExpectedRevision        int64            `json:"expected_revision"`
	ExpectedTaskRevision    int64            `json:"expected_task_revision"`
	ExpectedSeriesRevision  int64            `json:"expected_series_revision"`
	PlannedDate             *string          `json:"planned_date,omitempty"`
	CompletedDate           *string          `json:"completed_date,omitempty"`
	Reason                  string           `json:"reason,omitempty"`
	BranchDecision          string           `json:"branch_decision,omitempty"`
	SessionsDecision        string           `json:"sessions_decision,omitempty"`
	SessionRevisions        map[string]int64 `json:"session_revisions,omitempty"`
	ChildrenDecision        string           `json:"children_decision,omitempty"`
	RelatedRevisions        map[string]int64 `json:"related_revisions,omitempty"`
}
type IncidentDTO struct {
	ProjectID     *string `json:"project_id,omitempty"`
	ProjectStatus string  `json:"project_status,omitempty"`
	SourceType    string  `json:"source_type,omitempty"`
	RecordMeta
	OccurrenceID string  `json:"occurrence_id"`
	SeriesID     string  `json:"series_id"`
	CycleIndex   int64   `json:"cycle_index"`
	TaskID       string  `json:"task_id,omitempty"`
	TaskStatus   string  `json:"task_status,omitempty"`
	Kind         string  `json:"kind"`
	Source       string  `json:"source"`
	PauseID      *string `json:"pause_id"`
	PlannedDate  string  `json:"planned_date"`
	Reason       string  `json:"reason"`
	Disposition  string  `json:"disposition"`
	Resolution   string  `json:"resolution"`
	ReviewedAt   *string `json:"reviewed_at"`
}
type CommandResult struct {
	Undo          *CompletionUndo     `json:"undo,omitempty"`
	Deleted       *DeletionPreview    `json:"deleted,omitempty"`
	Task          *TaskDTO            `json:"task,omitempty"`
	CreatedRecord json.RawMessage     `json:"created_record,omitempty"`
	Attention     *AttentionSettings  `json:"attention,omitempty"`
	Calendar      *CalendarSummary    `json:"calendar,omitempty"`
	Template      *ProjectTemplateDTO `json:"template,omitempty"`
	Project       *ProjectDTO         `json:"project,omitempty"`
	Series        *SeriesDTO          `json:"series,omitempty"`
	Occurrence    *TaskOccurrenceDTO  `json:"occurrence,omitempty"`
}

// Preserve the distinction between omitted time and an explicit null. The wire
// command is also used by commandOnce to fingerprint idempotent requests.
func (in OccurrenceCommand) MarshalJSON() ([]byte, error) {
	type wire OccurrenceCommand
	var timeValue json.RawMessage
	if in.ScheduledTime.Set {
		var err error
		timeValue, err = json.Marshal(in.ScheduledTime.Value)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(struct {
		*wire
		ScheduledTime json.RawMessage `json:"scheduled_time,omitempty"`
	}{(*wire)(&in), timeValue})
}
