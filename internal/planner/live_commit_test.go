package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestLiveCommitObserverDoesNotLoopOnMaterialization(t *testing.T) {
	db, service, handler := seriesFixture(t)
	series := createSeries(t, handler, seriesInput("live-observer", "2026-11-16", recurrence.Schedule, 1))
	drainSeries(t, service, MaterializeInput{SeriesID: series.ID})
	notifications := 0
	db.SetCommitObserver(func() { notifications++ })
	for n := 0; n < 3; n++ {
		if _, err := service.Materialize(ctx, MaterializeInput{SeriesID: series.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.EnsureUpcoming(ctx, series.ID); err != nil {
			t.Fatal(err)
		}
	}
	if notifications != 0 {
		t.Fatalf("unchanged refresh emitted %d notifications", notifications)
	}
	service.recordCalculationError(ctx, series.ID, invalid("synthetic calculation error"))
	service.recordCalculationError(ctx, series.ID, invalid("synthetic calculation error"))
	service.recordCalculationError(ctx, series.ID, nil)
	if notifications != 2 {
		t.Fatalf("changed projections emitted %d notifications, want 2", notifications)
	}
	err := db.Transaction(ctx, func(tx *sql.Tx) error {
		if _, e := tx.Exec("UPDATE series SET calculation_error='rolled back' WHERE id=?", series.ID); e != nil {
			return e
		}
		return errors.New("synthetic rollback")
	})
	if err == nil || notifications != 2 {
		t.Fatal("rollback emitted a notification", err, notifications)
	}
}

func TestLiveCancelledTransactionDoesNotBecomeInternalError(t *testing.T) {
	db := testStore(t)
	notifications := 0
	db.SetCommitObserver(func() { notifications++ })
	for _, cancelled := range []bool{true, false} {
		request, cancel := context.WithCancel(context.Background())
		err := db.Transaction(request, func(tx *sql.Tx) error {
			if cancelled {
				cancel()
			}
			// Reproduce database/sql having already completed its rollback.
			_ = tx.Rollback()
			_, err := tx.Exec("SELECT 1")
			return err
		})
		cancel()
		want := sql.ErrTxDone
		if cancelled {
			want = context.Canceled
		}
		if !errors.Is(err, want) {
			t.Fatalf("cancelled=%v: got %v, want %v", cancelled, err, want)
		}
	}
	if notifications != 0 {
		t.Fatal("cancelled transaction published a change")
	}
}
