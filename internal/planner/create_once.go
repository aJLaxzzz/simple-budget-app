package planner

import (
	"database/sql"
	"encoding/json"
)

// createOnce shares the durable command ledger with recurrence operations.
// Omitting a key preserves the legacy API; the UI always supplies one for creates.
func createOnce[T any](tx *sql.Tx, key, kind, now string, input any, run func() (T, error)) (T, error) {
	if key == "" {
		return run()
	}
	var value T
	result, e := commandOnce(tx, key, kind, "create", now, input, func() (CommandResult, error) {
		v, e := run()
		if e != nil {
			return CommandResult{}, e
		}
		raw, e := json.Marshal(v)
		return CommandResult{CreatedRecord: raw}, e
	})
	if e == nil {
		e = json.Unmarshal(result.CreatedRecord, &value)
	}
	return value, e
}
