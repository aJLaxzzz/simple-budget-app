package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func fileOwner(path string) (int, int, error) {
	s, e := os.Stat(path)
	if e != nil {
		return 0, 0, e
	}
	st, ok := s.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, errors.New("unsupported ownership metadata")
	}
	return int(st.Uid), int(st.Gid), nil
}
func setOwner(path string, uid, gid int) error {
	u, g, e := fileOwner(path)
	if e != nil {
		return e
	}
	if u == uid && g == gid {
		return nil
	}
	return os.Chown(path, uid, gid)
}
func shareRepositoryFile(root, path string, directory bool) error {
	_, gid, e := fileOwner(root)
	if e != nil {
		return e
	}
	uid, _, e := fileOwner(path)
	if e != nil {
		return e
	}
	if e = setOwner(path, uid, gid); e != nil {
		return e
	}
	mode := os.FileMode(0640)
	if directory {
		mode = 0750
	}
	return os.Chmod(path, mode)
}
func restoreOwnership(stage, target string) error {
	owner := target
	if !exists(owner) {
		owner = stage
	}
	uid, gid, e := fileOwner(owner)
	if e != nil {
		return e
	}
	entries, e := os.ReadDir(stage)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if e = setOwner(filepath.Join(stage, entry.Name()), uid, gid); e != nil {
			return e
		}
	}
	return setOwner(stage, uid, gid)
}
func separateBackupPaths(live, root string) error {
	for _, pair := range [][2]string{{live, root}, {root, live}} {
		rel, e := filepath.Rel(pair[0], pair[1])
		if e != nil {
			return e
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
			return errors.New("live and backup directories must be separate")
		}
	}
	return nil
}
