package planner

import (
	"database/sql"
	"errors"
)

const sessionColumns = "id,revision,created_at,updated_at,task_id,date,start_time,end_time,timezone,status,completed_date,completed_at"

func scanSession(row rowScanner) (SessionDTO, error) {
	var s SessionDTO
	e := row.Scan(&s.ID, &s.Revision, &s.CreatedAt, &s.UpdatedAt, &s.TaskID, &s.Date, &s.StartTime, &s.EndTime, &s.Timezone, &s.Status, &s.CompletedDate, &s.CompletedAt)
	return s, e
}
func getSession(tx *sql.Tx, id string) (SessionDTO, error) {
	s, e := scanSession(tx.QueryRow("SELECT "+sessionColumns+" FROM sessions WHERE id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		e = notFound("Сессия", id)
	}
	return s, e
}
func listSessions(tx *sql.Tx, where string, args ...any) ([]SessionDTO, error) {
	rows, e := tx.Query("SELECT "+sessionColumns+" FROM sessions "+where+" ORDER BY date,start_time,id", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []SessionDTO{}
	for rows.Next() {
		s, e := scanSession(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, s)
	}
	return items, rows.Err()
}
func insertSession(tx *sql.Tx, s SessionDTO) error {
	_, e := tx.Exec("INSERT INTO sessions(id,task_id,date,start_time,end_time,timezone,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", s.ID, s.TaskID, s.Date, s.StartTime, s.EndTime, s.Timezone, s.Status, s.CreatedAt, s.UpdatedAt)
	return e
}
func saveSession(tx *sql.Tx, s SessionDTO) error {
	_, e := tx.Exec("UPDATE sessions SET date=?,start_time=?,end_time=?,timezone=?,status=?,completed_date=?,completed_at=?,revision=revision+1,updated_at=? WHERE id=?", s.Date, s.StartTime, s.EndTime, s.Timezone, s.Status, s.CompletedDate, s.CompletedAt, s.UpdatedAt, s.ID)
	return e
}
