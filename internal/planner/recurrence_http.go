package planner

import (
	"budget/internal/planner/recurrence"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
)

func recurrenceAPIError(err error) error {
	if err == nil {
		return nil
	}
	var missing *recurrence.MissingCalendarError
	if errors.As(err, &missing) {
		return &APIError{Status: 422, Code: "production_calendar_unavailable", Message: missing.Error(), Current: map[string]int{"year": missing.Year}}
	}
	var domain *recurrence.Error
	if errors.As(err, &domain) {
		return &APIError{Status: 422, Code: domain.Code, Message: domain.Message}
	}
	return err
}
func recurrencePreviewRoute(s *Service, w http.ResponseWriter, r *http.Request) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var raw json.RawMessage
	if e := decodeInput(w, r, &raw, fields("rule anchor count start_cycle")); e != nil {
		return 0, nil, e
	}
	var in recurrence.PreviewRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&in); e != nil {
		return 0, nil, badJSON("Некорректное правило предпросмотра: " + e.Error())
	}
	result, e := s.PreviewRecurrence(r.Context(), in)
	return 200, result, e
}
