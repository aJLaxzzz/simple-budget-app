package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type downloadWriter struct {
	http.ResponseWriter
	end time.Time
}

func (w downloadWriter) Write(b []byte) (int, error) {
	if time.Now().After(w.end) {
		return 0, context.DeadlineExceeded
	}
	deadline := time.Now().Add(90 * time.Second)
	if deadline.After(w.end) {
		deadline = w.end
	}
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
	return w.ResponseWriter.Write(b)
}
func backupHandler(c serverConfig) http.Handler {
	slots := make(chan struct{}, 2)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if req.Method != "GET" && req.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", 405)
			return
		}
		statusPath := req.URL.Path == "/api/backups/status"
		if c.BackupDir == "" {
			if !statusPath {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if req.Method != "HEAD" {
				_ = json.NewEncoder(w).Encode(backupStatus{Automatic: "not_installed", Stale: true, Warning: "Резервное копирование ещё не настроено"})
			}
			return
		}
		r, e := openRepository(c.BackupDir, c.InstallationID, c.Environment, false)
		if e != nil {
			http.Error(w, "Backup repository unavailable", 503)
			return
		}
		if statusPath {
			s, e := r.status(time.Now())
			if e != nil {
				http.Error(w, "Backup status unavailable", 503)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if req.Method != "HEAD" {
				_ = json.NewEncoder(w).Encode(s)
			}
			return
		}
		parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/api/backups/"), "/")
		if len(parts) != 2 || !backupIDPattern.MatchString(parts[0]) || (parts[1] != "full" && parts[1] != "planner") {
			http.NotFound(w, req)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "5")
			http.Error(w, "Two downloads already in progress", 429)
			return
		}
		receipt, p, e := r.find(parts[0])
		if e != nil {
			http.NotFound(w, req)
			return
		}
		f, e := safeOpen(p)
		if e != nil {
			http.Error(w, "Backup unavailable", 503)
			return
		}
		defer f.Close()
		// Check and serve the SAME descriptor. Rotation may unlink it; contents remain readable.
		ctx, cancel := context.WithTimeout(req.Context(), 20*time.Minute)
		defer cancel()
		end := time.Now().Add(20 * time.Minute)
		_ = http.NewResponseController(w).SetWriteDeadline(end)
		defer func() { _ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second)) }()
		hash := sha256.New()
		n, e := io.Copy(hash, contextReader{ctx, f})
		if e != nil || n != receipt.Size || hex.EncodeToString(hash.Sum(nil)) != receipt.SHA256 {
			http.Error(w, "Backup verification failed", 503)
			return
		}
		if _, e = f.Seek(0, io.SeekStart); e != nil {
			http.Error(w, "Backup unavailable", 503)
			return
		}
		writer := downloadWriter{w, end}
		if parts[1] == "full" {
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="budget-%s.tar.gz"`, receipt.Manifest.ID))
			w.Header().Set("ETag", `"`+receipt.SHA256+`"`)
			http.ServeContent(writer, req, "backup.tar.gz", receipt.Manifest.Created, f)
			return
		}
		if req.Header.Get("Range") != "" {
			http.Error(w, "Range is only supported for full archives", 416)
			return
		}
		gz, e := gzip.NewReader(contextReader{ctx, f})
		if e != nil {
			http.Error(w, "Backup unavailable", 503)
			return
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		h, e := tr.Next()
		if e != nil || h.Name != "manifest.json" || h.Size > 64<<10 {
			http.Error(w, "Invalid backup", 503)
			return
		}
		h, e = tr.Next()
		if e != nil || h.Name != "planner.db" || h.Size != receipt.Manifest.Files[0].Size || h.Typeflag != tar.TypeReg {
			http.Error(w, "Invalid planner snapshot", 503)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.sqlite3")
		w.Header().Set("Content-Length", strconv.FormatInt(h.Size, 10))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="planner-%s.db"`, receipt.Manifest.ID))
		if req.Method == "HEAD" {
			return
		}
		_, _ = io.Copy(writer, contextReader{ctx, tr})
	})
}
