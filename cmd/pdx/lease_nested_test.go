package main

import (
	"context"
	"testing"

	"github.com/wake/purdex/internal/resources"
)

func nestedDaemon() *fakeLeaseDaemon {
	return &fakeLeaseDaemon{post: func(resources.LeaseRequest) (int, any) {
		return 200, resources.LeaseResponse{State: resources.StateHeld, Granted: true, Mode: "lease", Nested: "outer-1"}
	}}
}

// A nested answer (P2f) is held with no row of its own: acquire prints the outer
// id in `nested`, an empty `id`, and there is nothing to poll or release.
func TestLeaseAcquire_NestedAnswerPrintsNested(t *testing.T) {
	fixedHolder(t)
	d := nestedDaemon()
	code, stdout, stderr := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build")
	l := parseAcquireLine(t, stdout)
	if code != ExitOK || stderr != "" || !l.Granted || l.ID != "" || l.Nested != "outer-1" {
		t.Errorf("code=%d stderr=%q line=%+v", code, stderr, l)
	}
	if _, polls, deletes := d.snapshot(); polls != 0 || len(deletes) != 0 {
		t.Errorf("polls=%d deletes=%v", polls, deletes)
	}
}

// A nested answer runs the command with nothing to release: the answer has no
// row.
func TestLeaseRun_NestedAnswerRunsWithoutRelease(t *testing.T) {
	d := nestedDaemon()
	code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build"}, "sh", "-c", "exit 4")
	posts, polls, deletes := d.snapshot()
	if code != 4 || stderr != "" || len(posts) != 1 || polls != 0 || len(deletes) != 0 {
		t.Errorf("code=%d stderr=%q posts=%d polls=%d deletes=%v", code, stderr, len(posts), polls, deletes)
	}
}
