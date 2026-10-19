package jobscmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func earnedList() wire.EarnedLabelList {
	return wire.EarnedLabelList{Host: "mserv", Labels: []wire.EarnedLabel{
		{Label: "task:purdex/tsc", State: wire.EarnedLabelEarned, Fingerprint: map[string]string{"os_version": "26.04", "node": "24.14.0"}, GrantedAt: time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)},
		{Label: "task:purdex/lint", State: wire.EarnedLabelQuarantined, Fingerprint: map[string]string{"node": "24.14.0"}},
	}}
}

func TestWwdLabelsListsEarnedAndQuarantinedWithFingerprints(t *testing.T) {
	code, out, _ := runWWD(&fakeAPI{earned: earnedList()}, "labels")
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"task:purdex/tsc", "earned", "node=24.14.0 os_version=26.04", "task:purdex/lint", "quarantined", "not advertised"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestWwdLabelsEmptyAndJSON(t *testing.T) {
	code, out, _ := runWWD(&fakeAPI{earned: wire.EarnedLabelList{Host: "mlab", Labels: []wire.EarnedLabel{}}}, "labels")
	if code != ExitOK || !strings.Contains(out, "mlab has earned no task labels") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = runWWD(&fakeAPI{earned: earnedList()}, "labels", "--json")
	var back wire.EarnedLabelList
	if code != ExitOK || json.Unmarshal([]byte(out), &back) != nil || len(back.Labels) != 2 {
		t.Fatalf("%d %q", code, out)
	}
}

// A wwd that predates the route (404) is "none yet", not a failure; --json still gives the empty list.
func TestWwdLabelsOlderWwdIsNoneYet(t *testing.T) {
	f := &fakeAPI{earnedUnknown: true, earned: wire.EarnedLabelList{Labels: []wire.EarnedLabel{}}}
	code, out, _ := runWWD(f, "labels")
	if code != ExitOK || !strings.Contains(out, "predates earned labels") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = runWWD(f, "labels", "--json")
	var back wire.EarnedLabelList
	if code != ExitOK || json.Unmarshal([]byte(out), &back) != nil || len(back.Labels) != 0 {
		t.Fatalf("%d %q", code, out)
	}
}

func TestWwdLabelsRefusesArgumentsAndReportsFailure(t *testing.T) {
	if code, _, _ := runWWD(&fakeAPI{}, "labels", "extra"); code != ExitUsage {
		t.Fatalf("code %d", code)
	}
	if code, _, errOut := runWWD(&fakeAPI{err: errors.New("down")}, "labels"); code == ExitOK || !strings.Contains(errOut, "wwd labels") {
		t.Fatalf("%d %q", code, errOut)
	}
}
