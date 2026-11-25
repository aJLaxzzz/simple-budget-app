package main

import (
	"budget/internal/planner"
	"budget/internal/storageguard"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const productionTestID = "00112233445566778899aabbccddeeff"

func TestCredentialDiagnosticDoesNotExposePassword(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CREDENTIALS_DIRECTORY", dir)
	if err := os.WriteFile(filepath.Join(dir, "app-password"), []byte("Test-pass-12\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := command([]string{"admin", "check-credential"}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "{\"credential\":\"valid\"}\n" {
		t.Fatal("unexpected diagnostic output")
	}
	t.Setenv("CREDENTIALS_DIRECTORY", "")
	if err := command([]string{"admin", "check-credential"}, &output); err == nil {
		t.Fatal("missing credential source accepted")
	}
}

func TestPasswordLengthAcrossConfigurationSources(t *testing.T) {
	for _, tc := range []struct {
		name, password string
		valid          bool
	}{
		{"eleven", strings.Repeat("p", 11), false},
		{"minimum", strings.Repeat("p", 12), true},
		{"recommended", strings.Repeat("p", 16), true},
		{"existing-long", strings.Repeat("p", 40), true},
		{"eleven-unicode", strings.Repeat("я", 11), false},
		{"minimum-unicode", strings.Repeat("я", 12), true},
		{"maximum-bytes", strings.Repeat("p", 4096), true},
		{"over-limit", strings.Repeat("p", 4097), false},
		{"over-limit-unicode", strings.Repeat("я", 2049), false},
	} {
		for _, source := range []string{"environment", "file", "credential"} {
			t.Run(tc.name+"/"+source, func(t *testing.T) {
				env := productionEnvironment(t)
				path := env["BUDGET_PASSWORD_FILE"]
				if e := os.WriteFile(path, []byte(tc.password+"\n"), 0600); e != nil {
					t.Fatal(e)
				}
				if source == "credential" {
					delete(env, "BUDGET_PASSWORD_FILE")
					env["CREDENTIALS_DIRECTORY"] = filepath.Dir(path)
				} else if source == "environment" {
					// Environment passwords are development-only; use isolated test storage.
					env = map[string]string{"BUDGET_ENV": "development", "BUDGET_DATA_DIR": t.TempDir(), "BUDGET_PASSWORD": tc.password}
				}
				c, e := readConfig(func(k string) string { return env[k] })
				if (e == nil) != tc.valid {
					t.Fatalf("accepted=%v, want=%v: %v", e == nil, tc.valid, e)
				}
				if e == nil && c.Password != tc.password {
					t.Fatal("password changed")
				}
			})
		}
	}
}

func TestPasswordFileLineAndEncodingBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app-password")
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"empty", "", false},
		{"lf", strings.Repeat("p", 12) + "\n", true},
		{"crlf-maximum", strings.Repeat("p", 4096) + "\r\n", true},
		{"over-limit-crlf", strings.Repeat("p", 4097) + "\r\n", false},
		{"multiple-lines", strings.Repeat("p", 12) + "\nother", false},
		{"nul", strings.Repeat("p", 12) + "\x00", false},
		{"invalid-utf8", strings.Repeat("p", 12) + "\xff", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if e := os.WriteFile(path, []byte(tc.raw), 0600); e != nil {
				t.Fatal(e)
			}
			_, e := readPasswordFile(path)
			if (e == nil) != tc.valid {
				t.Fatalf("accepted=%v want=%v", e == nil, tc.valid)
			}
		})
	}
}

func productionEnvironment(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	password := filepath.Join(t.TempDir(), "app-password")
	if e := os.WriteFile(password, []byte(strings.Repeat("p", 12)), 0600); e != nil {
		t.Fatal(e)
	}
	for name, body := range map[string]string{"data.json": `{"months":[]}`, "categories.json": `{"expense":[],"income":[]}`, "planner.db": "config fixture only"} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := storageguard.WriteMarker(dir, storageguard.Marker{Version: 1, Environment: "production", ID: productionTestID}); e != nil {
		t.Fatal(e)
	}
	return map[string]string{"BUDGET_ENV": "production", "BUDGET_DATA_DIR": dir, "BUDGET_INSTALLATION_ID": productionTestID, "BUDGET_PASSWORD_FILE": password, "BUDGET_ALLOWED_HOSTS": "budget.example.test", "BUDGET_PUBLIC_ORIGIN": "https://budget.example.test", "BUDGET_PROXY_MODE": "loopback-https", "BUDGET_MIGRATIONS": "check-only"}
}
func TestProductionConfigFailClosed(t *testing.T) {
	env := productionEnvironment(t)
	read := func(m map[string]string) (serverConfig, error) {
		return readConfig(func(k string) string { return m[k] })
	}
	c, e := read(env)
	if e != nil || c.StaticDir != "" || !c.Local {
		t.Fatal(c.Environment, e)
	}
	for _, key := range []string{"BUDGET_ENV", "BUDGET_DATA_DIR", "BUDGET_INSTALLATION_ID", "BUDGET_PASSWORD_FILE", "BUDGET_ALLOWED_HOSTS", "BUDGET_PUBLIC_ORIGIN", "BUDGET_PROXY_MODE", "BUDGET_MIGRATIONS"} {
		t.Run("missing "+key, func(t *testing.T) {
			copy := map[string]string{}
			for k, v := range env {
				copy[k] = v
			}
			delete(copy, key)
			if _, e := read(copy); e == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
	for key, value := range map[string]string{"BUDGET_ADDR": "0.0.0.0:3001", "BUDGET_DATA_DIR": ".", "BUDGET_STATIC_DIR": "static", "BUDGET_PUBLIC_ORIGIN": "http://budget.example.test", "BUDGET_ALLOWED_HOSTS": "budget.example.test,localhost:3001", "BUDGET_PASSWORD": strings.Repeat("s", 40), "BUDGET_MIGRATIONS": "auto", "BUDGET_INSTALLATION_ID": "wrong"} {
		t.Run(key, func(t *testing.T) {
			copy := map[string]string{}
			for k, v := range env {
				copy[k] = v
			}
			copy[key] = value
			if _, e := read(copy); e == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
	original := env["BUDGET_PASSWORD_FILE"]
	delete(env, "BUDGET_PASSWORD_FILE")
	env["CREDENTIALS_DIRECTORY"] = filepath.Dir(original)
	if _, e := read(env); e != nil {
		t.Fatal("systemd credential", e)
	}
	if e := os.Chmod(original, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := read(env); e == nil {
		t.Fatal("public credential accepted")
	}
}
func TestProductionMarkerCannotBeUsedByTestsOrDevelopment(t *testing.T) {
	env := productionEnvironment(t)
	dir := env["BUDGET_DATA_DIR"]
	for _, mode := range []string{"development", "test"} {
		if _, e := storageguard.Check(dir, mode, ""); e == nil {
			t.Fatal(mode)
		}
	}
	before, _ := os.ReadFile(filepath.Join(dir, "planner.db"))
	if _, e := planner.Open(context.Background(), filepath.Join(dir, "planner.db")); e == nil {
		t.Fatal("test opened production")
	}
	if _, e := planner.OpenProduction(context.Background(), filepath.Join(dir, "planner.db"), productionTestID); e == nil {
		t.Fatal("Go test opened production explicitly")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if e := os.Symlink(dir, alias); e != nil {
		t.Fatal(e)
	}
	if _, e := storageguard.Check(alias, "development", ""); e == nil {
		t.Fatal("symlink bypass")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "planner.db"))
	if !bytes.Equal(before, after) {
		t.Fatal("protected file changed")
	}
	if e := os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := storageguard.Check(dir, "production", productionTestID); e == nil {
		t.Fatal("maintenance accepted a public production directory")
	}
}
func TestActualHTTPReverseProxyOriginAndAuthentication(t *testing.T) {
	for _, host := range []string{"budget.example.test", "171.22.237.16"} {
		t.Run(host, func(t *testing.T) {
			// Host is a request header only: both servers listen on httptest loopback.
			// This never connects to the public IP or uses production data.
			testActualHTTPReverseProxyOriginAndAuthentication(t, host)
		})
	}
}

func testActualHTTPReverseProxyOriginAndAuthentication(t *testing.T, host string) {
	t.Helper()
	env := productionEnvironment(t)
	env["BUDGET_PUBLIC_ORIGIN"] = "https://" + host
	env["BUDGET_ALLOWED_HOSTS"] = host
	c, e := readConfig(func(k string) string { return env[k] })
	if e != nil {
		t.Fatal(e)
	}
	upstream := httptest.NewServer(accessHandler(c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	proxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(target))
	defer proxy.Close()
	send := func(base, path, host, origin, password string, forward bool) int {
		req, _ := http.NewRequest("POST", base+path, strings.NewReader(`{}`))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if password != "" {
			req.SetBasicAuth("budget", password)
		}
		if forward {
			req.Header.Set("X-Forwarded-Proto", "https")
			// Even a forged canonical forwarded host cannot override a foreign Host.
			req.Header.Set("X-Forwarded-Host", env["BUDGET_ALLOWED_HOSTS"])
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
			req.Header.Set("Forwarded", "for=127.0.0.1;proto=https")
		}
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.Header.Get("Content-Security-Policy") == "" {
			t.Fatal("missing security headers")
		}
		return resp.StatusCode
	}
	for _, tc := range []struct {
		host, origin, pass string
		want               int
	}{{host, "https://" + host, c.Password, 204}, {host, "http://" + host, c.Password, 403}, {host, "https://foreign.test", c.Password, 403}, {host, "null", c.Password, 403}, {"foreign.test", "https://" + host, c.Password, 403}, {host, "https://" + host, "", 401}} {
		if got := send(proxy.URL, "/api/planner/tasks", tc.host, tc.origin, tc.pass, true); got != tc.want {
			t.Fatalf("proxy got %d want %d", got, tc.want)
		}
	}
	for _, path := range []string{"/", "/js/app.js", "/api/backup", "/api/ready", "/api/planner/tasks"} {
		got := send(upstream.URL, path, host, "", "", true)
		if got != 401 && got != 429 {
			t.Fatalf("localhost bypass: %s %d", path, got)
		}
	}
	for i := 0; i < 40; i++ {
		send(proxy.URL, "/", host, "", "wrong", false)
	}
	if got := send(proxy.URL, "/", host, "", "wrong", false); got != 429 {
		t.Fatal("failure throttle", got)
	}
	if got := send(proxy.URL, "/", host, "", "", false); got != 401 {
		t.Fatal("new client cannot receive authentication challenge", got)
	}
	if got := send(proxy.URL, "/", host, "https://"+host, c.Password, false); got != 204 {
		t.Fatal("other device locked out", got)
	}
}
func TestFailureBudgetRefillsWithFixedMemory(t *testing.T) {
	b := &failureBudget{}
	now := time.Now()
	for i := 0; i < 10; i++ {
		if !b.allow(now) {
			t.Fatal(i)
		}
	}
	if b.allow(now) {
		t.Fatal("unlimited failures")
	}
	if !b.allow(now.Add(time.Second)) {
		t.Fatal("no recovery")
	}
}
func TestFinancialLimitRejectsBeforeMutation(t *testing.T) {
	h := limitFinancialBody(testHandler(t))
	before := cloneData(store)
	for _, length := range []int64{-1, financialBodyLimit + 1} {
		body := `{"month":11,"year":2026}` + strings.Repeat(" ", financialBodyLimit)
		r := httptest.NewRequest("POST", "/api/months", strings.NewReader(body))
		r.ContentLength = length
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 413 {
			t.Fatal(w.Code)
		}
		if len(store.Months) != len(before.Months) {
			t.Fatal("oversized request committed")
		}
	}
	request(t, h, "POST", "/api/months", `{"month":11,"year":2026}`, 201)
}
func TestProductionGoTestEnvironmentRefused(t *testing.T) {
	t.Setenv("BUDGET_ENV", "production")
	if storageguard.TestEnvironment() == nil {
		t.Fatal("test environment accepted production")
	}
}

func TestEmbeddedFrontendManifest(t *testing.T) {
	h, e := newStaticHandler("")
	if e != nil {
		t.Fatal(e)
	}
	for path := range publicAssets {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatal(path)
		}
	}
	for _, path := range []string{"/data.json", "/categories.json", "/planner.db", "/installation.json", "/.git/config", "/backups/full.tar.gz"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(path)
		}
	}
}
func cleanProcessEnv(extra map[string]string) []string {
	env := []string{}
	for _, s := range os.Environ() {
		k, _, _ := strings.Cut(s, "=")
		if !strings.HasPrefix(k, "BUDGET_") && k != "CREDENTIALS_DIRECTORY" {
			env = append(env, s)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
func TestProductionBinaryLifecycle(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "budget-test")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-X main.releaseProfile=production", "-o", binary, ".")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, out)
	}
	invoke := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Dir = t.TempDir()
		cmd.Env = cleanProcessEnv(nil)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("command failed %v: %s", e, out)
		}
		return out
	}
	var v versionInfo
	if e := json.Unmarshal(invoke("version"), &v); e != nil || !v.EmbeddedFrontend {
		t.Fatal(e)
	}
	// A release launched without configuration must not infer dev mode or
	// create a fresh database in the service's current working directory.
	empty := t.TempDir()
	unconfigured := exec.Command(binary)
	unconfigured.Dir = empty
	unconfigured.Env = cleanProcessEnv(nil)
	if out, e := unconfigured.CombinedOutput(); e == nil || !strings.Contains(string(out), "release requires explicit BUDGET_ENV") {
		t.Fatal("release did not reject missing environment before startup", e, string(out))
	}
	if files, e := os.ReadDir(empty); e != nil || len(files) != 0 {
		t.Fatal("unconfigured release wrote files", e)
	}
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	invoke("admin", "init", "--data-dir", dir, "--installation-id", productionTestID, "--confirm")
	env := productionEnvironment(t)
	env["BUDGET_DATA_DIR"] = dir
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	env["BUDGET_ADDR"] = listener.Addr().String()
	listener.Close()
	logPath := filepath.Join(t.TempDir(), "server.log")
	logs, e := os.Create(logPath)
	if e != nil {
		t.Fatal(e)
	}
	defer logs.Close()
	cmd := exec.Command(binary)
	cmd.Dir = t.TempDir()
	cmd.Env = cleanProcessEnv(env)
	cmd.Stdout = logs
	cmd.Stderr = logs
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	client := http.Client{Timeout: time.Second}
	get := func(path string, auth bool) (int, []byte) {
		r, _ := http.NewRequest("GET", "http://"+env["BUDGET_ADDR"]+path, nil)
		r.Host = "budget.example.test"
		if auth {
			r.SetBasicAuth("budget", strings.Repeat("p", 12))
		}
		resp, e := client.Do(r)
		if e != nil {
			return 0, nil
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		code, _ := get("/api/ready", true)
		if code == 200 {
			break
		}
		select {
		case e := <-done:
			b, _ := os.ReadFile(logPath)
			t.Fatalf("startup %v: %s", e, b)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("startup deadline")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, path := range []string{"/", "/js/app.js", "/api/ready", "/api/backup", "/api/planner/tasks"} {
		if code, _ := get(path, false); code != 401 {
			t.Fatalf("unauthorized %s %d", path, code)
		}
		if code, _ := get(path, true); code != 200 {
			t.Fatalf("authenticated %s %d", path, code)
		}
	}
	// An administrative readiness probe may read an admin-owned repository;
	// the ownership restriction is enforced when starting the web process.
	probeBackups := t.TempDir()
	os.Chmod(probeBackups, 0700)
	if _, err := openRepository(probeBackups, productionTestID, "production", true); err != nil {
		t.Fatal(err)
	}
	env["BUDGET_BACKUP_DIR"] = probeBackups
	ready := exec.Command(binary, "ready")
	ready.Env = cleanProcessEnv(env)
	if out, e := ready.CombinedOutput(); e != nil {
		t.Fatalf("ready: %v %s", e, out)
	}
	check := exec.Command(binary, "admin", "check", "--data-dir", dir, "--installation-id", productionTestID)
	if e := check.Run(); e == nil {
		t.Fatal("maintenance bypassed live lock")
	}
	if e = cmd.Process.Signal(os.Interrupt); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "planner.db"))
	invoke("admin", "check", "--data-dir", dir, "--installation-id", productionTestID)
	after, _ := os.ReadFile(filepath.Join(dir, "planner.db"))
	if !bytes.Equal(before, after) {
		t.Fatal("check changed DB")
	}
	// Stage 2: real production-mode administrative binary, synthetic data only.
	// No test Store opens the production marker, and the working server is untouched.
	backupRoot := t.TempDir()
	os.Chmod(backupRoot, 0700)
	var copyReceipt backupReceipt
	if e = json.Unmarshal(invoke("admin", "backup", "create", "--data-dir", dir, "--backup-dir", backupRoot, "--installation-id", productionTestID, "--reason", "pre-update", "--protect"), &copyReceipt); e != nil {
		t.Fatal(e)
	}
	archivePath := filepath.Join(backupRoot, "pre-update", copyReceipt.Manifest.ID, "backup.tar.gz")
	invoke("admin", "backup", "verify", "--archive", archivePath)
	recovered := filepath.Join(t.TempDir(), "recovered")
	invoke("admin", "backup", "restore", "--data-dir", recovered, "--backup-dir", backupRoot, "--installation-id", productionTestID, "--archive", archivePath, "--confirm")
	invoke("admin", "check", "--data-dir", recovered, "--installation-id", productionTestID)
	for _, name := range []string{"data.json", "categories.json"} {
		if !bytes.Equal(readBytes(t, filepath.Join(dir, name)), readBytes(t, filepath.Join(recovered, name))) {
			t.Fatal("production restore changed financial bytes")
		}
	}
	delete(env, "BUDGET_BACKUP_DIR")
	// Existing v13 installation: startup refuses, only explicit migration advances it.
	old := t.TempDir()
	os.Chmod(old, 0700)
	db, e := sql.Open("sqlite", "file:"+filepath.Join(old, "planner.db"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at TEXT NOT NULL DEFAULT '')`)
	if e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob("internal/planner/migrations/*.sql")
	for i, file := range files[:len(files)-1] {
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatal(file, e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations(version,checksum) VALUES(?,?)", i+1, fmt.Sprintf("%x", sha256.Sum256(raw))); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec("PRAGMA application_id=1396853840; PRAGMA user_version=13; PRAGMA journal_mode=WAL"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	os.Chmod(filepath.Join(old, "planner.db"), 0600)
	for _, name := range []string{"data.json", "categories.json"} {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		os.WriteFile(filepath.Join(old, name), b, 0600)
	}
	if e = storageguard.WriteMarker(old, storageguard.Marker{Version: 1, Environment: "production", ID: productionTestID}); e != nil {
		t.Fatal(e)
	}
	oldBefore, _ := os.ReadFile(filepath.Join(old, "planner.db"))
	env["BUDGET_DATA_DIR"] = old
	refused := exec.Command(binary)
	refused.Env = cleanProcessEnv(env)
	if e = refused.Run(); e == nil {
		t.Fatal("old schema automatically migrated")
	}
	oldAfter, _ := os.ReadFile(filepath.Join(old, "planner.db"))
	if !bytes.Equal(oldBefore, oldAfter) {
		t.Fatal("startup modified old schema")
	}
	invoke("admin", "migrate", "--data-dir", old, "--installation-id", productionTestID, "--confirm")
	invoke("admin", "check", "--data-dir", old, "--installation-id", productionTestID)
}

func TestFinancialRequestConcurrencyIsBounded(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	done := make(chan int, 4)
	h := limitFinancialBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release; w.WriteHeader(204) }))
	for i := 0; i < 4; i++ {
		go func() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/api/months", strings.NewReader(`{}`)))
			done <- w.Code
		}()
	}
	for i := 0; i < 4; i++ {
		<-entered
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/months", strings.NewReader(`{}`)))
	close(release)
	if w.Code != 503 {
		t.Fatal("unbounded request buffering", w.Code)
	}
	for i := 0; i < 4; i++ {
		if code := <-done; code != 204 {
			t.Fatal(code)
		}
	}
}

func TestBackupProductionWebCannotOwnRepository(t *testing.T) {
	env := productionEnvironment(t)
	root := t.TempDir()
	os.Chmod(root, 0700)
	if _, e := openRepository(root, productionTestID, "production", true); e != nil {
		t.Fatal(e)
	}
	env["BUDGET_BACKUP_DIR"] = root
	c, e := readConfig(func(k string) string { return env[k] })
	if e != nil {
		t.Fatal("read-only config probe refused", e)
	}
	if e = run(context.Background(), c); e == nil || !strings.Contains(e.Error(), "owned by a different administrative user") {
		t.Fatal("web writer not refused before opening SQLite", e)
	}
}
