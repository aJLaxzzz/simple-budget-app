package planner

import (
	"database/sql"
	"encoding/json"
	"errors"
)

func getSeries(tx *sql.Tx, id string) (SeriesDTO, error) {
	var s SeriesDTO
	var rule, snapshot string
	e := tx.QueryRow(`SELECT s.id,s.revision,s.created_at,s.updated_at,s.source_type,s.anchor_mode,s.state,v.id,v.rule_json,v.source_snapshot_json,r.next_cycle,r.anchor_date,r.anchor_cycle,r.waiting_occurrence_id,r.horizon_date,r.anchor_kind,v.template_version_id,r.end_date
 FROM series s JOIN series_runtime r ON r.series_id=s.id JOIN series_versions v ON v.series_id=s.id
 WHERE s.id=? ORDER BY v.version DESC LIMIT 1`, id).Scan(&s.ID, &s.Revision, &s.CreatedAt, &s.UpdatedAt, &s.SourceType, &s.AnchorMode, &s.State, &s.RuleVersionID, &rule, &snapshot, &s.NextCycle, &s.AnchorDate, &s.AnchorCycle, &s.WaitingOccurrenceID, &s.HorizonDate, &s.AnchorKind, &s.TemplateVersionID, &s.EndDate)
	if errors.Is(e, sql.ErrNoRows) {
		return s, notFound("Серия задач", id)
	}
	if e != nil {
		return s, e
	}
	if e = json.Unmarshal([]byte(rule), &s.Rule); e != nil {
		return s, e
	}
	if s.SourceType == "project" {
		e = json.Unmarshal([]byte(snapshot), &s.ProjectTemplate)
	} else {
		e = json.Unmarshal([]byte(snapshot), &s.Template)
	}
	return s, e
}
func saveSeriesRuntime(tx *sql.Tx, s SeriesDTO, now string) error {
	_, e := tx.Exec(`UPDATE series_runtime SET next_cycle=?,anchor_date=?,anchor_cycle=?,waiting_occurrence_id=?,horizon_date=?,anchor_kind=? WHERE series_id=?`, s.NextCycle, s.AnchorDate, s.AnchorCycle, s.WaitingOccurrenceID, s.HorizonDate, s.AnchorKind, s.ID)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`UPDATE series SET state=?,revision=revision+1,updated_at=? WHERE id=?`, s.State, now, s.ID)
	return e
}
func seriesOccurrences(tx *sql.Tx, id string, limit, offset int) ([]TaskOccurrenceDTO, bool, error) {
	rows, e := tx.Query("SELECT id FROM occurrences WHERE series_id=? ORDER BY cycle_index LIMIT ? OFFSET ?", id, limit+1, offset)
	if e != nil {
		return nil, false, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, false, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, false, e
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	items := []TaskOccurrenceDTO{}
	for _, id := range ids {
		o, e := getOccurrence(tx, id)
		if e != nil {
			return nil, false, e
		}
		items = append(items, o)
	}
	return items, more, nil
}
func seriesIDs(tx *sql.Tx, after string, limit int) ([]string, error) {
	rows, e := tx.Query(`SELECT s.id FROM series s JOIN series_runtime r ON r.series_id=s.id WHERE s.id>? ORDER BY s.id LIMIT ?`, after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
