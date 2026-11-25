package main

import (
	"budget/internal/planner"
	"budget/internal/storageguard"
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type restoreJournal struct {
	Format         int    `json:"format_version"`
	ID             string `json:"operation_id"`
	DataDir        string `json:"data_dir"`
	InstallationID string `json:"installation_id"`
	Environment    string `json:"environment"`
	Phase          string `json:"phase"`
	HadOriginal    bool   `json:"had_original"`
}

func (j restoreJournal) stage() string {
	return filepath.Join(filepath.Dir(j.DataDir), "."+filepath.Base(j.DataDir)+".restore-"+j.ID)
}
func (j restoreJournal) previous() string {
	return filepath.Join(filepath.Dir(j.DataDir), "."+filepath.Base(j.DataDir)+".previous-"+j.ID)
}
func (j restoreJournal) save() error { return privateJSON(storageguard.RestoreJournal(j.DataDir), j) }
func restoreDirectoryCheck(ctx context.Context, dir, environment, id string) error {
	if _, e := storageguard.Check(dir, environment, id); e != nil {
		return e
	}
	if e := validateFinancial(dir); e != nil {
		return e
	}
	_, e := planner.SnapshotInfo(ctx, filepath.Join(dir, "planner.db"), false)
	return e
}
func exists(p string) bool { _, e := os.Lstat(p); return e == nil }
func liveContents(dir string) error {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	allowed := map[string]bool{"planner.db": true, "planner.db-wal": true, "planner.db-shm": true, "planner.db.lock": true, "data.json": true, "categories.json": true, "installation.json": true}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return errors.New("unknown live file preserved: move it safely before restoring this directory")
		}
		if e = storageguard.Regular(filepath.Join(dir, entry.Name()), false); e != nil {
			return e
		}
	}
	return nil
}

// Identity and path checks do not open SQLite: a damaged database must remain
// restorable. Repeat under the exclusive lease before touching live data.
func restoreTarget(o backupOptions, scope string) error {
	if o.Environment != "test" && o.Environment != "production" {
		return errors.New("invalid restore environment")
	}
	if !exists(o.DataDir) {
		if scope == "planner" {
			return errors.New("planner-only restore requires an existing installation")
		}
		return nil
	}
	if _, e := storageguard.Resolve(o.DataDir); e != nil {
		return e
	}
	st, e := os.Stat(o.DataDir)
	if e != nil {
		return e
	}
	if st.Mode().Perm()&0077 != 0 {
		return errors.New("restore target must be owner-only")
	}
	entries, e := os.ReadDir(o.DataDir)
	if e != nil {
		return e
	}
	if len(entries) == 0 {
		if scope == "planner" {
			return errors.New("planner-only restore requires existing financial data")
		}
		return nil
	}
	if _, e = checkedData(o); e != nil {
		return e
	}
	return liveContents(o.DataDir)
}

// Used by the standalone recovery command, in addition to the parent lease.
// Protects against older application binaries that only hold the SQLite flock.
func lockRecoveryDatabases(o backupOptions) (func(), error) {
	dirs := []string{o.DataDir}
	if exists(storageguard.RestoreJournal(o.DataDir)) {
		var j restoreJournal
		if e := readSmall(storageguard.RestoreJournal(o.DataDir), &j, 64<<10); e != nil {
			return nil, e
		}
		if j.Format != 1 || !backupIDPattern.MatchString(j.ID) || j.DataDir != o.DataDir || j.InstallationID != o.ID || j.Environment != o.Environment {
			return nil, errors.New("restore journal identity mismatch")
		}
		dirs = append(dirs, j.previous())
	}
	var locks []*os.File
	closeAll := func() {
		for _, f := range locks {
			f.Close()
		}
	}
	for _, dir := range dirs {
		if !exists(dir) {
			continue
		}
		if _, e := storageguard.Resolve(dir); e != nil {
			closeAll()
			return nil, e
		}
		f, e := adminLock(filepath.Join(dir, "planner.db.lock"))
		if e != nil {
			closeAll()
			return nil, e
		}
		locks = append(locks, f)
	}
	return closeAll, nil
}

// Rollback/finish is state based: every rename is followed by parent fsync. The
// journal remains until a complete valid directory is selected; startup refuses
// while it exists. The old directory is never deleted by restore.
func recoverRestore(ctx context.Context, o backupOptions, decision string) error {
	var j restoreJournal
	p := storageguard.RestoreJournal(o.DataDir)
	if e := readSmall(p, &j, 64<<10); e != nil {
		return e
	}
	if j.Format != 1 || !backupIDPattern.MatchString(j.ID) || j.DataDir != o.DataDir || j.InstallationID != o.ID || j.Environment != o.Environment {
		return errors.New("restore journal identity mismatch")
	}
	if decision != "finish" && decision != "rollback" {
		return errors.New("recovery requires finish or rollback")
	}
	if j.Phase != "prepared" && j.Phase != "old-moved" && j.Phase != "new-live" {
		return errors.New("unknown restore phase")
	}
	for _, dir := range []string{j.DataDir, j.stage(), j.previous()} {
		if exists(dir) {
			if _, e := storageguard.Resolve(dir); e != nil {
				return e
			}
		}
	}
	if decision == "rollback" {
		if exists(j.previous()) {
			if exists(j.DataDir) {
				if exists(j.stage()) {
					return errors.New("ambiguous recovery directories; preserved for inspection")
				}
				if e := os.Rename(j.DataDir, j.stage()); e != nil {
					return e
				}
				if e := syncDir(filepath.Dir(j.DataDir)); e != nil {
					return e
				}
			}
			if e := os.Rename(j.previous(), j.DataDir); e != nil {
				return e
			}
		} else if !j.HadOriginal && exists(j.DataDir) {
			if exists(j.stage()) {
				return errors.New("ambiguous new installation recovery")
			}
			if e := os.Rename(j.DataDir, j.stage()); e != nil {
				return e
			}
		} else if j.HadOriginal && !exists(j.DataDir) {
			return errors.New("original installation missing; cannot rollback")
		}
	} else {
		if exists(j.stage()) {
			if e := restoreDirectoryCheck(ctx, j.stage(), j.Environment, j.InstallationID); e != nil {
				return e
			}
			if exists(j.DataDir) {
				if exists(j.previous()) {
					return errors.New("ambiguous recovery directories")
				}
				if e := os.Rename(j.DataDir, j.previous()); e != nil {
					return e
				}
				if e := syncDir(filepath.Dir(j.DataDir)); e != nil {
					return e
				}
			}
			if e := os.Rename(j.stage(), j.DataDir); e != nil {
				return e
			}
		}
		if e := restoreDirectoryCheck(ctx, j.DataDir, j.Environment, j.InstallationID); e != nil {
			return e
		}
	}
	if e := syncDir(filepath.Dir(j.DataDir)); e != nil {
		return e
	}
	if e := os.Remove(p); e != nil {
		return e
	}
	return syncDir(filepath.Dir(p))
}
func restoreBackup(ctx context.Context, r backupRepository, o backupOptions, archive, scope string) (previous string, err error) {
	if scope != "full" && scope != "planner" {
		return "", errors.New("restore scope must be full or planner")
	}
	if !filepath.IsAbs(o.DataDir) {
		return "", errors.New("absolute restore target required")
	}
	parent, e := storageguard.Resolve(filepath.Dir(o.DataDir))
	if e != nil {
		return "", e
	}
	o.DataDir = filepath.Join(parent, filepath.Base(o.DataDir))
	if e = separateBackupPaths(o.DataDir, r.Dir); e != nil {
		return "", e
	}
	if r.ID != o.ID || r.Environment != o.Environment {
		return "", errors.New("repository mismatch")
	}
	if e = restoreTarget(o, scope); e != nil {
		return "", e
	}
	if o.Environment == "test" {
		if e = storageguard.TestPath(parent); e != nil {
			return "", e
		}
	}
	lock, e := r.lock()
	if e != nil {
		return "", e
	}
	defer lock.Close()
	installationLock, e := installationAdminLock(o.DataDir)
	if e != nil {
		return "", e
	}
	defer installationLock.Close()
	if e = storageguard.CheckRecovery(o.DataDir); e != nil {
		return "", e
	}
	if _, e = os.Lstat(filepath.Join(r.Dir, "operation.json")); !os.IsNotExist(e) {
		return "", errors.New("unfinished administrative operation")
	}
	source, e := safeOpen(archive)
	if e != nil {
		return "", e
	}
	st, e := source.Stat()
	source.Close()
	if e != nil {
		return "", e
	}
	if e = o.spaceCheck(parent, st.Size()*4+(64<<20), 32); e != nil {
		return "", e
	}
	j := restoreJournal{Format: 1, ID: newBackupID(time.Now()), DataDir: o.DataDir, InstallationID: o.ID, Environment: o.Environment, Phase: "preparing", HadOriginal: exists(o.DataDir)}
	if e = os.Mkdir(j.stage(), 0700); e != nil {
		return "", e
	}
	journalWritten := false
	defer func() {
		if !journalWritten {
			os.RemoveAll(j.stage())
		}
	}()
	m, e := extractBackup(ctx, archive, j.stage())
	if e != nil {
		return "", e
	}
	if m.InstallationID != o.ID || r.ID != o.ID {
		return "", errors.New("archive installation ID mismatch")
	}
	if e = planner.UpgradeSnapshot(ctx, filepath.Join(j.stage(), "planner.db")); e != nil {
		return "", e
	}
	for _, name := range []string{"settings.json", "RESTORE.txt"} {
		if e = os.Remove(filepath.Join(j.stage(), name)); e != nil {
			return "", e
		}
	}
	op, e := r.begin(ctx, o, "restore", j.stage())
	if e != nil {
		if op.WasRunning {
			return "", errors.Join(e, r.resume(op))
		}
		return "", e
	}
	resumed := false
	safeResume := true
	defer func() {
		if !resumed && safeResume {
			err = errors.Join(err, r.resume(op))
		}
	}()
	lease, e := storageguard.ParentLease(o.DataDir, true)
	if e != nil {
		return "", e
	}
	defer func() {
		if lease != nil {
			lease.Close()
		}
	}()
	// A legacy process may not hold the new parent lease. Its SQLite flock still wins.
	if e = restoreTarget(o, scope); e != nil {
		return "", e
	}
	var dbLock *os.File
	if exists(o.DataDir) {
		entries, e := os.ReadDir(o.DataDir)
		if e != nil {
			return "", e
		}
		if len(entries) > 0 {
			if _, e = checkedData(o); e != nil {
				return "", e
			}
			if e = liveContents(o.DataDir); e != nil {
				return "", e
			}
			dbLock, e = adminLock(filepath.Join(o.DataDir, "planner.db.lock"))
			if e != nil {
				return "", e
			}
			defer dbLock.Close()
			if scope == "planner" {
				if e = validateFinancial(o.DataDir); e != nil {
					return "", e
				}
				for _, name := range []string{"data.json", "categories.json"} {
					if e = os.Remove(filepath.Join(j.stage(), name)); e != nil {
						return "", e
					}
					if e = copyPrivate(ctx, filepath.Join(o.DataDir, name), filepath.Join(j.stage(), name), jsonLimit); e != nil {
						return "", e
					}
				}
			}
		} else if scope == "planner" {
			return "", errors.New("planner-only restore requires existing financial data")
		}
	} else if scope == "planner" {
		return "", errors.New("planner-only restore requires an existing installation")
	}
	if e = storageguard.WriteMarker(j.stage(), storageguard.Marker{Version: 1, Environment: o.Environment, ID: o.ID}); e != nil {
		return "", e
	}
	if e = restoreOwnership(j.stage(), o.DataDir); e != nil {
		return "", e
	}
	if e = restoreDirectoryCheck(ctx, j.stage(), o.Environment, o.ID); e != nil {
		return "", e
	}
	if e = syncDir(j.stage()); e != nil {
		return "", e
	}
	j.Phase = "prepared"
	if e = j.save(); e != nil {
		return "", e
	}
	journalWritten = true
	rollback := func(cause error) (string, error) {
		if e := recoverRestore(context.Background(), o, "rollback"); e != nil {
			safeResume = false
			return j.previous(), errors.Join(cause, e)
		}
		return j.previous(), cause
	}
	if e = o.check("restore-prepared"); e != nil {
		return rollback(e)
	}
	if j.HadOriginal {
		if e = os.Rename(o.DataDir, j.previous()); e != nil {
			return rollback(e)
		}
		if e = syncDir(parent); e != nil {
			return rollback(e)
		}
	}
	j.Phase = "old-moved"
	if e = j.save(); e != nil {
		return rollback(e)
	}
	if e = o.check("restore-old-moved"); e != nil {
		return rollback(e)
	}
	if e = os.Rename(j.stage(), o.DataDir); e != nil {
		return rollback(e)
	}
	if e = syncDir(parent); e != nil {
		return rollback(e)
	}
	j.Phase = "new-live"
	if e = j.save(); e != nil {
		return rollback(e)
	}
	if e = o.check("restore-new-live"); e != nil {
		return rollback(e)
	}
	if e = restoreDirectoryCheck(ctx, o.DataDir, o.Environment, o.ID); e != nil {
		return rollback(e)
	}
	if e = os.Remove(storageguard.RestoreJournal(o.DataDir)); e != nil {
		safeResume = false
		return j.previous(), e
	}
	if e = syncDir(parent); e != nil {
		safeResume = false
		return j.previous(), e
	}
	if dbLock != nil {
		dbLock.Close()
	}
	lease.Close()
	lease = nil
	if e = r.resume(op); e != nil {
		return j.previous(), e
	}
	resumed = true
	return j.previous(), nil
}
