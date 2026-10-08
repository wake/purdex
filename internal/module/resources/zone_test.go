package resourcesmod

import (
	"os"
	"testing"
	"time"
)

// TestMain puts the whole package's tests on a host that is not on UTC, as
// mlab (UTC+8) is. Start texts are local clock readings that the process table
// parses in the local zone; tests that only ever ran on UTC hid that the
// sweeper read them as UTC (alpha.613's acceptance: every holder looked like a
// reused pid). Set once, before any goroutine exists, so nothing races on it.
func TestMain(m *testing.M) {
	time.Local = time.FixedZone("UTC+8", 8*3600)
	os.Exit(m.Run())
}
