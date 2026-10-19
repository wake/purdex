package jobscmd

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/client"
)

// pdx wwd quarantine|release <label> ask this host's wwd to change its own label, and say what happened.
func TestWWDQuarantineAndRelease(t *testing.T) {
	f := &fakeAPI{}
	code, out, _ := runWWD(f, "quarantine", "task:purdex/tsc")
	if code != 0 || len(f.labelCalls) != 1 || f.labelCalls[0] != "quarantine task:purdex/tsc" || !strings.Contains(out, "quarantined") {
		t.Fatalf("quarantine: code %d calls %v out %q", code, f.labelCalls, out)
	}
	code, out, _ = runWWD(f, "release", "task:purdex/tsc")
	if code != 0 || f.labelCalls[1] != "release task:purdex/tsc" || !strings.Contains(out, "released") {
		t.Fatalf("release: code %d calls %v out %q", code, f.labelCalls, out)
	}
}

// What is not a task label, or not one label, is a usage error and asks nothing of the wwd.
func TestWWDQuarantineWantsOneTaskLabel(t *testing.T) {
	for _, args := range [][]string{{"quarantine"}, {"quarantine", "os:linux"}, {"release", "task:a/b", "task:c/d"}, {"release", "--json", "task:a/b"}, {"quarantine", "tsc"}} {
		f := &fakeAPI{}
		if code, _, errOut := runWWD(f, args...); code != ExitUsage || len(f.labelCalls) != 0 || errOut == "" {
			t.Errorf("%v: code %d calls %v", args, code, f.labelCalls)
		}
	}
}

// A host with no such label is told so (not a bare 404), and a wwd that cannot be reached is the usual failure.
func TestWWDQuarantineSaysWhenThereIsNoSuchLabel(t *testing.T) {
	f := &fakeAPI{err: &client.StatusError{Code: 404, Body: "no such earned label"}}
	code, _, errOut := runWWD(f, "quarantine", "task:purdex/tsc")
	if code != ExitFailed || !strings.Contains(errOut, "no earned label task:purdex/tsc") {
		t.Fatalf("code %d err %q", code, errOut)
	}
}
