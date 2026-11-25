package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"budget/internal/storageguard"
)

const authFile = "web-auth.json"
const sessionCookie = "__Host-budget-session"

type webSession struct {
	ID      string    `json:"id"`
	Hash    string    `json:"hash"`
	Created time.Time `json:"created"`
	Active  time.Time `json:"active"`
	Expires time.Time `json:"expires"`
}
type authState struct {
	Version  int          `json:"version"`
	Password string       `json:"password_binding"`
	Sessions []webSession `json:"sessions"`
}
type webAuth struct {
	mu       sync.Mutex
	path     string
	cookie   string
	state    authState
	failures failureBudget
	now      func() time.Time
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func tokenHash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func newWebAuth(c serverConfig) (*webAuth, error) {
	a := &webAuth{path: filepath.Join(c.DataDir, authFile), now: time.Now, cookie: sessionCookie}
	if c.Instance == "acceptance" {
		a.cookie = "__Host-budget-stg-session"
	}
	a.state = authState{Version: 1, Password: tokenHash(c.InstallationID + "\x00" + c.Password), Sessions: []webSession{}}
	if _, err := os.Lstat(a.path); err == nil {
		if err = storageguard.Regular(a.path, true); err != nil {
			return nil, err
		}
		st, err := os.Stat(a.path)
		if err != nil {
			return nil, err
		}
		if st.Mode().Perm()&0077 != 0 {
			return nil, errors.New("web session file must be private")
		}
		var saved authState
		if err = readSmall(a.path, &saved, 256<<10); err != nil {
			return nil, err
		}
		if saved.Version != 1 || len(saved.Sessions) > 64 {
			return nil, errors.New("invalid web session state")
		}
		if saved.Password == a.state.Password {
			a.state = saved
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	a.prune()
	if err := a.save(); err != nil {
		return nil, err
	}
	return a, nil
}
func (a *webAuth) cookieName() string {
	if a.cookie == "" {
		return sessionCookie
	}
	return a.cookie
}
func (a *webAuth) save() error { return privateJSON(a.path, a.state) }
func (a *webAuth) prune() {
	now := a.now()
	out := a.state.Sessions[:0]
	for _, s := range a.state.Sessions {
		if now.Before(s.Expires) {
			out = append(out, s)
		}
	}
	a.state.Sessions = out
}
func (a *webAuth) session(r *http.Request) (webSession, bool) {
	c, e := r.Cookie(a.cookieName())
	if e != nil || len(c.Value) > 128 {
		return webSession{}, false
	}
	h := tokenHash(c.Value)
	a.mu.Lock()
	defer a.mu.Unlock()
	before := len(a.state.Sessions)
	a.prune()
	if len(a.state.Sessions) != before {
		if e := a.save(); e != nil {
			log.Printf("Session expiry persistence failed (%T)", e)
		}
	}
	for i, s := range a.state.Sessions {
		if s.Hash == h && a.now().Before(s.Expires) {
			if a.now().Sub(s.Active) > time.Minute {
				a.state.Sessions[i].Active = a.now()
				if e := a.save(); e != nil {
					log.Printf("Session activity persistence failed (%T)", e)
				}
			}
			return a.state.Sessions[i], true
		}
	}
	return webSession{}, false
}
func (a *webAuth) csrfToken(r *http.Request) string {
	c, e := r.Cookie(a.cookieName())
	if e != nil {
		return ""
	}
	return tokenHash("csrf\x00" + c.Value)
}
func authJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func authError(w http.ResponseWriter, status int, message string) {
	authJSON(w, status, map[string]string{"error": message})
}
func isWrite(r *http.Request) bool {
	return r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS"
}
func browserNavigation(r *http.Request) bool {
	return r.Method == "GET" && (r.URL.Path == "/" || r.URL.Path == "/index.html" || r.URL.Path == "/login.html") && r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
}
func loginAsset(p string) bool {
	return p == "/js/environment.js" || p == "/login.html" || p == "/js/login.js" || p == "/css/login.css"
}
func (a *webAuth) handler(c serverConfig, assets, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(c.Password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/environment" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"staging": c.Instance == "acceptance"})
			return
		}
		if loginAsset(r.URL.Path) && !isWrite(r) {
			assets.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/auth/login" {
			if r.Method != "POST" {
				authError(w, 405, "Метод не поддерживается")
				return
			}
			// Login CSRF is blocked even before a session exists. No Origin-less browser login.
			if r.Header.Get("Origin") == "" {
				authError(w, 403, "Требуется Origin")
				return
			}
			var in struct {
				Username string `json:"username"`
				Password string `json:"password"`
				Remember bool   `json:"remember"`
			}
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
			d.DisallowUnknownFields()
			if d.Decode(&in) != nil {
				authError(w, 400, "Некорректный запрос")
				return
			}
			actual := sha256.Sum256([]byte(in.Password))
			if in.Username != "budget" || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
				if !a.failures.allow(a.now()) {
					w.Header().Set("Retry-After", "1")
					authError(w, 429, "Слишком много попыток. Повторите позже.")
					return
				}
				authError(w, 401, "Неверный логин или пароль")
				return
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			a.prune()
			if len(a.state.Sessions) >= 64 {
				authError(w, 429, "Слишком много сессий. Завершите ненужные сессии.")
				return
			}
			token := randomToken()
			duration := 12 * time.Hour
			if in.Remember {
				duration = 30 * 24 * time.Hour
			}
			s := webSession{ID: randomToken(), Hash: tokenHash(token), Created: a.now(), Active: a.now(), Expires: a.now().Add(duration)}
			// A new login rotates the existing browser session, never adopts a supplied token.
			old := a.state.Sessions
			if ck, e := r.Cookie(a.cookieName()); e == nil {
				h := tokenHash(ck.Value)
				next := []webSession{}
				for _, x := range old {
					if x.Hash != h {
						next = append(next, x)
					}
				}
				a.state.Sessions = next
			}
			a.state.Sessions = append(a.state.Sessions, s)
			if e := a.save(); e != nil {
				a.state.Sessions = old
				authError(w, 500, "Не удалось сохранить сессию")
				return
			}
			cookie := &http.Cookie{Name: a.cookieName(), Value: token, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
			if in.Remember {
				cookie.MaxAge = int(duration.Seconds())
				cookie.Expires = s.Expires
			}
			http.SetCookie(w, cookie)
			authJSON(w, 200, map[string]string{"csrf": tokenHash("csrf\x00" + token)})
			return
		}
		s, ok := a.session(r)
		if c.Password == "" { // Isolated development/test mode only; production config requires a password.
			if strings.HasPrefix(r.URL.Path, "/api/auth/") {
				authJSON(w, 200, map[string]any{"enabled": false})
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !ok {
			// Machine requests explicitly opt in; browser cookies/fetch metadata cannot use this alternative.
			_, cookieErr := r.Cookie(a.cookieName())
			if r.Header.Get("X-Budget-Admin") == "1" && cookieErr != nil && r.Header.Get("Sec-Fetch-Mode") == "" && r.Header.Get("Sec-Fetch-Site") == "" && !strings.HasPrefix(r.URL.Path, "/api/auth/") && r.URL.Path != "/api/events" {
				user, password, basic := r.BasicAuth()
				actual := sha256.Sum256([]byte(password))
				if basic && user == "budget" && subtle.ConstantTimeCompare(actual[:], expected[:]) == 1 {
					next.ServeHTTP(w, r)
					return
				}
				if r.Header.Get("Authorization") != "" && !a.failures.allow(a.now()) {
					w.Header().Set("Retry-After", "1")
					authError(w, 429, "Слишком много попыток")
					return
				}
			}
			if browserNavigation(r) || ((r.URL.Path == "/" || r.URL.Path == "/index.html") && strings.Contains(r.Header.Get("Accept"), "text/html")) {
				http.Redirect(w, r, "/login.html", 303)
				return
			}
			authError(w, 401, "Требуется вход")
			return // Never WWW-Authenticate: no browser Basic dialog.
		}
		if isWrite(r) {
			csrf := a.csrfToken(r)
			if r.Header.Get("Origin") == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrf)) != 1 {
				authError(w, 403, "Проверка CSRF не пройдена")
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/auth/") {
			a.api(w, r, s)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *webAuth) api(w http.ResponseWriter, r *http.Request, current webSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prune()
	switch {
	case r.URL.Path == "/api/auth/session" && r.Method == "GET":
		authJSON(w, 200, map[string]any{"enabled": true, "id": current.ID, "csrf": a.csrfToken(r), "expires": current.Expires})
	case r.URL.Path == "/api/auth/sessions" && r.Method == "GET":
		rows := []map[string]any{}
		for _, s := range a.state.Sessions {
			rows = append(rows, map[string]any{"id": s.ID, "created": s.Created, "active": s.Active, "expires": s.Expires, "current": s.ID == current.ID})
		}
		authJSON(w, 200, rows)
	case r.Method == "POST" && (r.URL.Path == "/api/auth/logout" || r.URL.Path == "/api/auth/revoke"):
		var in struct {
			ID     string `json:"id"`
			Others bool   `json:"others"`
			All    bool   `json:"all"`
		}
		if r.URL.Path == "/api/auth/logout" {
			in.ID = current.ID
		} else if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
			authError(w, 400, "Некорректный запрос")
			return
		}
		old := a.state.Sessions
		out := []webSession{}
		for _, s := range old {
			if in.All || in.Others && s.ID != current.ID || s.ID == in.ID {
				continue
			}
			out = append(out, s)
		}
		a.state.Sessions = out
		if e := a.save(); e != nil {
			a.state.Sessions = old
			authError(w, 500, "Не удалось завершить сессии")
			return
		}
		if in.All || in.ID == current.ID {
			http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		}
		authJSON(w, 200, map[string]bool{"ok": true})
	default:
		authError(w, 404, "Не найдено")
	}
}

// Called only by stopped-service recovery, after path/identity/lease validation.
func invalidateWebSessions(dir string) error {
	p := filepath.Join(dir, authFile)
	if _, e := os.Lstat(p); errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e := storageguard.Regular(p, true); e != nil {
		return e
	}
	if e := os.Remove(p); e != nil {
		return e
	}
	return syncDir(dir)
}
