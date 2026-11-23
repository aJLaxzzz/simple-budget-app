package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func putFile(t *testing.T, path, raw string) {
	t.Helper()
	if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestMissingFilesAreInitializedOnlyOnEdit(t *testing.T) {
	h := testHandler(t)
	if e := os.Remove(dataFile); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(categoriesFile); e != nil {
		t.Fatal(e)
	}
	if e := loadData(); e != nil {
		t.Fatal(e)
	}
	if e := loadCategories(); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{dataFile, categoriesFile} {
		if _, e := os.Stat(path); !errors.Is(e, os.ErrNotExist) {
			t.Fatalf("load wrote %s", path)
		}
	}
	if len(store.Months) != 0 || len(expenseCategories) == 0 {
		t.Fatal("missing defaults")
	}
	request(t, h, "POST", "/api/months", `{"month":11,"year":2026}`, 201)
	if !json.Valid(readFile(t, dataFile)) {
		t.Fatal("invalid first save")
	}
	request(t, h, "PUT", "/api/categories", `{"expense":[],"income":[]}`, 200)
	if !json.Valid(readFile(t, categoriesFile)) {
		t.Fatal("invalid categories save")
	}
}

func TestInvalidFilesNeverReplaceMemoryOrDisk(t *testing.T) {
	testHandler(t)
	cases := []struct {
		name   string
		path   *string
		load   func() error
		inputs []string
	}{
		{"data", &dataFile, loadData, []string{"", `null`, `{}`, `[]`, `{"months":`, `{"months":"bad"}`, `{"months":[]} {}`, `{"months":[],"future":true}`, `{"months":[{"income":"bad"}]}`}},
		{"categories", &categoriesFile, loadCategories, []string{"", `null`, `{}`, `{"expense":`, `{"expense":"bad"}`, `{"expense":[],"income":[]} []`, `{"expense":[],"future":true}`}},
	}
	for _, c := range cases {
		for i, raw := range c.inputs {
			t.Run(fmt.Sprintf("%s-%d", c.name, i), func(t *testing.T) {
				before, exp, inc := cloneData(store), cloneCategories(expenseCategories), cloneCategories(incomeCategories)
				putFile(t, *c.path, raw)
				if e := c.load(); e == nil {
					t.Fatal("accepted invalid file")
				}
				if string(readFile(t, *c.path)) != raw {
					t.Fatal("invalid file overwritten")
				}
				if !reflect.DeepEqual(before, store) || !reflect.DeepEqual(exp, expenseCategories) || !reflect.DeepEqual(inc, incomeCategories) {
					t.Fatal("memory changed on load error")
				}
			})
		}
	}
}

func TestUnavailableFilesAndBrokenLinks(t *testing.T) {
	testHandler(t)
	for _, kind := range []string{"directory", "symlink", "permission"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "unavailable.json")
			switch kind {
			case "directory":
				if e := os.Mkdir(path, 0700); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				if e := os.Symlink(filepath.Join(dir, "missing"), path); e != nil {
					t.Fatal(e)
				}
			case "permission":
				putFile(t, path, `{"months":[]}`)
				if e := os.Chmod(path, 0000); e != nil {
					t.Fatal(e)
				}
				defer os.Chmod(path, 0600)
				if _, e := os.ReadFile(path); e == nil {
					t.Skip("environment bypasses file permissions")
				}
			}
			oldData, oldCategories := dataFile, categoriesFile
			defer func() { dataFile, categoriesFile = oldData, oldCategories }()
			dataFile, categoriesFile = path, path
			before, exp := cloneData(store), cloneCategories(expenseCategories)
			if e := loadData(); e == nil {
				t.Fatal("unavailable data accepted")
			}
			if e := loadCategories(); e == nil {
				t.Fatal("unavailable categories accepted")
			}
			if !reflect.DeepEqual(before, store) || !reflect.DeepEqual(exp, expenseCategories) {
				t.Fatal("memory changed")
			}
			if _, e := os.Lstat(path); e != nil {
				t.Fatal("original disappeared")
			}
		})
	}
}

func TestLoadingLegacyFilesDoesNotRewrite(t *testing.T) {
	testHandler(t)
	data := `{ "months": [{"id":"month-2020-01","month":1,"year":2020,"income":400,"expenses":null}] }`
	cats := `[ {"id":"custom","name":"Своя","subcategories":["А"]} ]`
	putFile(t, dataFile, data)
	putFile(t, categoriesFile, cats)
	if e := loadData(); e != nil {
		t.Fatal(e)
	}
	if e := loadCategories(); e != nil {
		t.Fatal(e)
	}
	if string(readFile(t, dataFile)) != data || string(readFile(t, categoriesFile)) != cats {
		t.Fatal("load rewrote legacy files")
	}
}

func TestAtomicSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	putFile(t, path, `{"old":true}`)
	if e := atomicWriteFile(path, []byte(`{"new":true}`)); e != nil {
		t.Fatal(e)
	}
	if string(readFile(t, path)) != `{"new":true}` {
		t.Fatal("replacement failed")
	}
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("permissions changed")
	}
	files, e := os.ReadDir(dir)
	if e != nil || len(files) != 1 {
		t.Fatal("temporary file leaked")
	}
	if e := atomicWriteFile(filepath.Join(dir, "absent", "data.json"), []byte("{}")); e == nil {
		t.Fatal("accepted missing directory")
	}
	// Never replace directories or symbolic links, including the link target.
	link := filepath.Join(dir, "link.json")
	if e := os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if e := atomicWriteFile(link, []byte("{}")); e == nil {
		t.Fatal("replaced symlink")
	}
	if string(readFile(t, path)) != `{"new":true}` {
		t.Fatal("symlink target changed")
	}
	if e := atomicWriteFile(dir, []byte("{}")); e == nil {
		t.Fatal("replaced directory")
	}
	if e := saveJSON(path, math.NaN()); e == nil {
		t.Fatal("accepted unserializable data")
	}
	if string(readFile(t, path)) != `{"new":true}` {
		t.Fatal("marshal failure damaged file")
	}
}

func TestEveryMutationRollsBackOnSaveFailure(t *testing.T) {
	h := testHandler(t)
	path := "/api/months/month-2026-11"
	request(t, h, "POST", "/api/months", `{"month":11,"year":2026}`, 201)
	exp := decode[Expense](t, request(t, h, "POST", path+"/expenses", `{"amount":12,"category":"food","day":1}`, 201))
	inc := decode[IncomeEntry](t, request(t, h, "POST", path+"/incomes", `{"amount":20,"category":"salary","day":1}`, 201))
	cases := []struct{ method, path, body string }{
		{"POST", "/api/months", `{"month":12,"year":2026}`},
		{"DELETE", path, ""}, {"PATCH", path + "/income", `{"income":900}`},
		{"POST", path + "/expenses", `{"amount":80}`}, {"PATCH", path + "/expenses/" + exp.ID, `{"amount":90}`}, {"DELETE", path + "/expenses/" + exp.ID, ""},
		{"POST", path + "/incomes", `{"amount":80}`}, {"PATCH", path + "/incomes/" + inc.ID, `{"amount":90}`}, {"DELETE", path + "/incomes/" + inc.ID, ""},
		{"PUT", "/api/categories", `{"expense":[{"id":"new","name":"new"}],"income":[]}`},
	}
	original := writeJSONFile
	defer func() { writeJSONFile = original }()
	for _, cause := range []error{syscall.ENOSPC, os.ErrPermission} {
		t.Run(cause.Error(), func(t *testing.T) {
			writeJSONFile = func(string, []byte) error { return cause }
			for _, c := range cases {
				t.Run(c.method+c.path, func(t *testing.T) {
					before, expCats, incCats := cloneData(store), cloneCategories(expenseCategories), cloneCategories(incomeCategories)
					data, cats := readFile(t, dataFile), readFile(t, categoriesFile)
					w := request(t, h, c.method, c.path, c.body, 500)
					if !strings.Contains(w.Body.String(), "Изменения не применены") {
						t.Fatal("missing save error")
					}
					if !reflect.DeepEqual(before, store) || !reflect.DeepEqual(expCats, expenseCategories) || !reflect.DeepEqual(incCats, incomeCategories) {
						t.Fatal("failed save changed memory")
					}
					if !bytes.Equal(data, readFile(t, dataFile)) || !bytes.Equal(cats, readFile(t, categoriesFile)) {
						t.Fatal("failed save changed files")
					}
					request(t, h, "GET", path, "", 200)
				})
			}
		})
	}
	writeJSONFile = original
	request(t, h, "PATCH", path+"/income", `{"income":70}`, 200)
	if e := loadData(); e != nil {
		t.Fatal(e)
	}
	if store.Months[0].Income != 70 {
		t.Fatal("recovery after failure")
	}
}

func TestMalformedMutationDoesNotSave(t *testing.T) {
	h := testHandler(t)
	path := "/api/months/month-2026-11"
	request(t, h, "POST", "/api/months", `{"month":11,"year":2026}`, 201)
	before := readFile(t, dataFile)
	for _, c := range []struct{ method, path string }{{"PATCH", path + "/income"}, {"POST", path + "/expenses"}, {"POST", path + "/incomes"}} {
		request(t, h, c.method, c.path, `{"income":7,"amount":10,`, 400)
		if !bytes.Equal(before, readFile(t, dataFile)) {
			t.Fatal("malformed request saved")
		}
	}
}

func TestConcurrentFinancialRequests(t *testing.T) {
	h := testHandler(t)
	path := "/api/months/month-2026-11"
	request(t, h, "POST", "/api/months", `{"month":11,"year":2026}`, 201)
	var wg sync.WaitGroup
	failures := make(chan string, 500)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for _, r := range []struct {
				method, path, body string
				status             int
			}{
				{"POST", path + "/expenses", fmt.Sprintf(`{"amount":1,"day":1,"description":"expense-%d"}`, i), 201},
				{"POST", path + "/incomes", `{"amount":2,"day":1}`, 201},
				{"PUT", "/api/categories", `{"expense":[{"id":"food","name":"Еда"}],"income":[{"id":"salary","name":"Зарплата"}]}`, 200},
				{"GET", "/api/months", "", 200}, {"GET", "/api/years/2026", "", 200}, {"GET", "/api/search?q=expense", "", 200}, {"GET", "/api/categories", "", 200}, {"GET", "/api/now", "", 200}, {"GET", "/api/backup", "", 200},
			} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(r.method, r.path, strings.NewReader(r.body)))
				if w.Code != r.status || !json.Valid(w.Body.Bytes()) {
					failures <- fmt.Sprintf("%s %s: %d %s", r.method, r.path, w.Code, w.Body.String())
				}
			}
		}(i)
	}
	// Independent file readers must never observe truncated/intermediate JSON.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			b, e := os.ReadFile(dataFile)
			if e != nil || !json.Valid(b) {
				failures <- "partial file observed"
				return
			}
		}
	}()
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
	m := decode[Month](t, request(t, h, "GET", path, "", 200))
	if len(m.Expenses) != 40 || len(m.Incomes) != 40 || totalIncome(&m) != 80 {
		t.Fatalf("lost writes: %+v", m)
	}
	ids := map[string]bool{}
	for _, e := range m.Expenses {
		if ids[e.ID] {
			t.Fatal("duplicate ID")
		}
		ids[e.ID] = true
	}
	if e := loadData(); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(store.Months[0], m) {
		t.Fatal("disk and memory diverged")
	}
}

func TestPartialTemporaryWriteFailurePreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	original := `{"months":[{"income":123}]}`
	putFile(t, path, original)
	err := atomicReplaceFile(path, func(f *os.File) error {
		if _, e := f.WriteString(`{"months":[`); e != nil {
			return e
		}
		// Simulate a disk-full error after a partial write, before rename.
		return syscall.ENOSPC
	})
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("got %v", err)
	}
	if string(readFile(t, path)) != original {
		t.Fatal("partial write damaged original")
	}
	files, e := os.ReadDir(dir)
	if e != nil || len(files) != 1 {
		t.Fatal("failed write left temporary file")
	}
}

func TestRenameFailurePreservesOriginalAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	original := `{"months":[]}`
	putFile(t, path, original)
	// Move the temporary source away before rename, forcing an actual OS error.
	moved := filepath.Join(dir, "moved.tmp")
	err := atomicReplaceFile(path, func(f *os.File) error {
		if _, e := f.WriteString(`{"months":[{}]}`); e != nil {
			return e
		}
		return os.Rename(f.Name(), moved)
	})
	if err == nil {
		t.Fatal("rename unexpectedly succeeded")
	}
	if string(readFile(t, path)) != original {
		t.Fatal("rename failure damaged original")
	}
	if e := os.Remove(moved); e != nil {
		t.Fatal(e)
	}
	files, e := os.ReadDir(dir)
	if e != nil || len(files) != 1 {
		t.Fatal("temporary file leaked")
	}
}
