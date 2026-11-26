package main

import (
	"archive/tar"
	"budget/internal/planner"
	"budget/internal/storageguard"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func backupFixture(t *testing.T) (backupRepository, backupOptions) {
	t.Helper()
	base := t.TempDir()
	live := filepath.Join(base, "live")
	root := filepath.Join(base, "archives")
	for _, p := range []string{live, root} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	id := "abcdef0123456789abcdef0123456789"
	ctx := context.Background()
	s, e := planner.Open(ctx, filepath.Join(live, "planner.db"))
	if e != nil {
		t.Fatal(e)
	}
	h := planner.NewHandler(planner.NewService(s))
	call := func(path, body string) map[string]any {
		r := httptest.NewRequest("POST", "/api/planner/"+path, strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code >= 400 {
			t.Fatal(w.Code, w.Body.String())
		}
		var v map[string]any
		json.Unmarshal(w.Body.Bytes(), &v)
		return v
	}
	p := call("projects", `{"title":"Backup fixture","color_id":"teal"}`)
	task := call("tasks", `{"title":"Keep ID","project_id":"`+p["id"].(string)+`","planned_date":"2026-12-02"}`)
	call("tasks", `{"title":"Child","project_id":"`+p["id"].(string)+`","parent_task_id":"`+task["id"].(string)+`"}`)
	call("sessions", `{"task_id":"`+task["id"].(string)+`","date":"2026-12-02","start_time":"10:00","end_time":"11:00"}`)
	call("series", `{"request_id":"backup-series-fixture","task":{"title":"Annual"},"first_date":"2026-12-01","rule":{"schema_version":1,"mode":"schedule","unit":"years","interval":1,"allowed_days":{"mode":"any"},"adjustment":"none"}}`)
	var icon bytes.Buffer
	if e = png.Encode(&icon, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]any{"name": "Synthetic custom icon", "data": icon.Bytes()})
	call("icons", string(raw))
	calendarResponse := httptest.NewRecorder()
	h.ServeHTTP(calendarResponse, httptest.NewRequest("GET", "/api/planner/production-calendar/bundled/2026", nil))
	if calendarResponse.Code != 200 {
		t.Fatal(calendarResponse.Code)
	}
	var calendar any
	json.Unmarshal(calendarResponse.Body.Bytes(), &calendar)
	raw, _ = json.Marshal(map[string]any{"calendar": calendar})
	preview := call("production-calendar/preview", string(raw))
	raw, _ = json.Marshal(map[string]any{"calendar": calendar, "expected_revision": preview["expected_revision"], "preview_token": preview["preview_token"], "source_confirmed": true, "request_id": "backup-calendar-fixture"})
	call("production-calendar/import", string(raw))
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	for name, b := range map[string]string{"data.json": `{"months":[{"id":"month-2026-11","month":11,"year":2026,"income":100,"incomes":[],"expenses":[]}]}`, "categories.json": `{"expense":[{"id":"food","name":"Еда","subcategories":[]}],"income":[]}`} {
		if e = os.WriteFile(filepath.Join(live, name), []byte(b), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = storageguard.WriteMarker(live, storageguard.Marker{Version: 1, Environment: "test", ID: id}); e != nil {
		t.Fatal(e)
	}
	r, e := openRepository(root, id, "test", true)
	if e != nil {
		t.Fatal(e)
	}
	return r, backupOptions{DataDir: live, ID: id, Environment: "test", Reason: "manual", Now: time.Date(2026, 11, 25, 0, 30, 0, 0, time.UTC)}
}
func makeBackup(t *testing.T, r backupRepository, o backupOptions) backupReceipt {
	t.Helper()
	v, e := createBackup(context.Background(), r, o)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func readBytes(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func tableCounts(t *testing.T, path string) map[string]int {
	t.Helper()
	db, e := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	rows, e := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if e != nil {
		t.Fatal(e)
	}
	var names []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	out := map[string]int{}
	for _, n := range names {
		var count int
		if e = db.QueryRow(`SELECT count(*) FROM "` + n + `"`).Scan(&count); e != nil {
			t.Fatal(e)
		}
		out[n] = count
	}
	return out
}
func TestBackupFullRoundTripCorruptionAndPlannerOnly(t *testing.T) {
	r, o := backupFixture(t)
	if e := os.WriteFile(filepath.Join(o.DataDir, authFile), []byte(`{"sessions":[{"hash":"synthetic-hash"}]}`), 0600); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	counts := tableCounts(t, filepath.Join(o.DataDir, "planner.db"))
	contents := tableFingerprints(t, filepath.Join(o.DataDir, "planner.db"))
	finance := readBytes(t, filepath.Join(o.DataDir, "data.json"))
	categories := readBytes(t, filepath.Join(o.DataDir, "categories.json"))
	v := makeBackup(t, r, o)
	_, archive, e := r.find(v.Manifest.ID)
	if e != nil {
		t.Fatal(e)
	}
	m, e := verifyBackup(ctx, archive, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range m.Files {
		if f.Name == authFile {
			t.Fatal("session state entered backup")
		}
	}
	if m.SQLite.Schema != planner.SchemaVersion() || len(m.Files) != 5 {
		t.Fatal(m)
	}
	target := filepath.Join(t.TempDir(), "new-live")
	rest := o
	rest.DataDir = target
	if _, e = restoreBackup(ctx, r, rest, archive, "full"); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(finance, readBytes(t, filepath.Join(target, "data.json"))) || !bytes.Equal(categories, readBytes(t, filepath.Join(target, "categories.json"))) {
		t.Fatal("financial bytes changed")
	}
	a, _ := json.Marshal(counts)
	b, _ := json.Marshal(tableCounts(t, filepath.Join(target, "planner.db")))
	if !bytes.Equal(a, b) {
		t.Fatal("tables changed", string(a), string(b))
	}
	if actual := tableFingerprints(t, filepath.Join(target, "planner.db")); actual != contents {
		t.Fatal("IDs, histories or other table values changed")
	}
	if e = os.WriteFile(filepath.Join(target, authFile), []byte(`{"sessions":[{"hash":"old-session"}]}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(target, "planner.db"), []byte("damaged database"), 0600); e != nil {
		t.Fatal(e)
	}
	newFinance := []byte(`{"months":[]}`)
	os.WriteFile(filepath.Join(target, "data.json"), newFinance, 0600)
	previous, e := restoreBackup(ctx, r, rest, archive, "planner")
	if e != nil {
		t.Fatal(e)
	}
	if exists(filepath.Join(previous, authFile)) || exists(filepath.Join(target, authFile)) {
		t.Fatal("restore retained browser sessions")
	}
	if string(readBytes(t, filepath.Join(previous, "planner.db"))) != "damaged database" {
		t.Fatal("previous corruption evidence lost")
	}
	if !bytes.Equal(newFinance, readBytes(t, filepath.Join(target, "data.json"))) {
		t.Fatal("planner restore changed finance")
	}
	if _, e = planner.SnapshotInfo(ctx, filepath.Join(target, "planner.db"), false); e != nil {
		t.Fatal(e)
	}
}
func TestBackupExcludesSecretsAndRejectsTampering(t *testing.T) {
	r, o := backupFixture(t)
	secret := []byte("private-key-do-not-export")
	os.WriteFile(filepath.Join(o.DataDir, "server.key"), secret, 0600)
	v := makeBackup(t, r, o)
	_, archive, _ := r.find(v.Manifest.ID)
	f, _ := os.Open(archive)
	gz, _ := gzip.NewReader(f)
	tr := tar.NewReader(gz)
	names := []string{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(b, secret) {
			t.Fatal("secret leaked")
		}
		names = append(names, h.Name)
	}
	gz.Close()
	f.Close()
	if len(names) != 6 {
		t.Fatal(names)
	}
	corrupt := filepath.Join(t.TempDir(), "bad.tar.gz")
	raw := readBytes(t, archive)
	raw[len(raw)-6] ^= 0x80
	os.WriteFile(corrupt, raw, 0600)
	if _, e := verifyBackup(context.Background(), corrupt, t.TempDir()); e == nil {
		t.Fatal("bad gzip accepted")
	}
	os.WriteFile(corrupt, raw[:len(raw)/2], 0600)
	if _, e := verifyBackup(context.Background(), corrupt, t.TempDir()); e == nil {
		t.Fatal("truncation accepted")
	}
}
func rewriteArchive(t *testing.T, source string, change func(int, *tar.Header, []byte) (*tar.Header, []byte)) string {
	t.Helper()
	in, _ := os.Open(source)
	defer in.Close()
	gz, _ := gzip.NewReader(in)
	defer gz.Close()
	tr := tar.NewReader(gz)
	p := filepath.Join(t.TempDir(), "mutation.tar.gz")
	out, _ := os.Create(p)
	zw := gzip.NewWriter(out)
	tw := tar.NewWriter(zw)
	for i := 0; ; i++ {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(tr)
		h, b = change(i, h, b)
		if e = tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write(b)
		}
	}
	tw.Close()
	zw.Close()
	out.Close()
	return p
}
func TestBackupMaliciousArchiveAndIdentity(t *testing.T) {
	r, o := backupFixture(t)
	v := makeBackup(t, r, o)
	_, archive, _ := r.find(v.Manifest.ID)
	for _, mode := range []string{"absolute", "traversal", "symlink", "hardlink", "extra", "checksum", "oversize", "id"} {
		t.Run(mode, func(t *testing.T) {
			p := rewriteArchive(t, archive, func(i int, h *tar.Header, b []byte) (*tar.Header, []byte) {
				if i == 1 {
					switch mode {
					case "absolute":
						h.Name = "/etc/passwd"
					case "traversal":
						h.Name = "../outside"
					case "symlink":
						h.Typeflag = tar.TypeSymlink
						h.Linkname = "/etc/passwd"
						h.Size = 0
					case "hardlink":
						h.Typeflag = tar.TypeLink
						h.Linkname = "data.json"
						h.Size = 0
					case "extra":
						h.Name = "password.key"
					case "checksum":
						b[0] ^= 1
					}
				}
				if i == 0 && (mode == "oversize" || mode == "id") {
					var m backupManifest
					json.Unmarshal(b, &m)
					if mode == "oversize" {
						m.Files[0].Size = archiveLimit + 1
					} else {
						m.InstallationID = "different-installation-123456"
					}
					b, _ = json.Marshal(m)
					h.Size = int64(len(b))
				}
				return h, b
			})
			target := o
			target.DataDir = filepath.Join(t.TempDir(), "restored")
			if _, e := restoreBackup(context.Background(), r, target, p, "full"); e == nil {
				t.Fatal("unsafe archive accepted")
			}
			if exists(target.DataDir) {
				t.Fatal("live data appeared before validation")
			}
		})
	}
}
func TestBackupRetentionPinsFailuresAndDailyReplay(t *testing.T) {
	r, o := backupFixture(t)
	ctx := context.Background()
	o.Reason = "daily"
	var ids []string
	for i := 0; i < 9; i++ {
		o.Now = o.Now.Add(24 * time.Hour)
		v := makeBackup(t, r, o)
		ids = append(ids, v.Manifest.ID)
		if i == 0 {
			if e := r.pin(v.Manifest.ID, true); e != nil {
				t.Fatal(e)
			}
		}
	}
	items, e := r.list()
	if e != nil || len(items) != 8 {
		t.Fatal(len(items), e)
	}
	v := makeBackup(t, r, o)
	if v.Manifest.ID != ids[8] {
		t.Fatal("daily command duplicated snapshot")
	}
	if _, _, e = r.find(ids[0]); e != nil {
		t.Fatal("protected daily lost")
	}
	o.Reason = "pre-update"
	for i := 0; i < 4; i++ {
		o.Now = o.Now.Add(time.Hour)
		o.Protect = i == 0
		makeBackup(t, r, o)
	}
	items, _ = r.list()
	count := 0
	for _, v := range items {
		if v.Manifest.Reason == "pre-update" {
			count++
		}
	}
	if count != 3 {
		t.Fatal("pre-update retention", count)
	}
	before := len(items)
	o.Now = o.Now.Add(24 * time.Hour)
	o.fault = func(s string) error {
		if s == "publish" {
			return errors.New("disk failure")
		}
		return nil
	}
	if _, e = createBackup(ctx, r, o); e == nil {
		t.Fatal("failure ignored")
	}
	items, _ = r.list()
	if len(items) != before {
		t.Fatal("failed backup pruned good copies")
	}
	o.fault = nil
	o.space = func(string, int64, uint64) error { return errors.New("no disk space") }
	if _, e = createBackup(ctx, r, o); e == nil {
		t.Fatal("space refusal ignored")
	}
	lock, e := r.lock()
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if _, e = createBackup(ctx, r, o); e == nil {
		t.Fatal("concurrent administrator admitted")
	}
}
func TestBackupRestoreRollbackAtEverySwitch(t *testing.T) {
	for _, phase := range []string{"restore-prepared", "restore-old-moved", "restore-new-live"} {
		t.Run(phase, func(t *testing.T) {
			r, o := backupFixture(t)
			v := makeBackup(t, r, o)
			_, archive, _ := r.find(v.Manifest.ID)
			before := readBytes(t, filepath.Join(o.DataDir, "data.json"))
			o.fault = func(p string) error {
				if p == phase {
					return errors.New("interrupted switch")
				}
				return nil
			}
			if _, e := restoreBackup(context.Background(), r, o, archive, "full"); e == nil {
				t.Fatal("injected error ignored")
			}
			if !bytes.Equal(before, readBytes(t, filepath.Join(o.DataDir, "data.json"))) {
				t.Fatal("rollback lost financial data")
			}
			if e := storageguard.CheckRecovery(o.DataDir); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestBackupHTTPAccessStreamingAndPaths(t *testing.T) {
	r, o := backupFixture(t)
	v := makeBackup(t, r, o)
	c := serverConfig{BackupDir: r.Dir, InstallationID: o.ID, Environment: "test", Password: strings.Repeat("p", 40), AllowedHosts: map[string]bool{"127.0.0.1": true}, Local: true}
	h := authenticatedTestPolicy(t, c, backupHandler(c))
	send := func(path, method, pass, rangeValue string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1"+path, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		if pass != "" {
			req.SetBasicAuth("budget", pass)
			req.Header.Set("X-Budget-Admin", "1")
		}
		req.Header.Set("Range", rangeValue)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	for _, path := range []string{"/api/backups/status", "/api/backups/" + v.Manifest.ID + "/full", "/api/backups/" + v.Manifest.ID + "/planner"} {
		if w := send(path, "GET", "", ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	for _, path := range []string{"/api/backups/../staging/x", "/api/backups/no/full", "/api/backups/" + v.Manifest.ID + "/restore"} {
		if w := send(path, "GET", c.Password, ""); w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	w := send("/api/backups/"+v.Manifest.ID+"/full", "GET", c.Password, "bytes=0-9")
	if w.Code != 206 || w.Body.Len() != 10 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Header())
	}
	w = send("/api/backups/"+v.Manifest.ID+"/planner", "GET", c.Password, "")
	if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("SQLite format 3")) {
		t.Fatal(w.Code)
	}
	w = send("/api/backups/status", "GET", c.Password, "")
	var status backupStatus
	if e := json.Unmarshal(w.Body.Bytes(), &status); e != nil {
		t.Fatal(e)
	}
	if status.Automatic != "not_installed" || status.Latest == nil {
		t.Fatal(status)
	}
	_, p, _ := r.find(v.Manifest.ID)
	os.Remove(p)
	os.Symlink(filepath.Join(o.DataDir, "data.json"), p)
	if w = send("/api/backups/"+v.Manifest.ID+"/full", "GET", c.Password, ""); w.Code != 503 {
		t.Fatal("symlink download", w.Code)
	}
}
func TestBackupParentLeaseStopsReplacement(t *testing.T) {
	r, o := backupFixture(t)
	lease, e := storageguard.ParentLease(o.DataDir, false)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Close()
	if _, e = createBackup(context.Background(), r, o); e == nil {
		t.Fatal("backup admitted while app holds lease")
	}
}
func TestBackupCLIRequiresConfirmation(t *testing.T) {
	r, o := backupFixture(t)
	var out bytes.Buffer
	e := backupCommand([]string{"restore", "--environment", "test", "--data-dir", o.DataDir, "--backup-dir", r.Dir, "--installation-id", o.ID}, &out)
	if e == nil || !strings.Contains(e.Error(), "confirm") {
		t.Fatal(e)
	}
}

func TestBackupControllerResumesOnSuccessAndFailure(t *testing.T) {
	for _, phase := range []string{"success", "snapshot", "resumed", "publish"} {
		t.Run(phase, func(t *testing.T) {
			r, o := backupFixture(t)
			dir := t.TempDir()
			state := filepath.Join(dir, "state")
			log := filepath.Join(dir, "calls")
			script := filepath.Join(dir, "controller")
			os.WriteFile(state, []byte("running"), 0600)
			body := "#!/bin/sh\n[ \"$2\" = \"" + o.ID + "\" ] || exit 8\ncase \"$1\" in\nstatus) [ \"$(cat '" + state + "')\" = running ];;\nstopped) [ \"$(cat '" + state + "')\" = stopped ];;\nstop) echo stopped > '" + state + "'; echo stop >> '" + log + "';;\nstart) echo running > '" + state + "'; echo start >> '" + log + "';;\n*) exit 9;;\nesac\n"
			if e := os.WriteFile(script, []byte(body), 0700); e != nil {
				t.Fatal(e)
			}
			o.Controller = script
			o.fault = func(p string) error {
				if p == phase {
					return errors.New("injected failure")
				}
				if p == "snapshot" && strings.TrimSpace(string(readBytes(t, state))) != "stopped" {
					t.Fatal("snapshot while running")
				}
				if p == "resumed" && strings.TrimSpace(string(readBytes(t, state))) != "running" {
					t.Fatal("compression while stopped")
				}
				return nil
			}
			_, e := createBackup(context.Background(), r, o)
			if (phase == "success") != (e == nil) {
				t.Fatal(phase, e)
			}
			if strings.TrimSpace(string(readBytes(t, state))) != "running" {
				t.Fatal("service left stopped")
			}
			if string(readBytes(t, log)) != "stop\nstart\n" {
				t.Fatal("controller sequence")
			}
			if exists(filepath.Join(r.Dir, "operation.json")) {
				t.Fatal("completed operation journal left")
			}
		})
	}
}
func TestBackupCrashRecoveryDirectoryStates(t *testing.T) {
	for _, decision := range []string{"rollback", "finish"} {
		for _, phase := range []string{"prepared", "old-moved", "new-live"} {
			t.Run(decision+phase, func(t *testing.T) {
				r, o := backupFixture(t)
				v := makeBackup(t, r, o)
				_, archive, _ := r.find(v.Manifest.ID)
				j := restoreJournal{Format: 1, ID: newBackupID(time.Now()), DataDir: o.DataDir, InstallationID: o.ID, Environment: o.Environment, Phase: phase, HadOriginal: true}
				os.Mkdir(j.stage(), 0700)
				if _, e := extractBackup(context.Background(), archive, j.stage()); e != nil {
					t.Fatal(e)
				}
				for _, n := range []string{"settings.json", "RESTORE.txt"} {
					os.Remove(filepath.Join(j.stage(), n))
				}
				if e := planner.UpgradeSnapshot(context.Background(), filepath.Join(j.stage(), "planner.db")); e != nil {
					t.Fatal(e)
				}
				if e := storageguard.WriteMarker(j.stage(), storageguard.Marker{Version: 1, Environment: o.Environment, ID: o.ID}); e != nil {
					t.Fatal(e)
				}
				os.WriteFile(filepath.Join(j.stage(), "data.json"), []byte(`{"months":[]}`), 0600)
				if e := j.save(); e != nil {
					t.Fatal(e)
				}
				if phase != "prepared" {
					if e := os.Rename(o.DataDir, j.previous()); e != nil {
						t.Fatal(e)
					}
				}
				if phase == "new-live" {
					if e := os.Rename(j.stage(), o.DataDir); e != nil {
						t.Fatal(e)
					}
				}
				if storageguard.CheckRecovery(o.DataDir) == nil {
					t.Fatal("startup allowed incomplete restore")
				}
				if e := recoverRestore(context.Background(), o, decision); e != nil {
					t.Fatal(e)
				}
				if storageguard.CheckRecovery(o.DataDir) != nil {
					t.Fatal("journal not cleared")
				}
				b := readBytes(t, filepath.Join(o.DataDir, "data.json"))
				if (string(b) == `{"months":[]}`) != (decision == "finish") {
					t.Fatal("wrong recovered generation", string(b))
				}
			})
		}
	}
}
func TestBackupCancelledBeforePublicationAndUnknownFiles(t *testing.T) {
	r, o := backupFixture(t)
	v := makeBackup(t, r, o)
	before, _ := r.list()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := createBackup(ctx, r, o); e == nil {
		t.Fatal("cancel ignored")
	}
	after, _ := r.list()
	if len(after) != len(before) {
		t.Fatal("cancelled backup published")
	}
	_, archive, _ := r.find(v.Manifest.ID)
	os.WriteFile(filepath.Join(o.DataDir, "unknown.txt"), []byte("preserve"), 0600)
	if _, e := restoreBackup(context.Background(), r, o, archive, "full"); e == nil {
		t.Fatal("unknown live file lost")
	}
	if string(readBytes(t, filepath.Join(o.DataDir, "unknown.txt"))) != "preserve" {
		t.Fatal("unknown changed")
	}
}
func TestBackupDownloadDescriptorSurvivesRotation(t *testing.T) {
	r, o := backupFixture(t)
	o.Reason = "daily"
	first := makeBackup(t, r, o)
	_, path, _ := r.find(first.Manifest.ID)
	f, e := safeOpen(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	for i := 0; i < 7; i++ {
		o.Now = o.Now.Add(24 * time.Hour)
		makeBackup(t, r, o)
	}
	if exists(path) {
		t.Fatal("old copy not rotated")
	}
	h := sha256.New()
	n, e := io.Copy(h, f)
	if e != nil || n != first.Size || hex.EncodeToString(h.Sum(nil)) != first.SHA256 {
		t.Fatal("open download corrupted", e)
	}
}
func TestBackupScheduleDoesNotInventTimer(t *testing.T) {
	r, _ := backupFixture(t)
	now := time.Now()
	s, e := r.status(now)
	if e != nil || s.Automatic != "not_installed" {
		t.Fatal(s, e)
	}
	privateJSON(filepath.Join(r.Dir, "schedule.json"), backupSchedule{Enabled: true, Observed: now, Next: now.Add(time.Hour), Source: "systemd"})
	s, e = r.status(now)
	if e != nil || s.Automatic != "enabled" || s.Next == nil {
		t.Fatal(s, e)
	}
	s, _ = r.status(now.Add(48 * time.Hour))
	if s.Automatic != "unknown" || s.Next != nil {
		t.Fatal("stale observation claims timer enabled")
	}
}
func TestBackupDiskBudget(t *testing.T) {
	const gib = uint64(1 << 30)
	for _, c := range []struct {
		capacity, free    uint64
		temporary         int64
		files, freeInodes uint64
		ok                bool
	}{
		{15 * gib, 4 * gib, int64(gib), 1000, 100, true},
		{15 * gib, 3 * gib, int64(gib), 1000, 100, false},
		{8 * gib, 3 * gib, int64(gib), 1000, 100, true},
		{8 * gib, 3 * gib, int64(gib) + 1, 1000, 100, false},
		{15 * gib, 8 * gib, 0, 1000, 63, false},
		{15 * gib, 8 * gib, 0, 1000, 65, true},
		{15 * gib, 8 * gib, -1, 1000, 100, false},
	} {
		if e := checkCapacity(c.capacity, c.free, c.files, c.freeInodes, c.temporary, 1); (e == nil) != c.ok {
			t.Fatalf("capacity %+v: %v", c, e)
		}
	}
}

func tableFingerprints(t *testing.T, path string) string {
	t.Helper()
	counts := tableCounts(t, path)
	names := []string{}
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	db, e := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	for _, name := range names {
		probe, e := db.Query(`SELECT * FROM "` + name + `" LIMIT 0`)
		if e != nil {
			t.Fatal(e)
		}
		columns, e := probe.Columns()
		probe.Close()
		if e != nil {
			t.Fatal(e)
		}
		order := []string{}
		for i := range columns {
			order = append(order, strconv.Itoa(i+1))
		}
		rows, e := db.Query(`SELECT * FROM "` + name + `" ORDER BY ` + strings.Join(order, ","))
		if e != nil {
			t.Fatal(e)
		}
		encoder.Encode(name)
		for rows.Next() {
			values := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if e = rows.Scan(ptrs...); e != nil {
				t.Fatal(e)
			}
			encoder.Encode(values)
		}
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
		rows.Close()
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// A real subprocess dies without running defers, reproducing a power-loss-like
// interruption at every durable rename boundary. Only synthetic test paths.
func TestBackupRestoreKilledWorker(t *testing.T) {
	if os.Getenv("BUDGET_BACKUP_KILL_WORKER") != "1" {
		return
	}
	root := os.Getenv("BUDGET_BACKUP_KILL_ROOT")
	if e := storageguard.TestPath(root); e != nil {
		t.Fatal(e)
	}
	id := "abcdef0123456789abcdef0123456789"
	r, e := openRepository(filepath.Join(root, "archives"), id, "test", false)
	if e != nil {
		t.Fatal(e)
	}
	items, e := r.list()
	if e != nil || len(items) != 1 {
		t.Fatal(e)
	}
	_, archive, e := r.find(items[0].Manifest.ID)
	if e != nil {
		t.Fatal(e)
	}
	o := backupOptions{DataDir: filepath.Join(root, "live"), ID: id, Environment: "test", fault: func(phase string) error {
		if phase == os.Getenv("BUDGET_BACKUP_KILL_PHASE") {
			p, _ := os.FindProcess(os.Getpid())
			_ = p.Kill()
			select {}
		}
		return nil
	}}
	_, e = restoreBackup(context.Background(), r, o, archive, "full")
	t.Fatalf("worker did not die: %v", e)
}
func TestBackupRestoreActualInterruption(t *testing.T) {
	for _, phase := range []string{"restore-prepared", "restore-old-moved", "restore-new-live"} {
		for _, decision := range []string{"finish", "rollback"} {
			t.Run(phase+"/"+decision, func(t *testing.T) {
				r, o := backupFixture(t)
				makeBackup(t, r, o)
				original := []byte(`{"months":[]}`)
				if e := os.WriteFile(filepath.Join(o.DataDir, "data.json"), original, 0600); e != nil {
					t.Fatal(e)
				}
				cmd := exec.Command(os.Args[0], "-test.run=^TestBackupRestoreKilledWorker$")
				cmd.Env = append(os.Environ(), "BUDGET_BACKUP_KILL_WORKER=1", "BUDGET_BACKUP_KILL_ROOT="+filepath.Dir(o.DataDir), "BUDGET_BACKUP_KILL_PHASE="+phase)
				output, e := cmd.CombinedOutput()
				if e == nil {
					t.Fatal("worker survived")
				}
				if !exists(storageguard.RestoreJournal(o.DataDir)) {
					t.Fatalf("no durable journal: %s %v", output, e)
				}
				if storageguard.CheckRecovery(o.DataDir) == nil {
					t.Fatal("startup allowed during interrupted restore")
				}
				var out bytes.Buffer
				e = backupCommand([]string{"recover", "--environment", "test", "--data-dir", o.DataDir, "--backup-dir", r.Dir, "--installation-id", o.ID, "--confirm", "--decision", decision}, &out)
				if e != nil {
					t.Fatal(e)
				}
				if e = restoreDirectoryCheck(context.Background(), o.DataDir, o.Environment, o.ID); e != nil {
					t.Fatal(e)
				}
				got := readBytes(t, filepath.Join(o.DataDir, "data.json"))
				if (bytes.Equal(got, original)) != (decision == "rollback") {
					t.Fatal("wrong recovered generation")
				}
				if exists(storageguard.RestoreJournal(o.DataDir)) || exists(filepath.Join(r.Dir, "operation.json")) {
					t.Fatal("recovery left active journal")
				}
			})
		}
	}
}

type blockedBackupWriter struct {
	*httptest.ResponseRecorder
	started chan struct{}
	release chan struct{}
	once    bool
}

func (w *blockedBackupWriter) Write(b []byte) (int, error) {
	if !w.once {
		w.once = true
		close(w.started)
		<-w.release
	}
	return w.ResponseRecorder.Write(b)
}
func TestBackupParallelDownloadsBounded(t *testing.T) {
	r, o := backupFixture(t)
	v := makeBackup(t, r, o)
	h := backupHandler(serverConfig{BackupDir: r.Dir, InstallationID: o.ID, Environment: "test"})
	path := "/api/backups/" + v.Manifest.ID + "/full"
	release := make(chan struct{})
	defer close(release)
	for i := 0; i < 2; i++ {
		w := &blockedBackupWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: release}
		go h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		select {
		case <-w.started:
		case <-time.After(10 * time.Second):
			t.Fatal("download did not reach writer")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatal(w.Code)
	}
}
func TestBackupCLIArchiveOnlyDisasterRecovery(t *testing.T) {
	r, o := backupFixture(t)
	v := makeBackup(t, r, o)
	_, archive, _ := r.find(v.Manifest.ID)
	root := t.TempDir()
	repository := filepath.Join(root, "new-archives")
	if e := os.Mkdir(repository, 0700); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(root, "new-live")
	var out bytes.Buffer
	if e := backupCommand([]string{"restore", "--environment", "test", "--data-dir", target, "--backup-dir", repository, "--installation-id", o.ID, "--archive", archive, "--confirm"}, &out); e != nil {
		t.Fatal(e)
	}
	if e := restoreDirectoryCheck(context.Background(), target, "test", o.ID); e != nil {
		t.Fatal(e)
	}
}

func TestBackupRestoreOlderCompatibleArchive(t *testing.T) {
	for _, schema := range []int{7, 14} {
		t.Run(fmt.Sprintf("schema%d", schema), func(t *testing.T) {
			r, o := backupFixture(t)
			v := makeBackup(t, r, o)
			_, archive, _ := r.find(v.Manifest.ID)
			stage := t.TempDir()
			m, e := extractBackup(context.Background(), archive, stage)
			if e != nil {
				t.Fatal(e)
			}
			dbPath := filepath.Join(stage, "planner.db")
			if e = os.Remove(dbPath); e != nil {
				t.Fatal(e)
			}
			db, e := sql.Open("sqlite", dbPath)
			if e != nil {
				t.Fatal(e)
			}
			tx, e := db.Begin()
			if e != nil {
				t.Fatal(e)
			}
			_, e = tx.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`)
			if e != nil {
				t.Fatal(e)
			}
			files, e := filepath.Glob("internal/planner/migrations/*.sql")
			if e != nil || len(files) < schema {
				t.Fatal(e)
			}
			sort.Strings(files)
			for i, name := range files[:schema] {
				raw := readBytes(t, name)
				if _, e = tx.Exec(string(raw)); e != nil {
					t.Fatal(e)
				}
				sum := sha256.Sum256(raw)
				if _, e = tx.Exec("INSERT INTO schema_migrations(version,checksum) VALUES(?,?)", i+1, hex.EncodeToString(sum[:])); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = tx.Exec(fmt.Sprintf(`PRAGMA application_id=1396853840; PRAGMA user_version=%d; INSERT INTO projects(id,title) VALUES('old-archive-project','Old compatible project')`, schema)); e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			db.Close()
			m.SQLite, e = planner.SnapshotInfo(context.Background(), dbPath, true)
			if e != nil {
				t.Fatal(e)
			}
			for i, name := range archiveNames {
				m.Files[i], e = hashFile(context.Background(), filepath.Join(stage, name))
				if e != nil {
					t.Fatal(e)
				}
			}
			oldArchive := filepath.Join(t.TempDir(), "old.tar.gz")
			if e = packBackup(context.Background(), stage, oldArchive, m); e != nil {
				t.Fatal(e)
			}
			target := o
			target.DataDir = filepath.Join(t.TempDir(), "restored")
			if _, e = restoreBackup(context.Background(), r, target, oldArchive, "full"); e != nil {
				t.Fatal(e)
			}
			info, e := planner.SnapshotInfo(context.Background(), filepath.Join(target.DataDir, "planner.db"), false)
			if e != nil || info.Schema != planner.SchemaVersion() {
				t.Fatal(info, e)
			}
			db, e = sql.Open("sqlite", "file:"+filepath.Join(target.DataDir, "planner.db")+"?mode=ro&immutable=1")
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var title string
			if e = db.QueryRow("SELECT title FROM projects WHERE id='old-archive-project'").Scan(&title); e != nil || title != "Old compatible project" {
				t.Fatal(title, e)
			}
			old, e := planner.SnapshotInfo(context.Background(), dbPath, true)
			if e != nil || old.Schema != schema {
				t.Fatal("source snapshot mutated", old, e)
			}
		})
	}
}

func TestBackupCompressionMemoryBounded(t *testing.T) {
	r, o := backupFixture(t)
	v := makeBackup(t, r, o)
	_, archive, _ := r.find(v.Manifest.ID)
	dir := t.TempDir()
	m, e := extractBackup(context.Background(), archive, dir)
	if e != nil {
		t.Fatal(e)
	}
	// Packing is deliberately tested independently of SQLite validation: this
	// sparse 32 MiB input catches accidental whole-file buffering in the writer.
	f, e := os.OpenFile(filepath.Join(dir, "planner.db"), os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(32 << 20); e != nil {
		t.Fatal(e)
	}
	f.Close()
	m.Files[0], e = hashFile(context.Background(), filepath.Join(dir, "planner.db"))
	if e != nil {
		t.Fatal(e)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	e = packBackup(context.Background(), dir, filepath.Join(t.TempDir(), "stream.tar.gz"), m)
	runtime.ReadMemStats(&after)
	if e != nil {
		t.Fatal(e)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated > 8<<20 {
		t.Fatalf("packing buffered too much: %d bytes", allocated)
	}
	t.Logf("32 MiB input: writer allocated %d bytes (cumulative, not process RSS)", allocated)
}

type deadlineBackupWriter struct {
	*httptest.ResponseRecorder
	lastRemaining time.Duration
	delayed       bool
}

func (w *deadlineBackupWriter) SetWriteDeadline(d time.Time) error {
	w.lastRemaining = time.Until(d)
	return nil
}
func (w *deadlineBackupWriter) Write(b []byte) (int, error) {
	if !w.delayed {
		w.delayed = true
		time.Sleep(200 * time.Millisecond)
	}
	return w.ResponseRecorder.Write(b)
}
func TestBackupDownloadRestoresDeadlineAfterWriting(t *testing.T) {
	r, o := backupFixture(t)
	v := makeBackup(t, r, o)
	h := backupHandler(serverConfig{BackupDir: r.Dir, InstallationID: o.ID, Environment: "test"})
	w := &deadlineBackupWriter{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/backups/"+v.Manifest.ID+"/planner", nil))
	if w.Code != 200 || w.lastRemaining < 29950*time.Millisecond || w.lastRemaining > 30*time.Second {
		t.Fatal(w.Code, w.lastRemaining)
	}
}
func TestBackupHTTPSIPv4Policy(t *testing.T) {
	r, o := backupFixture(t)
	v := makeBackup(t, r, o)
	c := serverConfig{BackupDir: r.Dir, InstallationID: o.ID, Environment: "test", Password: strings.Repeat("p", 40), AllowedHosts: map[string]bool{"171.22.237.16": true}, Local: true, PublicOrigin: "https://171.22.237.16", ProxyMode: "loopback-https"}
	h := authenticatedTestPolicy(t, c, backupHandler(c))
	for _, tc := range []struct {
		host, origin string
		auth         bool
		code         int
	}{
		{"171.22.237.16", "https://171.22.237.16", true, 200},
		{"171.22.237.16", "https://171.22.237.16", false, 401},
		{"evil.test", "https://171.22.237.16", true, 403},
		{"171.22.237.16", "https://evil.test", true, 403},
		{"171.22.237.16", "http://171.22.237.16", true, 403},
	} {
		req := httptest.NewRequest("GET", "http://127.0.0.1/api/backups/"+v.Manifest.ID+"/full", nil)
		req.Host = tc.host
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("X-Forwarded-Host", "171.22.237.16")
		req.Header.Set("X-Forwarded-Proto", "https")
		if tc.auth {
			req.SetBasicAuth("budget", c.Password)
			req.Header.Set("X-Budget-Admin", "1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code)
		}
	}
}

func TestBackupInstallationLeaseAcrossRepositories(t *testing.T) {
	r, o := backupFixture(t)
	lock, e := installationAdminLock(o.DataDir)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	other := t.TempDir()
	os.Chmod(other, 0700)
	second, e := openRepository(other, o.ID, o.Environment, true)
	if e != nil {
		t.Fatal(e)
	}
	for _, repo := range []backupRepository{r, second} {
		if _, e = createBackup(context.Background(), repo, o); e == nil {
			t.Fatal("concurrent installation admitted through another repository")
		}
		if exists(filepath.Join(repo.Dir, "operation.json")) {
			t.Fatal("service journal written before installation lock")
		}
	}
}

func TestBackupFinancialStructureBoundedBeforeDecoding(t *testing.T) {
	for _, raw := range []string{`{"months":[` + strings.Repeat(`{},`, 100001) + `{}]}`, strings.Repeat(`[`, 65) + `0` + strings.Repeat(`]`, 65)} {
		if e := checkJSONStructure([]byte(raw)); e == nil {
			t.Fatal("unbounded JSON structure accepted")
		}
	}
	if e := checkJSONStructure([]byte(`{"text":"` + strings.Repeat("x", 2<<20) + `"}`)); e != nil {
		t.Fatal("bounded ordinary strings rejected", e)
	}
}
