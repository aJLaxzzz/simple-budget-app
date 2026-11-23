package main

import (
	"budget/internal/planner"
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testAssets(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for path := range publicAssets {
		file := filepath.Join(dir, path[1:])
		if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
			t.Fatal(e)
		}
		putFile(t, file, "public asset")
	}
	return dir
}
func configForTest(t *testing.T, overrides map[string]string) serverConfig {
	t.Helper()
	env := map[string]string{"BUDGET_DATA_DIR": t.TempDir(), "BUDGET_STATIC_DIR": testAssets(t)}
	for k, v := range overrides {
		env[k] = v
	}
	c, e := readConfig(func(k string) string { return env[k] })
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func protectedTestHandler(t *testing.T, c serverConfig) http.Handler {
	t.Helper()
	testHandler(t) // registers cleanup for the legacy global store
	h, e := prepareServer(c)
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func accessRequest(h http.Handler, method, path, host, peer, origin, password string, https bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(`{"month":11,"year":2026}`))
	r.Host = host
	r.RemoteAddr = peer
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if password != "" {
		r.SetBasicAuth("budget", password)
	}
	if https {
		r.TLS = &tls.ConnectionState{}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestConfigDefaultsAndExternalFailClosed(t *testing.T) {
	c := configForTest(t, nil)
	if c.Addr != "127.0.0.1:3001" || !c.Local {
		t.Fatalf("defaults: %+v", c)
	}
	for _, env := range []map[string]string{
		{"BUDGET_ADDR": "0.0.0.0:3001"},
		{"BUDGET_ADDR": "192.0.2.1:3001", "BUDGET_PASSWORD": strings.Repeat("x", 32)},
		{"BUDGET_ADDR": "192.0.2.1:3001", "BUDGET_TLS_CERT": "cert", "BUDGET_TLS_KEY": "key"},
		{"BUDGET_ADDR": "0.0.0.0:3001", "BUDGET_PASSWORD": strings.Repeat("x", 32), "BUDGET_TLS_CERT": "cert", "BUDGET_TLS_KEY": "key"},
		{"BUDGET_ADDR": ":3001"}, {"BUDGET_ADDR": "localhost:0"}, {"BUDGET_ADDR": "example.com:3001"},
		{"BUDGET_PASSWORD": "short"}, {"BUDGET_TLS_CERT": "cert"}, {"BUDGET_ALLOWED_HOSTS": "*"},
		{"BUDGET_ALLOWED_HOSTS": "user@localhost:3001"}, {"BUDGET_ALLOWED_HOSTS": "localhost:3001/path"},
		{"BUDGET_DATA_DIR": filepath.Join(t.TempDir(), "missing")},
	} {
		if _, e := readConfig(func(k string) string { return env[k] }); e == nil {
			t.Fatalf("unsafe config accepted: %v", env)
		}
	}
	c = configForTest(t, map[string]string{"BUDGET_ADDR": "0.0.0.0:3443", "BUDGET_PASSWORD": strings.Repeat("x", 32), "BUDGET_TLS_CERT": "cert", "BUDGET_TLS_KEY": "key", "BUDGET_ALLOWED_HOSTS": "budget.example:3443"})
	if c.Local || !c.AllowedHosts["budget.example:3443"] {
		t.Fatal("external config")
	}
}

func TestLocalAccessPolicy(t *testing.T) {
	c := configForTest(t, nil)
	h := protectedTestHandler(t, c)
	for _, tc := range []struct {
		host, peer, origin string
		want               int
	}{
		{"127.0.0.1:3001", "127.0.0.1:1234", "", 200},
		{"localhost:3001", "127.0.0.1:1234", "http://localhost:3001", 200},
		{"evil.example:3001", "127.0.0.1:1234", "", 403},
		{"127.0.0.1:3001", "192.0.2.1:1234", "", 403},
		{"127.0.0.1:3001", "127.0.0.1:1234", "http://evil.example", 403},
		{"127.0.0.1:3001", "127.0.0.1:1234", "null", 403},
	} {
		w := accessRequest(h, "GET", "/api/months", tc.host, tc.peer, tc.origin, "", false)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d", tc, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/months", strings.NewReader(`{"month":11,"year":2026}`))
	r.Host = "localhost:3001"
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Del("Sec-Fetch-Site")
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("trusted forwarded header")
	}
}

func TestAuthenticationCoversAllRoutes(t *testing.T) {
	password := strings.Repeat("s", 32)
	c := configForTest(t, map[string]string{"BUDGET_ADDR": "192.0.2.1:3443", "BUDGET_PASSWORD": password, "BUDGET_TLS_CERT": "cert", "BUDGET_TLS_KEY": "key"})
	h := protectedTestHandler(t, c)
	routes := []string{"/", "/js/app.js", "/api/months", "/api/months/month-2026-11", "/api/months/month-2026-11/income", "/api/months/month-2026-11/expenses", "/api/months/month-2026-11/incomes", "/api/categories", "/api/years/2026", "/api/now", "/api/backup", "/api/search?q=ab", "/api/shutdown", "/api/future-admin"}
	for _, path := range routes {
		for _, method := range []string{"GET", "POST", "PATCH", "DELETE", "PUT"} {
			for _, bad := range []string{"", "wrong"} {
				w := accessRequest(h, method, path, c.Addr, "192.0.2.5:1234", "", bad, true)
				if w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
					t.Fatalf("unprotected %s %s: %d", method, path, w.Code)
				}
			}
		}
	}
	w := accessRequest(h, "POST", "/api/months", c.Addr, "192.0.2.5:1234", "https://"+c.Addr, password, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/", "/api/months", "/api/backup"} {
		if w := accessRequest(h, "GET", path, c.Addr, "192.0.2.5:1234", "", password, true); w.Code != 200 {
			t.Fatal(path, w.Code)
		}
	}
	if w := accessRequest(h, "GET", "/api/months", c.Addr, "192.0.2.5:1234", "", password, false); w.Code != 403 {
		t.Fatal("cleartext allowed")
	}
}

func TestStaticAllowlistAndTraversal(t *testing.T) {
	c := configForTest(t, nil)
	h := protectedTestHandler(t, c)
	secret := "PRIVATE FINANCIAL CONTENT"
	for _, name := range []string{"data.json", "categories.json", "planner.db", "backup.zip", ".env", "secret.key", "config.yaml", "other.js"} {
		putFile(t, filepath.Join(c.StaticDir, name), secret)
	}
	for _, path := range []string{"/data.json", "/categories.json", "/planner.db", "/backup.zip", "/.env", "/secret.key", "/config.yaml", "/other.js", "/js/", "/static/", "/../data.json", "/js/../../data.json", "/%2e%2e/data.json", "/js/%2e%2e/%2e%2e/data.json", "/%252e%252e/data.json", "/js%5c..%5cdata.json", "/js/app.js/data.json"} {
		w := accessRequest(h, "GET", path, c.Addr, "127.0.0.1:1234", "", "", false)
		if w.Code != 404 || strings.Contains(w.Body.String(), secret) {
			t.Fatalf("path %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for path := range publicAssets {
		w := accessRequest(h, "GET", path, c.Addr, "127.0.0.1:1234", "", "", false)
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
	}
}

func TestMissingStaticAndSymlinksFailStartup(t *testing.T) {
	c := configForTest(t, nil)
	testHandler(t)
	c.StaticDir = filepath.Join(t.TempDir(), "missing")
	if _, e := prepareServer(c); e == nil || !strings.Contains(e.Error(), "static") {
		t.Fatal("missing static accepted", e)
	}
	if files, e := os.ReadDir(c.DataDir); e != nil || len(files) != 0 {
		t.Fatal("failed startup wrote data")
	}
	c.StaticDir = testAssets(t)
	asset := filepath.Join(c.StaticDir, "js", "app.js")
	if e := os.Remove(asset); e != nil {
		t.Fatal(e)
	}
	secret := filepath.Join(t.TempDir(), "data.json")
	putFile(t, secret, `{"private":true}`)
	if e := os.Symlink(secret, asset); e != nil {
		t.Fatal(e)
	}
	if _, e := newStaticHandler(c.StaticDir); e == nil {
		t.Fatal("symlink asset accepted")
	}
	// A symlink directory inside static is rejected too.
	c.StaticDir = testAssets(t)
	if e := os.Rename(filepath.Join(c.StaticDir, "js"), filepath.Join(c.StaticDir, "scripts")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(c.StaticDir, "scripts"), filepath.Join(c.StaticDir, "js")); e != nil {
		t.Fatal(e)
	}
	if _, e := newStaticHandler(c.StaticDir); e == nil {
		t.Fatal("symlink directory accepted")
	}
}

func TestConfiguredDataDirectoryAndRemovedShutdown(t *testing.T) {
	c := configForTest(t, nil)
	h := protectedTestHandler(t, c)
	w := accessRequest(h, "POST", "/api/months", c.Addr, "127.0.0.1:1234", "", "", false)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	if !strings.Contains(string(readFile(t, filepath.Join(c.DataDir, "data.json"))), "month-2026-11") {
		t.Fatal("wrong data directory")
	}
	before := readFile(t, filepath.Join(c.DataDir, "data.json"))
	for _, method := range []string{"GET", "POST"} {
		w = accessRequest(h, method, "/api/shutdown", c.Addr, "127.0.0.1:1234", "", "", false)
		if w.Code != 404 && w.Code != 405 {
			t.Fatal(w.Code)
		}
	}
	// Old tabs sending shutdown requests cannot kill other connections.
	time.Sleep(350 * time.Millisecond)
	w = accessRequest(h, "GET", "/api/months", c.Addr, "127.0.0.1:1234", "", "", false)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if string(before) != string(readFile(t, filepath.Join(c.DataDir, "data.json"))) {
		t.Fatal("shutdown rewrote data")
	}
}

func TestGracefulShutdownWaitsForRequest(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveUntilCancelled(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; fmt.Fprint(w, "saved") }))
	}()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		r, e := client.Get("http://" + listener.Addr().String())
		if e != nil {
			response <- e
			return
		}
		defer r.Body.Close()
		b, e := io.ReadAll(r.Body)
		if string(b) != "saved" {
			e = fmt.Errorf("response %q", b)
		}
		response <- e
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case e := <-done:
		t.Fatalf("shutdown interrupted request: %v", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case e := <-response:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("response timeout")
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

// Exercise the actual main entry point and OS signal handlers in a child process.
func TestSignalServerHelper(t *testing.T) {
	if os.Getenv("BUDGET_TEST_HELPER") != "1" {
		return
	}
	main()
}
func TestSIGINTAndSIGTERM(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			probe, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			addr := probe.Addr().String()
			probe.Close()
			dir := t.TempDir()
			assets := testAssets(t)
			original := `{"months":[]}`
			putFile(t, filepath.Join(dir, "data.json"), original)
			cmd := exec.Command(os.Args[0], "-test.run=^TestSignalServerHelper$")
			for _, env := range os.Environ() {
				if !strings.HasPrefix(env, "BUDGET_") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Env = append(cmd.Env, "BUDGET_TEST_HELPER=1", "BUDGET_ADDR="+addr, "BUDGET_DATA_DIR="+dir, "BUDGET_STATIC_DIR="+assets)
			pipe, e := cmd.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			cmd.Stderr = cmd.Stdout
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			ready := make(chan bool, 1)
			go func() {
				scanner := bufio.NewScanner(pipe)
				sent := false
				for scanner.Scan() {
					if strings.Contains(scanner.Text(), "Личный бюджет:") && !sent {
						ready <- true
						sent = true
					}
				}
				if !sent {
					ready <- false
				}
			}()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("server failed to start")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("startup timeout")
			}
			client := &http.Client{Timeout: 5 * time.Second}
			r, e := client.Get("http://" + addr + "/api/months")
			if e != nil {
				t.Fatal(e)
			}
			r.Body.Close()
			if r.StatusCode != 200 {
				t.Fatal(r.StatusCode)
			}
			if e = cmd.Process.Signal(sig); e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case e := <-done:
				if e != nil {
					t.Fatal("unclean shutdown", e)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("signal shutdown timeout")
			}
			if string(readFile(t, filepath.Join(dir, "data.json"))) != original {
				t.Fatal("startup or shutdown changed existing data")
			}
		})
	}
}

func TestTLSAuthenticationOverHTTP(t *testing.T) {
	c := configForTest(t, map[string]string{"BUDGET_ADDR": "192.0.2.1:3443", "BUDGET_PASSWORD": strings.Repeat("p", 32), "BUDGET_TLS_CERT": "test-cert", "BUDGET_TLS_KEY": "test-key"})
	testHandler(t)
	server := httptest.NewUnstartedServer(nil)
	c.AllowedHosts = map[string]bool{server.Listener.Addr().String(): true}
	h, e := prepareServer(c)
	if e != nil {
		server.Close()
		t.Fatal(e)
	}
	server.Config.Handler = h
	server.StartTLS()
	defer server.Close()
	for _, tc := range []struct {
		user, password string
		status         int
	}{{"", "", 401}, {"wrong", c.Password, 401}, {"budget", c.Password, 200}} {
		r, e := http.NewRequest("GET", server.URL+"/api/months", nil)
		if e != nil {
			t.Fatal(e)
		}
		if tc.user != "" {
			r.SetBasicAuth(tc.user, tc.password)
		}
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("user %q: %d", tc.user, response.StatusCode)
		}
	}
}

func TestPlannerAPIProtectedAndFinanceUntouched(t *testing.T) {
	testHandler(t)
	c := configForTest(t, map[string]string{"BUDGET_PASSWORD": strings.Repeat("p", 32)})
	originalData := `{"months":[]}`
	originalCategories := `{"expense":[{"id":"food","name":"Еда","subcategories":[]}],"income":[{"id":"salary","name":"Зарплата"}]}`
	putFile(t, filepath.Join(c.DataDir, "data.json"), originalData)
	putFile(t, filepath.Join(c.DataDir, "categories.json"), originalCategories)
	store, e := planner.Open(context.Background(), filepath.Join(c.DataDir, "planner.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	h, e := prepareApplication(c, store)
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/planner/task-templates", "/api/planner/task-templates/id", "/api/planner/task-templates/id/versions", "/api/planner/task-templates/id/archive", "/api/planner/task-templates/id/instantiate", "/api/planner/projects/id/template-preview", "/api/planner/projects/id/save-template", "/api/planner/icons", "/api/planner/icons/id", "/api/planner/icons/lucide:house/image", "/api/planner/attention/id?type=task", "/api/planner/attention", "/api/planner/occurrences/id", "/api/planner/series/preview", "/api/planner/templates/id/projects", "/api/planner/production-calendar", "/api/planner/production-calendar/bundled/2026", "/api/planner/production-calendar/preview", "/api/planner/production-calendar/import", "/api/planner/templates", "/api/planner/templates/id", "/api/planner/templates/id/versions", "/api/planner/templates/id/archive", "/api/planner/templates/id/instantiate", "/api/planner/templates/id/preview", "/api/planner/series/id/change-template", "/api/planner/series/id/preview", "/api/planner/series/id/change-rule", "/api/planner/series/id/pause", "/api/planner/series/id/resume", "/api/planner/series/id/archive", "/api/planner/series/id/reanchor", "/api/planner/incidents", "/api/planner/occurrences/id/skip", "/api/planner/occurrences/id/review", "/api/planner/occurrences/id/restore", "/api/planner/occurrences/id/complete", "/api/planner/occurrences/id/cancel", "/api/planner/occurrences/id/reschedule", "/api/planner/series", "/api/planner/series/id", "/api/planner/materialize", "/api/planner/recurrence/preview", "/api/planner/calendar?from=2026-11-01&to=2026-11-30", "/api/planner/tasks/id/context", "/api/planner/projects", "/api/planner/projects/id", "/api/planner/projects/id/archive", "/api/planner/tasks", "/api/planner/tasks/id/complete", "/api/planner/tasks/id/cancel", "/api/planner/tasks/id/restore", "/api/planner/tasks/id/reschedule", "/api/planner/history", "/api/planner/sessions", "/api/planner/sessions/id", "/api/planner/sessions/id/complete", "/api/planner/sessions/id/cancel", "/api/planner/sessions/id/restore", "/api/planner/sessions/id/reschedule"} {
		for _, method := range []string{"GET", "POST", "PATCH"} {
			w := accessRequest(h, method, path, c.Addr, "127.0.0.1:1234", "", "", false)
			if w.Code != 401 || !strings.Contains(w.Body.String(), `"error"`) {
				t.Fatalf("unprotected %s %s: %d", method, path, w.Code)
			}
		}
	}
	send := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = c.Addr
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Content-Type", "application/json")
		r.SetBasicAuth("budget", c.Password)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := send("POST", "/api/planner/recurrence/preview", `{"rule":{"schema_version":1,"mode":"schedule","unit":"days","interval":45,"allowed_days":{"mode":"any"},"adjustment":"none"},"anchor":{"date":"2026-01-01","cycle_index":0,"kind":"schedule"},"count":5}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"base_date":"2026-02-15"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send("POST", "/api/planner/projects", `{"title":"Project"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send("POST", "/api/planner/tasks", `{"title":"Inbox"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	one := decode[planner.TaskDTO](t, w)
	w = send("POST", "/api/planner/sessions", fmt.Sprintf(`{"task_id":%q,"date":"2099-01-01"}`, one.ID))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	work := decode[planner.SessionDTO](t, w)
	w = send("GET", "/api/planner/calendar?from=2099-01-01&to=2099-01-31", "")
	if w.Code != 200 || len(decode[planner.CalendarProjection](t, w).Items) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send("GET", "/api/planner/tasks/"+one.ID+"/context", "")
	if w.Code != 200 || decode[planner.TaskContext](t, w).Task.ID != one.ID {
		t.Fatal(w.Code, w.Body.String())
	}

	w = send("POST", "/api/planner/tasks/"+one.ID+"/complete", fmt.Sprintf(`{"expected_revision":1,"completed_date":"2020-01-01","sessions_decision":"cancel","session_revisions":{%q:1}}`, work.ID))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send("GET", "/api/planner/history?object_type=task&object_id="+one.ID, "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = send("GET", "/api/planner/sessions/"+work.ID, "")
	if w.Code != 200 || decode[planner.SessionDTO](t, w).Status != "cancelled" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send("GET", "/api/planner/history?object_type=session&object_id="+work.ID, "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = send("POST", "/api/planner/series", `{"request_id":"financial-isolation","task":{"title":"Recurring"},"first_date":"2026-11-16","rule":{"schema_version":1,"mode":"schedule","unit":"days","interval":45,"allowed_days":{"mode":"any"},"adjustment":"none"}}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	createdSeries := decode[planner.SeriesDTO](t, w)
	command := planner.SeriesCommand{Action: "pause", ExpectedRevision: createdSeries.Revision, StartDate: "2026-11-16", EndDate: "2026-12-31"}
	rawCommand, _ := json.Marshal(command)
	w = send("POST", "/api/planner/series/"+createdSeries.ID+"/preview", string(rawCommand))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	preview := decode[planner.SeriesChangePreview](t, w)
	command.RequestID = "finance-safe-pause"
	command.PreviewToken = preview.PreviewToken
	rawCommand, _ = json.Marshal(command)
	w = send("POST", "/api/planner/series/"+createdSeries.ID+"/pause", string(rawCommand))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send("GET", "/api/planner/incidents?kind=skipped&disposition=needs_decision", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "pause") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send("POST", "/api/planner/materialize", `{}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send("POST", "/api/planner/templates", `{"request_id":"safe-template","snapshot":{"title":"Cleaning","tasks":[{"key":"one","title":"First","planned_offset":0}]}}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	templateResult := decode[planner.CommandResult](t, w)
	w = send("POST", "/api/planner/templates/"+templateResult.Template.ID+"/instantiate", fmt.Sprintf(`{"request_id":"safe-instance","expected_revision":%d,"start_date":"2026-12-20"}`, templateResult.Template.Revision))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send("POST", "/api/planner/series", fmt.Sprintf(`{"request_id":"safe-project-series","source_type":"project","template_id":%q,"expected_revision":%d,"first_date":"2026-12-20","rule":{"schema_version":1,"mode":"schedule","unit":"days","interval":14,"allowed_days":{"mode":"any"},"adjustment":"none"}}`, templateResult.Template.ID, templateResult.Template.Revision))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if string(readFile(t, filepath.Join(c.DataDir, "data.json"))) != originalData || string(readFile(t, filepath.Join(c.DataDir, "categories.json"))) != originalCategories {
		t.Fatal("planner changed financial files")
	}
}
