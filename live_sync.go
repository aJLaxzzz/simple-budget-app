package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type syncEvent struct {
	Generation string `json:"generation"`
	Revision   uint64 `json:"revision"`
	Scope      string `json:"scope"`
}
type eventHub struct {
	mu              sync.Mutex
	generation      string
	financeRevision uint64 // guarded by financialMu, unique within this random process generation
	revision        uint64
	clients         map[chan syncEvent]bool
}

func newEventHub() *eventHub {
	return &eventHub{generation: randomToken(), clients: make(map[chan syncEvent]bool)}
}
func (h *eventHub) publish(scope string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revision++
	event := syncEvent{h.generation, h.revision, scope}
	for ch, active := range h.clients {
		if !active {
			continue
		}
		select {
		case ch <- event:
		default:
			h.clients[ch] = false
			close(ch)
		}
	}
}
func (h *eventHub) subscribe() (chan syncEvent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= 32 {
		return nil, false
	}
	ch := make(chan syncEvent, 8)
	h.clients[ch] = true
	ch <- syncEvent{h.generation, h.revision, "all"}
	return ch, true
}
func (h *eventHub) unsubscribe(ch chan syncEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if active, exists := h.clients[ch]; exists {
		delete(h.clients, ch)
		if active {
			close(ch)
		}
	}
}

type syncContextKey struct{}
type serverShutdownKey struct{}

func withSync(h *eventHub, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Snapshot before the handler reads storage, never after it. This is a
		// conservative lower bound for the data returned by a successful GET.
		if r.Method == http.MethodGet {
			h.mu.Lock()
			w.Header().Set("X-Budget-Sync-Generation", h.generation)
			w.Header().Set("X-Budget-Sync-Revision", strconv.FormatUint(h.revision, 10))
			h.mu.Unlock()
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), syncContextKey{}, h)))
	})
}
func (h *eventHub) handler(a *webAuth, authentication bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		ch, ok := h.subscribe()
		if !ok {
			http.Error(w, "Too many streams", 503)
			return
		}
		defer h.unsubscribe(ch)
		shutdown, _ := r.Context().Value(serverShutdownKey{}).(<-chan struct{})
		rc := http.NewResponseController(w)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		beats := 0
		send := func(event string, data any) bool {
			if e := rc.SetWriteDeadline(time.Now().Add(10 * time.Second)); e != nil {
				return false
			}
			// The idle heartbeat interval must not inherit an already-expired write deadline.
			defer rc.SetWriteDeadline(time.Time{})
			b, _ := json.Marshal(data)
			if _, e := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); e != nil {
				return false
			}
			return rc.Flush() == nil
		}
		for {
			select {
			case <-shutdown:
				return
			case <-r.Context().Done():
				return
			case event, open := <-ch:
				if !open {
					return
				}
				if authentication {
					if _, ok := a.session(r); !ok {
						send("auth-required", struct{}{})
						return
					}
				}
				if !send("change", event) {
					return
				}
			case <-ticker.C:
				if authentication {
					if _, ok := a.session(r); !ok {
						send("auth-required", struct{}{})
						return
					}
				}
				beats++
				if beats%5 == 0 {
					if !send("heartbeat", struct{}{}) {
						return
					}
				}
			}
		}
	})
}
