package planner

import (
	"context"
	"database/sql"
)

// CalendarItem is a read-only projection, never a new task or occurrence.
// ID/revision identify the object mutated by the corresponding action API.
type CalendarItem struct {
	ProjectPending bool `json:"project_pending"`
	Dynamic        bool `json:"dynamic"`
	EffectiveAppearance
	OccurrenceID    *string `json:"occurrence_id,omitempty"`
	SeriesID        *string `json:"series_id,omitempty"`
	RuleVersionID   *string `json:"rule_version_id,omitempty"`
	CycleIndex      *int64  `json:"cycle_index,omitempty"`
	ID              string  `json:"id"`
	Type            string  `json:"type"`
	Revision        int64   `json:"revision"`
	TaskID          string  `json:"task_id"`
	TaskRevision    int64   `json:"task_revision"`
	ParentTaskID    *string `json:"parent_task_id"`
	Title           string  `json:"title"`
	Date            string  `json:"date"`
	StartTime       *string `json:"start_time"`
	EndTime         *string `json:"end_time"`
	Status          string  `json:"status"`
	TaskStatus      string  `json:"task_status"`
	Priority        string  `json:"priority"`
	ProjectID       *string `json:"project_id"`
	ProjectTitle    *string `json:"project_title"`
	ProjectArchived bool    `json:"project_archived"`
}
type CalendarProject struct {
	RepeatSeriesID *string `json:"repeat_series_id,omitempty"`
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	ArchivedAt     *string `json:"archived_at"`
}
type CalendarProjection struct {
	CalendarDays         []CalendarDisplayDay `json:"calendar_days"`
	CalendarMissingYears []int                `json:"calendar_missing_years"`
	From                 string               `json:"from"`
	To                   string               `json:"to"`
	Timezone             string               `json:"timezone"`
	Items                []CalendarItem       `json:"items"`
	Projects             []CalendarProject    `json:"projects"`
}

func (s *Service) CalendarProjection(ctx context.Context, from, to string) (CalendarProjection, error) {
	result := CalendarProjection{From: from, To: to, Timezone: "Europe/Moscow", Items: []CalendarItem{}, Projects: []CalendarProject{}}
	start, e := parseDate(Date(from))
	if e != nil || start.Year() < 1 {
		return result, invalid("from: требуется корректная дата YYYY-MM-DD")
	}
	end, e := parseDate(Date(to))
	if e != nil || end.Year() < 1 {
		return result, invalid("to: требуется корректная дата YYYY-MM-DD")
	}
	if end.Before(start) || end.After(start.AddDate(0, 0, 92)) {
		return result, invalid("Диапазон календаря должен содержать от 1 до 93 дней включительно")
	}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		result.Items, err = calendarItems(ctx, tx, from, to)
		if err != nil {
			return err
		}
		result.Projects, err = calendarProjects(ctx, tx)
		if err != nil {
			return err
		}
		result.CalendarDays, result.CalendarMissingYears, err = calendarDisplay(ctx, tx, start, end)
		return err
	})
	return result, e
}

// One snapshot for the existing completion dialog, including undated children
// and active sessions outside the currently visible calendar range.
type TaskContext struct {
	Task     TaskDTO      `json:"task"`
	Children []TaskDTO    `json:"children"`
	Sessions []SessionDTO `json:"sessions"`
}

func (s *Service) TaskContext(ctx context.Context, id string) (TaskContext, error) {
	var result TaskContext
	err := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		result.Task, e = getTask(tx, id)
		if e != nil {
			return e
		}
		if e = decorateTask(tx, &result.Task); e != nil {
			return e
		}
		result.Children, e = listTasks(tx, "WHERE parent_task_id=?", id)
		if e != nil {
			return e
		}
		result.Sessions, e = listSessions(tx, "WHERE status IN ('planned','in_progress') AND (task_id=? OR task_id IN (SELECT id FROM tasks WHERE parent_task_id=?))", id, id)
		return e
	})
	return result, err
}
