package main

import (
	"bytes"
	"embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Explicit manifest: files accidentally placed in static are never published.
// Assets are read once at startup, so later symlink swaps cannot expose data.
var publicAssets = map[string]string{
	"/favicon.ico":                       "image/x-icon",
	"/favicon.svg":                       "image/svg+xml",
	"/favicon-16.png":                    "image/png",
	"/favicon-32.png":                    "image/png",
	"/js/view-sync.js":                   "text/javascript; charset=utf-8",
	"/js/environment.js":                 "text/javascript; charset=utf-8",
	"/js/live.js":                        "text/javascript; charset=utf-8",
	"/login.html":                        "text/html; charset=utf-8",
	"/js/login.js":                       "text/javascript; charset=utf-8",
	"/css/login.css":                     "text/css; charset=utf-8",
	"/js/planner/repeat-editor.js":       "text/javascript; charset=utf-8",
	"/js/planner/beta3.js":               "text/javascript; charset=utf-8",
	"/js/planner/beta2.js":               "text/javascript; charset=utf-8",
	"/js/planner/icon-catalog.js":        "text/javascript; charset=utf-8",
	"/js/planner/calendar-days.js":       "text/javascript; charset=utf-8",
	"/js/planner/entity-templates.js":    "text/javascript; charset=utf-8",
	"/js/planner/appearance.js":          "text/javascript; charset=utf-8",
	"/js/planner/task-groups.js":         "text/javascript; charset=utf-8",
	"/js/planner/actions.js":             "text/javascript; charset=utf-8",
	"/js/planner/lifecycle.js":           "text/javascript; charset=utf-8",
	"/js/planner/disclosure.js":          "text/javascript; charset=utf-8",
	"/js/planner/navigation.js":          "text/javascript; charset=utf-8",
	"/vendor/chart.umd-4.4.0.js":         "text/javascript; charset=utf-8",
	"/js/planner/production-calendar.js": "text/javascript; charset=utf-8",
	"/js/planner/attention.js":           "text/javascript; charset=utf-8",
	"/js/planner/templates.js":           "text/javascript; charset=utf-8",
	"/js/planner/calendar-model.js":      "text/javascript; charset=utf-8",
	"/js/planner/calendar.js":            "text/javascript; charset=utf-8",
	"/css/calendar.css":                  "text/css; charset=utf-8",
	"/css/planner.css":                   "text/css; charset=utf-8",
	"/js/planner/core.js":                "text/javascript; charset=utf-8",
	"/js/planner/views.js":               "text/javascript; charset=utf-8",
	"/js/planner/editor.js":              "text/javascript; charset=utf-8",
	"/index.html":                        "text/html; charset=utf-8",
	"/css/style.css":                     "text/css; charset=utf-8",
	"/js/app.js":                         "text/javascript; charset=utf-8",
	"/js/api.js":                         "text/javascript; charset=utf-8",
	"/js/planner/recurrence-form.js":     "text/javascript; charset=utf-8",
	"/js/planner/series.js":              "text/javascript; charset=utf-8",
	"/js/planner/occurrences.js":         "text/javascript; charset=utf-8",
}

//go:embed static/js/planner/repeat-editor.js static/js/planner/beta3.js static/js/planner/beta2.js
//go:embed static/js/planner/icon-catalog.js static/js/planner/calendar-days.js static/js/planner/entity-templates.js
//go:embed static/js/planner/appearance.js static/js/planner/task-groups.js static/js/planner/actions.js
//go:embed static/js/planner/lifecycle.js static/js/planner/disclosure.js static/js/planner/navigation.js
//go:embed static/vendor/chart.umd-4.4.0.js static/js/planner/production-calendar.js static/js/planner/attention.js
//go:embed static/js/planner/templates.js static/js/planner/calendar-model.js static/js/planner/calendar.js
//go:embed static/css/calendar.css static/css/planner.css static/js/planner/core.js
//go:embed static/js/planner/views.js static/js/planner/editor.js static/index.html
//go:embed static/css/style.css static/js/app.js static/js/api.js
//go:embed static/js/planner/recurrence-form.js static/js/planner/series.js static/js/planner/occurrences.js
//go:embed static/login.html static/js/login.js static/css/login.css
//go:embed static/favicon.svg static/favicon.ico static/favicon-16.png static/favicon-32.png
//go:embed static/js/view-sync.js
//go:embed static/js/live.js static/js/environment.js
var embeddedFrontend embed.FS

func newStaticHandler(dir string) (http.Handler, error) {
	assets := map[string][]byte{}
	if dir == "" {
		for path := range publicAssets {
			b, e := embeddedFrontend.ReadFile("static" + path)
			if e != nil {
				return nil, e
			}
			assets[path] = b
		}
	} else {
		dir = filepath.Clean(dir)
		info, e := os.Lstat(dir)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("static directory missing or invalid: %s", dir)
		}
		for path := range publicAssets {
			file := filepath.Join(dir, filepath.FromSlash(path[1:]))
			// Reject symlinks in every asset component.
			for p := file; p != dir; p = filepath.Dir(p) {
				info, e := os.Lstat(p)
				if e != nil || info.Mode()&os.ModeSymlink != 0 {
					return nil, fmt.Errorf("static asset missing or symlink: %s", path)
				}
			}
			info, e := os.Stat(file)
			if e != nil || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("invalid static asset: %s", path)
			}
			b, e := os.ReadFile(file)
			if e != nil {
				return nil, fmt.Errorf("read static asset %s: %w", path, e)
			}
			assets[path] = b
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		data, ok := assets[path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", publicAssets[path])
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(data))
	}), nil
}
