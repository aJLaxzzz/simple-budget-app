package planner

import (
	"budget/internal/storageguard"
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if e := storageguard.TestEnvironment(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
