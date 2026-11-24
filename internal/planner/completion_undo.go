package planner

import (
	"context"
	"database/sql"
	"encoding/json"
)

type CompletionUndo struct {
	Signature string    `json:"signature"`
	Before    SeriesDTO `json:"before"`
}

func completionSignature(tx *sql.Tx, id string) (string, error) {
	p, e := deletionPlanTx(tx, "series", id)
	if e != nil {
		return "", e
	}
	return p.PreviewToken, nil
}
func (s *Service) undoCompletionTx(ctx context.Context, tx *sql.Tx, o TaskOccurrenceDTO, v SeriesDTO, in OccurrenceCommand, op, now string) (CommandResult, error) {
	var raw string
	if e := tx.QueryRow("SELECT response_json FROM planner_commands WHERE request_id=? AND target=? AND action='complete'", in.CompletionRequestID, "occurrence/"+o.ID).Scan(&raw); e != nil {
		return CommandResult{}, invalid("Не найдено подтверждённое завершение для отмены")
	}
	var previous CommandResult
	if e := json.Unmarshal([]byte(raw), &previous); e != nil {
		return CommandResult{}, e
	}
	if previous.Undo == nil {
		return CommandResult{}, conflict("undo_requires_decision", "Это завершение требует отдельного восстановления с проверкой связанных задач", o)
	}
	signature, e := completionSignature(tx, v.ID)
	if e != nil {
		return CommandResult{}, e
	}
	if signature != previous.Undo.Signature {
		return CommandResult{}, conflict("undo_changed", "После завершения повторение изменилось. Восстановите выполнение с отдельной проверкой продолжения.", v)
	}
	// Demote the promoted row before restoring the previous current task. Unique
	// current-cycle constraints stay enabled throughout this transaction.
	if v.WaitingOccurrenceID != nil {
		if _, e = tx.Exec("UPDATE occurrences SET pending_completion=0,dynamic=1,revision=revision+1,updated_at=? WHERE id=?", now, *v.WaitingOccurrenceID); e != nil {
			return CommandResult{}, e
		}
		if _, e = tx.Exec("UPDATE tasks SET revision=revision+1,updated_at=? WHERE occurrence_id=?", now, *v.WaitingOccurrenceID); e != nil {
			return CommandResult{}, e
		}
	}
	old := previous.Undo.Before
	v.AnchorDate = old.AnchorDate
	v.AnchorCycle = old.AnchorCycle
	v.AnchorKind = old.AnchorKind
	v.WaitingOccurrenceID = nil
	v.State = "active"
	if e = saveSeriesRuntime(tx, v, now); e != nil {
		return CommandResult{}, e
	}
	var task TaskDTO
	if e = s.changeTaskTx(ctx, tx, o.Task.ID, "restore", TaskInput{Mutation: Mutation{ExpectedRevision: o.Task.Revision}}, op, now, s.now(), &task, taskChangeOptions{AllowRestore: true}); e != nil {
		return CommandResult{}, e
	}
	if e = event(tx, "series", v.ID, "completion_undone", op, now, nil, map[string]any{"occurrence_id": o.ID, "completion_request_id": in.CompletionRequestID}); e != nil {
		return CommandResult{}, e
	}
	fresh, e := getOccurrence(tx, o.ID)
	return CommandResult{Occurrence: &fresh}, e
}
