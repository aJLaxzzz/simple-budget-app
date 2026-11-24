package planner

import "budget/internal/planner/recurrence"

// TaskBlueprint deliberately cannot contain links, dates, state, or sessions.
type TaskBlueprint struct {
	Appearance
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}
type TaskTemplateInput struct {
	RequestID        string         `json:"request_id"`
	ExpectedRevision int64          `json:"expected_revision"`
	SourceTaskID     string         `json:"source_task_id,omitempty"`
	Snapshot         *TaskBlueprint `json:"snapshot,omitempty"`
	ProjectID        *string        `json:"project_id,omitempty"`
	PlannedDate      *string        `json:"planned_date,omitempty"`
}

// The preview binds the full source tree, including task revisions, to the command.
// The original project is never attached to an occurrence or rescheduled.
type ProjectSourceInput struct {
	IndependentDecision string                   `json:"independent_decision,omitempty"`
	RequestID           string                   `json:"request_id"`
	ExpectedRevision    int64                    `json:"expected_revision"`
	PreviewToken        string                   `json:"preview_token,omitempty"`
	Snapshot            *ProjectTemplateSnapshot `json:"snapshot,omitempty"`
	AnchorDate          string                   `json:"anchor_date"`
	FirstDate           string                   `json:"first_date,omitempty"`
	Rule                *recurrence.Rule         `json:"rule,omitempty"`
	EndDate             *string                  `json:"end_date,omitempty"`
}
type ProjectSourcePreview struct {
	Conversions  []DisablePreview        `json:"conversions,omitempty"`
	Excluded     []TaskDTO               `json:"excluded,omitempty"`
	Snapshot     ProjectTemplateSnapshot `json:"snapshot"`
	PreviewToken string                  `json:"preview_token"`
	Items        []TemplatePreviewCycle  `json:"items"`
}
