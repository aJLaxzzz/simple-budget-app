package planner

import (
	"budget/internal/storageguard"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineSnapshotOlderSchemaForwardMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "old.db")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	db, e := openSQL(p, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations[:7]); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, "INSERT INTO projects(id,title) VALUES('old-id','Preserved project')"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	info, e := SnapshotInfo(ctx, p, true)
	if e != nil || info.Schema != 7 {
		t.Fatal(info, e)
	}
	if _, e = SnapshotInfo(ctx, p, false); e == nil {
		t.Fatal("older schema accepted without migration")
	}
	if e = UpgradeSnapshot(ctx, p); e != nil {
		t.Fatal(e)
	}
	db, e = openSQL(p, true)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var title string
	if e = db.QueryRow("SELECT title FROM projects WHERE id='old-id'").Scan(&title); e != nil || title != "Preserved project" {
		t.Fatal(title, e)
	}
	var n int
	db.QueryRow("SELECT count(*) FROM planner_icons").Scan(&n)
	if n < 321 {
		t.Fatal("icons missing", n)
	}
}
func TestOfflineSnapshotRejectsDDLAndLedgerTampering(t *testing.T) {
	for _, mutation := range []string{
		"CREATE TABLE unexpected(secret TEXT)",
		"UPDATE schema_migrations SET checksum='bad' WHERE version=1",
		"PRAGMA application_id=123",
		"UPDATE planner_icons SET checksum='bad' WHERE id=(SELECT id FROM planner_icons LIMIT 1)",
	} {
		t.Run(mutation, func(t *testing.T) {
			ctx := context.Background()
			p := filepath.Join(t.TempDir(), "test.db")
			s, e := Open(ctx, p)
			if e != nil {
				t.Fatal(e)
			}
			s.Close()
			db, e := openSQL(p, false)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(mutation); e != nil {
				db.Close()
				t.Fatal(e)
			}
			db.Close()
			if _, e = SnapshotInfo(ctx, p, true); e == nil {
				t.Fatal("tampered snapshot accepted")
			}
		})
	}
}

func TestOfflineSnapshotRejectsProductionMarkerInTests(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "planner.db")
	if e := os.WriteFile(p, []byte("must never be opened"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := storageguard.WriteMarker(dir, storageguard.Marker{Version: 1, Environment: "production", ID: "abcdef0123456789abcdef0123456789"}); e != nil {
		t.Fatal(e)
	}
	if _, e := SnapshotInfo(context.Background(), p, true); e == nil || !strings.Contains(e.Error(), "production storage") {
		t.Fatal("missing early storage guard", e)
	}
}
