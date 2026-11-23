package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every test uses private paths; never chdir or start the production server.
func testHandler(t *testing.T) http.Handler {
	t.Helper()
	oldData, oldCategories := dataFile, categoriesFile
	oldStore, oldExpense, oldIncome := store, expenseCategories, incomeCategories
	t.Cleanup(func() {
		dataFile, categoriesFile = oldData, oldCategories
		store, expenseCategories, incomeCategories = oldStore, oldExpense, oldIncome
	})
	dir := t.TempDir()
	dataFile, categoriesFile = filepath.Join(dir, "data.json"), filepath.Join(dir, "categories.json")
	store = dataStore{Months: []Month{}}
	expenseCategories, incomeCategories = defaultExpenseCategories(), defaultIncomeCategories()
	if err := saveData(); err != nil {
		t.Fatal(err)
	}
	if err := saveCategories(); err != nil {
		t.Fatal(err)
	}
	return newFinancialHandler(http.NotFoundHandler())
}

func request(t *testing.T, h http.Handler, method, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	if w.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w
}
func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestFinancialRegression(t *testing.T) {
	h := testHandler(t)
	path := "/api/months/month-2026-11"
	request(t, h, "POST", "/api/months", `{"month":11,"year":2026}`, 201)
	request(t, h, "POST", "/api/months", `{"month":11,"year":2026}`, 201)
	if n := len(decode[[]Month](t, request(t, h, "GET", "/api/months", "", 200))); n != 1 {
		t.Fatalf("duplicate months: %d", n)
	}
	request(t, h, "PATCH", path+"/income", `{"income":1000}`, 200)
	inc := decode[IncomeEntry](t, request(t, h, "POST", path+"/incomes", `{"category":"salary","amount":500,"day":2,"description":"bonus"}`, 201))
	exp := decode[Expense](t, request(t, h, "POST", path+"/expenses", `{"category":"food","subcategory":"Продукты","amount":125.5,"day":3,"description":"milk"}`, 201))
	if exp.Date != "2026-11-03" || exp.DayOfWeek != "Вт" || exp.ID == "" {
		t.Fatalf("expense: %+v", exp)
	}
	request(t, h, "PATCH", path+"/expenses/"+exp.ID, `{"amount":150,"day":4,"description":"milk updated"}`, 200)
	request(t, h, "PATCH", path+"/incomes/"+inc.ID, `{"amount":600,"day":5}`, 200)
	m := decode[Month](t, request(t, h, "GET", path, "", 200))
	if totalIncome(&m) != 1600 || m.Expenses[0].Amount != 150 || m.Incomes[0].Date != "2026-11-05" {
		t.Fatalf("month: %+v", m)
	}
	summary := decode[struct {
		TotalIncome, TotalExpenses, Balance float64
		ByMonthBalance                      map[string]float64
		ByCategory                          []struct {
			Name         string
			Sum, Percent float64
		}
	}](t, request(t, h, "GET", "/api/years/2026", "", 200))
	if summary.TotalIncome != 1600 || summary.TotalExpenses != 150 || summary.Balance != 1450 || summary.ByMonthBalance["2026-11"] != 1450 || len(summary.ByCategory) != 1 || summary.ByCategory[0].Percent != 100 {
		t.Fatalf("summary: %+v", summary)
	}
	for _, q := range []string{"milk", "150", "food"} {
		hits := decode[[]searchHit](t, request(t, h, "GET", "/api/search?q="+q, "", 200))
		if len(hits) != 1 || hits[0].ID != exp.ID {
			t.Fatalf("search %s: %+v", q, hits)
		}
	}
	b := request(t, h, "GET", "/api/backup", "", 200)
	disk, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if b.Body.String() != string(disk) || !strings.Contains(b.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("backup differs from saved data")
	}
	if err := loadData(); err != nil {
		t.Fatal(err)
	}
	if totalIncome(&store.Months[0]) != 1600 {
		t.Fatal("reload lost income")
	}
	request(t, h, "DELETE", path+"/expenses/"+exp.ID, "", 204)
	request(t, h, "DELETE", path+"/incomes/"+inc.ID, "", 204)
	request(t, h, "DELETE", path, "", 204)
	request(t, h, "GET", path, "", 404)
	if len(decode[[]Month](t, request(t, h, "GET", "/api/months", "", 200))) != 0 {
		t.Fatal("month not deleted")
	}
}

func TestCategoriesRegression(t *testing.T) {
	h := testHandler(t)
	request(t, h, "PUT", "/api/categories", `{"expense":[{"id":"food","name":" Еда ","subcategories":["Продукты"]}],"income":[{"id":"salary","name":"Зарплата"}]}`, 200)
	cats := decode[CategoriesResponse](t, request(t, h, "GET", "/api/categories", "", 200))
	if len(cats.Expense) != 1 || cats.Expense[0].Name != "Еда" || cats.Expense[0].Subcategories[0] != "Продукты" || cats.Income[0].ID != "salary" {
		t.Fatalf("categories: %+v", cats)
	}
	if err := loadCategories(); err != nil {
		t.Fatal(err)
	}
	if expenseCategories[0].Name != "Еда" {
		t.Fatal("categories not persisted")
	}
}

func TestLegacyFinancialFormat(t *testing.T) {
	testHandler(t)
	raw := `{"months":[{"id":"month-2020-01","month":1,"year":2020,"income":123.45,"expenses":null}]}`
	if err := os.WriteFile(dataFile, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadData(); err != nil {
		t.Fatal(err)
	}
	if len(store.Months) != 1 || totalIncome(&store.Months[0]) != 123.45 || store.Months[0].Incomes == nil || store.Months[0].Expenses == nil {
		t.Fatalf("legacy: %+v", store)
	}
	if err := os.WriteFile(categoriesFile, []byte(`[{"id":"custom","name":"Своя","subcategories":["А"]}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadCategories(); err != nil {
		t.Fatal(err)
	}
	if expenseCategories[0].ID != "custom" || len(incomeCategories) == 0 {
		t.Fatal("legacy categories")
	}
}
