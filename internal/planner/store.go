// Package planner owns only planner data. It never opens financial JSON files.
package planner

import (
	"budget/internal/storageguard"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	_ "modernc.org/sqlite"
)

var ErrClosed = errors.New("planner store is closed")

// Store serializes operations on one SQL connection. Transactions and maintenance
// use the same gate; no caller may retain a transaction beyond its callback.
type Store struct {
	mu       sync.Mutex
	db       *sql.DB
	file     *os.File
	path     string
	onCommit func()
}

func Open(ctx context.Context, path string) (*Store, error) {
	return openStore(ctx, path, "development", "", true)
}

// OpenProduction checks an existing installation without migrating or seeding it.
func OpenProduction(ctx context.Context, path, id string) (*Store, error) {
	return openStore(ctx, path, "production", id, false)
}

// OpenMaintenance owns the same exclusive lock as the server. It never creates a
// database, starts a worker or edits financial files. Migration is explicit.
func OpenMaintenance(ctx context.Context, path, id string, migrateSchema bool) (*Store, error) {
	return openStore(ctx, path, "production", id, migrateSchema)
}
func openStore(ctx context.Context, path, environment, id string, applyMigrations bool) (*Store, error) {
	path, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	if flag.Lookup("test.v") != nil {
		if e := storageguard.TestPath(filepath.Dir(path)); e != nil {
			return nil, e
		}
	}
	dir, e := storageguard.Check(filepath.Dir(path), environment, id)
	if e != nil {
		return nil, e
	}
	path = filepath.Join(dir, filepath.Base(path))
	for _, suffix := range []string{"", "-wal", "-shm", ".lock"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			if e := storageguard.Regular(path+suffix, false); e != nil {
				return nil, e
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if info, e := os.Lstat(path); e == nil && !info.Mode().IsRegular() {
		return nil, errors.New("planner database must be a regular file")
	}

	if info, e := os.Stat(path); e == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
			return nil, errors.New("hard-linked planner databases are not supported")
		}
	}
	lockPath := path + ".lock"
	if info, e := os.Lstat(lockPath); e == nil && !info.Mode().IsRegular() {
		return nil, errors.New("planner lock must be a regular file")
	}
	f, e := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("planner database already in use: %w", e)
	}
	flags := os.O_RDWR | os.O_CREATE
	if environment == "production" {
		if e = storageguard.Regular(path, true); e != nil {
			syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			f.Close()
			return nil, e
		}
		flags = os.O_RDWR
	}
	databaseFile, e := os.OpenFile(path, flags, 0600)
	if e != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, e
	}
	if e = databaseFile.Close(); e != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, e
	}
	s := &Store{file: f, path: path}
	fail := func(e error) (*Store, error) { s.Close(); return nil, e }
	s.db, e = openSQL(path, false)
	if e != nil {
		return fail(e)
	}
	if environment == "production" {
		var appID, version int
		if e = s.db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); e != nil {
			return fail(e)
		}
		if e = s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil {
			return fail(e)
		}
		if appID != applicationID || version < 1 {
			return fail(errors.New("production database is not initialized"))
		}
	}
	if applyMigrations {
		if e = migrate(ctx, s.db, migrations); e != nil {
			return fail(e)
		}
		if e = s.Transaction(ctx, func(tx *sql.Tx) error { return seedBuiltinIcons(ctx, tx) }); e != nil {
			return fail(e)
		}
		if _, e = s.db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); e != nil {
			return fail(e)
		}
	} else {
		if e = validateSnapshot(ctx, s.db); e != nil {
			return fail(e)
		}
		var journal string
		if e = s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); e != nil {
			return fail(e)
		}
		if journal != "wal" {
			return fail(errors.New("production database requires WAL; use administrative migration"))
		}
	}
	if e = checkIntegrity(ctx, s.db); e != nil {
		return fail(e)
	}
	return s, nil
}
func openSQL(path string, readonly bool) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	if readonly {
		q.Set("mode", "ro")
		q.Set("immutable", "1")
	} else {
		q.Set("mode", "rw")
	}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "trusted_schema(OFF)")
	q.Add("_pragma", "recursive_triggers(ON)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result error
	if s.db != nil {
		result = s.db.Close()
		s.db = nil
	}
	if s.file != nil {
		result = errors.Join(result, syscall.Flock(int(s.file.Fd()), syscall.LOCK_UN), s.file.Close())
		s.file = nil
	}
	return result
}

// Transaction commits all callback changes together, including history. Returning
// an error or panicking rolls back. Revision comparisons belong in later services.
func (s *Store) Transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrClosed
	}
	if s.onCommit == nil {
		return transaction(ctx, s.db, fn)
	}
	var before, after int64
	if e := s.db.QueryRowContext(ctx, "SELECT total_changes()").Scan(&before); e != nil {
		return e
	}
	err := transaction(ctx, s.db, fn)
	if err == nil && s.onCommit != nil {
		// The commit has already succeeded. A disconnected HTTP caller must not
		// suppress the notification needed by the other connected devices.
		if e := s.db.QueryRowContext(context.WithoutCancel(ctx), "SELECT total_changes()").Scan(&after); e != nil || after != before {
			s.onCommit()
		}
	}
	return err
}
func transaction(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) (result error) {
	defer func() {
		// database/sql can finish its cancellation rollback before the callback
		// observes cancellation. Preserve genuine errors; normalize only its
		// closed-transaction/connection sentinels with a cancelled parent.
		if ctx.Err() != nil && (errors.Is(result, sql.ErrTxDone) || errors.Is(result, sql.ErrConnDone)) {
			result = ctx.Err()
		}
	}()
	connection, e := db.Conn(ctx)
	if e != nil {
		return e
	}
	defer connection.Close()
	tx, e := connection.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = fn(tx); e != nil {
		return e
	}
	e = tx.Commit()
	if e != nil { // SQLite may leave a deferred-FK failed COMMIT open; use the same connection.
		_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
	}
	return e
}

// Ready uses the owned connection; it does not open another store or materialize.
func (s *Store) Ready(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrClosed
	}
	var v int
	if e := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); e != nil {
		return e
	}
	if v != len(migrations) {
		return errors.New("incompatible schema")
	}
	return nil
}
func SchemaVersion() int { return len(migrations) }

// SetCommitObserver installs a nonblocking notification invoked only after a successful write commit.
func (s *Store) SetCommitObserver(fn func()) { s.mu.Lock(); defer s.mu.Unlock(); s.onCommit = fn }
