package planner

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeploymentCancelledHTTPIsNotInternalError(t *testing.T) {
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	w := httptest.NewRecorder()
	WriteAPIError(w, context.Canceled)
	if w.Code != 499 || !strings.Contains(w.Body.String(), "request_cancelled") {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(logs.String(), "planner internal error") {
		t.Fatal("client cancellation logged as internal failure")
	}
	w = httptest.NewRecorder()
	WriteAPIError(w, errors.New("synthetic private detail"))
	if w.Code != 500 || !strings.Contains(logs.String(), "planner internal error") {
		t.Fatal("real error hidden")
	}
	if strings.Contains(logs.String(), "synthetic private detail") {
		t.Fatal("personal detail logged")
	}
}
