package planner

import (
	"context"
	"database/sql"
	"net/http"
)

type CompositionInput struct {
	SelectedProjectID    string                   `json:"selected_project_id,omitempty"`
	AfterCycle           *int64                   `json:"-"`
	RemovedTasksDecision string                   `json:"removed_tasks_decision,omitempty"`
	FromDate             string                   `json:"from_date,omitempty"`
	ThroughDate          string                   `json:"through_date,omitempty"`
	ExpectedRevision     int64                    `json:"expected_revision"`
	Snapshot             *ProjectTemplateSnapshot `json:"snapshot,omitempty"`
	SourceID             string                   `json:"source_id,omitempty"`
	AnchorDate           string                   `json:"anchor_date,omitempty"`
	PreviewToken         string                   `json:"preview_token"`
	RequestID            string                   `json:"request_id"`
}
type CompositionRow struct {
	Before ProjectDTO          `json:"before"`
	After  *ProjectComposition `json:"after,omitempty"`
	Reason string              `json:"reason"`
}
type CompositionPreview struct {
	Snapshot     ProjectTemplateSnapshot `json:"snapshot"`
	Rows         []CompositionRow        `json:"rows"`
	PreviewToken string                  `json:"preview_token"`
}

func compositionPreview(ctx context.Context, tx *sql.Tx, id string, in CompositionInput) (CompositionPreview, error) {
	out := CompositionPreview{Rows: []CompositionRow{}}
	if in.RemovedTasksDecision != "" && in.RemovedTasksDecision != "cancel" {
		return out, invalid("Выберите, как обработать исключённые задачи")
	}
	v, e := getSeries(tx, id)
	if e != nil {
		return out, e
	}
	if e = checkRevision(in.ExpectedRevision, v.RecordMeta); e != nil {
		return out, e
	}
	if v.SourceType != "project" || v.State == "disabled" {
		return out, invalid("Нет действующего повторения проекта")
	}
	snapshot := *v.ProjectTemplate
	if in.Snapshot != nil {
		snapshot = *in.Snapshot
	} else if in.SourceID != "" {
		p, e := projectWithTree(tx, in.SourceID)
		if e != nil {
			return out, e
		}
		if p.RepeatSeriesID == nil || *p.RepeatSeriesID != id {
			return out, invalid("Выберите выполнение этого повторения")
		}
		snapshot, e = projectSnapshot(p, in.AnchorDate)
		if e != nil {
			return out, e
		}
		if p.OccurrenceID != nil {
			keys, e := projectKeys(tx, p.ID)
			if e != nil {
				return out, e
			}
			reverse := map[string]string{}
			for k, id := range keys {
				reverse[id] = k
			}
			for i := range snapshot.Tasks {
				t := &snapshot.Tasks[i]
				if key, ok := reverse[t.Key]; ok {
					t.Key = key
				}
				if t.ParentKey != nil {
					if key, ok := reverse[*t.ParentKey]; ok {
						t.ParentKey = &key
					}
				}
			}
		}
	}
	snapshot, e = normalizeTemplate(snapshot)
	if e != nil {
		return out, e
	}
	out.Snapshot = snapshot
	// A bounded composition update. The next unopened periods use the new immutable snapshot.
	for _, date := range []string{in.FromDate, in.ThroughDate} {
		if date != "" {
			if e = managementDate(date); e != nil {
				return out, e
			}
		}
	}
	if in.FromDate != "" && in.ThroughDate != "" && in.FromDate > in.ThroughDate {
		return out, invalid("Начало диапазона позже окончания")
	}
	if in.SelectedProjectID != "" {
		selected, err := getProject(tx, in.SelectedProjectID)
		if err != nil {
			return out, err
		}
		if selected.RepeatSeriesID == nil || *selected.RepeatSeriesID != id {
			return out, invalid("Выберите выполнение этого повторения")
		}
		boundary := int64(-1)
		if selected.OccurrenceID != nil {
			occurrence, err := getOccurrence(tx, *selected.OccurrenceID)
			if err != nil {
				return out, err
			}
			boundary = occurrence.CycleIndex
		}
		in.AfterCycle = &boundary
	}
	afterCycle := int64(-1)
	if in.AfterCycle != nil {
		afterCycle = *in.AfterCycle
	}
	rows, e := tx.Query(`SELECT o.id FROM occurrences o JOIN projects p ON p.occurrence_id=o.id WHERE o.series_id=? AND o.cycle_index>? AND p.status IN('planned','in_progress') AND (?='' OR p.start_date>=?) AND (?='' OR p.start_date<=?) ORDER BY o.cycle_index LIMIT 101`, id, afterCycle, in.FromDate, in.FromDate, in.ThroughDate, in.ThroughDate)
	if e != nil {
		return out, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(ids) > 100 {
		return out, invalid("Более 100 открытых выполнений: укажите меньший диапазон дат в настройках обновления")
	}
	occurrences := []TaskOccurrenceDTO{}
	for _, id := range ids {
		o, e := getOccurrence(tx, id)
		if e != nil {
			return out, e
		}
		occurrences = append(occurrences, o)
	}
	for _, o := range occurrences {
		if o.Project == nil {
			continue
		}
		p := *o.Project
		row := CompositionRow{Before: p}
		sessions, e := projectSessions(tx, p.ID)
		if e != nil {
			return out, e
		}
		pristine, e := pristineProject(tx, p)
		if e != nil {
			return out, e
		}
		switch {
		case !active(p.Status):
			row.Reason = "Закрытая история сохранена"
		case p.CurrentProjectID != nil && *p.CurrentProjectID == p.ID:
			row.Reason = "Текущее выполнение сохранено"
		case p.ArchivedAt != nil:
			row.Reason = "Архив сохранён"
		case projectStarted(&p, sessions) || len(sessions) > 0:
			row.Reason = "Начатая работа или периоды работы сохранены"
		case o.ManualOverride || o.ManualDate != nil || !pristine:
			row.Reason = "Индивидуальные изменения сохранены"
		default:
			date := o.AdjustedDate
			if p.StartDate != nil {
				date = *p.StartDate
			}
			c, e := composeProject(snapshot, date)
			if e != nil {
				return out, e
			}
			if e = checkTemplateTreeChange(tx, p, c, in.RemovedTasksDecision); e != nil {
				return out, e
			}
			row.After = &c
			row.Reason = "Будет обновлено"
		}
		out.Rows = append(out.Rows, row)
	}
	clean := in
	clean.PreviewToken = ""
	clean.RequestID = ""
	out.PreviewToken, e = digest(struct {
		Input   CompositionInput
		Preview CompositionPreview
	}{clean, out})
	return out, e
}
func projectCompositionRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in CompositionInput
	if e := strictPlannerInput(w, r, &in, "expected_revision snapshot source_id anchor_date preview_token request_id from_date through_date removed_tasks_decision selected_project_id"); e != nil {
		return 0, nil, e
	}
	var out any
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		if action == "composition-preview" {
			p, e := compositionPreview(r.Context(), tx, id, in)
			out = p
			return e
		}
		result, e := commandOnce(tx, in.RequestID, "series/"+id, "composition", stamp(s.now()), in, func() (CommandResult, error) {
			p, e := compositionPreview(r.Context(), tx, id, in)
			if e != nil {
				return CommandResult{}, e
			}
			if in.PreviewToken == "" || p.PreviewToken != in.PreviewToken {
				return CommandResult{}, conflict("preview_changed", "Состав или состояния изменились. Повторите предпросмотр.", p)
			}
			return s.applyCompositionPreviewTx(r.Context(), tx, id, p, in.RemovedTasksDecision, in.RequestID, stamp(s.now()))
		})
		out = result
		return e
	})
	return 200, out, e
}

// Shared by the composition editor and atomic scoped task edits.
func (s *Service) applyCompositionPreviewTx(ctx context.Context, tx *sql.Tx, id string, p CompositionPreview, removed, op, now string) (CommandResult, error) {
	v, e := getSeries(tx, id)
	if e != nil {
		return CommandResult{}, e
	}
	// A private snapshot avoids modifying a saved user template shared with another repeat.
	t, e := createTemplateTx(tx, "project", p.Snapshot, op, now)
	if e != nil {
		return CommandResult{}, e
	}
	if _, e = tx.Exec(`UPDATE project_templates SET internal=1 WHERE id=?`, t.ID); e != nil {
		return CommandResult{}, e
	}
	v.ProjectTemplate = &p.Snapshot
	v.TemplateVersionID = &t.VersionID
	rule := v.Rule
	rule.FirstCycle = v.NextCycle
	if _, e = insertVersion(tx, v, rule, now); e != nil {
		return CommandResult{}, e
	}
	for _, row := range p.Rows {
		if row.After == nil {
			continue
		}
		if e = s.applyProjectComposition(ctx, tx, row.Before, *row.After, t.VersionID, removed, op, now); e != nil {
			return CommandResult{}, e
		}
		keys, e := projectKeys(tx, row.Before.ID)
		if e != nil {
			return CommandResult{}, e
		}
		for i, task := range row.After.Tasks {
			if _, e = tx.Exec(`UPDATE tasks SET sort_order=? WHERE id=?`, i, keys[task.Key]); e != nil {
				return CommandResult{}, e
			}
		}
		if e = projectBaseline(tx, row.Before.ID, keys); e != nil {
			return CommandResult{}, e
		}
	}
	if _, e = tx.Exec(`UPDATE series SET revision=revision+1,updated_at=? WHERE id=?`, now, id); e != nil {
		return CommandResult{}, e
	}
	if e = event(tx, "series", id, "composition_changed", op, now, nil, p); e != nil {
		return CommandResult{}, e
	}
	v, e = getSeries(tx, id)
	return CommandResult{Series: &v}, e
}
