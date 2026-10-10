package teammod

import (
	"bytes"
	"log"
	"testing"
)

// captureEventsLog sends the process log to a buffer for the test. The strict-broadcast tests below fill a subscriber on
// purpose, and the events hub logs "… frame could not be queued (send buffer full); closing the connection" when it drops
// it. That line is expected output of a passing test, but go test prints a package's log whenever any test fails, so it
// sat beside every unrelated failure and read like the cause (#2210). Nothing in this package runs in parallel, so the
// process-wide writer is safe to swap.
func captureEventsLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(prev) // first: nothing below may be swallowed again
		if t.Failed() {
			t.Logf("process log:\n%s", buf.String()) // a failing test keeps the debug log it would have had
		}
	})
	return &buf
}
