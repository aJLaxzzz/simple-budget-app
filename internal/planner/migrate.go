package planner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/001_initial.sql
var initialSQL string

type migration struct {
	version int
	sql     string
}

//go:embed migrations/002_project_archive.sql
var archiveSQL string

//go:embed migrations/003_session_completion.sql
var sessionSQL string

//go:embed migrations/004_task_series.sql
var taskSeriesSQL string

//go:embed migrations/005_recurrence_management.sql
var recurrenceManagementSQL string

//go:embed migrations/006_project_templates.sql
var projectTemplatesSQL string

//go:embed migrations/007_calendar_imports.sql
var calendarImportsSQL string

//go:embed migrations/008_appearance.sql
var appearanceSQL string

//go:embed migrations/009_entity_templates.sql
var entityTemplatesSQL string

var migrations = []migration{{1, initialSQL}, {2, archiveSQL}, {3, sessionSQL}, {4, taskSeriesSQL}, {5, recurrenceManagementSQL}, {6, projectTemplatesSQL}, {7, calendarImportsSQL}, {8, appearanceSQL}, {9, entityTemplatesSQL}}

const applicationID = 1396853840

func migrate(ctx context.Context, db *sql.DB, list []migration) error {
	return transaction(ctx, db, func(tx *sql.Tx) error {
		var id, version int
		if e := tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); e != nil {
			return e
		}
		if e := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil {
			return e
		}
		if id != 0 && id != applicationID {
			return fmt.Errorf("not a planner database (application_id %d)", id)
		}
		if version > len(list) || version < 0 {
			return fmt.Errorf("unsupported planner schema version %d (supported %d)", version, len(list))
		}
		if id == 0 {
			var n int
			if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&n); e != nil {
				return e
			}
			if n != 0 || version != 0 {
				return fmt.Errorf("refusing to initialize an unidentified database")
			}
		}
		if _, e := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
		if e != nil {
			return e
		}
		count := 0
		for rows.Next() {
			var v int
			var checksum string
			if e = rows.Scan(&v, &checksum); e != nil {
				rows.Close()
				return e
			}
			count++
			if v != count || v > len(list) || list[v-1].version != v || checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(list[v-1].sql))) {
				rows.Close()
				return fmt.Errorf("unknown or modified planner migration %d", v)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if count != version {
			return fmt.Errorf("planner migration ledger/version mismatch")
		}
		for i, m := range list {
			if m.version != i+1 {
				return fmt.Errorf("nonsequential migration %d", m.version)
			}
			if m.version <= version {
				continue
			}
			if _, e = tx.ExecContext(ctx, m.sql); e != nil {
				return fmt.Errorf("migration %d: %w", m.version, e)
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES(?,?)", m.version, fmt.Sprintf("%x", sha256.Sum256([]byte(m.sql)))); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=%d", applicationID, len(list)))
		return e
	})
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checkIntegrity(ctx context.Context, q queryer) error {
	rows, e := q.QueryContext(ctx, "PRAGMA integrity_check")
	if e != nil {
		return e
	}
	for rows.Next() {
		var status string
		if e = rows.Scan(&status); e != nil {
			rows.Close()
			return e
		}
		if status != "ok" {
			rows.Close()
			return fmt.Errorf("planner integrity check: %s", status)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = q.QueryContext(ctx, "PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("planner foreign key integrity check failed")
	}
	return rows.Err()
}
