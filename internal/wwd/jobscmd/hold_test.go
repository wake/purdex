package jobscmd

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// `pdx wwd hold` (spec §13.4, WWD-8 8b-3 PR-D): a line that needs a host quiet asks wwd. It saves the hold, then waits until the hold shows
// in that host's facts — the POST alone is not the proof, the worker decides from the facts (spec §13.4, "When a hold takes effect") —
// and a timeout is a failure.

// holdAPI is the fake wwd of a hold: StartExceptionOn records the call, and the pool shows the hold in the host's facts from the
// show-th reading on (0: at once, -1: never).
type holdAPI struct {
	*fakeAPI
	host    string
	started *wire.Exception
	startOn string
	startEr error
	show    int
	polls   int
	// shown lets a test change what the facts carry (a stale hold of the same name, say)
	shown func(wire.Exception) wire.FactExc
}

func (h *holdAPI) StartExceptionOn(_ context.Context, host string, e wire.Exception) error {
	h.startOn, h.started = host, &e
	return h.startEr
}

func (h *holdAPI) Pool(context.Context) (wire.Pool, error) {
	h.polls++
	p := wire.Pool{Peers: []wire.Peer{{Info: wire.Info{Alias: "mlab"}, Reachable: true, Facts: &wire.Facts{Exception: []wire.FactExc{}}}}}
	peer := wire.Peer{Info: wire.Info{Alias: h.host}, Reachable: true, Facts: &wire.Facts{Exception: []wire.FactExc{}}}
	if h.started != nil && h.show >= 0 && h.polls > h.show {
		fe := wire.FactExc{Name: h.started.Name, Since: t0, Until: h.started.Until, Allow: h.started.Allow, Flags: h.started.Flags}
		if h.shown != nil {
			fe = h.shown(*h.started)
		}
		peer.Facts.Exception = []wire.FactExc{fe}
	}
	p.Peers = append(p.Peers, peer)
	return p, nil
}

func runHold(h *holdAPI, args ...string) (code int, out, errOut, log string) {
	var so, se, lg bytes.Buffer
	slept := time.Duration(0)
	env := Env{API: h, Now: func() time.Time { return t0 }, Stdout: &so, Stderr: &se, Log: &lg,
		Sleep: func(d time.Duration) { slept += d }, HoldWait: 10 * time.Second}
	code = WWD(context.Background(), append([]string{"hold"}, args...), env)
	return code, so.String(), se.String(), lg.String()
}

func newHold(host string) *holdAPI { return &holdAPI{fakeAPI: &fakeAPI{}, host: host} }

func TestHoldSavesTheHoldOnTheNamedHostAndWaitsForTheFacts(t *testing.T) {
	h := newHold("air26")
	h.show = 2 // the third reading is the first to show it
	code, out, errOut, log := runHold(h, "air26", "--for", "2h", "--reason", "release 763",
		"--allow-class", "release", "--allow-class", "merge", "--allow-repo", "purdex", "--release", "--expect", "pressure")
	if code != ExitOK {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	if h.startOn != "air26" {
		t.Errorf("the hold goes to the host named: %q", h.startOn)
	}
	until := t0.Add(2 * time.Hour).UTC()
	want := wire.Exception{
		Name: "hold-release-763", Until: &until, Holds: []wire.Hold{wire.HoldHeavy, wire.HoldLight},
		Allow: &wire.HoldAllow{Classes: []wire.JobClass{wire.ClassRelease, wire.ClassMerge}, Repos: []string{"purdex"}},
		Flags: []wire.HoldFlag{wire.HoldFlagRelease, wire.HoldFlagExpectPressure},
	}
	if h.started == nil || !reflect.DeepEqual(*h.started, want) {
		t.Fatalf("started %+v, want %+v", h.started, want)
	}
	if h.polls != 3 {
		t.Errorf("it waited for the facts to show the hold: %d readings", h.polls)
	}
	if !strings.Contains(out, "hold-release-763") || !strings.Contains(out, "air26") || !strings.Contains(out, "release 763") {
		t.Errorf("the output names the hold, the host and the reason: %q", out)
	}
	if !strings.Contains(log, "air26") || !strings.Contains(log, "hold-release-763") || !strings.Contains(log, "release 763") {
		t.Errorf("the log keeps who and why: %q", log)
	}
}

// With none of the options that need a newer wwd the request is the old one — no allow, no flags — so it works on a host whose wwd
// predates holds, and it holds everything.
func TestHoldWithoutOptionsIsAPlainException(t *testing.T) {
	h := newHold("mserv")
	code, out, errOut, _ := runHold(h, "mserv", "--for", "30m", "--reason", "mysql restore")
	if code != ExitOK {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	if h.started == nil || h.started.Allow != nil || len(h.started.Flags) != 0 || len(h.started.Holds) != 2 {
		t.Fatalf("started %+v", h.started)
	}
	if h.started.Name != "hold-mysql-restore" {
		t.Errorf("name %q", h.started.Name)
	}
}

// Waiting for the facts is the success condition: a hold that never shows is a failure, even though it was saved.
func TestHoldFailsWhenTheFactsNeverShowIt(t *testing.T) {
	h := newHold("air26")
	h.show = -1
	code, out, errOut, _ := runHold(h, "air26", "--for", "1h", "--reason", "stress")
	if code == ExitOK {
		t.Fatalf("a timeout must not be success: %q", out)
	}
	if !strings.Contains(errOut, "hold-stress") || !strings.Contains(errOut, "facts") {
		t.Errorf("the message says it was saved but not seen: %q", errOut)
	}
}

// The facts must show *this* hold: a hold of the same name made before (other allow, other end) does not count.
func TestHoldWaitsForTheMatchingHoldNotJustTheName(t *testing.T) {
	h := newHold("air26")
	h.shown = func(e wire.Exception) wire.FactExc {
		old := t0.Add(5 * time.Minute)
		return wire.FactExc{Name: e.Name, Since: t0, Until: &old, Allow: &wire.HoldAllow{Classes: []wire.JobClass{wire.ClassDev}, Repos: []string{}}}
	}
	code, _, errOut, _ := runHold(h, "air26", "--for", "1h", "--reason", "stress", "--allow-class", "release")
	if code == ExitOK {
		t.Fatalf("an older hold of the same name was taken for this one")
	}
	if !strings.Contains(errOut, "facts") {
		t.Errorf("message: %q", errOut)
	}
}

// A wwd that predates holds answers 400 to the unknown field: said plainly.
func TestHoldTellsAnOldWWDFromABadRequest(t *testing.T) {
	h := newHold("air19")
	h.startEr = &client.StatusError{Code: 400, Body: `{"error":"json: unknown field \"allow\""}`}
	code, _, errOut, _ := runHold(h, "air19", "--for", "1h", "--reason", "x", "--allow-class", "release")
	if code == ExitOK || !strings.Contains(errOut, "does not know holds yet") {
		t.Fatalf("code %d: %q", code, errOut)
	}
}

func TestHoldRefusesWhatIsNotAHold(t *testing.T) {
	cases := map[string][]string{
		"no host":             {"--for", "1h", "--reason", "x"},
		"no duration":         {"air26", "--reason", "x"},
		"no reason":           {"air26", "--for", "1h"},
		"empty reason":        {"air26", "--for", "1h", "--reason", "  "},
		"reason too long":     {"air26", "--for", "1h", "--reason", strings.Repeat("x", 201)},
		"zero duration":       {"air26", "--for", "0s", "--reason", "x"},
		"negative duration":   {"air26", "--for", "-5m", "--reason", "x"},
		"not a duration":      {"air26", "--for", "tomorrow", "--reason", "x"},
		"unknown class":       {"air26", "--for", "1h", "--reason", "x", "--allow-class", "urgent"},
		"expect not pressure": {"air26", "--for", "1h", "--reason", "x", "--expect", "memory"},
		"unknown option":      {"air26", "--for", "1h", "--reason", "x", "--bogus", "1"},
		"two hosts":           {"air26", "mserv", "--for", "1h", "--reason", "x"},
	}
	for name, args := range cases {
		h := newHold("air26")
		code, _, errOut, _ := runHold(h, args...)
		if code != ExitUsage || h.started != nil {
			t.Errorf("%s: code %d started %+v (%s)", name, code, h.started, errOut)
		}
	}
}

// The name comes from the reason: a slug that is a valid exception name; a reason with nothing a name can hold (Chinese, say) gets a short
// hash, so two different reasons are two different holds.
func TestHoldName(t *testing.T) {
	cases := map[string]string{
		"Release 763: purdex!":     "hold-release-763-purdex",
		"  A  b  ":                 "hold-a-b",
		strings.Repeat("abc ", 40): "",
	}
	for reason, want := range cases {
		got := holdName(reason)
		if want != "" && got != want {
			t.Errorf("%q: %q, want %q", reason, got, want)
		}
		if err := (wire.Exception{Name: got, Until: &time.Time{}}).Validate(); err != nil && strings.Contains(err.Error(), "name") {
			t.Errorf("%q: %q is not a valid name", reason, got)
		}
		if len(got) > 64 {
			t.Errorf("%q: %d bytes", reason, len(got))
		}
	}
	a, b := holdName("發版 763"), holdName("發版 764")
	if a == b || !strings.HasPrefix(a, "hold-") || len(a) > 64 {
		t.Errorf("a name for a reason with no letters: %q %q", a, b)
	}
	if holdName("發版 763") != a {
		t.Error("the name must be the same every time (the same line again replaces the hold)")
	}
}

// `release` as a word is the label command (let go of an earned task label) and stays that; a hold ends with `exception stop`.
func TestHoldDoesNotTakeOverRelease(t *testing.T) {
	f := &fakeAPI{}
	code, _, _ := runWWD(f, "release", "task:purdex/test")
	if code != ExitOK || len(f.labelCalls) != 1 || f.labelCalls[0] != "release task:purdex/test" {
		t.Fatalf("code %d calls %v", code, f.labelCalls)
	}
}

// A hold of another host ends where it was made: `exception stop <name> --host <host>` goes to that host through the local wwd.
func TestExceptionStopCanNameTheHost(t *testing.T) {
	f := &fakeAPI{}
	if code, _, errOut := runWWD(f, "exception", "stop", "hold-x", "--host", "air26"); code != ExitOK {
		t.Fatalf("code %d: %s", code, errOut)
	}
	if f.stopped != "hold-x" || f.stoppedOn != "air26" {
		t.Fatalf("stopped %q on %q", f.stopped, f.stoppedOn)
	}
	f = &fakeAPI{}
	if code, _, _ := runWWD(f, "exception", "stop", "hold-x"); code != ExitOK || f.stoppedOn != "" {
		t.Fatalf("without --host it is this host: %q", f.stoppedOn)
	}
	if code, _, _ := runWWD(&fakeAPI{}, "exception", "stop", "hold-x", "--host", "a\nb"); code != ExitUsage {
		t.Fatalf("a bad alias: %d", code)
	}
}
