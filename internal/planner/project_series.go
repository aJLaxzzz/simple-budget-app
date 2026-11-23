package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *Service) createProjectSeries(ctx context.Context, in CreateSeriesInput) (SeriesDTO, error) {
	var result SeriesDTO
	if in.Task != nil || in.ExistingTaskID != "" || in.TemplateID == "" {
		return result, invalid("Серия проекта требует template_id и не принимает task")
	}
	if in.Rule.Mode != recurrence.Schedule {
		return result, invalid("Повторяющиеся проекты поддерживают только schedule")
	}
	rule, e := recurrence.NormalizeRule(in.Rule)
	if e != nil {
		return result, recurrenceAPIError(e)
	}
	if e = managementDate(in.FirstDate); e != nil {
		return result, e
	}
	now := stamp(s.now())
	op, e := operation()
	if e != nil {
		return result, e
	}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		r, e := commandOnce(tx, in.RequestID, "series/create", "create_project", now, in, func() (CommandResult, error) {
			return s.createProjectSeriesTx(ctx, tx, in, rule, op, now)
		})
		if r.Series != nil {
			result = *r.Series
		}
		return e
	})
	return result, recurrenceAPIError(e)
}

// Shared by direct creation and the atomic existing-project workflow.
func (s *Service) createProjectSeriesTx(ctx context.Context, tx *sql.Tx, in CreateSeriesInput, rule recurrence.Rule, op, now string) (CommandResult, error) {
	var prior string
	e := tx.QueryRow("SELECT id FROM series WHERE request_key=?", in.RequestID).Scan(&prior)
	if e == nil {
		return CommandResult{}, conflict("idempotency_conflict", "Ключ уже использован для создания серии", nil)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return CommandResult{}, e
	}
	template, e := getTemplate(tx, in.TemplateID, in.TemplateVersionID)
	if e != nil {
		return CommandResult{}, e
	}
	if template.Archived {
		return CommandResult{}, invalid("Шаблон архивирован")
	}
	if e = checkRevision(in.ExpectedRevision, template.RecordMeta); e != nil {
		return CommandResult{}, e
	}
	snapshot, e := normalizeTemplate(template.Snapshot)
	if e != nil {
		return CommandResult{}, e
	}
	id, e := operation()
	if e != nil {
		return CommandResult{}, e
	}
	hash, e := digest(in)
	if e != nil {
		return CommandResult{}, e
	}
	_, e = tx.Exec(`INSERT INTO series(id,source_type,anchor_mode,request_key,request_hash,created_at,updated_at) VALUES(?,'project','schedule',?,?,?,?)`, id, in.RequestID, hash, now, now)
	if e != nil {
		return CommandResult{}, e
	}
	v := SeriesDTO{EndDate: in.EndDate, RecordMeta: RecordMeta{ID: id, Revision: 1, CreatedAt: now, UpdatedAt: now}, SourceType: "project", AnchorMode: "schedule", State: "active", AnchorKind: recurrence.CompletedAnchor, TemplateVersionID: &template.VersionID, ProjectTemplate: &snapshot, NextCycle: 1, HorizonDate: s.horizon(), Rule: StoredTaskRule{Rule: rule, FirstDate: recurrence.Date(in.FirstDate), Anchor: recurrence.Anchor{Date: recurrence.Date(in.FirstDate), Kind: recurrence.ScheduleAnchor}}}
	version, e := insertVersion(tx, v, v.Rule, now)
	if e != nil {
		return CommandResult{}, e
	}
	v.RuleVersionID = version
	c, cal, e := calculateStoredCycle(ctx, tx, v, 0, nil)
	if e != nil {
		return CommandResult{}, e
	}
	if v.EndDate != nil && string(c.AdjustedDate) > *v.EndDate {
		return CommandResult{}, invalid("Первый проект позже окончания серии")
	}
	if _, e = s.insertProjectCycle(ctx, tx, &v, c, cal, nil, op, now); e != nil {
		return CommandResult{}, e
	}
	_, e = tx.Exec(`INSERT INTO series_runtime(series_id,next_cycle,anchor_cycle,horizon_date,end_date) VALUES(?,1,0,?,?)`, id, v.HorizonDate, v.EndDate)
	if e != nil {
		return CommandResult{}, e
	}
	if e = event(tx, "series", id, "created", op, now, nil, v); e != nil {
		return CommandResult{}, e
	}
	if _, _, e = s.materializeSeriesTx(ctx, tx, &v, op, now); e != nil {
		return CommandResult{}, e
	}
	v, e = getSeries(tx, id)
	if e != nil {
		return CommandResult{}, e
	}
	if e = decorateSeries(tx, &v); e != nil {
		return CommandResult{}, e
	}
	v.Occurrences, v.HasMore, e = seriesOccurrences(tx, id, 100, 0)
	return CommandResult{Series: &v}, e
}
func (s *Service) insertProjectCycle(ctx context.Context, tx *sql.Tx, v *SeriesDTO, c recurrence.Candidate, cal []recurrence.CalendarInfo, pause *PauseDTO, op, now string) (TaskOccurrenceDTO, error) {
	var result TaskOccurrenceDTO
	if v.ProjectTemplate == nil || v.TemplateVersionID == nil {
		return result, invalid("У версии серии отсутствует шаблон проекта")
	}
	id, e := operation()
	if e != nil {
		return result, e
	}
	raw, e := json.Marshal(map[string]any{"candidate": c, "calendars": cal})
	if e != nil {
		return result, e
	}
	state := "materialized"
	var link *string = &id
	var outcome *string
	if pause != nil {
		state = "compact"
		link = nil
		x := "skipped"
		outcome = &x
	}
	_, e = tx.Exec(`INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,calculation_json,project_state,project_link,outcome,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, v.ID, v.RuleVersionID, c.CycleIndex, c.BaseDate, c.AdjustedDate, string(raw), state, link, outcome, now, now)
	if e != nil {
		return result, e
	}
	if pause == nil {
		if _, e = s.instantiateProjectTx(tx, *v.ProjectTemplate, *v.TemplateVersionID, string(c.AdjustedDate), &id, op, now, "system"); e != nil {
			return result, e
		}
	}
	result, e = getOccurrence(tx, id)
	if e != nil {
		return result, e
	}
	if pause != nil {
		reason := pause.Reason
		if reason == "" {
			reason = "Пропуск проекта во время паузы"
		}
		if e = registerOccurrenceIncident(tx, result, nil, "skipped", "pause", reason, &pause.ID, op, now); e != nil {
			return result, e
		}
	}
	if e = eventAs(tx, "occurrence", id, "project_cycle_created", "system", op, now, nil, result); e != nil {
		return result, e
	}
	return getOccurrence(tx, id)
}
func projectOccurrenceVersion(tx *sql.Tx, v SeriesDTO, o TaskOccurrenceDTO) (SeriesDTO, error) {
	var raw, snapshot string
	e := tx.QueryRow("SELECT rule_json,source_snapshot_json,template_version_id FROM series_versions WHERE id=? AND series_id=?", o.RuleVersionID, v.ID).Scan(&raw, &snapshot, &v.TemplateVersionID)
	if e != nil {
		return v, e
	}
	v.RuleVersionID = o.RuleVersionID
	e = json.Unmarshal([]byte(raw), &v.Rule)
	if e == nil {
		e = json.Unmarshal([]byte(snapshot), &v.ProjectTemplate)
	}
	return v, e
}
func (s *Service) restoreCompactProject(tx *sql.Tx, v SeriesDTO, o TaskOccurrenceDTO, date, op, now string) (TaskOccurrenceDTO, error) {
	selected, e := projectOccurrenceVersion(tx, v, o)
	if e != nil {
		return o, e
	}
	if selected.TemplateVersionID == nil || selected.ProjectTemplate == nil {
		return o, invalid("Отсутствует версия шаблона пропуска")
	}
	_, e = tx.Exec("UPDATE occurrences SET project_link=id,project_state='materialized',outcome=NULL,revision=revision+1,updated_at=? WHERE id=? AND project_state='compact'", now, o.ID)
	if e != nil {
		return o, e
	}
	if _, e = s.instantiateProjectTx(tx, *selected.ProjectTemplate, *selected.TemplateVersionID, date, &o.ID, op, now, "owner"); e != nil {
		return o, e
	}
	return getOccurrence(tx, o.ID)
}
