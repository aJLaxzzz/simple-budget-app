package main

import (
	"budget/internal/planner"
	"budget/internal/storageguard"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"
)

type versionInfo struct {
	Application      string `json:"application"`
	Go               string `json:"go"`
	Revision         string `json:"revision"`
	Modified         bool   `json:"modified"`
	Schema           int    `json:"schema_version"`
	EmbeddedFrontend bool   `json:"embedded_frontend"`
}

func currentVersion() versionInfo {
	v := versionInfo{Application: "simple-budget-app", Go: runtime.Version(), Revision: "unknown", Schema: planner.SchemaVersion(), EmbeddedFrontend: true}
	if b, ok := debug.ReadBuildInfo(); ok {
		for _, s := range b.Settings {
			switch s.Key {
			case "vcs.revision":
				v.Revision = s.Value
			case "vcs.modified":
				v.Modified = s.Value == "true"
			}
		}
	}
	return v
}
func readyHandler(store *planner.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		if store == nil || store.Ready(r.Context()) != nil {
			http.Error(w, "Storage not ready", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "HEAD" {
			_ = json.NewEncoder(w).Encode(currentVersion())
		}
	})
}
func command(args []string, out io.Writer) error {
	if len(args) == 2 && args[0] == "admin" && args[1] == "check-credential" {
		dir := os.Getenv("CREDENTIALS_DIRECTORY")
		if dir == "" {
			return errors.New("credential check requires CREDENTIALS_DIRECTORY")
		}
		if _, err := readSystemdPasswordFile(filepath.Join(dir, "app-password")); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]string{"credential": "valid"})
	}
	if len(args) == 1 && args[0] == "version" {
		return json.NewEncoder(out).Encode(currentVersion())
	}
	if len(args) == 1 && args[0] == "ready" {
		c, e := readConfig(os.Getenv)
		if e != nil {
			return e
		}
		if c.CertFile != "" {
			return errors.New("ready CLI requires loopback HTTP; use authenticated HTTPS readiness for native TLS")
		}
		req, e := http.NewRequest("GET", "http://"+c.Addr+"/api/ready", nil)
		if e != nil {
			return e
		}
		for h := range c.AllowedHosts {
			req.Host = h
			break
		}
		if c.Password != "" {
			req.SetBasicAuth("budget", c.Password)
		}
		client := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, e := client.Do(req)
		if e != nil {
			return errors.New("readiness connection failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("not ready: HTTP %d", resp.StatusCode)
		}
		var v versionInfo
		if e = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&v); e != nil {
			return errors.New("invalid readiness response")
		}
		own := currentVersion()
		if v.Application != own.Application || v.Schema != own.Schema || v.Revision != own.Revision || v.Modified != own.Modified || v.Go != own.Go || !v.EmbeddedFrontend {
			return errors.New("readiness schema/application mismatch")
		}
		return json.NewEncoder(out).Encode(v)
	}
	if len(args) < 2 || args[0] != "admin" {
		return errors.New("usage: budget [version|ready|admin init|admin check|admin check-credential|admin migrate|admin backup]")
	}
	if args[1] == "legacy-export" {
		return legacyExportCommand(args[2:], out)
	}
	if args[1] == "backup" {
		return backupCommand(args[2:], out)
	}
	action := args[1]
	if action != "init" && action != "check" && action != "migrate" {
		return errors.New("unknown administrative command")
	}
	f := flag.NewFlagSet("admin "+action, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	dir := f.String("data-dir", "", "absolute data directory")
	id := f.String("installation-id", "", "expected installation ID")
	environment := f.String("environment", "production", "production or test (init only)")
	confirm := f.Bool("confirm", false, "explicit initialization/migration authorization")
	if e := f.Parse(args[2:]); e != nil || f.NArg() != 0 {
		return errors.New("invalid administrative arguments")
	}
	if !filepath.IsAbs(*dir) {
		return errors.New("administration requires absolute --data-dir")
	}
	if action == "init" {
		if !*confirm {
			return errors.New("initialization requires --confirm and an empty private directory")
		}
		if *environment != "production" && *environment != "test" {
			return errors.New("invalid initialization environment")
		}
		real, e := storageguard.Check(*dir, "development", "")
		if e != nil {
			return e
		}
		info, e := os.Stat(real)
		if e != nil || info.Mode().Perm()&0077 != 0 {
			return errors.New("initialization requires owner-only directory permissions")
		}
		entries, e := os.ReadDir(real)
		if e != nil || len(entries) != 0 {
			return errors.New("initialization requires an empty directory; existing data are never adopted automatically")
		}
		if *environment == "test" {
			if e = storageguard.TestPath(real); e != nil {
				return e
			}
		}
		if *id == "" {
			b := make([]byte, 16)
			if _, e = rand.Read(b); e != nil {
				return e
			}
			*id = hex.EncodeToString(b)
		}
		if len(*id) != 32 {
			return errors.New("installation ID must be 32 hex characters")
		}
		if _, e = hex.DecodeString(*id); e != nil {
			return errors.New("installation ID must be 32 hex characters")
		}
		s, e := planner.Open(context.Background(), filepath.Join(real, "planner.db"))
		if e != nil {
			return errors.New("initial database creation failed")
		}
		if e = s.Close(); e != nil {
			return e
		}
		if e = atomicWriteFile(filepath.Join(real, "data.json"), []byte("{\"months\":[]}\n")); e != nil {
			return e
		}
		if e = atomicWriteFile(filepath.Join(real, "categories.json"), []byte("{\"expense\":[],\"income\":[]}\n")); e != nil {
			return e
		}
		m := storageguard.Marker{Version: 1, Environment: *environment, ID: *id}
		if e = storageguard.WriteMarker(real, m); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(m)
	}
	if *environment != "production" || *id == "" {
		return errors.New("maintenance requires production installation identity")
	}
	if action == "migrate" && !*confirm {
		return errors.New("migration requires --confirm; create and verify a backup before authorizing it")
	}
	installationLock, e := installationAdminLock(*dir)
	if e != nil {
		return e
	}
	defer installationLock.Close()
	lease, e := storageguard.ParentLease(*dir, true)
	if e != nil {
		return e
	}
	defer lease.Close()
	if e = storageguard.CheckRecovery(*dir); e != nil {
		return e
	}
	s, e := planner.OpenMaintenance(context.Background(), filepath.Join(*dir, "planner.db"), *id, action == "migrate")
	if e != nil {
		return fmt.Errorf("maintenance refused: %w", e)
	}
	defer s.Close()
	return json.NewEncoder(out).Encode(currentVersion())
}
