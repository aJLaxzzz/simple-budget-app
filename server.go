package main

import (
	"budget/internal/planner"
	"budget/internal/storageguard"
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
	"strings"
	"syscall"
	"time"
)

func accessHandler(c serverConfig, next http.Handler) http.Handler {
	budget := &failureBudget{}
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
		// Forwarded headers never establish identity, origin or TLS trust.
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
			originHost := r.Host
			if c.ProxyMode == "loopback-https" {
				canonical, _ := url.Parse(c.PublicOrigin)
				scheme = "https"
				originHost = canonical.Host
			}
			if e != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, originHost) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				accessError(w, r, "Origin is not allowed", http.StatusForbidden)
				return
			}
		}
		if c.Password != "" {
			user, password, ok := r.BasicAuth()
			actual := sha256.Sum256([]byte(password))
			if !ok || user != "budget" || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
				if r.Header.Get("Authorization") != "" && !budget.allow(time.Now()) {
					w.Header().Set("Retry-After", "1")
					accessError(w, r, "Too many failed authentication attempts", http.StatusTooManyRequests)
					return
				}
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
	if _, e := storageguard.Check(c.DataDir, c.Environment, c.InstallationID); e != nil {
		return nil, e
	}
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
		mux.Handle("/api/ready", readyHandler(store))
		mux.Handle("/api/backups/", backupHandler(c))
		mux.Handle("/api/planner/", planner.NewHandler(planner.NewService(store)))
		mux.Handle("/", base)
		base = mux
	}
	return accessHandler(c, limitFinancialBody(base)), nil
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
	// This restriction applies to the web process, not to read-only admin
	// readiness probes running as the repository's administrative owner.
	if c.Environment == "production" && c.BackupDir != "" {
		uid, _, err := fileOwner(c.BackupDir)
		if err != nil {
			return err
		}
		if uid == os.Geteuid() {
			return errors.New("production backup repository must be owned by a different administrative user")
		}
	}
	lease, err := storageguard.ParentLease(c.DataDir, false)
	if err != nil {
		return err
	}
	defer lease.Close()
	if err = storageguard.CheckRecovery(c.DataDir); err != nil {
		return err
	}
	if _, e := storageguard.Check(c.DataDir, c.Environment, c.InstallationID); e != nil {
		return e
	}
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
	var plannerStore *planner.Store
	if c.Environment == "production" {
		plannerStore, e = planner.OpenProduction(ctx, filepath.Join(c.DataDir, "planner.db"), c.InstallationID)
	} else {
		plannerStore, e = planner.Open(ctx, filepath.Join(c.DataDir, "planner.db"))
	}
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
	log.Printf("Личный бюджет: %s listener ready (%s)", scheme, c.Environment)
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
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		if e := command(os.Args[1:], os.Stdout); e != nil {
			log.Print(e)
			os.Exit(1)
		}
		return
	}
	c, e := readConfig(os.Getenv)
	if e != nil {
		log.Fatal(e)
	}
	if c.Environment == "production" {
		time.Local, _ = time.LoadLocation("Europe/Moscow")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e = run(ctx, c); e != nil {
		if c.Environment == "production" {
			log.Printf("production startup/runtime failed (%T); use administrative checks while stopped", e)
			os.Exit(1)
		}
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
