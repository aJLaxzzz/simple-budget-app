package planner

// Offline archive validation never opens a live Store or runs a materializer.
import (
	"budget/internal/storageguard"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
)

type SnapshotMetadata struct {
	Schema     int      `json:"schema_version"`
	Migrations []string `json:"migration_checksums"`
}

func SnapshotInfo(ctx context.Context, path string, allowOlder bool) (SnapshotMetadata, error) {
	var out SnapshotMetadata
	if flag.Lookup("test.v") != nil {
		if e := storageguard.TestPath(filepath.Dir(path)); e != nil {
			return out, e
		}
	}
	if _, e := storageguard.Resolve(filepath.Dir(path)); e != nil {
		return out, e
	}
	if e := storageguard.Regular(path, false); e != nil {
		return out, e
	}
	db, e := openSQL(path, true)
	if e != nil {
		return out, e
	}
	defer db.Close()
	var id int
	if e = db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); e != nil {
		return out, e
	}
	if e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&out.Schema); e != nil {
		return out, e
	}
	if id != applicationID || out.Schema < 1 || out.Schema > len(migrations) || (!allowOlder && out.Schema != len(migrations)) {
		return out, errors.New("unsupported snapshot application/schema")
	}
	rows, e := db.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v int
		var sum string
		if e = rows.Scan(&v, &sum); e != nil {
			rows.Close()
			return out, e
		}
		if v != len(out.Migrations)+1 || v > out.Schema || sum != fmt.Sprintf("%x", sha256.Sum256([]byte(migrations[v-1].sql))) {
			rows.Close()
			return out, errors.New("snapshot migration ledger mismatch")
		}
		out.Migrations = append(out.Migrations, sum)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Migrations) != out.Schema {
		return out, errors.New("incomplete snapshot ledger")
	}
	if e = checkIntegrity(ctx, db); e != nil {
		return out, e
	}
	if out.Schema >= 8 {
		if e = checkIconIntegrity(ctx, db); e != nil {
			return out, e
		}
	}
	// Embedded migrations are immutable. Cache only their successfully built
	// reference DDL, never validation results for any user snapshot.
	expected, e := snapshotReferenceSchema(ctx, out.Schema)
	if e != nil {
		return out, e
	}
	actual, e := schema(ctx, db)
	if e != nil {
		return out, e
	}
	if !reflect.DeepEqual(actual, expected) {
		return out, errors.New("snapshot schema differs from embedded migrations")
	}
	return out, nil
}

var snapshotSchemas sync.Map // schema version -> immutable []string

func snapshotReferenceSchema(ctx context.Context, version int) ([]string, error) {
	if cached, ok := snapshotSchemas.Load(version); ok {
		return cached.([]string), nil
	}
	reference, e := os.CreateTemp("", "budget-schema-*.db")
	if e != nil {
		return nil, e
	}
	name := reference.Name()
	reference.Close()
	defer os.Remove(name)
	ref, e := openSQL(name, false)
	if e != nil {
		return nil, e
	}
	defer ref.Close()
	if e = migrate(ctx, ref, migrations[:version]); e != nil {
		return nil, e
	}
	expected, e := schema(ctx, ref)
	if e != nil {
		return nil, e
	}
	cached, _ := snapshotSchemas.LoadOrStore(version, expected)
	return cached.([]string), nil
}

// UpgradeSnapshot operates ONLY on a separate private, validated staging copy.
func UpgradeSnapshot(ctx context.Context, path string) error {
	if _, e := storageguard.Check(filepath.Dir(path), "development", ""); e != nil {
		return e
	}
	if _, e := SnapshotInfo(ctx, path, true); e != nil {
		return e
	}
	db, e := openSQL(path, false)
	if e != nil {
		return e
	}
	if e = migrate(ctx, db, migrations); e == nil {
		e = transaction(ctx, db, func(tx *sql.Tx) error { return seedBuiltinIcons(ctx, tx) })
	}
	if e == nil {
		_, e = db.ExecContext(ctx, "PRAGMA journal_mode=WAL")
	}
	// Close the final connection before validating the standalone snapshot.
	// SQLite checkpoints its private WAL on close; validation below reads no WAL.
	e = errors.Join(e, db.Close())
	if e != nil {
		return e
	}
	_, e = SnapshotInfo(ctx, path, false)
	return e
}
