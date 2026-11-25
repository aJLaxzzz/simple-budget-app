package main

import (
	"budget/internal/storageguard"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The current application has one in-memory financial store. All API access,
// including backups, must go through financialHandler.
var financialMu sync.Mutex
var writeJSONFile = atomicWriteFile

// Reject unsupported fields rather than silently dropping them on the next save.
// Missing legacy optional fields remain valid.
func decodeStoredJSON(raw []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected content after JSON")
	}
	return nil
}

func loadData() error {
	if _, e := os.Lstat(dataFile); e == nil {
		if e = storageguard.Regular(dataFile, false); e != nil {
			return e
		}
	}
	if flag.Lookup("test.v") != nil {
		if e := storageguard.TestPath(filepath.Dir(dataFile)); e != nil {
			return e
		}
	}
	raw, err := os.ReadFile(dataFile)
	if errors.Is(err, os.ErrNotExist) {
		// Do not create or rewrite files during loading. The first successful edit
		// persists a new store. A broken symlink is not a missing data file.
		if _, e := os.Lstat(dataFile); !errors.Is(e, os.ErrNotExist) {
			return fmt.Errorf("read %s: %w", dataFile, err)
		}
		store = dataStore{Months: []Month{}}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", dataFile, err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil || shape == nil || shape["months"] == nil {
		return fmt.Errorf("invalid %s: expected object with months", dataFile)
	}
	var next dataStore
	if err := decodeStoredJSON(raw, &next); err != nil {
		return fmt.Errorf("decode %s: %w", dataFile, err)
	}
	if next.Months == nil {
		next.Months = []Month{}
	}
	for i := range next.Months {
		if next.Months[i].Expenses == nil {
			next.Months[i].Expenses = []Expense{}
		}
		if next.Months[i].Incomes == nil {
			next.Months[i].Incomes = []IncomeEntry{}
		}
	}
	store = next
	return nil
}

func loadCategories() error {
	if _, e := os.Lstat(categoriesFile); e == nil {
		if e = storageguard.Regular(categoriesFile, false); e != nil {
			return e
		}
	}
	if flag.Lookup("test.v") != nil {
		if e := storageguard.TestPath(filepath.Dir(categoriesFile)); e != nil {
			return e
		}
	}
	raw, err := os.ReadFile(categoriesFile)
	if errors.Is(err, os.ErrNotExist) {
		if _, e := os.Lstat(categoriesFile); !errors.Is(e, os.ErrNotExist) {
			return fmt.Errorf("read %s: %w", categoriesFile, err)
		}
		expenseCategories, incomeCategories = defaultExpenseCategories(), defaultIncomeCategories()
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", categoriesFile, err)
	}
	var next CategoriesResponse
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := decodeStoredJSON(raw, &next.Expense); err != nil {
			return fmt.Errorf("decode %s: %w", categoriesFile, err)
		}
	} else {
		var shape map[string]json.RawMessage
		if err := json.Unmarshal(raw, &shape); err != nil || shape == nil || (shape["expense"] == nil && shape["income"] == nil) {
			return fmt.Errorf("invalid %s: expected categories", categoriesFile)
		}
		if err := decodeStoredJSON(raw, &next); err != nil {
			return fmt.Errorf("decode %s: %w", categoriesFile, err)
		}
	}
	if len(next.Expense) == 0 {
		next.Expense = defaultExpenseCategories()
	}
	if len(next.Income) == 0 {
		next.Income = defaultIncomeCategories()
	}
	expenseCategories, incomeCategories = next.Expense, next.Income
	return nil
}

func saveData() error { return saveJSON(dataFile, store) }
func saveCategories() error {
	return saveJSON(categoriesFile, CategoriesResponse{Expense: expenseCategories, Income: incomeCategories})
}
func saveJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return writeJSONFile(path, b)
}

// Rename is the commit point: every returned error leaves the destination intact.
// The temporary file lives on the same filesystem as the destination.
func atomicWriteFile(path string, data []byte) error {
	return atomicReplaceFile(path, func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}

func atomicReplaceFile(path string, write func(*os.File) error) error {
	if flag.Lookup("test.v") != nil {
		if e := storageguard.TestPath(filepath.Dir(path)); e != nil {
			return e
		}
	}
	mode := os.FileMode(0600)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular file %s", path)
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if err = write(f); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	// A directory-sync failure occurs after commit. Do not report a rollback:
	// the file has already changed. Log the reduced power-loss durability.
	if err = dir.Sync(); err != nil {
		log.Printf("WARNING: financial write committed, directory sync failed (%T)", err)
	}
	return nil
}

func reportSaveError(w http.ResponseWriter, err error) {
	log.Printf("Saving financial data failed (%T)", err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "Не удалось сохранить данные. Изменения не применены."})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func cloneCategories(src []Category) []Category {
	if src == nil {
		return nil
	}
	dst := append([]Category{}, src...)
	for i := range dst {
		if src[i].Subcategories != nil {
			dst[i].Subcategories = append([]string{}, src[i].Subcategories...)
		}
	}
	return dst
}
func cloneData(src dataStore) dataStore {
	dst := dataStore{}
	if src.Months == nil {
		return dst
	}
	dst.Months = append([]Month{}, src.Months...)
	for i := range dst.Months {
		if src.Months[i].Expenses != nil {
			dst.Months[i].Expenses = append([]Expense{}, src.Months[i].Expenses...)
		}
		if src.Months[i].Incomes != nil {
			dst.Months[i].Incomes = append([]IncomeEntry{}, src.Months[i].Incomes...)
		}
	}
	return dst
}

func financialHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		financialMu.Lock()
		defer financialMu.Unlock()
		hub, _ := r.Context().Value(syncContextKey{}).(*eventHub)
		revision := func() string {
			return fmt.Sprintf("\"%s:%d\"", hub.generation, hub.financeRevision)
		}
		if hub != nil {
			w.Header().Set("X-Budget-Revision", revision())
			if isWrite(r) && (r.URL.Path == "/api/categories" || strings.HasPrefix(r.URL.Path, "/api/months")) && r.Header.Get("If-Match") != revision() {
				authError(w, 409, "Данные изменились на другом устройстве. Обновите данные и проверьте изменения перед повторным сохранением.")
				return
			}
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		previous := cloneData(store)
		expense, income := cloneCategories(expenseCategories), cloneCategories(incomeCategories)
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if sw.status >= 400 {
				store, expenseCategories, incomeCategories = previous, expense, income
			}
		}()
		if hub == nil {
			next.ServeHTTP(sw, r)
			return
		}
		// Mutation responses are small; delay headers until the persisted revision is known.
		capture := &mutationResponse{header: w.Header(), status: 200}
		next.ServeHTTP(capture, r)
		sw.status = capture.status
		if capture.status < 400 {
			hub.financeRevision++
			w.Header().Set("X-Budget-Revision", revision())
			hub.publish("finance")
		}
		w.WriteHeader(capture.status)
		_, _ = w.Write(capture.body.Bytes())
	})
}

type mutationResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *mutationResponse) Header() http.Header         { return w.header }
func (w *mutationResponse) WriteHeader(status int)      { w.status = status }
func (w *mutationResponse) Write(p []byte) (int, error) { return w.body.Write(p) }
