package main

import (
	"budget/internal/storageguard"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func backupCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: admin backup create|list|verify|restore|recover|rotate|protect|unprotect|status|schedule")
	}
	f := flag.NewFlagSet("backup", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	data := f.String("data-dir", "", "absolute live directory")
	root := f.String("backup-dir", "", "absolute backup repository")
	id := f.String("installation-id", "", "expected installation ID")
	env := f.String("environment", "production", "production or test")
	archive := f.String("archive", "", "archive to verify or restore")
	reason := f.String("reason", "manual", "daily, pre-update, manual")
	scope := f.String("scope", "full", "full or planner")
	controller := f.String("controller", "", "trusted executable: status/stopped/stop/start <installation-id>")
	confirm := f.Bool("confirm", false, "explicit recovery authorization")
	protect := f.Bool("protect", false, "protect new backup for rollback")
	backupID := f.String("backup-id", "", "verified backup ID")
	decision := f.String("decision", "", "finish or rollback interrupted restore")
	next := f.String("next-at", "", "observed next systemd timer RFC3339")
	enabled := f.Bool("enabled", false, "confirmed installed timer")
	if e := f.Parse(args[1:]); e != nil || f.NArg() != 0 {
		return errors.New("invalid backup arguments")
	}
	if *env != "test" && *env != "production" {
		return errors.New("backup environment must be explicit test or production")
	}
	if flag.Lookup("test.v") != nil || *env == "test" {
		for _, p := range []string{*root, *data} {
			if p != "" {
				parent := p
				if !exists(parent) {
					parent = filepath.Dir(parent)
				}
				if e := storageguard.TestPath(parent); e != nil {
					return e
				}
			}
		}
		if *archive != "" {
			if e := storageguard.TestPath(filepath.Dir(*archive)); e != nil {
				return e
			}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if args[0] == "verify" {
		if !filepath.IsAbs(*archive) {
			return errors.New("absolute --archive required")
		}
		m, e := verifyBackup(ctx, *archive, "")
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(m)
	}
	if len(*id) < 16 {
		return errors.New("--installation-id required")
	}
	r, e := openRepository(*root, *id, *env, args[0] == "create" || args[0] == "restore")
	if e != nil {
		return e
	}
	o := backupOptions{DataDir: filepath.Clean(*data), ID: *id, Environment: *env, Reason: *reason, Controller: *controller, Protect: *protect}
	if filepath.IsAbs(o.DataDir) {
		parent, err := storageguard.Resolve(filepath.Dir(o.DataDir))
		if err != nil {
			return err
		}
		o.DataDir = filepath.Join(parent, filepath.Base(o.DataDir))
	}
	encode := func(v any, e error) error {
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	switch args[0] {
	case "create":
		v, e := createBackup(ctx, r, o)
		return encode(v, e)
	case "list":
		v, e := r.list()
		return encode(v, e)
	case "status":
		v, e := r.status(time.Now())
		return encode(v, e)
	case "restore":
		if !*confirm {
			return errors.New("restore requires --confirm; verify the archive and installation ID first")
		}
		if !filepath.IsAbs(*archive) {
			return errors.New("absolute --archive required")
		}
		previous, e := restoreBackup(ctx, r, o, *archive, *scope)
		return encode(map[string]string{"previous_data": previous, "result": "restored"}, e)
	case "rotate", "protect", "unprotect", "schedule", "delete":
		lock, e := r.lock()
		if e != nil {
			return e
		}
		defer lock.Close()
		if exists(filepath.Join(r.Dir, "operation.json")) {
			return errors.New("recover unfinished operation first")
		}
		if args[0] == "delete" {
			if !*confirm {
				return errors.New("backup deletion requires --confirm")
			}
			e = r.deleteConfirmed(ctx, *backupID)
		} else if args[0] == "rotate" {
			if e = r.rotate(ctx); e != nil {
				return e
			}
			e = r.cleanStaging()
		} else if args[0] == "schedule" {
			when := time.Time{}
			if *enabled {
				when, e = time.Parse(time.RFC3339, *next)
				if e != nil || !when.After(time.Now()) {
					return errors.New("enabled schedule requires actual future --next-at")
				}
			}
			e = privateJSON(filepath.Join(r.Dir, "schedule.json"), backupSchedule{Enabled: *enabled, Observed: time.Now().UTC(), Next: when, Source: "systemd"})
			if e == nil {
				e = shareRepositoryFile(r.Dir, filepath.Join(r.Dir, "schedule.json"), false)
			}
		} else {
			if args[0] == "unprotect" && !*confirm {
				return errors.New("unprotect requires --confirm; ensure no rollback depends on this copy")
			}
			e = r.pin(*backupID, args[0] == "protect")
		}
		return encode(map[string]string{"result": "ok"}, e)
	case "recover":
		if !*confirm {
			return errors.New("recovery requires --confirm")
		}
		lock, e := r.lock()
		if e != nil {
			return e
		}
		defer lock.Close()
		var op backupOperation
		if e = readSmall(filepath.Join(r.Dir, "operation.json"), &op, 64<<10); e != nil {
			return e
		}
		if op.InstallationID != *id || !filepath.IsAbs(op.DataDir) || op.DataDir != o.DataDir {
			return errors.New("recovery identity/data path mismatch")
		}
		installationLock, e := installationAdminLock(op.DataDir)
		if e != nil {
			return e
		}
		defer installationLock.Close()
		// Ensure service is stopped before directory recovery, even if a controller was interrupted.
		c := serviceController{op.Controller, op.InstallationID}
		running, e := c.running(ctx)
		if e != nil {
			return e
		}
		if running {
			if e = c.call(ctx, "stop"); e != nil {
				return e
			}
		}
		lease, e := storageguard.ParentLease(op.DataDir, true)
		if e != nil {
			return e
		}
		unlock, e := lockRecoveryDatabases(o)
		if e != nil {
			lease.Close()
			return e
		}
		if op.Kind == "restore" && exists(storageguard.RestoreJournal(op.DataDir)) {
			e = recoverRestore(ctx, o, *decision)
		} else if op.Kind != "backup" && op.Kind != "restore" {
			e = errors.New("unknown recovery operation")
		}
		unlock()
		lease.Close()
		if e != nil {
			return e
		}
		if e = r.resume(op); e != nil {
			return e
		}
		return encode(map[string]string{"result": "recovered"}, r.cleanStaging())
	default:
		return errors.New("unknown backup command")
	}
}
