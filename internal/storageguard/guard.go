// Package storageguard validates storage identity before any database or fixture write.
package storageguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const MarkerName = "installation.json"

type Marker struct {
	Version     int    `json:"version"`
	Environment string `json:"environment"`
	ID          string `json:"installation_id"`
}

// Resolve rejects application-controlled symlinks; macOS system /tmp and /var
// aliases are allowed, but are resolved before containment and marker checks.
func Resolve(dir string) (string, error) {
	abs, e := filepath.Abs(dir)
	if e != nil {
		return "", e
	}
	for p := abs; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return "", errors.New("storage path does not exist")
		}
		if info.Mode()&os.ModeSymlink != 0 && p != "/tmp" && p != "/var" {
			return "", errors.New("storage path must not contain symlinks")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	real, e := filepath.EvalSymlinks(abs)
	if e != nil {
		return "", e
	}
	info, e := os.Stat(real)
	if e != nil || !info.IsDir() {
		return "", errors.New("storage must be a directory")
	}
	return real, nil
}
func Regular(path string, private bool) error {
	i, e := os.Lstat(path)
	if e != nil {
		return errors.New("required storage file is missing")
	}
	if !i.Mode().IsRegular() {
		return errors.New("storage file must be regular, not a symlink")
	}
	if st, ok := i.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
		return errors.New("hard-linked storage files are forbidden")
	}
	if private && i.Mode().Perm()&0077 != 0 {
		return errors.New("storage file must have owner-only permissions")
	}
	return nil
}
func ReadMarker(dir string) (Marker, error) {
	var m Marker
	p := filepath.Join(dir, MarkerName)
	if e := Regular(p, false); e != nil {
		return m, e
	}
	i, e := os.Stat(p)
	if e != nil || i.Size() > 4096 {
		return m, errors.New("invalid installation marker")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return m, e
	}
	if e = json.Unmarshal(b, &m); e != nil || m.Version != 1 || (m.Environment != "production" && m.Environment != "test") || len(m.ID) < 16 || len(m.ID) > 128 || strings.ContainsAny(m.ID, "/\\\r\n\t ") {
		return m, errors.New("invalid installation marker")
	}
	return m, nil
}
func Check(dir, environment, id string) (string, error) {
	real, e := Resolve(dir)
	if e != nil {
		return "", e
	}
	for p := real; ; p = filepath.Dir(p) {
		_, err := os.Lstat(filepath.Join(p, MarkerName))
		if err == nil {
			m, err := ReadMarker(p)
			if err != nil {
				return "", err
			}
			if m.Environment == "production" && (environment != "production" || p != real) {
				return "", errors.New("production storage cannot be opened by development or tests")
			}
			if environment == "production" && (m.Environment != "production" || p != real || m.ID != id) {
				return "", errors.New("production installation identity mismatch")
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if environment == "production" || environment == "test" {
		m, err := ReadMarker(real)
		if err != nil {
			return "", err
		}
		if m.Environment != environment || (id != "" && m.ID != id) {
			return "", errors.New("installation marker does not match environment")
		}
	}
	if environment == "production" {
		info, err := os.Stat(real)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("production directory must have owner-only permissions")
		}
		if err := Regular(filepath.Join(real, MarkerName), true); err != nil {
			return "", err
		}
	}
	if environment == "test" {
		if e = TestPath(real); e != nil {
			return "", e
		}
	}
	return real, nil
}
func TestPath(dir string) error {
	real, e := Resolve(dir)
	if e != nil {
		return e
	}
	root, e := filepath.EvalSymlinks(os.TempDir())
	if e != nil {
		return e
	}
	rel, e := filepath.Rel(root, real)
	if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("tests require a private directory below the system temporary directory")
	}
	_, e = Check(real, "development", "")
	return e
}
func WriteMarker(dir string, m Marker) error {
	if m.Environment != "test" && m.Environment != "production" {
		return errors.New("invalid environment")
	}
	if _, e := Check(dir, "development", ""); e != nil {
		return e
	}
	if m.Environment == "test" {
		if e := TestPath(dir); e != nil {
			return e
		}
	}
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(dir, MarkerName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fmt.Errorf("create installation marker: %w", e)
	}
	_, e = f.Write(append(b, '\n'))
	if e == nil {
		e = f.Sync()
	}
	return errors.Join(e, f.Close())
}

// TestEnvironment is called before test fixtures are created. Environment
// variables must not redirect a Go test process into a production installation.
func TestEnvironment() error {
	if os.Getenv("BUDGET_ENV") == "production" || os.Getenv("CREDENTIALS_DIRECTORY") != "" {
		return errors.New("production environment is forbidden for tests")
	}
	if _, e := Check(os.TempDir(), "development", ""); e != nil {
		return e
	}
	for _, key := range []string{"BUDGET_DATA_DIR", "BUDGET_BACKUP_DIR", "PLANNER_TEST_DIR", "FINAL_REVIEW_DIR"} {
		if dir := os.Getenv(key); dir != "" {
			if e := TestPath(dir); e != nil {
				return errors.New("unsafe test storage environment: " + key)
			}
		}
	}
	return nil
}
