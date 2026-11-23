package planner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

//go:embed calendars/2026.json
var verified2026 []byte

//go:embed calendars/2027.json
var verified2027 []byte

type ImportedDay struct {
	Date   Date   `json:"date"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}
type CalendarDocument struct {
	SchemaVersion int           `json:"schema_version"`
	Year          int           `json:"year"`
	Source        string        `json:"source"`
	PublishedAt   string        `json:"published_at"`
	Version       string        `json:"version"`
	Days          []ImportedDay `json:"days"`
	Checksum      string        `json:"checksum,omitempty"`
}
type CalendarSummary struct {
	Year        int    `json:"year"`
	Source      string `json:"source"`
	Revision    int64  `json:"revision"`
	Checksum    string `json:"checksum"`
	UpdatedAt   string `json:"updated_at"`
	PublishedAt string `json:"published_at"`
	Version     string `json:"version"`
	Days        int    `json:"days"`
	Complete    bool   `json:"complete"`
}
type CalendarImportInput struct {
	Calendar         CalendarDocument `json:"calendar"`
	ExpectedRevision int64            `json:"expected_revision"`
	PreviewToken     string           `json:"preview_token"`
	RequestID        string           `json:"request_id"`
	SourceConfirmed  bool             `json:"source_confirmed"`
}

func validateCalendarDocument(v CalendarDocument) (CalendarYear, string, error) {
	year := CalendarYear{Year: v.Year, Source: v.Source}
	if v.SchemaVersion != 1 || v.Version == "" || len(v.Version) > 100 {
		return year, "", invalid("Нужны schema_version=1 и версия календаря")
	}
	if _, e := parseDate(Date(v.PublishedAt)); e != nil {
		return year, "", invalid("Некорректная дата публикации")
	}
	u, e := url.Parse(v.Source)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Fragment != "" {
		return year, "", invalid("Нужна HTTPS-ссылка на официальный источник")
	}
	official := false
	for _, domain := range []string{"government.ru", "pravo.gov.ru", "rostrud.gov.ru", "trudvsem.ru"} {
		if u.Hostname() == domain || strings.HasSuffix(u.Hostname(), "."+domain) {
			official = true
		}
	}
	if !official {
		return year, "", invalid("Источник должен быть официальной публикацией Правительства, правового портала или Роструда")
	}
	for _, day := range v.Days {
		year.Days = append(year.Days, CalendarDay{Date: day.Date, Kind: day.Kind, Reason: day.Reason})
	}
	if e = validateCalendarYear(year); e != nil {
		return year, "", invalid(e.Error())
	}
	sorted := append([]ImportedDay{}, v.Days...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })
	v.Days = sorted
	claimed := v.Checksum
	v.Checksum = ""
	raw, _ := json.Marshal(v)
	checksum := fmt.Sprintf("%x", sha256.Sum256(raw))
	if claimed != checksum {
		return year, "", invalid("Контрольная сумма SHA-256 не совпадает с содержимым календаря")
	}
	return year, checksum, nil
}
func calendarSummaryTx(tx *sql.Tx, year int) (CalendarSummary, error) {
	v := CalendarSummary{Year: year}
	e := tx.QueryRow(`SELECT y.source,y.revision,COALESCE(i.checksum,y.source_checksum),y.imported_at,COALESCE(i.published_at,''),COALESCE(i.version,''),(SELECT count(*) FROM production_calendar_days d WHERE d.year=y.year) FROM production_calendar_years y LEFT JOIN calendar_imports i ON i.year=y.year AND i.calendar_revision=y.revision WHERE y.year=?`, year).Scan(&v.Source, &v.Revision, &v.Checksum, &v.UpdatedAt, &v.PublishedAt, &v.Version, &v.Days)
	if e == sql.ErrNoRows {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	first, _ := parseDate(Date(fmt.Sprintf("%04d-01-01", year)))
	v.Complete = v.Days == int(first.AddDate(1, 0, 0).Sub(first).Hours()/24)
	return v, nil
}
func calendarImportToken(checksum string, revision int64) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", checksum, revision))))
}
func (s *Service) ImportProductionCalendar(ctx context.Context, in CalendarImportInput) (CommandResult, error) {
	year, checksum, e := validateCalendarDocument(in.Calendar)
	if e != nil {
		return CommandResult{}, e
	}
	if !in.SourceConfirmed {
		return CommandResult{}, invalid("Подтвердите сверку файла с официальным источником")
	}
	now := stamp(s.now())
	op, e := operation()
	if e != nil {
		return CommandResult{}, e
	}
	var result CommandResult
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = commandOnce(tx, in.RequestID, "calendar/"+strconv.Itoa(year.Year), "import", now, in, func() (CommandResult, error) {
			before, e := calendarSummaryTx(tx, year.Year)
			if e != nil {
				return CommandResult{}, e
			}
			if before.Revision != in.ExpectedRevision || in.PreviewToken != calendarImportToken(checksum, before.Revision) {
				return CommandResult{}, &APIError{Status: 409, Code: "calendar_conflict", Message: "Календарь изменился. Повторите предпросмотр импорта.", Current: before}
			}
			if e = importCalendarYearTx(ctx, tx, year, now); e != nil {
				return CommandResult{}, e
			}
			raw, _ := json.Marshal(in.Calendar)
			if _, e = tx.Exec(`INSERT INTO calendar_imports(id,year,calendar_revision,source,published_at,version,checksum,snapshot_json,operation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, op, year.Year, before.Revision+1, year.Source, in.Calendar.PublishedAt, in.Calendar.Version, checksum, string(raw), op, now); e != nil {
				return CommandResult{}, e
			}
			after, e := calendarSummaryTx(tx, year.Year)
			return CommandResult{Calendar: &after}, e
		})
		return err
	})
	return result, e
}
func productionCalendarRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method == "GET" && id == "bundled" && (action == "2026" || action == "2027") {
		var v CalendarDocument
		raw := verified2026
		if action == "2027" {
			raw = verified2027
		}
		if e := json.Unmarshal(raw, &v); e != nil {
			return 0, nil, e
		}
		return 200, v, nil
	}
	if r.Method == "GET" && id == "" {
		items := []CalendarSummary{}
		e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
			rows, e := tx.Query(`SELECT year FROM production_calendar_years ORDER BY year`)
			if e != nil {
				return e
			}
			years := []int{}
			for rows.Next() {
				var y int
				if e = rows.Scan(&y); e != nil {
					rows.Close()
					return e
				}
				years = append(years, y)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			for _, y := range years {
				v, e := calendarSummaryTx(tx, y)
				if e != nil {
					return e
				}
				items = append(items, v)
			}
			return nil
		})
		return 200, map[string]any{"items": items, "bundled_years": []int{2026, 2027}}, e
	}
	if r.Method != "POST" {
		return 0, nil, methodError(w, "GET, POST")
	}
	if id != "preview" && id != "import" || action != "" {
		return 0, nil, notFound("Маршрут", id)
	}
	var in CalendarImportInput
	if e := strictPlannerInput(w, r, &in, "calendar expected_revision preview_token request_id source_confirmed"); e != nil {
		return 0, nil, e
	}
	if id == "import" {
		v, e := s.ImportProductionCalendar(r.Context(), in)
		return 200, v, e
	}
	year, checksum, e := validateCalendarDocument(in.Calendar)
	if e != nil {
		return 0, nil, e
	}
	var current CalendarSummary
	changed := []map[string]any{}
	e = s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		var e error
		current, e = calendarSummaryTx(tx, year.Year)
		if e != nil {
			return e
		}
		for _, d := range year.Days {
			var kind string
			e = tx.QueryRow(`SELECT kind FROM production_calendar_days WHERE date=?`, d.Date).Scan(&kind)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			if kind != d.Kind {
				changed = append(changed, map[string]any{"date": d.Date, "before": kind, "after": d.Kind})
			}
		}
		return nil
	})
	return 200, map[string]any{"current": current, "changed_days": changed, "preview_token": calendarImportToken(checksum, current.Revision), "expected_revision": current.Revision, "message": "Импорт не переносит существующие выполнения. Для пересчёта откройте серию и подтвердите предпросмотр изменения правила."}, e
}
