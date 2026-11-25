package main

import (
	"budget/internal/planner"
	"budget/internal/storageguard"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type serviceController struct{ Path, ID string }

func (c serviceController) call(ctx context.Context, action string) error {
	if c.Path == "" {
		return nil
	}
	if !filepath.IsAbs(c.Path) {
		return errors.New("controller must be an absolute trusted executable")
	}
	if e := storageguard.Regular(c.Path, false); e != nil {
		return e
	}
	if _, e := storageguard.Resolve(filepath.Dir(c.Path)); e != nil {
		return e
	}
	st, e := os.Stat(c.Path)
	if e != nil || st.Mode().Perm()&0022 != 0 {
		return errors.New("unsafe controller permissions")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Path, action, c.ID)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("service controller %s failed", action)
	}
	return nil
}
func (c serviceController) running(ctx context.Context) (bool, error) {
	if c.Path == "" {
		return false, nil
	}
	if e := c.call(ctx, "status"); e == nil {
		return true, nil
	}
	if e := c.call(ctx, "stopped"); e == nil {
		return false, nil
	}
	return false, errors.New("controller cannot confirm service state and installation identity")
}

type backupOperation struct {
	Kind           string `json:"kind"`
	DataDir        string `json:"data_dir"`
	InstallationID string `json:"installation_id"`
	Controller     string `json:"controller"`
	WasRunning     bool   `json:"was_running"`
	Stage          string `json:"stage"`
}
type backupOptions struct {
	DataDir, ID, Environment, Reason, Controller string
	Protect                                      bool
	Now                                          time.Time
	space                                        func(string, int64, uint64) error
	fault                                        func(string) error
}

func (o backupOptions) check(label string) error {
	if o.fault != nil {
		return o.fault(label)
	}
	return nil
}
func (o backupOptions) spaceCheck(p string, n int64, i uint64) error {
	if o.space != nil {
		return o.space(p, n, i)
	}
	return checkSpace(p, n, i)
}
func (r backupRepository) begin(ctx context.Context, o backupOptions, kind, stage string) (backupOperation, error) {
	op := backupOperation{Kind: kind, DataDir: o.DataDir, InstallationID: o.ID, Controller: o.Controller, Stage: stage}
	p := filepath.Join(r.Dir, "operation.json")
	if _, e := os.Lstat(p); !os.IsNotExist(e) {
		return op, errors.New("unfinished operation; use admin backup recover")
	}
	c := serviceController{o.Controller, o.ID}
	running, e := c.running(ctx)
	if e != nil {
		return op, e
	}
	op.WasRunning = running
	if e = privateJSON(p, op); e != nil {
		return op, e
	}
	if running {
		if e = c.call(ctx, "stop"); e != nil {
			return op, e
		}
		if running, e = c.running(ctx); e != nil || running {
			return op, errors.New("controller did not stop service")
		}
	}
	return op, nil
}
func (r backupRepository) resume(op backupOperation) error {
	if op.WasRunning {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		c := serviceController{op.Controller, op.InstallationID}
		running, e := c.running(ctx)
		if e != nil {
			return e
		}
		if !running {
			if e = c.call(ctx, "start"); e != nil {
				return e
			}
		}
		running, e = c.running(ctx)
		if e != nil || !running {
			return errors.New("service restart not confirmed; run backup recover")
		}
	}
	if e := os.Remove(filepath.Join(r.Dir, "operation.json")); e != nil && !os.IsNotExist(e) {
		return e
	}
	return syncDir(r.Dir)
}
func checkedData(o backupOptions) (string, error) {
	if !filepath.IsAbs(o.DataDir) || o.ID == "" || (o.Environment != "test" && o.Environment != "production") {
		return "", errors.New("explicit absolute data directory, environment and installation ID required")
	}
	return storageguard.Check(o.DataDir, o.Environment, o.ID)
}

// Serializes service stop/resume for an installation even when two operators
// supply different archive repositories. The web process never opens this file.
func installationAdminLock(live string) (*os.File, error) {
	if !filepath.IsAbs(live) {
		return nil, errors.New("absolute installation directory required")
	}
	parent, e := storageguard.Resolve(filepath.Dir(live))
	if e != nil {
		return nil, e
	}
	return adminLock(filepath.Join(parent, "."+filepath.Base(live)+".admin.lock"))
}
func createBackup(ctx context.Context, r backupRepository, o backupOptions) (result backupReceipt, err error) {
	live, e := checkedData(o)
	if e != nil {
		return result, e
	}
	o.DataDir = live
	if e = separateBackupPaths(live, r.Dir); e != nil {
		return result, e
	}
	if r.ID != o.ID || r.Environment != o.Environment {
		return result, errors.New("repository mismatch")
	}
	if !validReason(o.Reason) {
		return result, errors.New("reason must be daily, pre-update or manual")
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	lock, e := r.lock()
	if e != nil {
		return result, e
	}
	defer lock.Close()
	installationLock, e := installationAdminLock(live)
	if e != nil {
		return result, e
	}
	defer installationLock.Close()
	if _, e = os.Lstat(filepath.Join(r.Dir, "operation.json")); !os.IsNotExist(e) {
		return result, errors.New("unfinished operation; recover first")
	}
	if o.Reason == "daily" {
		items, e := r.list()
		if e != nil {
			return result, e
		}
		loc, _ := time.LoadLocation("Europe/Moscow")
		for _, v := range items {
			if v.Manifest.Reason == "daily" && v.Manifest.Created.In(loc).Format("2006-01-02") == o.Now.In(loc).Format("2006-01-02") {
				p, _ := r.path("daily", v.Manifest.ID)
				if _, e = verifyBackup(ctx, filepath.Join(p, "backup.tar.gz"), filepath.Join(r.Dir, "staging")); e != nil {
					return result, e
				}
				return v, nil
			}
		}
	}
	var size int64
	for _, name := range []string{"planner.db", "planner.db-wal", "data.json", "categories.json"} {
		p := filepath.Join(live, name)
		st, e := os.Lstat(p)
		if os.IsNotExist(e) && name == "planner.db-wal" {
			continue
		}
		if e != nil {
			return result, e
		}
		if e = storageguard.Regular(p, false); e != nil {
			return result, e
		}
		size += st.Size()
	}
	if size > archiveLimit {
		return result, errors.New("store exceeds archive limit")
	}
	if e = o.spaceCheck(r.Dir, 4*size+(64<<20), 32); e != nil {
		return result, e
	}
	// The live filesystem may differ from the archive filesystem. SQLite can
	// checkpoint its existing WAL when maintenance closes the last connection.
	if e = o.spaceCheck(live, size+(64<<20), 16); e != nil {
		return result, e
	}
	stage, e := os.MkdirTemp(filepath.Join(r.Dir, "staging"), ".work-")
	if e != nil {
		return result, e
	}
	defer func() {
		if _, e := os.Lstat(filepath.Join(r.Dir, "operation.json")); os.IsNotExist(e) {
			os.RemoveAll(stage)
		}
	}()
	op, e := r.begin(ctx, o, "backup", stage)
	if e != nil {
		if op.WasRunning {
			err = errors.Join(e, r.resume(op))
			return result, err
		}
		return result, e
	}
	resumed := false
	defer func() {
		if !resumed {
			err = errors.Join(err, r.resume(op))
		}
	}()
	snapshot := filepath.Join(stage, "snapshot")
	if e = os.Mkdir(snapshot, 0700); e != nil {
		return result, e
	}
	lease, e := storageguard.ParentLease(live, true)
	if e != nil {
		return result, e
	}
	// All exits release the lease before trying to resume the application.
	snapshotErr := func() error {
		defer lease.Close()
		if e = storageguard.CheckRecovery(live); e != nil {
			return e
		}
		var store *planner.Store
		if o.Environment == "production" {
			store, e = planner.OpenMaintenance(ctx, filepath.Join(live, "planner.db"), o.ID, false)
		} else {
			store, e = planner.Open(ctx, filepath.Join(live, "planner.db"))
		}
		if e != nil {
			return e
		}
		defer store.Close()
		if e = store.Export(ctx, filepath.Join(snapshot, "planner.db")); e != nil {
			return e
		}
		for _, name := range []string{"data.json", "categories.json"} {
			if e = copyPrivate(ctx, filepath.Join(live, name), filepath.Join(snapshot, name), jsonLimit); e != nil {
				return e
			}
		}
		if e = validateFinancial(snapshot); e != nil {
			return e
		}
		info, e := planner.SnapshotInfo(ctx, filepath.Join(snapshot, "planner.db"), false)
		if e != nil {
			return e
		}
		result.Manifest = backupManifest{Format: 1, ID: newBackupID(o.Now), InstallationID: o.ID, Created: o.Now.UTC(), Reason: o.Reason, Application: currentVersion(), SQLite: info, Checks: backupChecks}
		if e = privateJSON(filepath.Join(snapshot, "settings.json"), map[string]string{"timezone": "Europe/Moscow"}); e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(snapshot, "RESTORE.txt"), []byte(restoreText), 0600); e != nil {
			return e
		}
		for _, name := range archiveNames {
			f, e := hashFile(ctx, filepath.Join(snapshot, name))
			if e != nil {
				return e
			}
			result.Manifest.Files = append(result.Manifest.Files, f)
			descriptor, err := safeOpen(filepath.Join(snapshot, name))
			if err != nil {
				return err
			}
			err = descriptor.Sync()
			descriptor.Close()
			if err != nil {
				return err
			}
		}
		if e = syncDir(snapshot); e != nil {
			return e
		}
		return o.check("snapshot")
	}()
	if snapshotErr != nil {
		return result, snapshotErr
	}
	if e = r.resume(op); e != nil {
		return result, e
	}
	resumed = true
	if e = o.check("resumed"); e != nil {
		return result, e
	}
	publish := filepath.Join(stage, "published")
	if e = os.Mkdir(publish, 0700); e != nil {
		return result, e
	}
	archive := filepath.Join(publish, "backup.tar.gz")
	if e = packBackup(ctx, snapshot, archive, result.Manifest); e != nil {
		return result, e
	}
	if _, e = verifyBackup(ctx, archive, filepath.Join(r.Dir, "staging")); e != nil {
		return result, e
	}
	h, e := hashFile(ctx, archive)
	if e != nil {
		return result, e
	}
	result.Size = h.Size
	result.SHA256 = h.SHA256
	result.Verified = time.Now().UTC()
	if e = privateJSON(filepath.Join(publish, "receipt.json"), result); e != nil {
		return result, e
	}
	for _, name := range []string{"backup.tar.gz", "receipt.json"} {
		if e = shareRepositoryFile(r.Dir, filepath.Join(publish, name), false); e != nil {
			return result, e
		}
	}
	if e = shareRepositoryFile(r.Dir, publish, true); e != nil {
		return result, e
	}
	if e = syncDir(publish); e != nil {
		return result, e
	}
	if e = o.check("publish"); e != nil {
		return result, e
	}
	dest := filepath.Join(r.Dir, o.Reason, result.Manifest.ID)
	if _, e = os.Lstat(dest); !os.IsNotExist(e) {
		return result, errors.New("backup ID collision")
	}
	// Persist protection before publication: a crash must never expose a rollback
	// archive to rotation without its pin. An orphan pin is harmless and retained.
	if o.Protect {
		if e = privateJSON(filepath.Join(r.Dir, "pins", result.Manifest.ID+".json"), map[string]string{"backup_id": result.Manifest.ID, "purpose": "rollback"}); e != nil {
			return result, e
		}
	}
	if e = os.Rename(publish, dest); e != nil {
		return result, e
	}
	if e = syncDir(filepath.Dir(dest)); e != nil {
		return result, e
	}
	if e = r.rotate(ctx); e != nil {
		return result, e
	}
	return result, nil
}
