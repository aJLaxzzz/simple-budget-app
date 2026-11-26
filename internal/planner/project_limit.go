package planner

import (
	"database/sql"
	"net/http"
	"strconv"
)

// Stored in the existing settings table: no schema or user-data migration.
type ProjectRepeatLimit struct {
	Limit    int   `json:"limit"`
	Revision int64 `json:"revision"`
}

func projectRepeatLimit(tx *sql.Tx) (ProjectRepeatLimit, error) {
	v := ProjectRepeatLimit{Limit: 3}
	err := tx.QueryRow(`SELECT value,revision FROM planner_settings WHERE key='project_repeat_limit'`).Scan(&v.Limit, &v.Revision)
	if err == sql.ErrNoRows {
		return v, nil
	}
	return v, err
}

// Count projects, not their tasks. A multi-day project stays current through
// its deadline, or (without one) its last planned task/start date. Overdue open
// projects remain visible but do not consume the forward-looking quota.
func (s *Service) projectRepeatRoom(tx *sql.Tx, v SeriesDTO) (bool, error) {
	limit, err := projectRepeatLimit(tx)
	if err != nil {
		return false, err
	}
	var count int
	err = tx.QueryRow(`SELECT count(*) FROM projects p JOIN project_repeat_projection r ON r.id=p.id
 WHERE (r.series_id=? OR p.id=?) AND p.status IN ('planned','in_progress') AND p.archived_at IS NULL
 AND COALESCE(p.due_date,(SELECT max(COALESCE(t.due_date,t.planned_date)) FROM tasks t WHERE t.project_id=p.id),p.start_date,'9999-12-31')>=?`, v.ID, v.SourceProjectID, s.today()).Scan(&count)
	return count < limit.Limit, err
}

func projectLimitRoute(s *Service, w http.ResponseWriter, r *http.Request) (int, any, error) {
	var out ProjectRepeatLimit
	var in struct {
		Limit            int   `json:"limit"`
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if r.Method != "GET" && r.Method != "PUT" {
		return 0, nil, methodError(w, "GET, PUT")
	}
	if r.Method == "PUT" {
		if err := strictPlannerInput(w, r, &in, "limit expected_revision"); err != nil {
			return 0, nil, err
		}
		if in.Limit < 1 || in.Limit > 20 {
			return 0, nil, invalid("Число ближайших проектов должно быть от 1 до 20")
		}
	}
	err := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		var err error
		out, err = projectRepeatLimit(tx)
		if err != nil || r.Method == "GET" {
			return err
		}
		if in.ExpectedRevision != out.Revision {
			return conflict("revision_conflict", "Настройка изменилась. Обновите страницу перед сохранением.", out)
		}
		if in.Limit == out.Limit {
			return nil
		}
		_, err = tx.Exec(`INSERT INTO planner_settings(key,value) VALUES('project_repeat_limit',?)
 ON CONFLICT(key) DO UPDATE SET value=excluded.value,revision=planner_settings.revision+1,updated_at=?`, strconv.Itoa(in.Limit), stamp(s.now()))
		if err != nil {
			return err
		}
		out, err = projectRepeatLimit(tx)
		return err
	})
	return 200, out, err
}
