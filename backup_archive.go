package main

import (
	"archive/tar"
	"budget/internal/planner"
	"budget/internal/storageguard"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"
)

const archiveLimit int64 = 8 << 30
const jsonLimit int64 = 16 << 20

var backupIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[a-f0-9]{16}$`)
var archiveNames = []string{"planner.db", "data.json", "categories.json", "settings.json", "RESTORE.txt"}

type backupFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type backupManifest struct {
	Format         int                      `json:"format_version"`
	ID             string                   `json:"backup_id"`
	InstallationID string                   `json:"installation_id"`
	Created        time.Time                `json:"created_at"`
	Reason         string                   `json:"reason"`
	Application    versionInfo              `json:"application"`
	SQLite         planner.SnapshotMetadata `json:"sqlite"`
	Files          []backupFile             `json:"files"`
	Checks         []string                 `json:"checks"`
}
type backupReceipt struct {
	Manifest backupManifest `json:"manifest"`
	Size     int64          `json:"size"`
	SHA256   string         `json:"sha256"`
	Verified time.Time      `json:"verified_at"`
}

var backupChecks = []string{"sqlite-application-schema-ledger", "sqlite-integrity-foreign-keys", "sqlite-icons", "financial-json", "file-sha256"}

const restoreText = "Simple Budget backup format 1. Restore with a compatible budget binary: admin backup verify --archive <file>; then admin backup restore --archive <file> --data-dir <absolute private directory> --backup-dir <repository> --installation-id <manifest installation_id> --confirm. Stop the application or supply a trusted service controller. Secrets and access configuration are NOT included. See docs/BACKUP-RECOVERY.md. Never overwrite a live WAL database.\n"

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func syncDir(p string) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func privateJSON(p string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	mode := os.FileMode(0600)
	uid, gid := -1, -1
	if st, err := os.Lstat(p); err == nil {
		if e = storageguard.Regular(p, false); e != nil {
			return e
		}
		mode = st.Mode().Perm()
		uid, gid, e = fileOwner(p)
		if e != nil {
			return e
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".metadata-*.tmp")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if e = f.Chmod(mode); e != nil {
		return e
	}
	if uid >= 0 {
		if e = setOwner(f.Name(), uid, gid); e != nil {
			return e
		}
	}
	if _, e = f.Write(append(b, '\n')); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), p); e != nil {
		return e
	}
	return syncDir(filepath.Dir(p))
}
func readSmall(p string, v any, limit int64) error {
	f, e := safeOpen(p)
	if e != nil {
		return e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return e
	}
	if int64(len(b)) > limit {
		return errors.New("metadata too large")
	}
	return decodeStoredJSON(b, v)
}
func safeOpen(p string) (*os.File, error) {
	if _, e := storageguard.Resolve(filepath.Dir(p)); e != nil {
		return nil, e
	}
	if e := storageguard.Regular(p, false); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	a, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	b, e := os.Lstat(p)
	if e != nil || !os.SameFile(a, b) || !a.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("file changed while opening")
	}
	if st, ok := a.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
		f.Close()
		return nil, errors.New("hardlinks forbidden")
	}
	return f, nil
}
func copyPrivate(ctx context.Context, src, dst string, limit int64) error {
	in, e := safeOpen(src)
	if e != nil {
		return e
	}
	defer in.Close()
	st, e := in.Stat()
	if e != nil {
		return e
	}
	if st.Size() > limit {
		return errors.New("file exceeds backup limit")
	}
	out, e := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	n, e := io.Copy(out, contextReader{ctx, io.LimitReader(in, limit+1)})
	if e == nil && n > limit {
		e = errors.New("file exceeds backup limit")
	}
	if e == nil {
		e = out.Sync()
	}
	return errors.Join(e, out.Close())
}
func checkSpace(path string, temporary int64, inodes uint64) error {
	if temporary < 0 || temporary > 4*archiveLimit {
		return errors.New("invalid space estimate")
	}
	var s syscall.Statfs_t
	if e := syscall.Statfs(path, &s); e != nil {
		return e
	}
	capacity := uint64(s.Blocks) * uint64(s.Bsize)
	free := uint64(s.Bavail) * uint64(s.Bsize)
	return checkCapacity(capacity, free, uint64(s.Files), uint64(s.Ffree), temporary, inodes)
}
func checkCapacity(capacity, free, files, freeInodes uint64, temporary int64, inodes uint64) error {
	if temporary < 0 || temporary > 4*archiveLimit {
		return errors.New("invalid space estimate")
	}
	reserve := capacity / 100 * 15
	if reserve < 2<<30 {
		reserve = 2 << 30
	}
	if free < reserve || uint64(temporary) > free-reserve {
		return errors.New("insufficient free space: reserve max(2 GiB, 15%) plus operation staging required")
	}
	if files > 0 && freeInodes < inodes+64 {
		return errors.New("insufficient free inodes")
	}
	return nil
}

// A byte limit alone does not bound decoded slices of millions of empty
// objects. Bound JSON structure before allocating the financial model.
func checkJSONStructure(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	depth, tokens := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("invalid financial JSON")
		}
		tokens++
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter == '{' || delimiter == '[' {
				depth++
			} else {
				depth--
			}
		}
		if tokens > 200000 || depth > 64 {
			return errors.New("financial JSON structure exceeds backup resource limits")
		}
	}
}

func validateFinancial(dir string) error {
	var d dataStore
	for _, name := range []string{"data.json", "categories.json"} {
		f, e := safeOpen(filepath.Join(dir, name))
		if e != nil {
			return e
		}
		b, e := io.ReadAll(io.LimitReader(f, jsonLimit+1))
		f.Close()
		if e != nil {
			return e
		}
		if int64(len(b)) > jsonLimit {
			return errors.New("financial JSON exceeds 16 MiB")
		}
		if e = checkJSONStructure(b); e != nil {
			return e
		}
		if name == "data.json" {
			var shape map[string]json.RawMessage
			if json.Unmarshal(b, &shape) != nil || shape["months"] == nil {
				return errors.New("invalid financial store shape")
			}
			if e = decodeStoredJSON(b, &d); e != nil {
				return errors.New("financial JSON incompatible with application")
			}
		} else {
			var c CategoriesResponse
			if bytes.HasPrefix(bytes.TrimSpace(b), []byte("[")) {
				e = decodeStoredJSON(b, &c.Expense)
			} else {
				var shape map[string]json.RawMessage
				if json.Unmarshal(b, &shape) != nil || (shape["expense"] == nil && shape["income"] == nil) {
					return errors.New("invalid categories shape")
				}
				e = decodeStoredJSON(b, &c)
			}
			if e != nil {
				return errors.New("categories JSON incompatible with application")
			}
		}
	}
	return nil
}
func hashFile(ctx context.Context, p string) (backupFile, error) {
	f, e := safeOpen(p)
	if e != nil {
		return backupFile{}, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, contextReader{ctx, f})
	return backupFile{Name: filepath.Base(p), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, e
}
func newBackupID(now time.Time) string {
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b)
}
func validReason(s string) bool { return s == "daily" || s == "pre-update" || s == "manual" }
func validateManifest(m backupManifest) error {
	if m.Format != 1 || !backupIDPattern.MatchString(m.ID) || !validReason(m.Reason) || m.Created.IsZero() || m.Application.Application != "simple-budget-app" || m.SQLite.Schema < 1 || m.SQLite.Schema > planner.SchemaVersion() || len(m.SQLite.Migrations) != m.SQLite.Schema {
		return errors.New("unsupported backup manifest")
	}
	if len(m.InstallationID) < 16 || len(m.InstallationID) > 128 || filepath.Base(m.InstallationID) != m.InstallationID {
		return errors.New("invalid archive installation identity")
	}
	if len(m.Files) != len(archiveNames) || len(m.Checks) != len(backupChecks) {
		return errors.New("incomplete manifest")
	}
	total := int64(0)
	for i, f := range m.Files {
		if f.Name != archiveNames[i] || f.Size < 0 || f.Size > archiveLimit {
			return errors.New("invalid archive file list")
		}
		if ((f.Name == "data.json" || f.Name == "categories.json") && f.Size > jsonLimit) || (f.Name == "settings.json" && f.Size > 4096) || (f.Name == "RESTORE.txt" && f.Size > 64<<10) {
			return errors.New("archive member exceeds format limit")
		}
		sum, e := hex.DecodeString(f.SHA256)
		if e != nil || len(sum) != 32 {
			return errors.New("invalid checksum")
		}
		total += f.Size
	}
	if total > archiveLimit {
		return errors.New("archive expanded size exceeds limit")
	}
	for i, c := range backupChecks {
		if m.Checks[i] != c {
			return errors.New("missing validation result")
		}
	}
	return nil
}
func packBackup(ctx context.Context, dir, destination string, m backupManifest) error {
	f, e := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	write := func(name string, size int64, r io.Reader) error {
		if e := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: size, Typeflag: tar.TypeReg, ModTime: m.Created}); e != nil {
			return e
		}
		_, e := io.Copy(tw, contextReader{ctx, r})
		return e
	}
	raw, e := json.Marshal(m)
	if e == nil {
		e = write("manifest.json", int64(len(raw)), bytes.NewReader(raw))
	}
	for _, entry := range m.Files {
		if e != nil {
			break
		}
		var in *os.File
		in, e = safeOpen(filepath.Join(dir, entry.Name))
		if e == nil {
			e = write(entry.Name, entry.Size, in)
			in.Close()
		}
	}
	e = errors.Join(e, tw.Close(), gz.Close())
	if e == nil {
		e = f.Sync()
	}
	return errors.Join(e, f.Close())
}

// extractBackup accepts exactly six regular entries, bounded before allocation.
// gzip CRC and trailing data are checked; no path from the archive is trusted.
func extractBackup(ctx context.Context, archive, dir string) (backupManifest, error) {
	var m backupManifest
	f, e := safeOpen(archive)
	if e != nil {
		return m, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return m, e
	}
	if st.Size() > archiveLimit {
		return m, errors.New("compressed archive too large")
	}
	gz, e := gzip.NewReader(contextReader{ctx, f})
	if e != nil {
		return m, e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	h, e := tr.Next()
	if e != nil {
		return m, e
	}
	if h.Name != "manifest.json" || h.Typeflag != tar.TypeReg || h.Size > 64<<10 || h.Size < 1 || h.Linkname != "" || len(h.PAXRecords) > 0 {
		return m, errors.New("manifest must be first bounded regular entry")
	}
	raw, e := io.ReadAll(tr)
	if e != nil {
		return m, e
	}
	if e = decodeStoredJSON(raw, &m); e != nil {
		return m, e
	}
	if e = validateManifest(m); e != nil {
		return m, e
	}
	total := int64(0)
	for _, entry := range m.Files {
		total += entry.Size
	}
	// Reserve the expanded snapshot AND SQLite migration journal/WAL overhead;
	// compressed size alone is not a safe estimate for highly compressible data.
	if e = checkSpace(dir, 3*total+(64<<20), uint64(len(m.Files)+8)); e != nil {
		return m, e
	}
	for _, entry := range m.Files {
		h, e = tr.Next()
		if e != nil {
			return m, e
		}
		if h.Typeflag != tar.TypeReg || h.Name != entry.Name || h.Size != entry.Size || h.Linkname != "" || len(h.PAXRecords) > 0 {
			return m, errors.New("unexpected or unsafe archive entry")
		}
		out, e := os.OpenFile(filepath.Join(dir, entry.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return m, e
		}
		hash := sha256.New()
		n, e := io.Copy(io.MultiWriter(out, hash), contextReader{ctx, tr})
		if e == nil {
			e = out.Sync()
		}
		e = errors.Join(e, out.Close())
		if e != nil {
			return m, e
		}
		if n != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return m, errors.New("archive checksum mismatch")
		}
	}
	if _, e = tr.Next(); e != io.EOF {
		return m, errors.New("unexpected trailing archive entry")
	}
	// tar stops at its end marker; consume gzip to verify CRC and reject trailing payload.
	trailing, e := io.ReadAll(io.LimitReader(gz, 1025))
	if e != nil {
		return m, e
	}
	if len(trailing) != 0 {
		return m, errors.New("trailing archive data")
	}
	if e = validateFinancial(dir); e != nil {
		return m, e
	}
	var settings struct {
		Timezone string `json:"timezone"`
	}
	if e = readSmall(filepath.Join(dir, "settings.json"), &settings, 4096); e != nil || settings.Timezone != "Europe/Moscow" {
		return m, errors.New("unsupported settings")
	}
	info, e := planner.SnapshotInfo(ctx, filepath.Join(dir, "planner.db"), true)
	if e != nil {
		return m, e
	}
	a, _ := json.Marshal(info)
	b, _ := json.Marshal(m.SQLite)
	if !bytes.Equal(a, b) {
		return m, errors.New("SQLite metadata differs from manifest")
	}
	return m, nil
}
func verifyBackup(ctx context.Context, archive, parent string) (backupManifest, error) {
	dir, e := os.MkdirTemp(parent, ".verify-")
	if e != nil {
		return backupManifest{}, e
	}
	defer os.RemoveAll(dir)
	return extractBackup(ctx, archive, dir)
}
func sortedReceipts(items []backupReceipt) {
	sort.Slice(items, func(i, j int) bool { return items[i].Manifest.Created.After(items[j].Manifest.Created) })
}
