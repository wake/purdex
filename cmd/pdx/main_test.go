//go:build !integration

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/gate/gatetest"
)

// HOME and PDX_GATE_DIR in a temporary directory: no test reaches the real gate (#2932). The integration build has its own
// TestMain (restart_integration_test.go), which does the same.
func TestMain(m *testing.M) { gatetest.Main(m) }

// WWD-9 9f: `pdx lease run` is gone. main.go no longer dispatches "lease", so it falls to the default branch, which prints
// "unknown command" and exits 1; the Commands line no longer names it.
func TestDispatch_LeaseIsGone(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, `case "lease"`) || strings.Contains(s, "runLease(") {
		t.Error("main.go still dispatches \"lease\"")
	}
	if !strings.Contains(s, "default:\n\t\tfmt.Fprintf(os.Stderr, \"unknown command: %s\\n\", os.Args[1])\n\t\tos.Exit(1)\n") {
		t.Error("main.go's default branch is not \"unknown command\" + exit 1")
	}
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "Commands:") && strings.Contains(l, " lease,") {
			t.Errorf("the Commands line still lists lease: %s", l)
		}
	}
}
