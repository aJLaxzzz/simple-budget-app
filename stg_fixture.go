package main

import (
	"budget/internal/planner"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"time"
)

// This command only creates a new, empty directory using the same init guard.
// It never reads an existing user's dataset or contacts an HTTP server.
func stagingFixture(args []string, out io.Writer) error {
	f := flag.NewFlagSet("stg-fixture", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	dir := f.String("data-dir", "", "")
	id := f.String("installation-id", "", "")
	confirm := f.Bool("confirm", false, "")
	if err := f.Parse(args); err != nil || f.NArg() != 0 || !*confirm {
		return errors.New("fixture requires directory, identity and confirmation")
	}
	if decoded, err := hex.DecodeString(*id); err != nil || len(decoded) != 16 {
		return errors.New("fixture requires explicit 32-hex installation identity")
	}
	if err := command([]string{"admin", "init", "--data-dir", *dir, "--installation-id", *id, "--confirm"}, io.Discard); err != nil {
		return err
	}
	store, err := planner.OpenMaintenance(context.Background(), filepath.Join(*dir, "planner.db"), *id, false)
	if err != nil {
		return err
	}
	defer store.Close()
	handler := planner.NewHandler(planner.NewService(store))
	call := func(method, path string, body any) (map[string]any, error) {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/planner/"+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code < 200 || w.Code >= 300 {
			return nil, fmt.Errorf("synthetic fixture %s %s: HTTP %d", method, path, w.Code)
		}
		var result map[string]any
		err := json.Unmarshal(w.Body.Bytes(), &result)
		return result, err
	}
	zone := time.FixedZone("Europe/Moscow", 3*3600)
	now := time.Now().In(zone)
	date := func(offset int) string { return now.AddDate(0, 0, offset).Format("2006-01-02") }
	var first map[string]any
	for i, title := range []string{"STG · Дом и повседневные дела", "STG · Работа — длинное название проекта для проверки мобильной карточки", "STG · Учёба и отдых"} {
		project, e := call("POST", "projects", map[string]any{"title": title, "description": "Синтетические данные для проверки интерфейса. Не настоящие планы."})
		if e != nil {
			return e
		}
		for j := 0; j < 7; j++ {
			body := map[string]any{"title": fmt.Sprintf("STG %d.%d · Проверить карточку и сохранение черновика", i+1, j+1), "project_id": project["id"], "description": "Длинное описание\nВторая строка и проверка прокрутки.", "planned_date": date(j - 2)}
			task, e := call("POST", "tasks", body)
			if e != nil {
				return e
			}
			if first == nil {
				first = task
			}
			if j == 0 {
				_, e = call("POST", "tasks", map[string]any{"title": "STG · Подзадача", "project_id": project["id"], "parent_task_id": task["id"], "planned_date": date(0)})
				if e != nil {
					return e
				}
			}
			if j == 6 {
				_, e = call("POST", "tasks/"+task["id"].(string)+"/complete", map[string]any{"expected_revision": task["revision"], "completed_date": date(-1)})
				if e != nil {
					return e
				}
			}
			if j == 5 {
				if _, e = call("POST", "tasks/"+task["id"].(string)+"/cancel", map[string]any{"expected_revision": task["revision"]}); e != nil {
					return e
				}
			}
		}
	}
	for _, title := range []string{"STG · Входящие без даты", "STG · Идея для будущего проекта"} {
		if _, err = call("POST", "tasks", map[string]any{"title": title}); err != nil {
			return err
		}
	}
	if _, err = call("POST", "sessions", map[string]any{"task_id": first["id"], "date": date(0), "start_time": "10:00", "end_time": "11:00"}); err != nil {
		return err
	}
	for i, title := range []string{"STG · Ежедневная привычка", "STG · Повторяющаяся задача"} {
		_, err = call("POST", "series", map[string]any{"request_id": fmt.Sprintf("stg-series-%d", i), "task": map[string]any{"title": title}, "first_date": date(-2), "rule": map[string]any{"schema_version": 1, "mode": "schedule", "unit": "days", "interval": i + 1, "allowed_days": map[string]any{"mode": "any"}, "adjustment": "none"}})
		if err != nil {
			return err
		}
	}
	snapshot := map[string]any{"title": "STG · Еженедельный обзор", "description": "Повторяющийся проект и шаблон", "tasks": []any{
		map[string]any{"key": "one", "title": "STG · Собрать заметки", "planned_offset": 0},
		map[string]any{"key": "two", "title": "STG · Подвести итоги", "planned_offset": 1},
	}}
	if _, err = call("POST", "templates", map[string]any{"request_id": "stg-template", "snapshot": snapshot}); err != nil {
		return err
	}
	draft := map[string]any{"request_id": "stg-project-repeat", "snapshot": snapshot, "anchor_date": date(0), "has_start": true, "project_mode": "sequential",
		"rule": map[string]any{"schema_version": 1, "mode": "schedule", "unit": "weeks", "interval": 1, "allowed_days": map[string]any{"mode": "any"}, "adjustment": "none"}}
	preview, err := call("POST", "project-drafts/preview", draft)
	if err != nil {
		return err
	}
	draft["preview_token"] = preview["preview_token"]
	if _, err = call("POST", "project-drafts/apply", draft); err != nil {
		return err
	}
	materialize := map[string]any{}
	finished := false
	for i := 0; i < 30; i++ {
		r, e := call("POST", "materialize", materialize)
		if e != nil {
			return e
		}
		if results, ok := r["results"].([]any); ok {
			for _, result := range results {
				if result.(map[string]any)["error"] != nil {
					return errors.New("synthetic series materialization failed")
				}
			}
		}
		if r["pending"] != true && r["next_after_series_id"] == nil {
			finished = true
			break
		}
		materialize = map[string]any{}
		if next, ok := r["next_after_series_id"].(string); ok && next != "" {
			materialize["after_series_id"] = next
		}
	}
	if !finished {
		return errors.New("synthetic materialization did not converge")
	}
	return json.NewEncoder(out).Encode(map[string]any{"result": "synthetic_fixture_created", "ordinary_projects": 3, "ordinary_tasks": 26, "series": 3, "templates": 1})
}
