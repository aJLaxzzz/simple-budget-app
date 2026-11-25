package main

import (
	"budget/internal/planner"
	"budget/internal/storageguard"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Legacy export NEVER opens a Store on the source. The existing app flock plus
// an explicit stopped-app acknowledgement protects the SQLite/JSON copy window.
func legacyExportCommand(args []string, out io.Writer) error {
	f := flag.NewFlagSet("legacy-export", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	source := f.String("source", "", "absolute stopped legacy data directory")
	destination := f.String("backup-dir", "", "new private transfer repository")
	id := f.String("installation-id", "", "new installation ID, retained on the server")
	confirm := f.Bool("confirm-stopped", false, "application was gracefully stopped")
	if e := f.Parse(args); e != nil || f.NArg() != 0 {
		return errors.New("invalid legacy-export arguments")
	}
	if !*confirm {
		return errors.New("legacy export requires explicit --confirm-stopped; do not copy live SQLite/WAL")
	}
	receipt, archive, e := legacyExport(context.Background(), *source, *destination, *id)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(map[string]any{"receipt": receipt, "archive": archive})
}
func legacyExport(ctx context.Context, source, destination, id string) (backupReceipt, string, error) {
	var empty backupReceipt
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) || len(id) != 32 {
		return empty, "", errors.New("absolute source/repository and 32-hex installation ID required")
	}
	if _, e := hex.DecodeString(id); e != nil {
		return empty, "", errors.New("invalid installation ID")
	}
	if flag.Lookup("test.v") != nil {
		for _, p := range []string{source, destination} {
			if e := storageguard.TestPath(p); e != nil {
				return empty, "", e
			}
		}
	}
	real, e := storageguard.Check(source, "development", "")
	if e != nil {
		return empty, "", e
	}
	if exists(filepath.Join(real, storageguard.MarkerName)) {
		return empty, "", errors.New("marked installations must use normal backup, not legacy adoption")
	}
	if e = separateBackupPaths(real, destination); e != nil {
		return empty, "", e
	}
	// No source lock file is created, chmodded or removed.
	lockPath := filepath.Join(real, "planner.db.lock")
	if e = storageguard.Regular(lockPath, false); e != nil {
		return empty, "", errors.New("existing planner.db.lock required; source application must have been run and stopped")
	}
	lock, e := safeOpen(lockPath)
	if e != nil {
		return empty, "", e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return empty, "", errors.New("legacy app is still running")
	}
	lease, e := storageguard.ParentLease(real, true)
	if e != nil {
		return empty, "", e
	}
	defer lease.Close()
	if e = checkSpace(destination, 128<<20, 64); e != nil {
		return empty, "", e
	}
	work, e := os.MkdirTemp(filepath.Dir(destination), ".budget-legacy-")
	if e != nil {
		return empty, "", e
	}
	defer os.RemoveAll(work)
	clone := filepath.Join(work, "clone")
	live := filepath.Join(work, "live")
	if e = os.Mkdir(clone, 0700); e != nil {
		return empty, "", e
	}
	if e = os.Mkdir(live, 0700); e != nil {
		return empty, "", e
	}
	names := []string{"planner.db", "data.json", "categories.json"}
	if exists(filepath.Join(real, "planner.db-wal")) {
		names = append(names, "planner.db-wal")
	}
	before := map[string]backupFile{}
	var total int64
	for _, n := range names {
		h, e := hashFile(ctx, filepath.Join(real, n))
		if e != nil {
			return empty, "", e
		}
		before[n] = h
		total += h.Size
	}
	if total > archiveLimit {
		return empty, "", errors.New("legacy source exceeds transfer limit")
	}
	if e = checkSpace(destination, 6*total+(128<<20), 64); e != nil {
		return empty, "", e
	}
	for _, n := range names {
		limit := archiveLimit
		if n == "data.json" || n == "categories.json" {
			limit = jsonLimit
		}
		if e = copyPrivate(ctx, filepath.Join(real, n), filepath.Join(clone, n), limit); e != nil {
			return empty, "", e
		}
	}
	for _, n := range names {
		h, e := hashFile(ctx, filepath.Join(real, n))
		if e != nil || h != before[n] {
			return empty, "", errors.New("source changed during offline transfer")
		}
	}
	if e = validateFinancial(clone); e != nil {
		return empty, "", e
	}
	// WAL is recovered only in this disposable copy. No source migration/checkpoint.
	store, e := planner.Open(ctx, filepath.Join(clone, "planner.db"))
	if e != nil {
		return empty, "", e
	}
	e = store.Export(ctx, filepath.Join(live, "planner.db"))
	e = errors.Join(e, store.Close())
	if e != nil {
		return empty, "", e
	}
	for _, n := range []string{"data.json", "categories.json"} {
		if e = copyPrivate(ctx, filepath.Join(clone, n), filepath.Join(live, n), jsonLimit); e != nil {
			return empty, "", e
		}
	}
	environment := "production"
	if flag.Lookup("test.v") != nil {
		environment = "test"
	}
	if e = storageguard.WriteMarker(live, storageguard.Marker{Version: 1, Environment: environment, ID: id}); e != nil {
		return empty, "", e
	}
	// The repository is initialized only after the source copy has been verified.
	repo, e := openRepository(destination, id, environment, true)
	if e != nil {
		return empty, "", e
	}
	receipt, e := createBackup(ctx, repo, backupOptions{DataDir: live, ID: id, Environment: environment, Reason: "manual"})
	if e != nil {
		return empty, "", e
	}
	dir, e := repo.path("manual", receipt.Manifest.ID)
	return receipt, filepath.Join(dir, "backup.tar.gz"), e
}
