package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
)

// Date repair deliberately does not apply a template: content, order, appearance,
// identities and user edits remain independent of schedule corrections.
type ProjectDateRepairInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	PreviewToken     string `json:"preview_token"`
	RequestID        string `json:"request_id"`
	FromDate         string `json:"from_date,omitempty"`
	ThroughDate      string `json:"through_date,omitempty"`
}
type ProjectDateChange struct {
	Before      TaskDTO `json:"before"`
	PlannedDate *string `json:"planned_date"`
	DueDate     *string `json:"due_date"`
	Safe        bool    `json:"safe"`
	Reason      string  `json:"reason"`
}
type ProjectDateRepairRow struct {
	Project       ProjectDTO          `json:"project"`
	ExpectedStart string              `json:"expected_start"`
	ExpectedDue   *string             `json:"expected_due"`
	Changes       []ProjectDateChange `json:"changes"`
	RepairProject bool                `json:"repair_project"`
	Reason        string              `json:"reason"`
	Occurrence    TaskOccurrenceDTO   `json:"occurrence"`
	RuleVersion   string              `json:"rule_version"`
	BaseDate      string              `json:"base_date"`
}
type ProjectDateDuplicate struct {
	Primary ProjectDTO `json:"primary"`
	Other   ProjectDTO `json:"other"`
	Reason  string     `json:"reason"`
}
type ProjectDateRepairPreview struct {
	Rows         []ProjectDateRepairRow `json:"rows"`
	Duplicates   []ProjectDateDuplicate `json:"duplicates"`
	Missing      []int64                `json:"missing"`
	PreviewToken string                 `json:"preview_token"`
}

func projectDateRepairPreview(ctx context.Context, tx *sql.Tx, id string, in ProjectDateRepairInput) (ProjectDateRepairPreview, error) {
	out := ProjectDateRepairPreview{Rows: []ProjectDateRepairRow{}, Duplicates: []ProjectDateDuplicate{}, Missing: []int64{}}
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
	for _, d := range []string{in.FromDate, in.ThroughDate} {
		if d != "" {
			if e = managementDate(d); e != nil {
				return out, e
			}
		}
	}
	if in.FromDate != "" && in.ThroughDate != "" && in.FromDate > in.ThroughDate {
		return out, invalid("Начало диапазона позже окончания")
	}
	rows, e := tx.Query(`SELECT id FROM occurrences WHERE series_id=? ORDER BY cycle_index LIMIT 2001`, id)
	if e != nil {
		return out, e
	}
	ids := []string{}
	for rows.Next() {
		var x string
		if e = rows.Scan(&x); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(ids) > 2000 {
		return out, invalid("Проверка ограничена 2000 выполнениями")
	}
	byDate := map[string][]ProjectDTO{}
	seen := map[int64]bool{}
	for _, oid := range ids {
		o, e := getOccurrence(tx, oid)
		if e != nil {
			return out, e
		}
		seen[o.CycleIndex] = true
		if o.Project == nil {
			continue
		}
		p := *o.Project
		scoped, e := versionForCycle(tx, v, o.CycleIndex)
		if e != nil {
			return out, e
		}
		candidate, _, e := calculateStoredCycle(ctx, tx, scoped, o.CycleIndex, nil)
		if e != nil {
			return out, e
		}
		date := string(candidate.AdjustedDate)
		// Equal adjusted dates of different cycles are legitimate (e.g. holidays).
		if string(candidate.BaseDate) == date {
			byDate[date] = append(byDate[date], p)
		}
		if in.FromDate != "" && date < in.FromDate || in.ThroughDate != "" && date > in.ThroughDate {
			continue
		}
		// Use the current immutable composition, without changing its content/version.
		c, e := composeProject(*v.ProjectTemplate, date)
		if e != nil {
			return out, e
		}
		row := ProjectDateRepairRow{Project: p, ExpectedStart: date, ExpectedDue: c.DueDate, Changes: []ProjectDateChange{}, Occurrence: o, RuleVersion: scoped.RuleVersionID, BaseDate: string(candidate.BaseDate)}
		keys, e := projectKeys(tx, p.ID)
		if e != nil {
			return out, e
		}
		var raw string
		var baseline ProjectDTO
		if e = tx.QueryRow(`SELECT baseline_json FROM project_instances WHERE project_id=?`, p.ID).Scan(&raw); e != nil {
			return out, e
		}
		if e = json.Unmarshal([]byte(raw), &baseline); e != nil {
			return out, e
		}
		baseTasks := map[string]TaskDTO{}
		for _, t := range baseline.Tasks {
			baseTasks[t.ID] = t
		}
		tasks := map[string]TaskDTO{}
		for _, t := range p.Tasks {
			tasks[t.ID] = t
		}
		sessions, e := projectSessions(tx, p.ID)
		if e != nil {
			return out, e
		}
		hasSessions := map[string]bool{}
		for _, x := range sessions {
			hasSessions[x.TaskID] = true
		}
		protected := ""
		switch {
		case p.Status != "planned":
			protected = "Статус выполнения сохранён"
		case p.ArchivedAt != nil:
			protected = "Архив сохранён"
		case o.ManualOverride || o.ManualDate != nil:
			protected = "Ручной перенос проекта сохранён"
		case !reflect.DeepEqual(p.StartDate, baseline.StartDate) || !reflect.DeepEqual(p.DueDate, baseline.DueDate):
			protected = "Индивидуальные сроки проекта сохранены"
		}
		projectDiff := p.StartDate == nil || *p.StartDate != date || !reflect.DeepEqual(p.DueDate, c.DueDate) || o.BaseDate != row.BaseDate || o.AdjustedDate != date
		row.RepairProject = projectDiff && protected == "" && !projectStarted(&p, sessions) && len(sessions) == 0
		row.Reason = "Даты соответствуют расписанию"
		if projectDiff && !row.RepairProject {
			row.Reason = "Сроки проекта защищены: требуется отдельное решение"
		}
		for _, expected := range c.Tasks {
			t, ok := tasks[keys[expected.Key]]
			if !ok {
				row.Reason = "Состав отличается: отсутствующие задачи не создаются автоматически"
				continue
			}
			if reflect.DeepEqual(t.PlannedDate, expected.PlannedDate) && reflect.DeepEqual(t.DueDate, expected.DueDate) {
				continue
			}
			b, exists := baseTasks[t.ID]
			change := ProjectDateChange{Before: t, PlannedDate: expected.PlannedDate, DueDate: expected.DueDate, Safe: true, Reason: "Исправить даты по расписанию"}
			switch {
			case protected != "":
				change.Reason = protected
				change.Safe = false
			case t.OccurrenceID != nil:
				change.Reason = "Независимое повторение задачи сохранено"
				change.Safe = false
			case t.Status != "planned":
				change.Reason = "Начатая или закрытая задача сохранена"
				change.Safe = false
			case hasSessions[t.ID]:
				change.Reason = "Периоды работы сохранены"
				change.Safe = false
			case !exists || !reflect.DeepEqual(t.PlannedDate, b.PlannedDate) || !reflect.DeepEqual(t.DueDate, b.DueDate):
				change.Reason = "Ручные даты сохранены"
				change.Safe = false
			}
			row.Changes = append(row.Changes, change)
		}
		if len(row.Changes) > 0 || row.RepairProject {
			row.Reason = "Проверьте изменения и защищённые даты"
		}
		out.Rows = append(out.Rows, row)
		if len(out.Rows) > 100 {
			return out, invalid("Более 100 выполнений: сузьте диапазон проверки")
		}
	}
	// Only an explicitly linked source can be a legacy duplicate. Matching titles
	// alone never prove ownership. The deletion flow remains a separate confirmation.
	sources, e := tx.Query(`SELECT id FROM project_repeat_projection WHERE series_id=? AND id NOT IN(SELECT p.id FROM projects p JOIN occurrences o ON o.id=p.occurrence_id WHERE o.series_id=?) ORDER BY id`, id, id)
	if e != nil {
		return out, e
	}
	sourceIDs := []string{}
	for sources.Next() {
		var x string
		if e = sources.Scan(&x); e != nil {
			sources.Close()
			return out, e
		}
		sourceIDs = append(sourceIDs, x)
	}
	e = sources.Err()
	sources.Close()
	if e != nil {
		return out, e
	}
	for _, pid := range sourceIDs {
		p, e := projectWithTree(tx, pid)
		if e != nil {
			return out, e
		}
		if p.StartDate != nil {
			byDate[*p.StartDate] = append([]ProjectDTO{p}, byDate[*p.StartDate]...)
		}
	}
	// Iterate in stable order: preview tokens must not depend on map iteration.
	dates := []string{}
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	for _, d := range dates {
		peers := byDate[d]
		if len(peers) < 2 || peers[0].OccurrenceID != nil {
			continue
		}
		for _, p := range peers[1:] {
			out.Duplicates = append(out.Duplicates, ProjectDateDuplicate{Primary: peers[0], Other: p, Reason: "Один период: исходный проект и отдельное выполнение. Сравните задачи; удаление требует отдельного подтверждения."})
		}
	}
	// Diagnose holes only within the materialized span. Tombstones and source
	// cycle zero are intentional and must never be resurrected by repair.
	if len(seen) > 0 {
		var min, max int64
		first := true
		for n := range seen {
			if first || n < min {
				min = n
			}
			if first || n > max {
				max = n
			}
			first = false
		}
		if max-min <= 2000 {
			for n := min; n <= max; n++ {
				if seen[n] {
					continue
				}
				var removed int
				if e = tx.QueryRow(`SELECT count(*) FROM removed_cycles WHERE series_id=? AND cycle_index=?`, id, n).Scan(&removed); e != nil {
					return out, e
				}
				if removed == 0 {
					out.Missing = append(out.Missing, n)
				}
			}
		}
	}
	clean := in
	clean.RequestID = ""
	clean.PreviewToken = ""
	out.PreviewToken, e = digest(struct {
		Input   ProjectDateRepairInput
		Preview ProjectDateRepairPreview
	}{clean, out})
	return out, e
}
func projectDateRepairRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in ProjectDateRepairInput
	if e := strictPlannerInput(w, r, &in, "expected_revision preview_token request_id from_date through_date"); e != nil {
		return 0, nil, e
	}
	var out any
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		if action == "dates-preview" {
			p, e := projectDateRepairPreview(r.Context(), tx, id, in)
			out = p
			return e
		}
		result, e := commandOnce(tx, in.RequestID, "series/"+id, "repair_dates", stamp(s.now()), in, func() (CommandResult, error) {
			p, e := projectDateRepairPreview(r.Context(), tx, id, in)
			if e != nil {
				return CommandResult{}, e
			}
			if in.PreviewToken == "" || in.PreviewToken != p.PreviewToken {
				return CommandResult{}, conflict("preview_changed", "Данные изменились. Повторите проверку дат.", p)
			}
			now := stamp(s.now())
			changed := false
			for _, row := range p.Rows {
				var raw string
				if e = tx.QueryRow(`SELECT baseline_json FROM project_instances WHERE project_id=?`, row.Project.ID).Scan(&raw); e != nil {
					return CommandResult{}, e
				}
				var baseline ProjectDTO
				if e = json.Unmarshal([]byte(raw), &baseline); e != nil {
					return CommandResult{}, e
				}
				local := false
				for _, c := range row.Changes {
					if !c.Safe {
						continue
					}
					next := c.Before
					next.PlannedDate = c.PlannedDate
					next.DueDate = c.DueDate
					next.UpdatedAt = now
					if e = saveTask(tx, next); e != nil {
						return CommandResult{}, e
					}
					next.Revision++
					if e = event(tx, "task", next.ID, "schedule_dates_repaired", in.RequestID, now, c.Before, next); e != nil {
						return CommandResult{}, e
					}
					for i := range baseline.Tasks {
						if baseline.Tasks[i].ID == next.ID {
							baseline.Tasks[i].PlannedDate = next.PlannedDate
							baseline.Tasks[i].DueDate = next.DueDate
							baseline.Tasks[i].Revision = next.Revision
							baseline.Tasks[i].UpdatedAt = now
						}
					}
					local = true
				}
				if row.RepairProject {
					next := row.Project
					next.StartDate = &row.ExpectedStart
					next.DueDate = row.ExpectedDue
					next.UpdatedAt = now
					if e = saveProject(tx, next); e != nil {
						return CommandResult{}, e
					}
					next.Revision++
					if e = event(tx, "project", next.ID, "schedule_dates_repaired", in.RequestID, now, row.Project, next); e != nil {
						return CommandResult{}, e
					}
					_, e = tx.Exec(`INSERT INTO occurrence_schedule(occurrence_id,series_id,rule_version_id,base_date,adjusted_date,calculation_json,created_at,updated_at) VALUES(?,?,?,?,?,'{}',?,?) ON CONFLICT(occurrence_id) DO UPDATE SET rule_version_id=excluded.rule_version_id,base_date=excluded.base_date,adjusted_date=excluded.adjusted_date,revision=occurrence_schedule.revision+1,updated_at=excluded.updated_at`, row.Occurrence.ID, id, row.RuleVersion, row.BaseDate, row.ExpectedStart, now, now)
					if e != nil {
						return CommandResult{}, e
					}
					baseline.StartDate = next.StartDate
					baseline.DueDate = next.DueDate
					baseline.Revision = next.Revision
					baseline.UpdatedAt = now
					local = true
				}
				if local {
					b, e := json.Marshal(baseline)
					if e != nil {
						return CommandResult{}, e
					}
					if _, e = tx.Exec(`UPDATE project_instances SET baseline_json=? WHERE project_id=?`, string(b), row.Project.ID); e != nil {
						return CommandResult{}, e
					}
					changed = true
				}
			}
			if changed {
				if _, e = tx.Exec(`UPDATE series SET revision=revision+1,updated_at=? WHERE id=?`, now, id); e != nil {
					return CommandResult{}, e
				}
				if e = event(tx, "series", id, "schedule_dates_repaired", in.RequestID, now, nil, p); e != nil {
					return CommandResult{}, e
				}
			}
			v, e := getSeries(tx, id)
			return CommandResult{Series: &v}, e
		})
		out = result
		return e
	})
	return 200, out, e
}
