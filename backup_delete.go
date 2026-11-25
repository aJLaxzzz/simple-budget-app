package main

import (
	"budget/internal/storageguard"
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Called with repository admin.lock held. Explicit pruning never removes a pin
// or the final verified copy; deployment references are checked by root wrapper.
func (r backupRepository) deleteConfirmed(ctx context.Context, id string) error {
	if !backupIDPattern.MatchString(id) {
		return errors.New("invalid backup ID")
	}
	pinned, e := r.pinned(id)
	if e != nil {
		return e
	}
	if pinned {
		return errors.New("backup is protected; resolve rollback dependencies before unprotecting")
	}
	item, p, e := r.find(id)
	if e != nil {
		return e
	}
	items, e := r.list()
	if e != nil {
		return e
	}
	replacement := false
	for _, v := range items {
		if v.Manifest.ID == id {
			continue
		}
		_, other, e := r.find(v.Manifest.ID)
		if e != nil {
			continue
		}
		if _, e = verifyBackup(ctx, other, filepath.Join(r.Dir, "staging")); e == nil {
			replacement = true
			break
		}
	}
	if !replacement {
		return errors.New("refusing deletion of the last verified backup")
	}
	dir := filepath.Dir(p)
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 2 {
		return errors.New("unexpected backup contents")
	}
	for _, name := range []string{"backup.tar.gz", "receipt.json"} {
		if e = storageguard.Regular(filepath.Join(dir, name), false); e != nil {
			return e
		}
	}
	tomb := filepath.Join(r.Dir, "staging", ".retired-"+item.Manifest.ID)
	if exists(tomb) {
		return errors.New("unfinished retirement; clean known staging first")
	}
	if e = os.Rename(dir, tomb); e != nil {
		return e
	}
	if e = syncDir(filepath.Dir(dir)); e != nil {
		return e
	}
	if e = os.RemoveAll(tomb); e != nil {
		return e
	}
	return syncDir(filepath.Dir(tomb))
}
