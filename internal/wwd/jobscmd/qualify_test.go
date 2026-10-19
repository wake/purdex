package jobscmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// pdx wwd qualify <repo>/<task> asks this host's wwd to start a pair and says which jobs it queued; it does not wait for them.
func TestWWDQualifyQueuesAPair(t *testing.T) {
	f := &fakeAPI{}
	code, out, _ := runWWD(f, "qualify", "purdex/tsc")
	if code != 0 || len(f.qualified) != 1 || f.qualified[0] != "purdex/tsc" {
		t.Fatalf("code %d asked %v", code, f.qualified)
	}
	for _, want := range []string{"task:purdex/tsc", "j_cand", "j_ref", "--status"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %q", want, out)
		}
	}
}

// A task that is not <repo>/<task>, none, two, or --status with a task is a usage error and asks nothing of the wwd.
func TestWWDQualifyWantsOneTaskOrStatus(t *testing.T) {
	for _, args := range [][]string{{"qualify"}, {"qualify", "tsc"}, {"qualify", "purdex/tsc", "purdex/spa"}, {"qualify", "--status", "purdex/tsc"}, {"qualify", "--json", "purdex/tsc"}, {"qualify", "task:purdex/tsc"}} {
		f := &fakeAPI{}
		if code, _, errOut := runWWD(f, args...); code != ExitUsage || len(f.qualified) != 0 || errOut == "" {
			t.Errorf("%v: code %d asked %v", args, code, f.qualified)
		}
	}
}

// A refusal is shown in the wwd's own words; an unknown task is a usage error and the others are a failure of the request.
func TestWWDQualifyShowsWhyItWasRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		code int
		body string
		want int
	}{
		"unknown":   {404, `{"error":"no task \"purdex/nope\" with a qualify in this host's prepared clones"}`, ExitUsage},
		"reference": {409, `{"error":"this host is the task's reference: it holds the label without qualifying"}`, ExitFailed},
		"failure":   {500, `{"error":"preparing the snapshot: no commit"}`, ExitFailed},
	} {
		f := &fakeAPI{err: &client.StatusError{Code: tc.code, Body: tc.body}}
		code, _, errOut := runWWD(f, "qualify", "purdex/tsc")
		var body struct{ Error string }
		_ = json.Unmarshal([]byte(tc.body), &body)
		if code != tc.want || !strings.Contains(errOut, body.Error) {
			t.Errorf("%s: code %d err %q", name, code, errOut)
		}
	}
	f := &fakeAPI{err: client.ErrUnavailable}
	if code, _, _ := runWWD(f, "qualify", "purdex/tsc"); code != ExitNoWWD {
		t.Errorf("no wwd: code %d", code)
	}
}

func TestWWDQualifyStatus(t *testing.T) {
	f := &fakeAPI{qualifyStatus: wire.QualifyStatus{
		InFlight: []wire.QualifyStarted{{Label: "task:purdex/tsc", Candidate: "j_1", Reference: "j_2"}},
		Recent:   []wire.QualifyRecord{{Label: "task:purdex/spa-related", Outcome: "mismatch", Why: "exit 1 on the candidate, 0 on the reference", At: time.Unix(100, 0), Candidate: "j_3", Reference: "j_4"}},
	}}
	code, out, _ := runWWD(f, "qualify", "--status")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"task:purdex/tsc", "running", "j_1 j_2", "task:purdex/spa-related", "mismatch", "exit 1 on the candidate"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	code, out, _ = runWWD(f, "qualify", "--status", "--json")
	var got wire.QualifyStatus
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got.Recent) != 1 {
		t.Fatalf("json: code %d out %q", code, out)
	}
	empty := &fakeAPI{}
	if _, out, _ := runWWD(empty, "qualify", "--status"); !strings.Contains(out, "no qualification pairs yet") {
		t.Errorf("empty status: %q", out)
	}
}
