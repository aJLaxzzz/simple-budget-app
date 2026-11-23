package planner

import "budget/internal/planner/recurrence"

type ProjectTemplateTask struct {
	Appearance
	Key           string  `json:"key"`
	ParentKey     *string `json:"parent_key,omitempty"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Priority      string  `json:"priority"`
	PlannedOffset *int    `json:"planned_offset"`
	DueOffset     *int    `json:"due_offset"`
	ScheduledTime *string `json:"scheduled_time,omitempty"`
}
type ProjectTemplateSnapshot struct {
	Appearance
	Title       string                `json:"title"`
	Description string                `json:"description"`
	DueOffset   *int                  `json:"due_offset"`
	Tasks       []ProjectTemplateTask `json:"tasks"`
}
type ProjectTemplateDTO struct {
	RecordMeta
	Kind      string                  `json:"kind"`
	Archived  bool                    `json:"archived"`
	VersionID string                  `json:"version_id"`
	Version   int                     `json:"version"`
	Snapshot  ProjectTemplateSnapshot `json:"snapshot"`
}
type TemplateInput struct {
	RequestID         string                   `json:"request_id"`
	ExpectedRevision  int64                    `json:"expected_revision"`
	Snapshot          *ProjectTemplateSnapshot `json:"snapshot,omitempty"`
	TemplateVersionID string                   `json:"template_version_id,omitempty"`
	StartDate         string                   `json:"start_date,omitempty"`
	Rule              *recurrence.Rule         `json:"rule,omitempty"`
	Count             int                      `json:"count,omitempty"`
}
type ComposedTask struct {
	Appearance
	Key           string  `json:"key"`
	ParentKey     *string `json:"parent_key,omitempty"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Priority      string  `json:"priority"`
	PlannedDate   *string `json:"planned_date"`
	DueDate       *string `json:"due_date"`
	ScheduledTime *string `json:"scheduled_time,omitempty"`
}
type ProjectComposition struct {
	Appearance
	Title       string         `json:"title"`
	Description string         `json:"description"`
	StartDate   string         `json:"start_date"`
	DueDate     *string        `json:"due_date"`
	Tasks       []ComposedTask `json:"tasks"`
}
type TemplatePreviewCycle struct {
	Candidate *recurrence.Candidate `json:"candidate,omitempty"`
	Project   ProjectComposition    `json:"project"`
}
