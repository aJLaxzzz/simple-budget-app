package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStagingCookiesAndPortBoundary(t *testing.T) {
	configs := []serverConfig{
		configForTest(t, map[string]string{"BUDGET_PASSWORD": "Prod-synthetic-12", "BUDGET_ADDR": "127.0.0.1:3001"}),
		configForTest(t, map[string]string{"BUDGET_INSTANCE": "acceptance", "BUDGET_PASSWORD": "Stg-synthetic-12", "BUDGET_ADDR": "127.0.0.1:3002"}),
	}
	handlers := make([]http.Handler, 2)
	cookies := make([]*http.Cookie, 2)
	csrf := make([]string, 2)
	for i, c := range configs {
		a, err := newWebAuth(c)
		if err != nil {
			t.Fatal(err)
		}
		handlers[i] = accessHandler(c, a.handler(c, http.NotFoundHandler(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })))
		cookies[i], csrf[i] = loginTest(t, handlers[i], c, true)
	}
	if cookies[0].Name != "__Host-budget-session" || cookies[1].Name != "__Host-budget-stg-session" {
		t.Fatal("cookie namespaces overlap")
	}
	for i, c := range configs {
		h := handlers[i]
		if w := browserCall(h, c, "GET", "/api/private", "", cookies[1-i], ""); w.Code != 401 {
			t.Fatal("other environment session accepted", w.Code)
		}
		if w := browserCall(h, c, "POST", "/api/private", "{}", cookies[i], csrf[1-i]); w.Code != 403 {
			t.Fatal("other CSRF accepted", w.Code)
		}
		if w := browserCall(h, c, "POST", "/api/private", "{}", cookies[i], csrf[i]); w.Code != 200 {
			t.Fatal("own session failed", w.Code)
		}
		for _, path := range []string{"/api/private", "/api/events", "/api/auth/session", "/api/environment", "/js/environment.js"} {
			r := httptest.NewRequest("GET", path, nil)
			r.Host = c.Addr
			r.AddCookie(cookies[i])
			r.Header.Set("Sec-Fetch-Site", "same-site")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal("same-site cross-port request allowed", path, w.Code)
			}
		}
		r := httptest.NewRequest("GET", "/api/private", nil)
		r.Host = c.Addr
		r.AddCookie(cookies[i])
		r.Header.Set("Origin", "https://"+configs[1-i].Addr)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("foreign port Origin accepted")
		}
		r = httptest.NewRequest("GET", "/api/private", nil)
		r.Host = configs[1-i].Addr
		r.AddCookie(cookies[i])
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("foreign port Host accepted")
		}
	}
}
