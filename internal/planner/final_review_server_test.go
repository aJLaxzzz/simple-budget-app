package planner

// Test-only visual fixture host. No production clock or server configuration is changed.
import (
	"budget/internal/storageguard"
	"context"
	"net/http"
	"os"
	"path/filepath"

	"testing"
	"time"
)

func TestFinalReviewServer(t *testing.T) {
	dir := os.Getenv("FINAL_REVIEW_DIR")
	if dir == "" {
		t.Skip("only run by the isolated visual review runner")
	}
	if _, e := storageguard.Check(dir, "test", ""); e != nil {
		t.Fatal(e)
	}
	store, err := Open(context.Background(), filepath.Join(dir, "planner.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(store)
	fixed, _ := time.Parse(time.RFC3339, "2026-11-18T09:00:00+03:00")
	service.now = func() time.Time { return fixed }
	mux := http.NewServeMux()
	mux.Handle("/api/planner/", NewHandler(service))
	mux.Handle("/", http.FileServer(http.Dir(os.Getenv("FINAL_REVIEW_STATIC"))))
	t.Fatal(http.ListenAndServe("127.0.0.1:31372", mux))
}
