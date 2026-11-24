package planner

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

type TaskMoveInput struct {
	TaskIDs           []string         `json:"task_ids"`
	ProjectID         *string          `json:"project_id"`
	Scope             string           `json:"scope"`
	ExpectedRevisions map[string]int64 `json:"expected_revisions"`
	PreviewToken      string           `json:"preview_token,omitempty"`
	RequestID         string           `json:"request_id,omitempty"`
}
type TaskMovePreview struct {
	Tasks        []TaskDTO   `json:"tasks"`
	Series       []SeriesDTO `json:"series"`
	Project      *ProjectDTO `json:"project"`
	PreviewToken string      `json:"preview_token"`
}

func seriesProject(tx *sql.Tx, id string) (*string, bool, error) {
	var p *string
	e := tx.QueryRow("SELECT project_id FROM series_assignments WHERE series_id=? ORDER BY version DESC LIMIT 1", id).Scan(&p)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, false, nil
	}
	return p, e == nil, e
}

// An independent task schedule belongs in a permanent project, never in a
// repeating project source or one of its materialized copies.
func independentTaskProject(tx *sql.Tx, id string) error {
	p, e := getProject(tx, id)
	if e != nil {
		return e
	}
	if p.OccurrenceID != nil || p.RepeatSeriesID != nil {
		return conflict("double_recurrence", "Здесь повторяется весь проект. Для независимого повторения задачи выберите постоянный проект; иначе настройте относительную дату внутри шаблона проекта.", nil)
	}
	return nil
}
func movePreviewTx(tx *sql.Tx, in TaskMoveInput) (TaskMovePreview, error) {
	out := TaskMovePreview{Tasks: []TaskDTO{}, Series: []SeriesDTO{}}
	if len(in.TaskIDs) == 0 || len(in.TaskIDs) > 100 {
		return out, invalid("Выберите от 1 до 100 задач")
	}
	if in.Scope != "instance" && in.Scope != "series" {
		return out, invalid("Выберите одно выполнение или всё повторение")
	}
	if in.ProjectID != nil {
		p, e := getProject(tx, *in.ProjectID)
		if e != nil {
			return out, e
		}
		if p.ArchivedAt != nil || !active(p.Status) {
			return out, invalid("Выберите открытый проект вне архива")
		}
		out.Project = &p
	}
	seen, seriesSeen := map[string]bool{}, map[string]bool{}
	add := func(t TaskDTO) error {
		if seen[t.ID] {
			return nil
		}
		seen[t.ID] = true
		if t.ParentTaskID != nil {
			return invalid("Подзадача перемещается вместе с родительской задачей")
		}
		if t.SeriesID != nil && out.Project != nil && (out.Project.OccurrenceID != nil || out.Project.RepeatSeriesID != nil) {
			return conflict("double_recurrence", "Независимое повторение нельзя поместить в повторяющийся проект. Выберите постоянный проект либо повторение всего дерева.", nil)
		}
		out.Tasks = append(out.Tasks, t)
		children, e := listTasks(tx, "WHERE parent_task_id=?", t.ID)
		if e != nil {
			return e
		}
		if in.ProjectID == nil && len(children) > 0 {
			return invalid("Для дерева с подзадачами нужен проект")
		}
		for _, c := range children {
			if !seen[c.ID] {
				seen[c.ID] = true
				out.Tasks = append(out.Tasks, c)
			}
		}
		return nil
	}
	for _, id := range in.TaskIDs {
		t, e := getTask(tx, id)
		if e != nil {
			return out, e
		}
		if e = checkRevision(in.ExpectedRevisions[id], t.RecordMeta); e != nil {
			return out, e
		}
		if in.Scope == "series" && t.SeriesID != nil {
			if seriesSeen[*t.SeriesID] {
				continue
			}
			seriesSeen[*t.SeriesID] = true
			v, e := getSeries(tx, *t.SeriesID)
			if e != nil {
				return out, e
			}
			if v.Template.ParentTaskID != nil {
				return out, invalid("Повторение подзадачи связано с родителем; переместите родительское дерево")
			}
			v.Occurrences = nil
			out.Series = append(out.Series, v)
			rows, e := listTasks(tx, "WHERE status IN ('planned','in_progress') AND NOT EXISTS(SELECT 1 FROM projects p WHERE p.id=tasks.project_id AND (p.archived_at IS NOT NULL OR p.status NOT IN ('planned','in_progress'))) AND occurrence_id IN (SELECT id FROM occurrences WHERE series_id=?)", v.ID)
			if e != nil {
				return out, e
			}
			for _, row := range rows {
				if e = add(row); e != nil {
					return out, e
				}
			}
		} else {
			if e = add(t); e != nil {
				return out, e
			}
		}
	}
	token, e := digest(out)
	out.PreviewToken = token
	return out, e
}
func (s *Service) PreviewTaskMove(ctx context.Context, in TaskMoveInput) (TaskMovePreview, error) {
	var out TaskMovePreview
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error { var e error; out, e = movePreviewTx(tx, in); return e })
	return out, e
}
func (s *Service) MoveTasks(ctx context.Context, in TaskMoveInput) (CommandResult, error) {
	var out CommandResult
	now := stamp(s.now())
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		out, e = commandOnce(tx, in.RequestID, "task-moves", "move", now, in, func() (CommandResult, error) {
			p, e := movePreviewTx(tx, in)
			if e != nil {
				return CommandResult{}, e
			}
			if in.PreviewToken == "" || in.PreviewToken != p.PreviewToken {
				return CommandResult{}, conflict("preview_changed", "Задачи или проект изменились. Повторите предпросмотр.", p)
			}
			revisions := map[string]int64{}
			for _, t := range p.Tasks {
				revisions[t.ID] = t.Revision
			}
			for _, t := range p.Tasks {
				if t.ParentTaskID != nil {
					continue
				}
				if (t.ProjectID == nil && in.ProjectID == nil) || (t.ProjectID != nil && in.ProjectID != nil && *t.ProjectID == *in.ProjectID) {
					continue
				}
				var moved TaskDTO
				e = s.changeTaskTx(ctx, tx, t.ID, "", TaskInput{Mutation: Mutation{ExpectedRevision: t.Revision, RelatedRevisions: revisions}, ProjectID: Field[string]{Set: true, Value: in.ProjectID}}, in.RequestID, now, s.now(), &moved, taskChangeOptions{})
				if e != nil {
					return CommandResult{}, e
				}
			}
			for _, v := range p.Series {
				id, e := operation()
				if e != nil {
					return CommandResult{}, e
				}
				if _, e = tx.Exec("INSERT INTO series_assignments(id,series_id,version,project_id,created_at) VALUES(?,?,(SELECT COALESCE(max(version),0)+1 FROM series_assignments WHERE series_id=?),?,?)", id, v.ID, v.ID, in.ProjectID, now); e != nil {
					return CommandResult{}, e
				}
				if _, e = tx.Exec("UPDATE series SET revision=revision+1,updated_at=? WHERE id=?", now, v.ID); e != nil {
					return CommandResult{}, e
				}
				if e = event(tx, "series", v.ID, "project_changed", in.RequestID, now, map[string]any{"project_id": v.Template.ProjectID}, map[string]any{"project_id": in.ProjectID}); e != nil {
					return CommandResult{}, e
				}
			}
			return CommandResult{}, nil
		})
		return e
	})
	return out, e
}
func taskMoveRoute(s *Service, w http.ResponseWriter, r *http.Request, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in TaskMoveInput
	if e := strictPlannerInput(w, r, &in, "task_ids project_id scope expected_revisions preview_token request_id"); e != nil {
		return 0, nil, e
	}
	if action == "preview" {
		p, e := s.PreviewTaskMove(r.Context(), in)
		return 200, p, e
	}
	if action == "apply" {
		p, e := s.MoveTasks(r.Context(), in)
		return 200, p, e
	}
	return 0, nil, notFound("Маршрут перемещения", action)
}
