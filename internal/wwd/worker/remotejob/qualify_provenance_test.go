package remotejob

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/labels"
)

const noQualifyTasks = `
[tasks.tsc]
run    = "make tsc"
args   = "none"
labels = ["os:darwin", "arch:arm64", "tool:node", "task:purdex/tsc"]
locks  = ["db"]
`

// A half is accepted only when four things hold besides its metadata: it was claimed from the host it names as submitter, the task
// declares a qualification, a candidate half runs on the submitter host, and a reference half runs on the task's reference host. Each
// one alone refuses an otherwise exact half.
func TestAQualifyHalfNeedsItsProvenance(t *testing.T) {
	root := snapshot(t, qualifyTasks)
	// all four hold: accepted
	for _, c := range []struct{ role, host string }{{labels.RoleReference, "air26"}, {labels.RoleCandidate, "mserv"}} {
		if _, err := PrepareAt(half(c.role, c.host, nil), root, t.TempDir(), c.host, "mserv"); err != nil {
			t.Errorf("%s half on %s: %v", c.role, c.host, err)
		}
	}
	// 1. claimed from another host than the submitter it names
	for _, src := range []string{"elsewhere", ""} {
		if _, err := PrepareAt(half(labels.RoleReference, "air26", nil), root, t.TempDir(), "air26", src); code(t, err) != CodeMetaMismatch {
			t.Errorf("claimed from %q: %v", src, err)
		}
	}
	// 2. the task has no qualification
	if _, err := PrepareAt(half(labels.RoleReference, "air26", nil), snapshot(t, noQualifyTasks), t.TempDir(), "air26", "mserv"); code(t, err) != CodeMetaMismatch {
		t.Errorf("task without qualify: %v", err)
	}
	// 3. a candidate half for a host that is not the submitter's
	if _, err := PrepareAt(half(labels.RoleCandidate, "air26", nil), root, t.TempDir(), "air26", "mserv"); code(t, err) != CodeMetaMismatch {
		t.Errorf("candidate half on another host: %v", err)
	}
	// 4. a reference half for a host that is not the task's reference
	if _, err := PrepareAt(half(labels.RoleReference, "mserv", nil), root, t.TempDir(), "mserv", "mserv"); code(t, err) != CodeMetaMismatch {
		t.Errorf("reference half on a host that is not the reference: %v", err)
	}
}
