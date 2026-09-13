// internal/module/peers/version_warning_test.go
package peers

import (
	"bytes"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent"
)

// logCaptureBuffer is a thread-safe wrapper around bytes.Buffer for
// capturing log.Printf output in tests (mirrors
// internal/module/agent/handler_devlog_test.go's devLogBuffer).
type logCaptureBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logCaptureBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logCaptureBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog redirects the standard logger's output to an in-memory buffer
// for the duration of the test, restoring it via t.Cleanup.
func captureLog(t *testing.T) *logCaptureBuffer {
	t.Helper()
	buf := &logCaptureBuffer{}
	prev := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return buf
}

// registryFixtureWithVersion returns a minimal well-formed registry file
// for pid, with the given version string and fixture76973ProcStart as its
// procStart (so allLiveLiveness(fixture76973ProcStart) treats it as live).
func registryFixtureWithVersion(pid int, version string) string {
	p := strconv.Itoa(pid)
	return `{"pid":` + p + `,"sessionId":"sess-` + p + `","cwd":"/w","procStart":"Sun Sep 13 15:22:36 2026","messagingSocketPath":"/tmp/` + p + `.sock","version":"` + version + `"}`
}

// TestLocalEnvelope_VersionWarning_FiresOnceAcrossTwoCalls pins the
// one-shot version warning: a registry entry reporting a Claude Code
// version newer than ccuds.VerifiedCCVersion logs a warning line, but only
// once even across multiple /api/peers calls that see the same version
// again.
func TestLocalEnvelope_VersionWarning_FiresOnceAcrossTwoCalls(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "76980.json", registryFixtureWithVersion(76980, "2.1.271"))

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0), time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	buf := captureLog(t)

	if rr := doGetPeers(t, m, "/api/peers"); rr.Code != http.StatusOK {
		t.Fatalf("call 1: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if rr := doGetPeers(t, m, "/api/peers"); rr.Code != http.StatusOK {
		t.Fatalf("call 2: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	logs := buf.String()
	got := strings.Count(logs, "2.1.271")
	if got != 1 {
		t.Errorf("occurrences of the warned version in the log = %d, want 1 (fires once); log=%q", got, logs)
	}
}

// TestLocalEnvelope_VersionWarning_NeverFiresForVerifiedVersion pins the
// negative case: a registry entry reporting exactly VerifiedCCVersion
// (2.1.270) never logs the warning.
func TestLocalEnvelope_VersionWarning_NeverFiresForVerifiedVersion(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "76973.json", fixture76973) // version 2.1.270

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	buf := captureLog(t)

	if rr := doGetPeers(t, m, "/api/peers"); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	if logs := buf.String(); strings.Contains(logs, "is newer than the last verified") {
		t.Errorf("warning logged for the verified version 2.1.270: log=%q", logs)
	}
}
