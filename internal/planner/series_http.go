package planner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
)

func strictPlannerInput(w http.ResponseWriter, r *http.Request, target any, allowed string) error {
	var raw json.RawMessage
	if e := decodeInput(w, r, &raw, fields(allowed)); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(target); e != nil {
		return badJSON("Некорректные поля: " + e.Error())
	}
	return nil
}
func seriesRoute(s *Service, w http.ResponseWriter, r *http.Request, id string) (int, any, error) {
	if id == "preview" {
		return seriesCreationPreviewRoute(s, w, r)
	}
	if id == "" {
		switch r.Method {
		case "GET":
			items, next, e := s.SeriesList(r.Context(), r.URL.Query().Get("after"))
			return 200, map[string]any{"items": items, "next_after": next}, e
		case "POST":
			var in CreateSeriesInput
			if e := strictPlannerInput(w, r, &in, "request_id task existing_task_id expected_revision first_date end_date rule source_type template_id template_version_id"); e != nil {
				return 0, nil, e
			}
			item, e := s.CreateSeries(r.Context(), in)
			return 201, item, e
		default:
			return 0, nil, methodError(w, "GET, POST")
		}
	}
	if r.Method != "GET" {
		return 0, nil, methodError(w, "GET")
	}
	limit, offset := 100, 0
	for key, dst := range map[string]*int{"limit": &limit, "offset": &offset} {
		if raw := r.URL.Query().Get(key); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil {
				return 0, nil, invalid("Некорректная пагинация")
			}
			*dst = n
		}
	}
	item, e := s.Series(r.Context(), id, limit, offset)
	return 200, item, e
}
func materializeRoute(s *Service, w http.ResponseWriter, r *http.Request) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in MaterializeInput
	if e := strictPlannerInput(w, r, &in, "through series_id after_series_id"); e != nil {
		return 0, nil, e
	}
	result, e := s.Materialize(r.Context(), in)
	return 200, result, e
}
