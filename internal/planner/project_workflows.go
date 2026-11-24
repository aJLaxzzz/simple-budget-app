package planner

import (
	"context"
	"database/sql"
	"net/http"
)

// Advance only after an explicit closure. Past periods require an explicit choice.
func (s *Service) advanceProjectRepeat(tx *sql.Tx, projectID string) error {
	var series string
	e := tx.QueryRow(`SELECT series_id FROM project_repeat_state WHERE mode='sequential' AND current_project_id=?`, projectID).Scan(&series)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	var status, start string
	e = tx.QueryRow(`SELECT status,coalesce(start_date,'') FROM projects WHERE id=?`, projectID).Scan(&status, &start)
	if e == nil && active(status) {
		return nil
	}
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	return s.selectNextProject(tx, series, start)
}
func (s *Service) selectNextProject(tx *sql.Tx, series, start string) error {
	var next, date string
	var e error
	e = tx.QueryRow(`SELECT p.id,coalesce(p.start_date,'') FROM projects p JOIN project_repeat_projection v ON v.id=p.id WHERE v.series_id=? AND coalesce(p.start_date,'')>=? AND p.status IN ('planned','in_progress') AND p.archived_at IS NULL ORDER BY coalesce(p.start_date,'9999'),p.id LIMIT 1`, series, start).Scan(&next, &date)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	if next == "" {
		v, err := getSeries(tx, series)
		if err != nil {
			return err
		}
		if v.State == "active" {
			candidate, _, err := calculateStoredCycle(context.Background(), tx, v, v.NextCycle, nil)
			if err != nil {
				return err
			}
			if v.EndDate == nil || string(candidate.AdjustedDate) <= *v.EndDate {
				if string(candidate.AdjustedDate) > v.HorizonDate {
					v.HorizonDate = string(candidate.AdjustedDate)
				}
				if _, _, err = s.materializeSeriesTx(context.Background(), tx, &v, "project-advance:"+series+":"+start, stamp(s.now())); err != nil {
					return err
				}
				e = tx.QueryRow(`SELECT p.id,coalesce(p.start_date,'') FROM projects p JOIN project_repeat_projection v ON v.id=p.id WHERE v.series_id=? AND coalesce(p.start_date,'')>=? AND p.status IN ('planned','in_progress') AND p.archived_at IS NULL ORDER BY coalesce(p.start_date,'9999'),p.id LIMIT 1`, series, start).Scan(&next, &date)
				if e != nil && e != sql.ErrNoRows {
					return e
				}
			}
		}
	}
	if next == "" {
		_, e = tx.Exec(`UPDATE project_repeat_state SET current_project_id=NULL,needs_decision=0 WHERE series_id=?`, series)
	} else if date != "" && date < s.today() {
		_, e = tx.Exec(`UPDATE project_repeat_state SET current_project_id=NULL,needs_decision=1 WHERE series_id=?`, series)
	} else {
		_, e = tx.Exec(`UPDATE project_repeat_state SET current_project_id=?,needs_decision=0 WHERE series_id=?`, next, series)
	}
	return e
}
func checkProjectPending(tx *sql.Tx, id *string) error {
	if id == nil {
		return nil
	}
	var pending bool
	if e := tx.QueryRow(`SELECT preliminary FROM project_repeat_projection WHERE id=?`, *id).Scan(&pending); e != nil {
		return e
	}
	if pending {
		return conflict("project_not_current", "Это запланированное выполнение. Сначала завершите текущее или явно выберите выполнение в настройках повторения.", nil)
	}
	return nil
}

type ProjectModeInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Mode             string `json:"mode"`
	CurrentID        string `json:"current_id"`
	PreviewToken     string `json:"preview_token"`
	RequestID        string `json:"request_id"`
}
type ProjectModePreview struct {
	Projects     []ProjectDTO `json:"projects"`
	PreviewToken string       `json:"preview_token"`
}

func projectModePreview(tx *sql.Tx, id string, in ProjectModeInput) (ProjectModePreview, error) {
	out := ProjectModePreview{Projects: []ProjectDTO{}}
	v, e := getSeries(tx, id)
	if e != nil {
		return out, e
	}
	if e = checkRevision(in.ExpectedRevision, v.RecordMeta); e != nil {
		return out, e
	}
	if v.SourceType != "project" || v.State == "disabled" {
		return out, invalid("Повторение проекта отключено")
	}
	if in.Mode != "sequential" && in.Mode != "independent" {
		return out, invalid("Выберите режим выполнения")
	}
	ps, e := listProjects(tx, "", false)
	if e != nil {
		return out, e
	}
	found := false
	for _, p := range ps {
		var sid sql.NullString
		if e = tx.QueryRow(`SELECT series_id FROM project_repeat_projection WHERE id=?`, p.ID).Scan(&sid); e != nil {
			return out, e
		}
		if sid.String != id {
			continue
		}
		out.Projects = append(out.Projects, p)
		if in.Mode == "sequential" && active(p.Status) {
			if p.ID == in.CurrentID {
				found = true
			} else {
				var n int
				if e = tx.QueryRow(`SELECT count(*) FROM tasks t WHERE t.project_id=? AND (t.status IN('in_progress','completed') OR EXISTS(SELECT 1 FROM sessions w WHERE w.task_id=t.id AND w.status='in_progress'))`, p.ID).Scan(&n); e != nil {
					return out, e
				}
				if p.Status == "in_progress" || n > 0 {
					return out, conflict("project_started", "Другое выполнение уже начато. Завершите его или выберите его текущим.", p)
				}
			}
		}
	}
	if in.Mode == "sequential" && !found {
		return out, invalid("Выберите открытое текущее выполнение")
	}
	clean := in
	clean.RequestID = ""
	clean.PreviewToken = ""
	out.PreviewToken, e = digest(struct {
		Input    ProjectModeInput
		Projects []ProjectDTO
	}{clean, out.Projects})
	return out, e
}
func projectModeRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in ProjectModeInput
	if e := strictPlannerInput(w, r, &in, "expected_revision mode current_id preview_token request_id"); e != nil {
		return 0, nil, e
	}
	var out any
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		if action == "project-preview" {
			v, e := projectModePreview(tx, id, in)
			out = v
			return e
		}
		result, e := commandOnce(tx, in.RequestID, "series/"+id, "project-mode", stamp(s.now()), in, func() (CommandResult, error) {
			p, e := projectModePreview(tx, id, in)
			if e != nil {
				return CommandResult{}, e
			}
			if in.PreviewToken == "" || in.PreviewToken != p.PreviewToken {
				return CommandResult{}, conflict("preview_changed", "Повторите предпросмотр", p)
			}
			var current any
			if in.Mode == "sequential" {
				current = in.CurrentID
			}
			_, e = tx.Exec(`INSERT INTO project_repeat_state(series_id,mode,current_project_id) VALUES(?,?,?) ON CONFLICT(series_id) DO UPDATE SET mode=excluded.mode,current_project_id=excluded.current_project_id,needs_decision=0`, id, in.Mode, current)
			if e != nil {
				return CommandResult{}, e
			}
			_, e = tx.Exec(`UPDATE series SET revision=revision+1,updated_at=? WHERE id=?`, stamp(s.now()), id)
			if e != nil {
				return CommandResult{}, e
			}
			if e = event(tx, "series", id, "project_mode_changed", in.RequestID, stamp(s.now()), p, in); e != nil {
				return CommandResult{}, e
			}
			v, e := getSeries(tx, id)
			return CommandResult{Series: &v}, e
		})
		out = result
		return e
	})
	return 200, out, e
}

type ProjectOrderInput struct {
	Mutation
	TaskIDs   []string `json:"task_ids"`
	RequestID string   `json:"request_id"`
}

func projectOrderRoute(s *Service, w http.ResponseWriter, r *http.Request, id string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in ProjectOrderInput
	if e := strictPlannerInput(w, r, &in, "expected_revision related_revisions task_ids request_id"); e != nil {
		return 0, nil, e
	}
	var out CommandResult
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		var e error
		out, e = commandOnce(tx, in.RequestID, "project/"+id, "order", stamp(s.now()), in, func() (CommandResult, error) {
			p, e := getProject(tx, id)
			if e != nil {
				return CommandResult{}, e
			}
			if e = checkRevision(in.ExpectedRevision, p.RecordMeta); e != nil {
				return CommandResult{}, e
			}
			if p.ArchivedAt != nil {
				return CommandResult{}, invalid("Проект архивирован")
			}
			if len(in.TaskIDs) == 0 || len(in.TaskIDs) > 5000 {
				return CommandResult{}, invalid("Выберите задачи одного уровня")
			}
			seen := map[string]bool{}
			parent := ""
			series := map[string]bool{}
			for i, id := range in.TaskIDs {
				if seen[id] {
					return CommandResult{}, invalid("Повтор задачи в порядке")
				}
				seen[id] = true
				t, e := getTask(tx, id)
				if e != nil {
					return CommandResult{}, e
				}
				if t.ProjectID == nil || *t.ProjectID != p.ID {
					return CommandResult{}, invalid("Задача из другого проекта")
				}
				par := ""
				if t.ParentTaskID != nil {
					par = *t.ParentTaskID
				}
				if i == 0 {
					parent = par
				} else if parent != par {
					return CommandResult{}, invalid("Перемещайте задачи внутри одного родителя")
				}
				if e = checkRevision(in.RelatedRevisions[id], t.RecordMeta); e != nil {
					return CommandResult{}, e
				}
				if t.SeriesID != nil {
					if series[*t.SeriesID] {
						return CommandResult{}, invalid("Для ручного порядка выберите ближайшее выполнение каждого регулярного дела")
					}
					series[*t.SeriesID] = true
					_, e = tx.Exec(`UPDATE series SET task_order=?,revision=revision+1,updated_at=? WHERE id=?`, i, stamp(s.now()), *t.SeriesID)
					if e != nil {
						return CommandResult{}, e
					}
				}
				if _, e = tx.Exec(`UPDATE tasks SET sort_order=?,revision=revision+1,updated_at=? WHERE id=?`, i, stamp(s.now()), id); e != nil {
					return CommandResult{}, e
				}
				if e = event(tx, "task", id, "reordered", in.RequestID, stamp(s.now()), t, map[string]any{"sort_order": i}); e != nil {
					return CommandResult{}, e
				}
			}
			if _, e = tx.Exec(`UPDATE projects SET revision=revision+1,updated_at=? WHERE id=?`, stamp(s.now()), p.ID); e != nil {
				return CommandResult{}, e
			}
			p, e = projectWithTree(tx, p.ID)
			return CommandResult{Project: &p}, e
		})
		return e
	})
	return 200, out, e
}
