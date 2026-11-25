package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const financialBodyLimit = 1 << 20

// One bounded bucket for failed credentials, not client IPs. Valid credentials
// never consume this budget or wait behind it: a shared Serve peer cannot lock
// out other devices. No password, username, forwarding header or IP is retained.
type failureBudget struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func (b *failureBudget) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last.IsZero() {
		b.tokens = 10
		b.last = now
	}
	b.tokens = min(10, b.tokens+now.Sub(b.last).Seconds()*2)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Buffer at most limit+1 before entering a finance handler: decoding the first
// JSON object must not commit a write before a chunked oversized tail is seen.
func limitFinancialBody(next http.Handler) http.Handler {
	slots := make(chan struct{}, 4)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/api/planner/") && r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				http.Error(w, "Too many simultaneous requests", http.StatusServiceUnavailable)
				return
			}
			if r.ContentLength > financialBodyLimit {
				http.Error(w, "Request body too large", 413)
				return
			}
			body, e := io.ReadAll(io.LimitReader(r.Body, financialBodyLimit+1))
			if e != nil {
				http.Error(w, "Cannot read request body", 400)
				return
			}
			if len(body) > financialBodyLimit {
				http.Error(w, "Request body too large", 413)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, r)
	})
}
