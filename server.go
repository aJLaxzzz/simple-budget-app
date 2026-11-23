package main

import (
	"budget/internal/planner"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type serverConfig struct {
	Addr, DataDir, StaticDir, Password, CertFile, KeyFile string
	AllowedHosts                                          map[string]bool
	Local                                                 bool
}

func readConfig(getenv func(string) string) (serverConfig, error) {
	c := serverConfig{Addr: getenv("BUDGET_ADDR"), DataDir: getenv("BUDGET_DATA_DIR"), StaticDir: getenv("BUDGET_STATIC_DIR"), Password: getenv("BUDGET_PASSWORD"), CertFile: getenv("BUDGET_TLS_CERT"), KeyFile: getenv("BUDGET_TLS_KEY")}
	if c.Addr == "" {
		c.Addr = "127.0.0.1:3001"
	}
	host, port, e := net.SplitHostPort(c.Addr)
	if e != nil {
		return c, fmt.Errorf("BUDGET_ADDR: expected IP:port: %w", e)
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return c, errors.New("BUDGET_ADDR: port must be 1..65535")
	}
	if host == "localhost" {
		host = "127.0.0.1"
		c.Addr = net.JoinHostPort(host, port)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return c, errors.New("BUDGET_ADDR: use a literal IP address")
	}
	c.Local = ip.IsLoopback()
	if c.Password != "" && len(c.Password) < 32 {
		return c, errors.New("BUDGET_PASSWORD: use at least 32 characters")
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return c, errors.New("both BUDGET_TLS_CERT and BUDGET_TLS_KEY are required")
	}
	if !c.Local && (c.Password == "" || c.CertFile == "") {
		return c, errors.New("external binding requires BUDGET_PASSWORD and TLS certificate/key")
	}
	c.AllowedHosts = map[string]bool{}
	if raw := getenv("BUDGET_ALLOWED_HOSTS"); raw != "" {
		for _, h := range strings.Split(raw, ",") {
			h = strings.ToLower(strings.TrimSpace(h))
			u, e := url.Parse("https://" + h)
			if e != nil || u.Host != h || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" || strings.ContainsAny(h, "*\\ \t\r\n") {
				return c, errors.New("BUDGET_ALLOWED_HOSTS: expected exact host[:port], no wildcards")
			}
			c.AllowedHosts[h] = true
		}
	} else {
		if ip.IsUnspecified() {
			return c, errors.New("wildcard binding requires BUDGET_ALLOWED_HOSTS")
		}
		c.AllowedHosts[strings.ToLower(c.Addr)] = true
		if c.Local {
			c.AllowedHosts[net.JoinHostPort("localhost", port)] = true
		}
	}
	if c.DataDir == "" {
		c.DataDir = "."
	}
	if c.StaticDir == "" {
		c.StaticDir = "static"
	}
	c.DataDir, e = filepath.Abs(c.DataDir)
	if e != nil {
		return c, e
	}
	c.StaticDir, e = filepath.Abs(c.StaticDir)
	if e != nil {
		return c, e
	}
	info, e := os.Stat(c.DataDir)
	if e != nil || !info.IsDir() {
		return c, fmt.Errorf("BUDGET_DATA_DIR must be an existing directory: %s", c.DataDir)
	}
	return c, nil
}

func accessHandler(c serverConfig, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(c.Password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Budget-App", "simple-budget-app")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if !c.AllowedHosts[strings.ToLower(r.Host)] {
			accessError(w, r, "Host is not allowed", http.StatusForbidden)
			return
		}
		peer, _, e := net.SplitHostPort(r.RemoteAddr)
		if c.Local && (e != nil || !net.ParseIP(peer).IsLoopback()) {
			accessError(w, r, "Local access only", http.StatusForbidden)
			return
		}
		// Forwarded headers are deliberately not trusted. TLS terminates here.
		if c.CertFile != "" && r.TLS == nil {
			accessError(w, r, "HTTPS required", http.StatusForbidden)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			accessError(w, r, "Cross-site access denied", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if e != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, r.Host) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				accessError(w, r, "Origin is not allowed", http.StatusForbidden)
				return
			}
		}
		if c.Password != "" {
			user, password, ok := r.BasicAuth()
			actual := sha256.Sum256([]byte(password))
			if !ok || user != "budget" || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Local budget", charset="UTF-8"`)
				accessError(w, r, "Authentication required", http.StatusUnauthorized)
				return
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			media := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
			if media != "application/json" {
				accessError(w, r, "application/json required", http.StatusUnsupportedMediaType)
				return
			}
		}
		if unsafePath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unsafePath(path string) bool {
	if strings.ContainsAny(path, "\\\x00%") {
		return true
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}

func prepareServer(c serverConfig) (http.Handler, error) {
	return prepareApplication(c, nil)
}

func prepareApplication(c serverConfig, store *planner.Store) (http.Handler, error) {
	assets, e := newStaticHandler(c.StaticDir)
	if e != nil {
		return nil, e
	}
	// Startup only: production has a single store and one serving process.
	dataFile, categoriesFile = filepath.Join(c.DataDir, "data.json"), filepath.Join(c.DataDir, "categories.json")
	if e = loadCategories(); e != nil {
		return nil, e
	}
	if e = loadData(); e != nil {
		return nil, e
	}
	base := newFinancialHandler(assets)
	if store != nil {
		mux := http.NewServeMux()
		mux.Handle("/api/planner/", planner.NewHandler(planner.NewService(store)))
		mux.Handle("/", base)
		base = mux
	}
	return accessHandler(c, base), nil
}

func serveUntilCancelled(ctx context.Context, listener net.Listener, handler http.Handler) error {
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	select {
	case e := <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	case <-ctx.Done():
		stop, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		if e := srv.Shutdown(stop); e != nil {
			_ = srv.Close()
			return fmt.Errorf("graceful shutdown: %w", e)
		}
		e := <-done
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	}
}

func run(ctx context.Context, c serverConfig) error {
	var tlsConfig *tls.Config
	if c.CertFile != "" {
		pair, e := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if e != nil {
			return fmt.Errorf("TLS configuration: %w", e)
		}
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	}
	// Acquire the port before loading the global store; a second launch fails early.
	listener, e := net.Listen("tcp", c.Addr)
	if e != nil {
		return e
	}
	defer listener.Close()
	if _, e := newStaticHandler(c.StaticDir); e != nil {
		return e
	}
	plannerStore, e := planner.Open(ctx, filepath.Join(c.DataDir, "planner.db"))
	if e != nil {
		return fmt.Errorf("planner storage: %w", e)
	}
	defer plannerStore.Close()
	handler, e := prepareApplication(c, plannerStore)
	if e != nil {
		return e
	}
	scheme := "http"
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
		scheme = "https"
	}
	log.Printf("Личный бюджет: %s://%s (данные: %s)", scheme, c.Addr, c.DataDir)
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		planner.NewService(plannerStore).RunMaterializer(workerCtx, time.Minute)
	}()
	serveErr := serveUntilCancelled(ctx, listener, handler)
	stopWorker()
	<-workerDone
	return errors.Join(serveErr, plannerStore.Close())
}

func main() {
	c, e := readConfig(os.Getenv)
	if e != nil {
		log.Fatal(e)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e = run(ctx, c); e != nil {
		log.Fatal(e)
	}
}

func accessError(w http.ResponseWriter, r *http.Request, message string, status int) {
	if strings.HasPrefix(r.URL.Path, "/api/planner/") {
		planner.WriteAPIError(w, &planner.APIError{Status: status, Code: "access_denied", Message: message})
		return
	}
	http.Error(w, message, status)
}
