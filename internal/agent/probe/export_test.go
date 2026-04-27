package probe

import (
	"testing"
	"time"
)

// SetWatchPollIntervalForTest overrides the watch loop poll cadence for the
// duration of the test and restores the previous value via t.Cleanup.
// Tests live in package probe_test, so this hook is exported for them.
func SetWatchPollIntervalForTest(t *testing.T, d time.Duration) {
	t.Helper()
	prev := watchPollInterval
	watchPollInterval = d
	t.Cleanup(func() {
		watchPollInterval = prev
	})
}
