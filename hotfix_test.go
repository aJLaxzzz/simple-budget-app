package main

import (
	"encoding/binary"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSyncReadWatermarkPrecedesHandler(t *testing.T) {
	h := newEventHub()
	h.publish("planner")
	handler := withSync(h, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.publish("finance") // A concurrent change must not be acknowledged by this read.
		w.Write([]byte(`{"synthetic":true}`))
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/planner/tasks", nil))
	if w.Header().Get("X-Budget-Sync-Revision") != "1" || w.Header().Get("X-Budget-Sync-Generation") != h.generation {
		t.Fatal("unsafe read watermark")
	}
	other := newEventHub()
	if other.generation == h.generation {
		t.Fatal("generation reused")
	}
}

func TestFaviconEmbeddedAndAvailableAtLogin(t *testing.T) {
	c := configForTest(t, map[string]string{"BUDGET_PASSWORD": "Synthetic-secret-12"})
	c.StaticDir = "" // Exercise the real embedded assets, not testAssets placeholders.
	h := protectedTestHandler(t, c)
	for _, path := range []string{"/favicon.svg", "/favicon.ico", "/favicon-16.png", "/favicon-32.png"} {
		w := browserCall(h, c, "GET", path, "", nil, "")
		if w.Code != 200 || w.Body.Len() == 0 || w.Header().Get("WWW-Authenticate") != "" {
			t.Fatalf("icon unavailable: %s %d", path, w.Code)
		}
		if strings.HasSuffix(path, ".png") {
			image, e := png.Decode(w.Body)
			if e != nil {
				t.Fatal(e)
			}
			size := 32
			if strings.Contains(path, "16") {
				size = 16
			}
			if image.Bounds().Dx() != size || image.Bounds().Dy() != size {
				t.Fatal("wrong icon dimensions")
			}
		}
		if strings.HasSuffix(path, ".ico") {
			b := w.Body.Bytes()
			if len(b) < 38 || binary.LittleEndian.Uint16(b[4:6]) != 2 {
				t.Fatal("ICO must contain 16/32 fallback images")
			}
		}
	}
	for _, path := range []string{"/index.html", "/login.html"} {
		b, e := embeddedFrontend.ReadFile("static" + path)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(b), `href="/favicon.svg"`) || !strings.Contains(string(b), `href="/favicon.ico"`) {
			t.Fatal("missing icons", path)
		}
	}
	if w := browserCall(h, c, "GET", "/api/planner/tasks", "", nil, ""); w.Code != 401 {
		t.Fatal("favicon allowlist weakened auth")
	}
}
