// internal/module/peers/version_warning_test.go
package peers

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent"
)

// registryFixtureWithVersion returns a minimal well-formed registry file
// for pid, with the given version string and fixture76973ProcStart as its
// procStart (so allLiveLiveness(fixture76973ProcStart) treats it as live).
func registryFixtureWithVersion(pid int, version string) string {
	p := strconv.Itoa(pid)
	return `{"pid":` + p + `,"sessionId":"sess-` + p + `","cwd":"/w","procStart":"Sun Sep 13 15:22:36 2026","messagingSocketPath":"/tmp/` + p + `.sock","version":"` + version + `"}`
}

// versionWarningFixture builds a module over dir whose log lines are
// captured through the module's own log seam (m.logf), not the global
// logger — the warning must go where every other module line goes.
func versionWarningFixture(t *testing.T, dir string, calls int) *moduleFixture {
	t.Helper()
	times := make([]time.Time, calls)
	for i := range times {
		times[i] = time.Unix(0, 0)
	}
	return newTestModuleWith(t, fixtureOpts{
		core:        newTestCore(t, "mlab:abc123", "mlab"),
		sessions:    &fakeSessions{sessions: nil},
		owners:      &fakeOwners{owners: map[string]agent.PaneOwner{}},
		registryDir: dir,
		liveness:    allLiveLiveness(fixture76973ProcStart),
		clock:       &fakeClock{times: times},
		budget:      2 * time.Second,
	})
}

// TestLocalEnvelope_VersionWarning_FiresOnceAcrossTwoCalls pins the
// one-shot version warning: a registry entry reporting a Claude Code
// version newer than ccuds.VerifiedCCVersion logs a warning line through
// the module's logger, but only once even across multiple /api/peers
// calls that see the same version again.
func TestLocalEnvelope_VersionWarning_FiresOnceAcrossTwoCalls(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "76980.json", registryFixtureWithVersion(76980, "2.1.271"))
	f := versionWarningFixture(t, dir, 2)

	if rr := doGetPeers(t, f.m, "/api/peers"); rr.Code != http.StatusOK {
		t.Fatalf("call 1: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if rr := doGetPeers(t, f.m, "/api/peers"); rr.Code != http.StatusOK {
		t.Fatalf("call 2: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	logs := strings.Join(f.logs.all(), "\n")
	got := strings.Count(logs, "2.1.271")
	if got != 1 {
		t.Errorf("occurrences of the warned version in the module log = %d, want 1 (fires once); log=%q", got, logs)
	}
	if !strings.Contains(logs, "is newer than the last verified") {
		t.Errorf("module log lacks the warning line; log=%q", logs)
	}
}

// TestLocalEnvelope_VersionWarning_NeverFiresForVerifiedVersion pins the
// negative case: a registry entry reporting exactly VerifiedCCVersion
// (2.1.270) never logs the warning.
func TestLocalEnvelope_VersionWarning_NeverFiresForVerifiedVersion(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "76973.json", fixture76973) // version 2.1.270
	f := versionWarningFixture(t, dir, 1)

	if rr := doGetPeers(t, f.m, "/api/peers"); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	if logs := strings.Join(f.logs.all(), "\n"); strings.Contains(logs, "is newer than the last verified") {
		t.Errorf("warning logged for the verified version 2.1.270: log=%q", logs)
	}
}
