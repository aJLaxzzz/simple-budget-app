package planner

import "database/sql"

// Completion is an explicit acknowledgement of finished work, never a cascade.
// Parents count too: completing a child must not implicitly complete its parent.
func requireProjectComplete(tx *sql.Tx, id string) error {
	var total, remaining int
	if err := tx.QueryRow(`SELECT count(*), COALESCE(sum(CASE WHEN status='completed' THEN 0 ELSE 1 END),0) FROM tasks WHERE project_id=?`, id).Scan(&total, &remaining); err != nil {
		return err
	}
	if total == 0 {
		return invalid("В пустом проекте нет выполненных задач. Сначала добавьте и выполните задачи")
	}
	if remaining != 0 {
		return invalid("Сначала выполните все задачи и подзадачи проекта. Отменённые и пропущенные задачи не считаются выполненными")
	}
	return nil
}
