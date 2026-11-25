package storageguard

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// A parent-directory flock survives whole-data-directory replacement. Servers
// hold a shared lease, maintenance an exclusive lease. No privileged file write
// by the web process is necessary. Sibling installations serialize maintenance.
func ParentLease(dir string, exclusive bool) (*os.File, error) {
	parent, e := Resolve(filepath.Dir(dir))
	if e != nil {
		return nil, e
	}
	f, e := os.Open(parent)
	if e != nil {
		return nil, e
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if e = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("installation is busy; stop the application before maintenance")
	}
	return f, nil
}
func RestoreJournal(dir string) string {
	return filepath.Join(filepath.Dir(dir), "."+filepath.Base(dir)+".restore.json")
}
func CheckRecovery(dir string) error {
	if _, e := os.Lstat(RestoreJournal(dir)); !os.IsNotExist(e) {
		return errors.New("incomplete administrative restore; run backup recover before startup")
	}
	return nil
}
