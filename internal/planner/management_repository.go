package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"encoding/json"
	"errors"
)

func getOccurrence(tx *sql.Tx, id string) (TaskOccurrenceDTO, error) {
	var o TaskOccurrenceDTO
	var taskID, projectID, state, outcome sql.NullString
	e := tx.QueryRow(`SELECT o.id,o.revision,o.created_at,o.updated_at,o.series_id,o.current_rule_version_id,o.cycle_index,o.current_base_date,o.current_adjusted_date,o.manual_date,t.id,o.rule_version_id,o.base_date,o.adjusted_date,o.manual_override,s.source_type,p.id,o.project_state,o.outcome,v.template_version_id FROM occurrence_current o JOIN series s ON s.id=o.series_id JOIN series_versions v ON v.id=o.current_rule_version_id LEFT JOIN tasks t ON t.occurrence_id=o.id LEFT JOIN projects p ON p.occurrence_id=o.id WHERE o.id=?`, id).Scan(&o.ID, &o.Revision, &o.CreatedAt, &o.UpdatedAt, &o.SeriesID, &o.RuleVersionID, &o.CycleIndex, &o.BaseDate, &o.AdjustedDate, &o.ManualDate, &taskID, &o.OriginalRuleVersionID, &o.OriginalBaseDate, &o.OriginalAdjustedDate, &o.ManualOverride, &o.SourceType, &projectID, &state, &outcome, &o.TemplateVersionID)
	if errors.Is(e, sql.ErrNoRows) {
		return o, notFound("Экземпляр", id)
	}
	if e != nil {
		return o, e
	}
	var detached string
	err := tx.QueryRow("SELECT snapshot_json FROM detached_occurrences WHERE occurrence_id=?", id).Scan(&detached)
	if err == nil {
		if o.SourceType == "project" {
			err = json.Unmarshal([]byte(detached), &o.Project)
		} else {
			err = json.Unmarshal([]byte(detached), &o.Task)
		}
		return o, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return o, err
	}
	if o.SourceType == "project" {
		o.Compact = state.String == "compact"
		o.ProjectStatus = outcome.String
		if projectID.Valid {
			p, e := projectWithTree(tx, projectID.String)
			if e != nil {
				return o, e
			}
			o.Project = &p
			o.TemplateVersionID = p.TemplateVersionID
			o.ProjectStatus = p.Status
		}
		return o, nil
	}
	o.Task, e = getTask(tx, taskID.String)
	return o, e
}
func loadPauses(tx *sql.Tx, id string) ([]PauseDTO, error) {
	rows, e := tx.Query(`SELECT id,revision,created_at,updated_at,series_id,start_date,end_date,reason,resumed_on FROM series_pauses WHERE series_id=? ORDER BY start_date,id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []PauseDTO{}
	for rows.Next() {
		var p PauseDTO
		if e = rows.Scan(&p.ID, &p.Revision, &p.CreatedAt, &p.UpdatedAt, &p.SeriesID, &p.StartDate, &p.EndDate, &p.Reason, &p.ResumedOn); e != nil {
			return nil, e
		}
		items = append(items, p)
	}
	return items, rows.Err()
}
func pauseOn(pauses []PauseDTO, date string) *PauseDTO {
	for i := range pauses {
		p := &pauses[i]
		if date >= p.StartDate && (p.EndDate == nil || date <= *p.EndDate) && (p.ResumedOn == nil || date < *p.ResumedOn) {
			return p
		}
	}
	return nil
}
func decorateSeries(tx *sql.Tx, v *SeriesDTO) error {
	var e error
	v.Pauses, e = loadPauses(tx, v.ID)
	if e != nil {
		return e
	}
	rows, e := tx.Query(`SELECT s.version_id,s.from_cycle,s.until_cycle,v.rule_json,v.template_version_id,v.source_snapshot_json FROM series_version_scopes s JOIN series_versions v ON v.id=s.version_id WHERE s.series_id=? ORDER BY v.version`, v.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	v.Versions = []VersionScopeDTO{}
	for rows.Next() {
		var x VersionScopeDTO
		var raw, snapshot string
		if e = rows.Scan(&x.VersionID, &x.FromCycle, &x.UntilCycle, &raw, &x.TemplateVersionID, &snapshot); e != nil {
			return e
		}
		if e = json.Unmarshal([]byte(raw), &x.Rule); e != nil {
			return e
		}
		if v.SourceType == "project" {
			if e = json.Unmarshal([]byte(snapshot), &x.ProjectTemplate); e != nil {
				return e
			}
		}
		v.Versions = append(v.Versions, x)
	}
	return rows.Err()
}
func versionForCycle(tx *sql.Tx, v SeriesDTO, index int64) (SeriesDTO, error) {
	var rule, snapshot string
	e := tx.QueryRow(`SELECT v.id,v.rule_json,v.source_snapshot_json,v.template_version_id FROM series_version_scopes s JOIN series_versions v ON v.id=s.version_id WHERE s.series_id=? AND s.from_cycle<=? AND (s.until_cycle IS NULL OR s.until_cycle>?)`, v.ID, index, index).Scan(&v.RuleVersionID, &rule, &snapshot, &v.TemplateVersionID)
	if errors.Is(e, sql.ErrNoRows) {
		return v, invalid("Нет версии правила для номера цикла")
	}
	if e != nil {
		return v, e
	}
	if e = json.Unmarshal([]byte(rule), &v.Rule); e != nil {
		return v, e
	}
	if v.SourceType == "project" {
		e = json.Unmarshal([]byte(snapshot), &v.ProjectTemplate)
	} else {
		e = json.Unmarshal([]byte(snapshot), &v.Template)
	}
	return v, e
}
func insertVersion(tx *sql.Tx, v SeriesDTO, rule StoredTaskRule, now string) (string, error) {
	id, e := operation()
	if e != nil {
		return "", e
	}
	var number int
	if e = tx.QueryRow("SELECT COALESCE(max(version),0)+1 FROM series_versions WHERE series_id=?", v.ID).Scan(&number); e != nil {
		return "", e
	}
	raw, e := json.Marshal(rule)
	if e != nil {
		return "", e
	}
	var source any = v.Template
	if v.SourceType == "project" {
		source = v.ProjectTemplate
	}
	snapshot, e := json.Marshal(source)
	if e != nil {
		return "", e
	}
	effective := string(rule.FirstDate)
	if rule.Rule.Mode == recurrence.Completion {
		effective = "0001-01-01"
	}
	_, e = tx.Exec(`INSERT INTO series_versions(id,series_id,version,effective_from,rule_json,source_snapshot_json,created_at,template_version_id) VALUES(?,?,?,?,?,?,?,?)`, id, v.ID, number, effective, string(raw), string(snapshot), now, v.TemplateVersionID)
	if e != nil {
		return "", e
	}
	// Retired scopes may be empty. Historical payloads/occurrence provenance remain.
	_, e = tx.Exec(`UPDATE series_version_scopes SET until_cycle=CASE WHEN from_cycle>=? THEN from_cycle ELSE ? END WHERE series_id=? AND (until_cycle IS NULL OR until_cycle>?)`, rule.FirstCycle, rule.FirstCycle, v.ID, rule.FirstCycle)
	if e != nil {
		return "", e
	}
	_, e = tx.Exec(`INSERT INTO series_version_scopes(version_id,series_id,from_cycle) VALUES(?,?,?)`, id, v.ID, rule.FirstCycle)
	return id, e
}
func syncAttention(tx *sql.Tx, occ, now string) error {
	var n int
	if e := tx.QueryRow(`SELECT count(*) FROM occurrence_incidents WHERE occurrence_id=? AND disposition='needs_decision'`, occ).Scan(&n); e != nil {
		return e
	}
	disposition := "processed"
	if n > 0 {
		disposition = "needs_decision"
	}
	id, e := operation()
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO attention_state(id,occurrence_id,disposition,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(occurrence_id) DO UPDATE SET disposition=excluded.disposition,revision=attention_state.revision+1,updated_at=excluded.updated_at WHERE attention_state.disposition!=excluded.disposition`, id, occ, disposition, now, now)
	return e
}
func registerIncident(tx *sql.Tx, task TaskDTO, kind, source, reason string, pauseID *string, op, now string) error {
	if task.OccurrenceID == nil {
		return invalid("Пропуск должен относиться к циклу серии")
	}
	o, e := getOccurrence(tx, *task.OccurrenceID)
	if e != nil {
		return e
	}
	return registerOccurrenceIncident(tx, o, task.PlannedDate, kind, source, reason, pauseID, op, now)
}
func registerOccurrenceIncident(tx *sql.Tx, o TaskOccurrenceDTO, planned *string, kind, source, reason string, pauseID *string, op, now string) error {
	id, e := operation()
	if e != nil {
		return e
	}
	date := o.AdjustedDate
	if planned != nil {
		date = *planned
	}
	disposition, resolution := "needs_decision", "pending"
	var reviewed *string
	if kind == "skipped" && source == "user" {
		disposition, resolution = "processed", "acknowledged"
		reviewed = &now
	}
	if kind == "cancelled" {
		disposition, resolution = "processed", "cancelled"
		reviewed = &now
	}
	if _, e = tx.Exec(`INSERT INTO occurrence_incidents(id,occurrence_id,kind,source,pause_id,planned_date,reason,disposition,resolution,reviewed_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, o.ID, kind, source, pauseID, date, reason, disposition, resolution, reviewed, now, now); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE occurrences SET outcome=?,reason=?,revision=revision+1,updated_at=? WHERE id=?`, kind, reason, now, o.ID); e != nil {
		return e
	}
	if e = eventAs(tx, "occurrence", o.ID, "incident_registered", map[bool]string{true: "system", false: "owner"}[source == "pause"], op, now, nil, map[string]any{"incident_id": id, "kind": kind, "source": source, "planned_date": date, "reason": reason, "pause_id": pauseID, "disposition": disposition}); e != nil {
		return e
	}
	return syncAttention(tx, o.ID, now)
}
func resolveIncidents(tx *sql.Tx, occ, resolution, op, now string) error {
	_, e := tx.Exec(`UPDATE occurrence_incidents SET disposition='processed',resolution=?,reviewed_at=?,revision=revision+1,updated_at=? WHERE occurrence_id=? AND disposition='needs_decision'`, resolution, now, now, occ)
	if e != nil {
		return e
	}
	if e = event(tx, "occurrence", occ, "incident_"+resolution, op, now, nil, map[string]any{"resolution": resolution}); e != nil {
		return e
	}
	return syncAttention(tx, occ, now)
}
func incidentList(tx *sql.Tx, series, kind, disposition string, limit, offset int) ([]IncidentDTO, error) {
	rows, e := tx.Query(`SELECT i.id,i.revision,i.created_at,i.updated_at,i.occurrence_id,o.series_id,o.cycle_index,COALESCE(t.id,''),COALESCE(t.status,''),i.kind,i.source,i.pause_id,i.planned_date,i.reason,i.disposition,i.resolution,i.reviewed_at,p.id,COALESCE(p.status,o.outcome,''),s.source_type FROM occurrence_incidents i JOIN occurrences o ON o.id=i.occurrence_id JOIN series s ON s.id=o.series_id LEFT JOIN tasks t ON t.occurrence_id=o.id LEFT JOIN projects p ON p.occurrence_id=o.id WHERE (?='' OR o.series_id=?) AND (?='' OR i.kind=?) AND (?='' OR i.disposition=?) ORDER BY i.created_at DESC,i.id LIMIT ? OFFSET ?`, series, series, kind, kind, disposition, disposition, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []IncidentDTO{}
	for rows.Next() {
		var x IncidentDTO
		if e = rows.Scan(&x.ID, &x.Revision, &x.CreatedAt, &x.UpdatedAt, &x.OccurrenceID, &x.SeriesID, &x.CycleIndex, &x.TaskID, &x.TaskStatus, &x.Kind, &x.Source, &x.PauseID, &x.PlannedDate, &x.Reason, &x.Disposition, &x.Resolution, &x.ReviewedAt, &x.ProjectID, &x.ProjectStatus, &x.SourceType); e != nil {
			return nil, e
		}
		if x.SourceType != "project" {
			x.ProjectStatus = ""
		}
		items = append(items, x)
	}
	return items, rows.Err()
}

// Bound only the actual change set, not the full lifetime of a long-lived series.
func managementOccurrences(tx *sql.Tx, v SeriesDTO, in SeriesCommand, today string) ([]TaskOccurrenceDTO, int, error) {
	where := "o.series_id=?"
	args := []any{v.ID}
	switch in.Action {
	case "change_rule":
		where += " AND o.cycle_index>=? AND (o.dynamic=1 OR t.planned_date IS NULL OR t.planned_date>=? OR t.status='in_progress')"
		args = append(args, *in.FromCycle, today)
	case "pause":
		where += " AND (o.current_adjusted_date BETWEEN ? AND ? OR ((o.manual_override=1 OR o.manual_date IS NOT NULL) AND t.planned_date BETWEEN ? AND ?))"
		args = append(args, in.StartDate, in.EndDate, in.StartDate, in.EndDate)
	case "reanchor":
		where += " AND o.dynamic=1 AND t.status IN ('planned','in_progress')"
	case "resume":
		where += " AND o.current_adjusted_date>=? AND EXISTS(SELECT 1 FROM occurrence_incidents i WHERE i.occurrence_id=o.id AND i.pause_id=?)"
		args = append(args, today, in.PauseID)
	default:
		where += " AND o.cycle_index>=?"
		args = append(args, v.NextCycle-1)
	}
	rows, e := tx.Query("SELECT o.id FROM occurrence_current o JOIN tasks t ON t.occurrence_id=o.id WHERE "+where+" ORDER BY o.cycle_index LIMIT 1001", args...)
	if e != nil {
		return nil, 0, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, 0, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, 0, e
	}
	if len(ids) > 1000 {
		return nil, 0, invalid("Изменение затрагивает более 1000 экземпляров; сузьте область изменения")
	}
	var total int
	if e = tx.QueryRow("SELECT count(*) FROM occurrences WHERE series_id=?", v.ID).Scan(&total); e != nil {
		return nil, 0, e
	}
	items := []TaskOccurrenceDTO{}
	for _, id := range ids {
		o, e := getOccurrence(tx, id)
		if e != nil {
			return nil, 0, e
		}
		items = append(items, o)
	}
	return items, total - len(ids), nil
}
