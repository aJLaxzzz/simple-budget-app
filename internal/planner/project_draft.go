package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"net/http"
	"sort"
)

type ProjectDraftInput struct {
	Snapshot     ProjectTemplateSnapshot `json:"snapshot"`
	AnchorDate   string                  `json:"anchor_date"`
	HasStart     bool                    `json:"has_start"`
	Rule         *recurrence.Rule        `json:"rule,omitempty"`
	ProjectMode  string                  `json:"project_mode,omitempty"`
	PreviewToken string                  `json:"preview_token"`
	RequestID    string                  `json:"request_id"`
}
type ProjectDraftPreview struct {
	Project      ProjectComposition  `json:"project"`
	Next         *ProjectComposition `json:"next,omitempty"`
	FirstDate    string              `json:"first_date,omitempty"`
	PreviewToken string              `json:"preview_token"`
}

func projectDraftRoute(s *Service, w http.ResponseWriter, r *http.Request, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in ProjectDraftInput
	if e := strictPlannerInput(w, r, &in, "snapshot anchor_date has_start rule project_mode preview_token request_id"); e != nil {
		return 0, nil, e
	}
	if action != "preview" && action != "apply" {
		return 0, nil, notFound("Маршрут", action)
	}
	var out any
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		preview := func() (ProjectDraftPreview, error) {
			p := ProjectDraftPreview{}
			snapshot, e := normalizeTemplate(in.Snapshot)
			if e != nil {
				return p, e
			}
			if e = managementDate(in.AnchorDate); e != nil {
				return p, e
			}
			p.Project, e = composeProject(snapshot, in.AnchorDate)
			if e != nil {
				return p, e
			}
			if in.Rule != nil {
				rule, e := recurrence.NormalizeRule(*in.Rule)
				if e != nil {
					return p, e
				}
				if rule.Mode != recurrence.Schedule {
					return p, invalid("Набор задач повторяется по расписанию")
				}
				if in.ProjectMode != "sequential" && in.ProjectMode != "independent" {
					return p, invalid("Выберите режим выполнения")
				}
				v := SeriesDTO{SourceType: "project", AnchorMode: "schedule", Rule: StoredTaskRule{Rule: rule, FirstDate: recurrence.Date(in.AnchorDate), Anchor: recurrence.Anchor{Date: recurrence.Date(in.AnchorDate), Kind: recurrence.ScheduleAnchor}}}
				c, _, e := calculateStoredCycle(r.Context(), tx, v, 1, nil)
				if e != nil {
					return p, e
				}
				p.FirstDate = string(c.AdjustedDate)
				next, e := composeProject(snapshot, p.FirstDate)
				if e != nil {
					return p, e
				}
				p.Next = &next
			}
			clean := in
			clean.PreviewToken = ""
			clean.RequestID = ""
			p.PreviewToken, e = digest(struct {
				Input ProjectDraftInput
				Next  *ProjectComposition
			}{clean, p.Next})
			return p, e
		}
		if action == "preview" {
			v, e := preview()
			out = v
			return e
		}
		result, e := commandOnce(tx, in.RequestID, "project-draft", "create", stamp(s.now()), in, func() (CommandResult, error) {
			p, e := preview()
			if e != nil {
				return CommandResult{}, e
			}
			if in.PreviewToken == "" || p.PreviewToken != in.PreviewToken {
				return CommandResult{}, conflict("preview_changed", "Повторите предпросмотр проекта", p)
			}
			id, e := operation()
			if e != nil {
				return CommandResult{}, e
			}
			now := stamp(s.now())
			project := ProjectDTO{RecordMeta: RecordMeta{ID: id, Revision: 1, CreatedAt: now, UpdatedAt: now}, Appearance: p.Project.Appearance, Title: p.Project.Title, Description: p.Project.Description, Status: "planned", DueDate: p.Project.DueDate}
			if in.HasStart || in.Rule != nil {
				project.StartDate = &in.AnchorDate
			}
			if e = validateProject(project); e != nil {
				return CommandResult{}, e
			}
			if e = insertProject(tx, project); e != nil {
				return CommandResult{}, e
			}
			keys := map[string]string{}
			for _, t := range p.Project.Tasks {
				tid, e := operation()
				if e != nil {
					return CommandResult{}, e
				}
				keys[t.Key] = tid
			}
			ordered := append([]ComposedTask{}, p.Project.Tasks...)
			sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ParentKey == nil && ordered[j].ParentKey != nil })
			for i, t := range ordered {
				task := TaskDTO{RecordMeta: RecordMeta{ID: keys[t.Key], Revision: 1, CreatedAt: now, UpdatedAt: now}, Appearance: t.Appearance, Title: t.Title, Description: t.Description, Priority: t.Priority, Status: "planned", ProjectID: &id, PlannedDate: t.PlannedDate, DueDate: t.DueDate, ScheduledTime: t.ScheduledTime}
				if t.ParentKey != nil {
					parent := keys[*t.ParentKey]
					task.ParentTaskID = &parent
				}
				if e = validateTask(tx, task); e != nil {
					return CommandResult{}, e
				}
				if e = insertTask(tx, task); e != nil {
					return CommandResult{}, e
				}
				if _, e = tx.Exec(`UPDATE tasks SET sort_order=? WHERE id=?`, i, task.ID); e != nil {
					return CommandResult{}, e
				}
				if e = event(tx, "task", task.ID, "created", in.RequestID, now, nil, task); e != nil {
					return CommandResult{}, e
				}
			}
			if e = event(tx, "project", id, "created", in.RequestID, now, nil, project); e != nil {
				return CommandResult{}, e
			}
			if in.Rule != nil {
				source, e := projectWithTree(tx, id)
				if e != nil {
					return CommandResult{}, e
				}
				snapshot, e := projectSnapshot(source, in.AnchorDate)
				if e != nil {
					return CommandResult{}, e
				}
				t, e := createTemplateTx(tx, "project", snapshot, in.RequestID, now)
				if e != nil {
					return CommandResult{}, e
				}
				if _, e = tx.Exec(`UPDATE project_templates SET internal=1 WHERE id=?`, t.ID); e != nil {
					return CommandResult{}, e
				}
				rule, e := recurrence.NormalizeRule(*in.Rule)
				if e != nil {
					return CommandResult{}, e
				}
				result, e := s.createProjectSeriesFromOrigin(r.Context(), tx, CreateSeriesInput{SourceType: "project", RequestID: in.RequestID + ":repeat", TemplateID: t.ID, TemplateVersionID: t.VersionID, ExpectedRevision: t.Revision, FirstDate: p.FirstDate, Rule: rule}, rule, in.RequestID, now, in.AnchorDate)
				if e != nil {
					return CommandResult{}, e
				}
				if e = linkTemplate(tx, nil, &id, t, "saved", &result.Series.ID, now); e != nil {
					return CommandResult{}, e
				}
				var current any
				if in.ProjectMode == "sequential" {
					current = id
				}
				if _, e = tx.Exec(`INSERT INTO project_repeat_state(series_id,mode,source_project_id,current_project_id) VALUES(?,?,?,?)`, result.Series.ID, in.ProjectMode, id, current); e != nil {
					return CommandResult{}, e
				}
			}
			project, e = projectWithTree(tx, id)
			return CommandResult{Project: &project}, e
		})
		out = result
		return e
	})
	return 200, out, recurrenceAPIError(e)
}
