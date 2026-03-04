package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	dataFile       = "data.json"
	categoriesFile = "categories.json"
	addr           = ":3001"
)

var dayNamesRu = []string{"Вс", "Пн", "Вт", "Ср", "Чт", "Пт", "Сб"}
var moscowLoc *time.Location

type Expense struct {
	ID          string  `json:"id"`
	Date        string  `json:"date"`
	DayOfWeek   string  `json:"dayOfWeek"`
	Category    string  `json:"category"`
	Subcategory string  `json:"subcategory"`
	Amount      float64 `json:"amount"`
	Description string  `json:"description"`
}

type IncomeEntry struct {
	ID          string  `json:"id"`
	Date        string  `json:"date"`
	DayOfWeek   string  `json:"dayOfWeek"`
	Category    string  `json:"category"`
	Amount      float64 `json:"amount"`
	Description string  `json:"description"`
}

type Month struct {
	ID       string        `json:"id"`
	Month    int           `json:"month"`
	Year     int           `json:"year"`
	Income   float64       `json:"income"`
	Incomes  []IncomeEntry `json:"incomes"`
	Expenses []Expense     `json:"expenses"`
}

type Category struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Subcategories []string `json:"subcategories"`
}

type CategoriesResponse struct {
	Expense []Category `json:"expense"`
	Income  []Category `json:"income"`
}

type dataStore struct {
	Months []Month `json:"months"`
}

var (
	store            dataStore
	expenseCategories []Category
	incomeCategories  []Category
)

func loadCategories() {
	raw, err := os.ReadFile(categoriesFile)
	if err != nil || len(raw) == 0 {
		expenseCategories = defaultExpenseCategories()
		incomeCategories = defaultIncomeCategories()
		_ = saveCategories()
		return
	}
	var parsed struct {
		Expense []Category `json:"expense"`
		Income  []Category `json:"income"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		var arr []Category
		if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
			expenseCategories = arr
			incomeCategories = defaultIncomeCategories()
			_ = saveCategories()
			return
		}
		expenseCategories = defaultExpenseCategories()
		incomeCategories = defaultIncomeCategories()
		_ = saveCategories()
		return
	}
	if len(parsed.Expense) > 0 {
		expenseCategories = parsed.Expense
	} else {
		expenseCategories = defaultExpenseCategories()
	}
	if len(parsed.Income) > 0 {
		incomeCategories = parsed.Income
	} else {
		incomeCategories = defaultIncomeCategories()
	}
	_ = saveCategories()
}

func defaultExpenseCategories() []Category {
	return []Category{
		{ID: "food", Name: "Еда", Subcategories: []string{"Продукты", "Кафе", "Доставка", "Перекусы"}},
		{ID: "transport", Name: "Транспорт", Subcategories: []string{"Общественный транспорт", "Такси", "Бензин", "Парковка"}},
		{ID: "housing", Name: "Жильё", Subcategories: []string{"Аренда", "Коммунальные", "Интернет"}},
		{ID: "entertainment", Name: "Развлечения", Subcategories: []string{"Кино", "Подписки", "Игры", "Хобби"}},
		{ID: "health", Name: "Здоровье", Subcategories: []string{"Аптека", "Врачи", "Спорт"}},
		{ID: "clothing", Name: "Одежда", Subcategories: []string{"Одежда", "Обувь"}},
		{ID: "other", Name: "Прочее", Subcategories: []string{"Подарки", "Образование", "Другое"}},
	}
}

func defaultIncomeCategories() []Category {
	return []Category{
		{ID: "salary", Name: "Зарплата"},
		{ID: "freelance", Name: "Фриланс"},
		{ID: "bonus", Name: "Премия"},
		{ID: "other_income", Name: "Прочий доход"},
	}
}

func saveCategories() error {
	b, _ := json.MarshalIndent(map[string]interface{}{
		"expense": expenseCategories,
		"income":  incomeCategories,
	}, "", "  ")
	return os.WriteFile(categoriesFile, b, 0644)
}

func loadData() {
	raw, err := os.ReadFile(dataFile)
	if err != nil {
		store.Months = []Month{}
		_ = saveData()
		return
	}
	var d dataStore
	if err := json.Unmarshal(raw, &d); err != nil {
		store.Months = []Month{}
		_ = saveData()
		return
	}
	store.Months = d.Months
	if store.Months == nil {
		store.Months = []Month{}
	}
	for i := range store.Months {
		if store.Months[i].Expenses == nil {
			store.Months[i].Expenses = []Expense{}
		}
		if store.Months[i].Incomes == nil {
			store.Months[i].Incomes = []IncomeEntry{}
		}
	}
}

func saveData() error {
	b, _ := json.MarshalIndent(store, "", "  ")
	return os.WriteFile(dataFile, b, 0644)
}

func monthID(month, year int) string {
	return fmt.Sprintf("month-%d-%02d", year, month)
}

func findMonth(id string) *Month {
	for i := range store.Months {
		if store.Months[i].ID == id {
			return &store.Months[i]
		}
	}
	return nil
}

func dayOfWeek(dateStr string) string {
	if moscowLoc == nil {
		moscowLoc, _ = time.LoadLocation("Europe/Moscow")
		if moscowLoc == nil {
			moscowLoc = time.UTC
		}
	}
	t, err := time.ParseInLocation("2006-01-02", dateStr, moscowLoc)
	if err != nil {
		return ""
	}
	idx := int(t.Weekday())
	if idx < 0 || idx > 6 {
		return ""
	}
	return dayNamesRu[idx]
}

func totalIncome(m *Month) float64 {
	sum := m.Income
	for _, e := range m.Incomes {
		sum += e.Amount
	}
	return sum
}

func apiMonths(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(store.Months)
}

func apiMonthByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/months/")
	id = strings.TrimSuffix(id, "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	m := findMonth(id)
	if m == nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Month not found"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

func apiDeleteMonth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/months/")
	id = strings.TrimSuffix(id, "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	for i := range store.Months {
		if store.Months[i].ID == id {
			store.Months = append(store.Months[:i], store.Months[i+1:]...)
			_ = saveData()
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func apiCreateMonth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Month int `json:"month"`
		Year  int `json:"year"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Month < 1 || body.Month > 12 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "month and year required"})
		return
	}
	id := monthID(body.Month, body.Year)
	if m := findMonth(id); m != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(m)
		return
	}
	newMonth := Month{
		ID:       id,
		Month:    body.Month,
		Year:     body.Year,
		Income:   0,
		Incomes:  []IncomeEntry{},
		Expenses: []Expense{},
	}
	store.Months = append(store.Months, newMonth)
	sort.Slice(store.Months, func(i, j int) bool {
		if store.Months[i].Year != store.Months[j].Year {
			return store.Months[i].Year < store.Months[j].Year
		}
		return store.Months[i].Month < store.Months[j].Month
	})
	_ = saveData()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(newMonth)
}

func apiMonthIncome(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/months/")
	id = strings.TrimSuffix(id, "/income")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	m := findMonth(id)
	if m == nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Month not found"})
		return
	}
	var body struct {
		Income float64 `json:"income"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.Income = body.Income
	_ = saveData()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

func dateFromDay(month, year, day int) string {
	if day < 1 {
		day = 1
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	daysInMonth := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > daysInMonth {
		t = time.Date(year, time.Month(month), daysInMonth, 0, 0, 0, 0, time.UTC)
	}
	return t.Format("2006-01-02")
}

func apiExpenses(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/months/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	monthIDStr := parts[0]
	m := findMonth(monthIDStr)
	if m == nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Month not found"})
		return
	}

	if r.Method == http.MethodPost {
		var body struct {
			Category    string  `json:"category"`
			Subcategory string  `json:"subcategory"`
			Amount      float64 `json:"amount"`
			Description string  `json:"description"`
			Date        string  `json:"date"`
			Day         int     `json:"day"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		dateStr := body.Date
		if body.Day >= 1 && body.Day <= 31 {
			dateStr = dateFromDay(m.Month, m.Year, body.Day)
		}
		if dateStr == "" {
			dateStr = time.Now().Format("2006-01-02")
		}
		expID := fmt.Sprintf("exp-%d-%s", time.Now().UnixMilli(), randomStr(7))
		exp := Expense{
			ID:          expID,
			Date:        dateStr,
			DayOfWeek:   dayOfWeek(dateStr),
			Category:    body.Category,
			Subcategory: body.Subcategory,
			Amount:      body.Amount,
			Description: body.Description,
		}
		m.Expenses = append(m.Expenses, exp)
		_ = saveData()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(exp)
		return
	}

	if r.Method == http.MethodDelete && len(parts) == 3 && parts[1] == "expenses" {
		expID := parts[2]
		for i, e := range m.Expenses {
			if e.ID == expID {
				m.Expenses = append(m.Expenses[:i], m.Expenses[i+1:]...)
				_ = saveData()
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}

	http.NotFound(w, r)
}

func apiIncomes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/months/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	monthIDStr := parts[0]
	m := findMonth(monthIDStr)
	if m == nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Month not found"})
		return
	}

	if r.Method == http.MethodPost {
		var body struct {
			Category    string  `json:"category"`
			Amount      float64 `json:"amount"`
			Description string  `json:"description"`
			Date        string  `json:"date"`
			Day         int     `json:"day"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		dateStr := body.Date
		if body.Day >= 1 && body.Day <= 31 {
			dateStr = dateFromDay(m.Month, m.Year, body.Day)
		}
		if dateStr == "" {
			dateStr = time.Now().Format("2006-01-02")
		}
		entryID := fmt.Sprintf("inc-%d-%s", time.Now().UnixMilli(), randomStr(7))
		entry := IncomeEntry{
			ID:          entryID,
			Date:        dateStr,
			DayOfWeek:   dayOfWeek(dateStr),
			Category:    body.Category,
			Amount:      body.Amount,
			Description: body.Description,
		}
		m.Incomes = append(m.Incomes, entry)
		_ = saveData()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(entry)
		return
	}

	if r.Method == http.MethodDelete && len(parts) == 3 && parts[1] == "incomes" {
		entryID := parts[2]
		for i, e := range m.Incomes {
			if e.ID == entryID {
				m.Incomes = append(m.Incomes[:i], m.Incomes[i+1:]...)
				_ = saveData()
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}

	http.NotFound(w, r)
}

func randomStr(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func apiCategories(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CategoriesResponse{
			Expense: expenseCategories,
			Income:  incomeCategories,
		})
		return
	case http.MethodPut:
		apiSaveCategories(w, r)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func slugify(s string) string {
	var b []rune
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b = append(b, r)
		} else if r == ' ' || r == '-' || r == '_' {
			if len(b) > 0 && b[len(b)-1] != '_' {
				b = append(b, '_')
			}
		}
	}
	return strings.Trim(string(b), "_")
}

func ensureUniqueID(id string, existing map[string]bool) string {
	if id == "" {
		id = "cat"
	}
	base := id
	n := 1
	for existing[id] {
		id = base + "_" + strconv.Itoa(n)
		n++
	}
	return id
}

func apiSaveCategories(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Expense []Category `json:"expense"`
		Income  []Category `json:"income"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json"})
		return
	}
	usedIDs := make(map[string]bool)
	var newExpense []Category
	for _, c := range body.Expense {
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			continue
		}
		if c.Subcategories == nil {
			c.Subcategories = []string{}
		}
		id := strings.TrimSpace(c.ID)
		if id == "" {
			id = slugify(c.Name)
			if id == "" {
				id = "cat"
			}
		}
		id = ensureUniqueID(id, usedIDs)
		usedIDs[id] = true
		newExpense = append(newExpense, Category{ID: id, Name: c.Name, Subcategories: c.Subcategories})
	}
	usedIDs = make(map[string]bool)
	var newIncome []Category
	for _, c := range body.Income {
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			continue
		}
		id := strings.TrimSpace(c.ID)
		if id == "" {
			id = slugify(c.Name)
			if id == "" {
				id = "inc"
			}
		}
		id = ensureUniqueID(id, usedIDs)
		usedIDs[id] = true
		newIncome = append(newIncome, Category{ID: id, Name: c.Name})
	}
	if len(newExpense) == 0 {
		newExpense = defaultExpenseCategories()
	}
	if len(newIncome) == 0 {
		newIncome = defaultIncomeCategories()
	}
	expenseCategories = newExpense
	incomeCategories = newIncome
	if err := saveCategories(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to save"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CategoriesResponse{
		Expense: expenseCategories,
		Income:  incomeCategories,
	})
}

func apiYearSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/years/")
	path = strings.TrimSuffix(path, "/")
	year, err := strconv.Atoi(path)
	if err != nil || year < 2000 || year > 2100 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid year"})
		return
	}
	var incSum, totalExpenses float64
	byCategory := make(map[string]float64)
	byMonthExpenses := make(map[string]float64)
	byMonthIncome := make(map[string]float64)
	byMonthBalance := make(map[string]float64)
	var months []Month
	for _, m := range store.Months {
		if m.Year != year {
			continue
		}
		months = append(months, m)
		inc := totalIncome(&m)
		incSum += inc
		key := fmt.Sprintf("%d-%02d", m.Year, m.Month)
		byMonthIncome[key] = inc
		var expSum float64
		for _, e := range m.Expenses {
			totalExpenses += e.Amount
			expSum += e.Amount
			name := ""
			for _, c := range expenseCategories {
				if c.ID == e.Category {
					name = c.Name
					break
				}
			}
			if name == "" {
				name = e.Category
			}
			byCategory[name] += e.Amount
		}
		byMonthExpenses[key] = expSum
		byMonthBalance[key] = inc - expSum
	}
	type catSum struct {
		Name  string  `json:"name"`
		Sum   float64 `json:"sum"`
		Percent float64 `json:"percent"`
	}
	var catList []catSum
	for name, sum := range byCategory {
		pct := 0.0
		if totalExpenses > 0 {
			pct = sum / totalExpenses * 100
		}
		catList = append(catList, catSum{Name: name, Sum: sum, Percent: pct})
	}
	sort.Slice(catList, func(i, j int) bool { return catList[i].Sum > catList[j].Sum })
	out := map[string]interface{}{
		"year":              year,
		"totalIncome":       incSum,
		"totalExpenses":     totalExpenses,
		"balance":           incSum - totalExpenses,
		"byCategory":       catList,
		"byMonthExpenses":   byMonthExpenses,
		"byMonthIncome":     byMonthIncome,
		"byMonthBalance":    byMonthBalance,
		"months":            months,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func apiNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if moscowLoc == nil {
		moscowLoc, _ = time.LoadLocation("Europe/Moscow")
		if moscowLoc == nil {
			moscowLoc = time.UTC
		}
	}
	now := time.Now().In(moscowLoc)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"month": int(now.Month()),
		"year":  now.Year(),
	})
}

// apiShutdown сохраняет данные и завершает процесс (вызывается при закрытии вкладки).
func apiShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = saveData()
	w.WriteHeader(http.StatusOK)
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
}

// apiBackup отдаёт data.json как файл для скачивания (бэкап).
func apiBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	data, err := os.ReadFile(dataFile)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	name := "budget-backup-" + time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Write(data)
}

func expenseCategoryName(id string) string {
	for _, c := range expenseCategories {
		if c.ID == id {
			return c.Name
		}
	}
	return id
}

func incomeCategoryName(id string) string {
	for _, c := range incomeCategories {
		if c.ID == id {
			return c.Name
		}
	}
	return id
}

type searchHit struct {
	MonthID    string  `json:"monthId"`
	Month      int     `json:"month"`
	Year       int     `json:"year"`
	Type       string  `json:"type"`
	Date       string  `json:"date"`
	Category   string  `json:"category"`
	Subcategory string `json:"subcategory,omitempty"`
	Amount     float64 `json:"amount"`
	Description string `json:"description"`
	ID         string  `json:"id"`
}

func apiSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		_ = json.NewEncoder(w).Encode([]searchHit{})
		return
	}
	qLower := strings.ToLower(q)
	// Поиск по сумме: убираем пробелы и запятую, сравниваем число или подстроку
	qNumStr := strings.ReplaceAll(strings.ReplaceAll(q, " ", ""), ",", ".")
	var results []searchHit
	if store.Months != nil {
		for _, m := range store.Months {
			for _, e := range m.Expenses {
				catName := expenseCategoryName(e.Category)
				match := strings.Contains(strings.ToLower(e.Description), qLower) ||
					strings.Contains(strings.ToLower(e.Category), qLower) ||
					strings.Contains(strings.ToLower(catName), qLower) ||
					strings.Contains(strings.ToLower(e.Subcategory), qLower) ||
					strings.Contains(fmt.Sprintf("%.2f", e.Amount), qNumStr) ||
					strings.Contains(fmt.Sprintf("%.0f", e.Amount), strings.TrimRight(strings.TrimRight(qNumStr, "0"), "."))
				if match {
					results = append(results, searchHit{
						MonthID:     m.ID,
						Month:       m.Month,
						Year:        m.Year,
						Type:        "expense",
						Date:        e.Date,
						Category:    catName,
						Subcategory: e.Subcategory,
						Amount:      e.Amount,
						Description: e.Description,
						ID:          e.ID,
					})
				}
			}
			for _, e := range m.Incomes {
				catName := incomeCategoryName(e.Category)
				match := strings.Contains(strings.ToLower(e.Description), qLower) ||
					strings.Contains(strings.ToLower(e.Category), qLower) ||
					strings.Contains(strings.ToLower(catName), qLower) ||
					strings.Contains(fmt.Sprintf("%.2f", e.Amount), qNumStr) ||
					strings.Contains(fmt.Sprintf("%.0f", e.Amount), strings.TrimRight(strings.TrimRight(qNumStr, "0"), "."))
				if match {
					results = append(results, searchHit{
						MonthID:     m.ID,
						Month:       m.Month,
						Year:        m.Year,
						Type:        "income",
						Date:        e.Date,
						Category:    catName,
						Amount:      e.Amount,
						Description: e.Description,
						ID:          e.ID,
					})
				}
			}
		}
	}
	// Свежие сверху
	sort.Slice(results, func(i, j int) bool {
		if results[i].Year != results[j].Year {
			return results[i].Year > results[j].Year
		}
		if results[i].Month != results[j].Month {
			return results[i].Month > results[j].Month
		}
		return results[i].Date > results[j].Date
	})
	if len(results) > 50 {
		results = results[:50]
	}
	_ = json.NewEncoder(w).Encode(results)
}

func staticHandler() http.Handler {
	dir := "static"
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		dir = "."
	}
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "" {
			indexPath := filepath.Join(dir, "index.html")
			http.ServeFile(w, r, indexPath)
			return
		}
		fs.ServeHTTP(w, r)
	})
}

func main() {
	rand.Seed(time.Now().UnixNano())
	loadCategories()
	loadData()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/months", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/months" {
			if r.Method == http.MethodGet {
				apiMonths(w, r)
				return
			}
			if r.Method == http.MethodPost {
				apiCreateMonth(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/api/months/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasSuffix(p, "/income") {
			apiMonthIncome(w, r)
			return
		}
		if strings.Contains(p, "/expenses") {
			apiExpenses(w, r)
			return
		}
		if strings.Contains(p, "/incomes") {
			apiIncomes(w, r)
			return
		}
		if r.Method == http.MethodDelete && len(strings.TrimPrefix(p, "/api/months/")) > 0 && !strings.Contains(strings.TrimPrefix(p, "/api/months/"), "/") {
			apiDeleteMonth(w, r)
			return
		}
		apiMonthByID(w, r)
	})
	mux.HandleFunc("/api/categories", apiCategories)
	mux.HandleFunc("/api/years/", apiYearSummary)
	mux.HandleFunc("/api/now", apiNow)
	mux.HandleFunc("/api/shutdown", apiShutdown)
	mux.HandleFunc("/api/backup", apiBackup)
	mux.HandleFunc("/api/search", apiSearch)
	mux.Handle("/", staticHandler())

	abs, _ := filepath.Abs(".")
	log.Printf("Личный бюджет: http://localhost%s (каталог: %s)", addr, abs)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
