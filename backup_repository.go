package main

import (
	"budget/internal/storageguard"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type backupRepository struct{ Dir, ID, Environment string }
type repositoryMarker struct {
	Format      int    `json:"format_version"`
	ID          string `json:"installation_id"`
	Environment string `json:"environment"`
}

func openRepository(dir, id, environment string, create bool) (backupRepository, error) {
	r := backupRepository{ID: id, Environment: environment}
	if !filepath.IsAbs(dir) {
		return r, errors.New("absolute backup directory required")
	}
	real, e := storageguard.Resolve(dir)
	if e != nil {
		return r, e
	}
	r.Dir = real
	if environment == "test" {
		if e = storageguard.TestPath(real); e != nil {
			return r, e
		}
	}
	if _, e = storageguard.Check(real, "development", ""); e != nil {
		return r, e
	} // repositories must never be live data stores
	st, e := os.Stat(real)
	if e != nil {
		return r, e
	}
	if st.Mode().Perm()&0022 != 0 {
		return r, errors.New("backup repository must not be group/world writable")
	}
	if create {
		lock, e := adminLock(filepath.Join(real, "admin.lock"))
		if e != nil {
			return r, e
		}
		defer lock.Close()
	}
	p := filepath.Join(real, "repository.json")
	var m repositoryMarker
	if _, e = os.Lstat(p); os.IsNotExist(e) && create {
		entries, e := os.ReadDir(real)
		if e != nil {
			return r, e
		}
		for _, entry := range entries {
			if entry.Name() != "admin.lock" {
				return r, errors.New("new backup repository must be empty")
			}
		}
		if e = privateJSON(p, repositoryMarker{1, id, environment}); e != nil {
			return r, e
		}

	}
	if e = readSmall(p, &m, 4096); e != nil {
		return r, e
	}
	if m.Format != 1 || m.ID != id || m.Environment != environment {
		return r, errors.New("backup repository installation/environment mismatch")
	}
	if create {
		// Complete a safely interrupted repository initialization under its lock.
		for _, name := range []string{"daily", "pre-update", "manual", "staging", "pins"} {
			p := filepath.Join(real, name)
			if _, err := os.Lstat(p); os.IsNotExist(err) {
				if e = os.Mkdir(p, 0700); e != nil {
					return r, e
				}
			}
			if _, e = storageguard.Resolve(p); e != nil {
				return r, e
			}
		}
		for _, name := range []string{"repository.json", "daily", "pre-update", "manual"} {
			if e = shareRepositoryFile(real, filepath.Join(real, name), name != "repository.json"); e != nil {
				return r, e
			}
		}
		if e = syncDir(real); e != nil {
			return r, e
		}
	}
	for _, name := range []string{"daily", "pre-update", "manual", "staging", "pins"} {
		if _, e = storageguard.Resolve(filepath.Join(real, name)); e != nil {
			return r, e
		}
	}
	return r, nil
}
func adminLock(path string) (*os.File, error) {
	if _, e := os.Lstat(path); e == nil {
		if e = storageguard.Regular(path, false); e != nil {
			return nil, e
		}
	}
	f, e := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("another administrative operation is running")
	}
	return f, nil
}
func (r backupRepository) lock() (*os.File, error) {
	return adminLock(filepath.Join(r.Dir, "admin.lock"))
}
func (r backupRepository) path(reason, id string) (string, error) {
	if !validReason(reason) || !backupIDPattern.MatchString(id) {
		return "", errors.New("invalid backup ID/category")
	}
	p := filepath.Join(r.Dir, reason, id)
	if _, e := storageguard.Resolve(p); e != nil {
		return "", e
	}
	return p, nil
}
func (r backupRepository) receipt(reason, id string) (backupReceipt, error) {
	var out backupReceipt
	p, e := r.path(reason, id)
	if e != nil {
		return out, e
	}
	if e = readSmall(filepath.Join(p, "receipt.json"), &out, 128<<10); e != nil {
		return out, e
	}
	if e = validateManifest(out.Manifest); e != nil {
		return out, e
	}
	if out.Manifest.ID != id || out.Manifest.Reason != reason || out.Manifest.InstallationID != r.ID || out.Size < 1 || out.Size > archiveLimit || len(out.SHA256) != 64 || out.Verified.IsZero() {
		return out, errors.New("invalid published receipt")
	}
	return out, nil
}
func (r backupRepository) list() ([]backupReceipt, error) {
	items := []backupReceipt{}
	for _, reason := range []string{"daily", "pre-update", "manual"} {
		entries, e := os.ReadDir(filepath.Join(r.Dir, reason))
		if e != nil {
			return nil, e
		}
		if len(entries) > 10000 {
			return nil, errors.New("too many repository entries")
		}
		for _, entry := range entries {
			if !backupIDPattern.MatchString(entry.Name()) {
				return nil, errors.New("unexpected published entry")
			}
			v, e := r.receipt(reason, entry.Name())
			if e != nil {
				return nil, e
			}
			items = append(items, v)
		}
	}
	sortedReceipts(items)
	return items, nil
}
func (r backupRepository) find(id string) (backupReceipt, string, error) {
	for _, reason := range []string{"daily", "pre-update", "manual"} {
		v, e := r.receipt(reason, id)
		if e == nil {
			return v, filepath.Join(r.Dir, reason, id, "backup.tar.gz"), nil
		}
	}
	return backupReceipt{}, "", errors.New("verified backup not found")
}
func (r backupRepository) pin(id string, protect bool) error {
	if _, _, e := r.find(id); e != nil {
		return e
	}
	p := filepath.Join(r.Dir, "pins", id+".json")
	if protect {
		return privateJSON(p, map[string]string{"backup_id": id, "purpose": "rollback"})
	}
	if e := storageguard.Regular(p, false); e != nil {
		return e
	}
	if e := os.Remove(p); e != nil {
		return e
	}
	return syncDir(filepath.Dir(p))
}
func (r backupRepository) pinned(id string) (bool, error) {
	p := filepath.Join(r.Dir, "pins", id+".json")
	if _, e := os.Lstat(p); os.IsNotExist(e) {
		return false, nil
	}
	var v map[string]string
	if e := readSmall(p, &v, 4096); e != nil {
		return false, e
	}
	if v["backup_id"] != id {
		return false, errors.New("invalid protection record")
	}
	return true, nil
}
func (r backupRepository) rotate(ctx context.Context) error {
	items, e := r.list()
	if e != nil {
		return e
	}
	kept := map[string]int{}
	for _, v := range items {
		reason := v.Manifest.Reason
		if reason == "manual" {
			continue
		}
		pin, e := r.pinned(v.Manifest.ID)
		if e != nil {
			return e
		}
		if pin {
			continue
		}
		limit := 7
		if reason == "pre-update" {
			limit = 2
		}
		kept[reason]++
		if kept[reason] <= limit {
			continue
		}
		// Never prune based on an unverified set of candidates. Validate every kept copy first.
		for _, keep := range items {
			if keep.Manifest.Reason != reason || !keep.Manifest.Created.After(v.Manifest.Created) {
				continue
			}
			p, _ := r.path(reason, keep.Manifest.ID)
			h, e := hashFile(ctx, filepath.Join(p, "backup.tar.gz"))
			if e != nil || h.SHA256 != keep.SHA256 {
				return errors.New("rotation refused: published archive checksum failed")
			}
		}
		p, e := r.path(reason, v.Manifest.ID)
		if e != nil {
			return e
		}
		for _, name := range []string{"backup.tar.gz", "receipt.json"} {
			if e = storageguard.Regular(filepath.Join(p, name), false); e != nil {
				return e
			}
		}
		entries, e := os.ReadDir(p)
		if e != nil || len(entries) != 2 {
			return errors.New("rotation refused: unexpected backup files")
		}
		// Open download descriptors remain valid on Unix after unlink; no shared write lease needed.
		tomb := filepath.Join(r.Dir, "staging", ".retired-"+v.Manifest.ID)
		if e = os.Rename(p, tomb); e != nil {
			return e
		}
		if e = syncDir(filepath.Dir(p)); e != nil {
			return e
		}
		if e = os.RemoveAll(tomb); e != nil {
			return e
		}
	}
	return nil
}
func (r backupRepository) cleanStaging() error {
	// Under admin lock, and only when no recovery operation references staging.
	if _, e := os.Lstat(filepath.Join(r.Dir, "operation.json")); !os.IsNotExist(e) {
		return errors.New("recover the unfinished operation before cleaning staging")
	}
	entries, e := os.ReadDir(filepath.Join(r.Dir, "staging"))
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".work-") && !strings.HasPrefix(entry.Name(), ".retired-") && !strings.HasPrefix(entry.Name(), ".verify-") {
			return errors.New("unknown staging entry preserved")
		}
		p := filepath.Join(r.Dir, "staging", entry.Name())
		if _, e = storageguard.Resolve(p); e != nil {
			return e
		}
		if e = os.RemoveAll(p); e != nil {
			return e
		}
	}
	return nil
}

type backupSchedule struct {
	Enabled  bool      `json:"enabled"`
	Observed time.Time `json:"observed_at"`
	Next     time.Time `json:"next_at"`
	Source   string    `json:"source"`
}
type backupStatus struct {
	Configured bool           `json:"configured"`
	Latest     *backupReceipt `json:"latest"`
	Daily      int            `json:"daily_count"`
	Stale      bool           `json:"stale"`
	Automatic  string         `json:"automatic"`
	Next       *time.Time     `json:"next_at,omitempty"`
	Warning    string         `json:"warning,omitempty"`
}

func (r backupRepository) status(now time.Time) (backupStatus, error) {
	s := backupStatus{Configured: true, Stale: true, Automatic: "not_installed"}
	items, e := r.list()
	if e != nil {
		return s, e
	}
	if len(items) > 0 {
		s.Latest = &items[0]
		s.Stale = now.Sub(items[0].Manifest.Created) > 26*time.Hour
	}
	for _, v := range items {
		if v.Manifest.Reason == "daily" {
			s.Daily++
		}
	}
	var schedule backupSchedule
	if e = readSmall(filepath.Join(r.Dir, "schedule.json"), &schedule, 4096); e == nil {
		if schedule.Enabled && schedule.Source == "systemd" && now.Sub(schedule.Observed) >= 0 && now.Sub(schedule.Observed) < 24*time.Hour && schedule.Next.After(now) {
			s.Automatic = "enabled"
			s.Next = &schedule.Next
		} else {
			s.Automatic = "unknown"
		}
	}
	if s.Latest == nil {
		s.Warning = "Проверенных резервных копий пока нет"
	} else if s.Stale {
		s.Warning = "Последняя успешная копия старше 26 часов"
	}
	return s, nil
}
