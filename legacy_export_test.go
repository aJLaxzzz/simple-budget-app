package main

import (
	"budget/internal/planner"
	"budget/internal/storageguard"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacyExportPreservesSourceAndRejectsRunningStore(t *testing.T) {
	_, o := backupFixture(t)
	if e := os.Remove(filepath.Join(o.DataDir, storageguard.MarkerName)); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(t.TempDir(), "transfer")
	os.Mkdir(dest, 0700)
	s, e := planner.Open(context.Background(), filepath.Join(o.DataDir, "planner.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = legacyExport(context.Background(), o.DataDir, dest, o.ID); e == nil {
		t.Fatal("running source accepted")
	}
	s.Close()
	source := map[string]backupFile{}
	for _, n := range []string{"planner.db", "data.json", "categories.json"} {
		v, e := hashFile(context.Background(), filepath.Join(o.DataDir, n))
		if e != nil {
			t.Fatal(e)
		}
		source[n] = v
	}
	contents := tableFingerprints(t, filepath.Join(o.DataDir, "planner.db"))
	receipt, path, e := legacyExport(context.Background(), o.DataDir, dest, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	extracted := t.TempDir()
	if _, e = extractBackup(context.Background(), path, extracted); e != nil {
		t.Fatal(e)
	}
	if tableFingerprints(t, filepath.Join(extracted, "planner.db")) != contents {
		t.Fatal("legacy IDs/history/content changed")
	}
	if receipt.Manifest.InstallationID != o.ID {
		t.Fatal("identity changed")
	}
	if _, e = verifyBackup(context.Background(), path, ""); e != nil {
		t.Fatal(e)
	}
	for n, v := range source {
		now, e := hashFile(context.Background(), filepath.Join(o.DataDir, n))
		if e != nil || now != v {
			t.Fatal("source changed", n, e)
		}
	}
	if exists(filepath.Join(o.DataDir, storageguard.MarkerName)) {
		t.Fatal("source marker created")
	}
	var output bytes.Buffer
	if e = legacyExportCommand([]string{"--source", o.DataDir, "--backup-dir", dest, "--installation-id", o.ID}, &output); e == nil {
		t.Fatal("confirmation omitted")
	}
}
func TestBackupExplicitDeleteProtectionAndLastCopy(t *testing.T) {
	r, o := backupFixture(t)
	ctx := context.Background()
	one, e := createBackup(ctx, r, o)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.deleteConfirmed(ctx, one.Manifest.ID); e == nil {
		t.Fatal("last copy deleted")
	}
	two, e := createBackup(ctx, r, o)
	if e != nil {
		t.Fatal(e)
	}
	r.pin(one.Manifest.ID, true)
	if e = r.deleteConfirmed(ctx, one.Manifest.ID); e == nil {
		t.Fatal("pin deleted")
	}
	r.pin(one.Manifest.ID, false)
	if e = r.deleteConfirmed(ctx, one.Manifest.ID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = r.find(two.Manifest.ID); e != nil {
		t.Fatal(e)
	}
	s, e := r.status(two.Manifest.Created.Add(26*time.Hour + time.Second))
	if e != nil || !s.Stale {
		t.Fatal("26 hour warning missing", e)
	}
}
