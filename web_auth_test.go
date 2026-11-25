package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func browserCall(h http.Handler, c serverConfig, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = c.Addr
	r.RemoteAddr = "127.0.0.1:1234"
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Origin", "https://"+c.Addr)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Mode", "cors")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func loginTest(t *testing.T, h http.Handler, c serverConfig, remember bool) (*http.Cookie, string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"username": "budget", "password": c.Password, "remember": remember})
	w := browserCall(h, c, "POST", "/api/auth/login", string(raw), nil, "")
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	var data map[string]string
	json.Unmarshal(w.Body.Bytes(), &data)
	return cookies[0], data["csrf"]
}
func TestWebSessionsPersistenceRevocationAndIsolation(t *testing.T) {
	c := configForTest(t, map[string]string{"BUDGET_PASSWORD": "Synthetic-secret-12"})
	h := protectedTestHandler(t, c)
	cookie, csrf := loginTest(t, h, c, true)
	if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 30*24*3600 {
		t.Fatal("cookie attributes", cookie)
	}
	raw, e := os.ReadFile(filepath.Join(c.DataDir, authFile))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(raw, []byte(cookie.Value)) || bytes.Contains(raw, []byte(c.Password)) {
		t.Fatal("secret stored verbatim")
	}
	h2, e := prepareServer(c)
	if e != nil {
		t.Fatal(e)
	}
	if w := browserCall(h2, c, "GET", "/api/months", "", cookie, ""); w.Code != 200 {
		t.Fatal("session lost after restart", w.Code)
	}
	if w := browserCall(h2, c, "POST", "/api/auth/logout", "{}", cookie, ""); w.Code != 403 {
		t.Fatal("missing CSRF accepted", w.Code)
	}
	second, _ := loginTest(t, h2, c, false)
	if second.MaxAge != 0 || !second.Expires.IsZero() {
		t.Fatal("browser session persisted")
	}
	if w := browserCall(h2, c, "POST", "/api/auth/revoke", `{"others":true}`, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := browserCall(h2, c, "GET", "/api/months", "", second, ""); w.Code != 401 {
		t.Fatal("revoked session accepted")
	}
	if w := browserCall(h2, c, "GET", "/api/months", "", cookie, ""); w.Code != 200 {
		t.Fatal("current session revoked")
	}
	c.Password = "Changed-secret-12"
	h3, e := prepareServer(c)
	if e != nil {
		t.Fatal(e)
	}
	if w := browserCall(h3, c, "GET", "/api/months", "", cookie, ""); w.Code != 401 {
		t.Fatal("password change kept session")
	}
	fresh, _ := loginTest(t, h3, c, false)
	if e := invalidateWebSessions(c.DataDir); e != nil {
		t.Fatal(e)
	}
	h4, e := prepareServer(c)
	if e != nil {
		t.Fatal(e)
	}
	if w := browserCall(h4, c, "GET", "/api/months", "", fresh, ""); w.Code != 401 {
		t.Fatal("restore kept session")
	}
}
func TestWebSessionExpiryAndLoginCSRF(t *testing.T) {
	c := configForTest(t, map[string]string{"BUDGET_PASSWORD": "Synthetic-secret-12"})
	a, e := newWebAuth(c)
	if e != nil {
		t.Fatal(e)
	}
	outer := c
	outer.Password = ""
	h := accessHandler(outer, a.handler(c, http.NotFoundHandler(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	cookie, _ := loginTest(t, h, c, false)
	a.now = func() time.Time { return time.Now().Add(13 * time.Hour) }
	if w := browserCall(h, c, "GET", "/api/months", "", cookie, ""); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"budget","password":"Synthetic-secret-12"}`))
	r.Host = c.Addr
	r.RemoteAddr = "127.0.0.1:12"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("Origin-less login", w.Code)
	}
	for i := 0; i < 12; i++ {
		w = browserCall(h, c, "POST", "/api/auth/login", `{"username":"budget","password":"wrong"}`, nil, "")
	}
	if w.Code != 429 {
		t.Fatal("login rate limit", w.Code)
	}
}
func TestBrowserNavigationAndMachineAuthBoundary(t *testing.T) {
	c := configForTest(t, map[string]string{"BUDGET_PASSWORD": "Synthetic-secret-12"})
	h := protectedTestHandler(t, c)
	for _, path := range []string{"/", "/login.html", "/api/months", "/api/backup", "/api/events"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = c.Addr
		r.RemoteAddr = "127.0.0.1:12"
		r.Header.Set("Sec-Fetch-Mode", "navigate")
		r.Header.Set("Sec-Fetch-Dest", "document")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		expected := 403
		if path == "/" {
			expected = 303
		}
		if path == "/login.html" {
			expected = 200
		}
		if w.Code != expected {
			t.Fatal(path, w.Code)
		}
		if w.Header().Get("WWW-Authenticate") != "" {
			t.Fatal("browser Basic challenge")
		}
	}
	for _, browser := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/months", nil)
		r.Host = c.Addr
		r.RemoteAddr = "127.0.0.1:12"
		r.SetBasicAuth("budget", c.Password)
		r.Header.Set("X-Budget-Admin", "1")
		if browser {
			r.Header.Set("Sec-Fetch-Mode", "cors")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		expected := 200
		if browser {
			expected = 401
		}
		if w.Code != expected {
			t.Fatal("machine boundary", browser, w.Code)
		}
	}
}
func TestFinancialRevisionAtomicConcurrencyAndGeneration(t *testing.T) {
	c := configForTest(t, nil)
	h := protectedTestHandler(t, c)
	revision := accessRequest(h, "GET", "/api/months", c.Addr, "127.0.0.1:1", "", "", false).Header().Get("X-Budget-Revision")
	run := func() int {
		r := httptest.NewRequest("POST", "/api/months", strings.NewReader(`{"month":11,"year":2026}`))
		r.Host = c.Addr
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("If-Match", revision)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- run() }()
	}
	wg.Wait()
	close(codes)
	seen := map[int]int{}
	for c := range codes {
		seen[c]++
	}
	if seen[201] != 1 || seen[409] != 1 {
		t.Fatal(seen)
	}
	current := accessRequest(h, "GET", "/api/months", c.Addr, "127.0.0.1:1", "", "", false).Header().Get("X-Budget-Revision")
	h2, e := prepareServer(c)
	if e != nil {
		t.Fatal(e)
	}
	after := accessRequest(h2, "GET", "/api/months", c.Addr, "127.0.0.1:1", "", "", false).Header().Get("X-Budget-Revision")
	if after == current {
		t.Fatal("generation reused after restart")
	}
}
func TestEventHubBoundsAndRevokedStream(t *testing.T) {
	hub := newEventHub()
	slow, _ := hub.subscribe()
	for i := 0; i < 20; i++ {
		hub.publish("planner")
	}
	if hub.clients[slow] {
		t.Fatal("slow client kept active")
	}
	hub.unsubscribe(slow)
	for range slow {
	}
	var slots []chan syncEvent
	for i := 0; i < 32; i++ {
		ch, ok := hub.subscribe()
		if !ok {
			t.Fatal("available subscriber slot refused")
		}
		slots = append(slots, ch)
	}
	if _, ok := hub.subscribe(); ok {
		t.Fatal("subscriber bound bypassed")
	}
	for _, ch := range slots {
		hub.unsubscribe(ch)
	}
	c := configForTest(t, map[string]string{"BUDGET_PASSWORD": "Synthetic-secret-12"})
	a, e := newWebAuth(c)
	if e != nil {
		t.Fatal(e)
	}
	token := randomToken()
	a.state.Sessions = []webSession{{ID: randomToken(), Hash: tokenHash(token), Expires: time.Now().Add(time.Hour)}}
	srv := httptest.NewServer(hub.handler(a, true))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	resp, e := srv.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1024)
	if _, e = resp.Body.Read(buf); e != nil {
		t.Fatal(e)
	}
	a.mu.Lock()
	a.state.Sessions = nil
	a.mu.Unlock()
	hub.publish("planner")
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(resp.Body); done <- string(b) }()
	select {
	case b := <-done:
		if !strings.Contains(b, "auth-required") {
			t.Fatal(b)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("revoked stream still open")
	}
}

func TestFinancialFailedSaveDoesNotPublishOrAdvanceRevision(t *testing.T) {
	hub := newEventHub()
	h := withSync(hub, testHandler(t))
	ch, _ := hub.subscribe()
	defer hub.unsubscribe(ch)
	<-ch
	revision := request(t, h, "GET", "/api/months", "", 200).Header().Get("X-Budget-Revision")
	original := writeJSONFile
	defer func() { writeJSONFile = original }()
	writeJSONFile = func(string, []byte) error { return errors.New("synthetic disk failure") }
	r := httptest.NewRequest("POST", "/api/months", strings.NewReader(`{"month":11,"year":2026}`))
	r.Header.Set("If-Match", revision)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 || len(store.Months) != 0 || hub.financeRevision != 0 {
		t.Fatal(w.Code, hub.financeRevision)
	}
	select {
	case <-ch:
		t.Fatal("failed save published a live event")
	default:
	}
	if got := request(t, h, "GET", "/api/months", "", 200).Header().Get("X-Budget-Revision"); got != revision {
		t.Fatal("failed save advanced revision")
	}
}

func TestWebSessionUnsafeStorageAndPersistenceFailure(t *testing.T) {
	c := configForTest(t, map[string]string{"BUDGET_PASSWORD": "Synthetic-secret-12"})
	a, err := newWebAuth(c)
	if err != nil {
		t.Fatal(err)
	}
	h := accessHandler(c, a.handler(c, http.NotFoundHandler(), http.NotFoundHandler()))
	cookie, csrf := loginTest(t, h, c, false)
	path := a.path
	a.path = filepath.Join(c.DataDir, "missing", "state.json")
	if w := browserCall(h, c, "POST", "/api/auth/logout", `{}`, cookie, csrf); w.Code != 500 {
		t.Fatal(w.Code)
	}
	if w := browserCall(h, c, "GET", "/api/auth/session", "", cookie, ""); w.Code != 200 {
		t.Fatal("failed revoke silently lost session", w.Code)
	}
	raw, _ := json.Marshal(map[string]string{"username": "budget", "password": c.Password})
	if w := browserCall(h, c, "POST", "/api/auth/login", string(raw), nil, ""); w.Code != 500 || len(w.Result().Cookies()) != 0 || len(a.state.Sessions) != 1 {
		t.Fatal("failed persistence granted a session")
	}
	a.path = path
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := newWebAuth(c); err == nil {
		t.Fatal("public session storage accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, path+".link"); err != nil {
		t.Fatal(err)
	}
	if _, err := newWebAuth(c); err == nil {
		t.Fatal("hardlinked session storage accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".link", path); err != nil {
		t.Fatal(err)
	}
	if _, err := newWebAuth(c); err == nil {
		t.Fatal("symlink session storage accepted")
	}
}

func authenticatedTestPolicy(t *testing.T, c serverConfig, next http.Handler) http.Handler {
	t.Helper()
	if c.DataDir == "" {
		c.DataDir = t.TempDir()
	}
	a, e := newWebAuth(c)
	if e != nil {
		t.Fatal(e)
	}
	return accessHandler(c, a.handler(c, http.NotFoundHandler(), next))
}

func TestSSEIdleDoesNotExpireWriteDeadline(t *testing.T) {
	hub := newEventHub()
	server := httptest.NewServer(hub.handler(nil, false))
	defer server.Close()
	client := server.Client()
	client.Timeout = 14 * time.Second
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	buf := make([]byte, 4096)
	if _, err = response.Body.Read(buf); err != nil {
		t.Fatal(err)
	}
	n, err := response.Body.Read(buf)
	if err != nil {
		t.Fatal("stream expired during heartbeat idle", err)
	}
	if !strings.Contains(string(buf[:n]), "heartbeat") {
		t.Fatal(string(buf[:n]))
	}
}
