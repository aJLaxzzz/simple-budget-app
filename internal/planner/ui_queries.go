package planner

import (
	"context"
	"database/sql"
	"net/http"
)

// OccurrenceContext gives the UI one consistent snapshot for optimistic commands.
func (s *Service) OccurrenceContext(ctx context.Context, id string) (CommandResult, error) {
	var result CommandResult
	err := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		o, e := getOccurrence(tx, id)
		if e != nil {
			return e
		}
		v, e := getSeries(tx, o.SeriesID)
		if e != nil {
			return e
		}
		result.Occurrence = &o
		result.Series = &v
		return nil
	})
	return result, err
}
func occurrenceContextRoute(s *Service, w http.ResponseWriter, r *http.Request, id string) (int, any, error) {
	if r.Method != "GET" {
		return 0, nil, methodError(w, "GET")
	}
	v, e := s.OccurrenceContext(r.Context(), id)
	return 200, v, e
}
func seriesSummary(tx *sql.Tx, v *SeriesDTO) error {
	if v.TemplateVersionID != nil {
		if e := tx.QueryRow(`SELECT template_id FROM template_versions WHERE id=?`, *v.TemplateVersionID).Scan(&v.TemplateID); e != nil {
			return e
		}
	}
	return tx.QueryRow(`SELECT
 (SELECT min(COALESCE(t.planned_date,p.start_date,o.manual_date,o.adjusted_date)) FROM occurrences o LEFT JOIN tasks t ON t.occurrence_id=o.id LEFT JOIN projects p ON p.occurrence_id=o.id WHERE o.series_id=? AND o.dynamic=0 AND (t.status IN('planned','in_progress') OR p.status IN('planned','in_progress'))),
 (SELECT max(t.completed_date) FROM tasks t JOIN occurrences o ON o.id=t.occurrence_id WHERE o.series_id=? AND t.status='completed'),
 (SELECT count(DISTINCT occurrence_id) FROM occurrence_incidents i JOIN occurrences o ON o.id=i.occurrence_id WHERE o.series_id=? AND i.disposition='needs_decision')`, v.ID, v.ID, v.ID).Scan(&v.NextDate, &v.LastCompletedDate, &v.UnresolvedCount)
}

func templateProjectsRoute(s *Service, w http.ResponseWriter, r *http.Request, id string) (int, any, error) {
	if r.Method != "GET" {
		return 0, nil, methodError(w, "GET")
	}
	items := []map[string]any{}
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		rows, e := tx.Query(`SELECT p.id,p.title,p.start_date,p.status FROM projects p JOIN template_versions v ON v.id=p.template_version_id WHERE v.template_id=? ORDER BY p.created_at DESC LIMIT 500`, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id, title, status string
			var date *string
			if e = rows.Scan(&id, &title, &date, &status); e != nil {
				return e
			}
			items = append(items, map[string]any{"id": id, "title": title, "start_date": date, "status": status})
		}
		return rows.Err()
	})
	return 200, map[string]any{"items": items, "limit": 500}, e
}

func upcomingOccurrences(tx *sql.Tx, id, today string) ([]TaskOccurrenceDTO, error) {
	rows, e := tx.Query(`SELECT o.id FROM occurrence_current o LEFT JOIN tasks t ON t.occurrence_id=o.id LEFT JOIN projects p ON p.occurrence_id=o.id WHERE o.series_id=? AND COALESCE(t.status,p.status) IN('planned','in_progress') AND COALESCE(t.planned_date,p.start_date,o.current_adjusted_date)>=? ORDER BY COALESCE(t.planned_date,p.start_date,o.current_adjusted_date),o.cycle_index LIMIT 5`, id, today)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	items := []TaskOccurrenceDTO{}
	for _, id := range ids {
		v, e := getOccurrence(tx, id)
		if e != nil {
			return nil, e
		}
		items = append(items, v)
	}
	return items, nil
}
