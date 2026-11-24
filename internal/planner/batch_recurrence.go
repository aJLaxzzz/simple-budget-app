package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
)

type BatchRepeatInput struct {
	TaskIDs           []string         `json:"task_ids"`
	ExpectedRevisions map[string]int64 `json:"expected_revisions"`
	Rule              recurrence.Rule  `json:"rule"`
	PreviewToken      string           `json:"preview_token,omitempty"`
	RequestID         string           `json:"request_id,omitempty"`
}
type BatchRepeatRow struct {
	Task  TaskDTO                `json:"task"`
	Dates []recurrence.Candidate `json:"dates"`
}
type BatchRepeatPreview struct {
	Rows         []BatchRepeatRow `json:"rows"`
	PreviewToken string           `json:"preview_token"`
}

func (s *Service) batchRepeatPreviewTx(ctx context.Context, tx *sql.Tx, in BatchRepeatInput) (BatchRepeatPreview, error) {
	out := BatchRepeatPreview{Rows: []BatchRepeatRow{}}
	if len(in.TaskIDs) < 1 || len(in.TaskIDs) > 20 {
		return out, invalid("Выберите от 1 до 20 задач")
	}
	rule, e := recurrence.NormalizeRule(in.Rule)
	if e != nil {
		return out, recurrenceAPIError(e)
	}
	seen := map[string]bool{}
	for _, id := range in.TaskIDs {
		if seen[id] {
			return out, invalid("Задача выбрана дважды")
		}
		seen[id] = true
		t, e := getTask(tx, id)
		if e != nil {
			return out, e
		}
		if e = checkRevision(in.ExpectedRevisions[id], t.RecordMeta); e != nil {
			return out, e
		}
		if t.SeriesID != nil {
			return out, conflict("already_repeating", "У задачи «"+t.Title+"» уже настроено независимое повторение", t)
		}
		if !active(t.Status) || t.PlannedDate == nil {
			return out, invalid("Каждой открытой задаче нужна собственная плановая дата")
		}
		if t.ProjectID != nil {
			if e = independentTaskProject(tx, *t.ProjectID); e != nil {
				return out, e
			}
		}
		var n int
		if e = taskChildCount(tx, t.ID, &n); e != nil {
			return out, e
		}
		if n > 0 {
			return out, invalid("Для дерева задач используйте повторение проекта")
		}
		row := BatchRepeatRow{Task: t, Dates: []recurrence.Candidate{}}
		v := SeriesDTO{Rule: StoredTaskRule{Rule: rule, FirstDate: recurrence.Date(*t.PlannedDate), Anchor: recurrence.Anchor{Date: recurrence.Date(*t.PlannedDate), Kind: recurrence.ScheduleAnchor}}}
		for i := int64(0); i < 3; i++ {
			c, _, err := calculateStoredCycle(ctx, tx, v, i, nil)
			if err != nil {
				return out, err
			}
			row.Dates = append(row.Dates, c)
			date := string(c.AdjustedDate)
			v.AnchorDate = &date
			v.AnchorCycle = i
			v.AnchorKind = recurrence.CompletedAnchor
		}
		out.Rows = append(out.Rows, row)
	}
	out.PreviewToken, e = digest(struct {
		Rows []BatchRepeatRow
		Rule recurrence.Rule
	}{out.Rows, rule})
	return out, e
}
func batchRepeatRoute(s *Service, w http.ResponseWriter, r *http.Request, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	if action != "preview" && action != "apply" {
		return 0, nil, notFound("Маршрут", action)
	}
	var in BatchRepeatInput
	if e := strictPlannerInput(w, r, &in, "task_ids expected_revisions rule preview_token request_id"); e != nil {
		return 0, nil, e
	}
	var out any
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		if action == "preview" {
			p, e := s.batchRepeatPreviewTx(r.Context(), tx, in)
			out = p
			return e
		}
		result, e := commandOnce(tx, in.RequestID, "batch-recurrence", "create", stamp(s.now()), in, func() (CommandResult, error) {
			p, e := s.batchRepeatPreviewTx(r.Context(), tx, in)
			if e != nil {
				return CommandResult{}, e
			}
			if in.PreviewToken == "" || p.PreviewToken != in.PreviewToken {
				return CommandResult{}, conflict("preview_changed", "Повторите предпросмотр выбранных задач", p)
			}
			items := []SeriesDTO{}
			for _, row := range p.Rows {
				v, e := s.createSeriesTx(r.Context(), tx, CreateSeriesInput{RequestID: in.RequestID + ":" + row.Task.ID, ExistingTaskID: row.Task.ID, ExpectedRevision: row.Task.Revision, FirstDate: *row.Task.PlannedDate, Rule: in.Rule})
				if e != nil {
					return CommandResult{}, e
				}
				items = append(items, v)
			}
			raw, e := json.Marshal(items)
			return CommandResult{CreatedRecord: raw}, e
		})
		out = result
		return e
	})
	return 200, out, e
}
