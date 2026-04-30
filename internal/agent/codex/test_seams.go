// internal/agent/codex/test_seams.go
//
// Cross-package test seams for the ProcessDead detector. Lives in a
// non-_test.go production file so external packages (notably
// internal/module/agent integration tests) can override the detector's
// private polling cadence + pid-liveness function.
//
// Both seams operate on atomic primitives that the detector goroutine
// reads each tick — so a test mutating them while the goroutine is
// running does not race with the read. The previous value is captured
// inside SetXxxForTest and returned via the restore func; callers
// (tests) defer or t.Cleanup that closure. The `*ForTest` naming + the
// fact that production code never calls these helpers (grep enforces
// it) keeps the test contract clear without pulling the testing
// package into the production import graph.
package codex

import "time"

// SetIsPidAliveFnForTest swaps the pid-liveness probe and returns a
// restore func. Production code MUST NOT call this helper.
//
// Usage in cross-package tests:
//
//	t.Cleanup(codex.SetIsPidAliveFnForTest(func(int) bool { return false }))
//
// Used by internal/module/agent dispatcher integration tests (P2-T4 /
// P2-T7) to drive the alive/dead × pane-alive/pane-gone matrix without
// spawning real processes.
func SetIsPidAliveFnForTest(fn func(int) bool) (restore func()) {
	prev := *isPidAliveFn.Load()
	isPidAliveFn.Store(&fn)
	return func() {
		isPidAliveFn.Store(&prev)
	}
}

// SetProcessDeadPollIntervalForTest swaps the polling cadence and
// returns a restore func. Production code MUST NOT call this helper.
//
// Usage in cross-package tests:
//
//	t.Cleanup(codex.SetProcessDeadPollIntervalForTest(time.Millisecond))
//
// Used by integration tests that need detector emission to land within
// a test deadline rather than the production 1Hz cadence.
func SetProcessDeadPollIntervalForTest(d time.Duration) (restore func()) {
	prev := processDeadPollInterval.Load()
	processDeadPollInterval.Store(int64(d))
	return func() {
		processDeadPollInterval.Store(prev)
	}
}
