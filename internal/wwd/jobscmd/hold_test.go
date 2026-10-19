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
	slept   time.Duration
	polls   int
	// poolCost is how long a reading of the pool takes on the test's clock; poolErr fails the n-th reading (1-based)
	poolCost time.Duration
	poolErr  func(n int) error
	advance  func(time.Duration)
	// shown lets a test change what the facts carry (a stale hold of the same name, say)
	shown func(wire.Exception) wire.FactExc
}

func (h *holdAPI) StartExceptionOn(_ context.Context, host string, e wire.Exception) error {
	h.startOn, h.started = host, &e
	return h.startEr
}

func (h *holdAPI) Pool(context.Context) (wire.Pool, error) {
	h.polls++
	if h.advance != nil && h.poolCost > 0 {
		h.advance(h.poolCost)
	}
	if h.poolErr != nil {
		if err := h.poolErr(h.polls); err != nil {
			return wire.Pool{}, err
		}
	}
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
	clock := t0 // the test's clock: it moves when the command sleeps and when a reading of the pool takes time
	h.advance = func(d time.Duration) { clock = clock.Add(d) }
	env := Env{API: h, Now: func() time.Time { return clock }, Stdout: &so, Stderr: &se, Log: &lg,
		Sleep: func(d time.Duration) { clock = clock.Add(d); h.slept += d }, HoldWait: 10 * time.Second}
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
	want.Name = holdName("release 763")
	if !strings.HasPrefix(want.Name, "hold-release-763-") {
		t.Fatalf("name %q", want.Name)
	}
	if h.started == nil || !reflect.DeepEqual(*h.started, want) {
		t.Fatalf("started %+v, want %+v", h.started, want)
	}
	if h.polls != 3 {
		t.Errorf("it waited for the facts to show the hold: %d readings", h.polls)
	}
	if !strings.Contains(out, want.Name) || !strings.Contains(out, "air26") || !strings.Contains(out, "release 763") {
		t.Errorf("the output names the hold, the host and the reason: %q", out)
	}
	if !strings.Contains(log, "air26") || !strings.Contains(log, want.Name) || !strings.Contains(log, "release 763") {
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
	if !strings.HasPrefix(h.started.Name, "hold-mysql-restore-") {
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
	if !strings.Contains(errOut, holdName("stress")) || !strings.Contains(errOut, "facts") {
		t.Errorf("the message says it was saved but not seen: %q", errOut)
	}
}

// The facts must show *this* hold: a hold of the same name made before does not count. Each field is tried alone (the other three are
// the same), so a comparison that leaves one out is caught.
func TestHoldWaitsForTheMatchingHoldNotJustTheName(t *testing.T) {
	stale := map[string]func(wire.Exception) wire.FactExc{
		"another end": func(e wire.Exception) wire.FactExc {
			u := e.Until.Add(5 * time.Minute)
			return wire.FactExc{Name: e.Name, Since: t0, Until: &u, Allow: e.Allow, Flags: e.Flags}
		},
		"another allow": func(e wire.Exception) wire.FactExc {
			return wire.FactExc{Name: e.Name, Since: t0, Until: e.Until, Allow: &wire.HoldAllow{Classes: []wire.JobClass{wire.ClassDev}, Repos: []string{}}, Flags: e.Flags}
		},
		"no allow": func(e wire.Exception) wire.FactExc {
			return wire.FactExc{Name: e.Name, Since: t0, Until: e.Until, Flags: e.Flags}
		},
		"other flags": func(e wire.Exception) wire.FactExc {
			return wire.FactExc{Name: e.Name, Since: t0, Until: e.Until, Allow: e.Allow, Flags: []wire.HoldFlag{wire.HoldFlagExpectPressure}}
		},
		"no flags": func(e wire.Exception) wire.FactExc {
			return wire.FactExc{Name: e.Name, Since: t0, Until: e.Until, Allow: e.Allow}
		},
	}
	for name, shown := range stale {
		h := newHold("air26")
		h.shown = shown
		code, _, errOut, _ := runHold(h, "air26", "--for", "1h", "--reason", "stress", "--allow-class", "release", "--release")
		if code == ExitOK {
			t.Errorf("%s: an older hold of the same name was taken for this one", name)
		}
		if !strings.Contains(errOut, "facts") {
			t.Errorf("%s: message: %q", name, errOut)
		}
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

// The name comes from the reason: a readable slug of its letters and digits and a short hash of the whole reason. Two different reasons are
// two holds — also when their slugs are the same, or cut to the same 40 letters (R1) — and the same reason again is the same hold.
func TestHoldName(t *testing.T) {
	cases := map[string]string{
		"Release 763: purdex!": "hold-release-763-purdex-",
		"  A  b  ":             "hold-a-b-",
	}
	for reason, prefix := range cases {
		if got := holdName(reason); !strings.HasPrefix(got, prefix) {
			t.Errorf("%q: %q, want prefix %q", reason, got, prefix)
		}
	}
	long := strings.Repeat("release candidate verification ", 6)
	for _, reason := range []string{long, "x", "發版 763", "!!!", strings.Repeat("abc ", 40)} {
		got := holdName(reason)
		if len(got) > 64 {
			t.Errorf("%q: %d bytes", reason, len(got))
		}
		if err := (wire.Exception{Name: got, Until: &time.Time{}}).Validate(); err != nil && strings.Contains(err.Error(), "name") {
			t.Errorf("%q: %q is not a valid name", reason, got)
		}
		if holdName(reason) != got {
			t.Errorf("%q: the name must be the same every time (the same line again replaces the hold)", reason)
		}
	}
	distinct := [][2]string{
		{long + "alpha", long + "beta"}, // the first 40 letters are the same
		{"Release 763", "release-763"},  // the same slug
		{"發版 763", "發版 764"},            // no letters at all
		{"fix: mysql", "fix mysql"},     // punctuation only
	}
	for _, d := range distinct {
		if holdName(d[0]) == holdName(d[1]) {
			t.Errorf("%q and %q are one hold", d[0], d[1])
		}
	}
}

// Many reasons with one slug (what a name can hold ignores them: CJK text, punctuation) are still many holds: the hash is wide enough that
// a few hundred thousand of them do not meet (a 32-bit hash meets at about 77 000: R1 asked again, critic), and the longest name fits the
// 64 bytes an exception name may have.
func TestHoldNameDoesNotMeetAmongManyReasonsOfOneSlug(t *testing.T) {
	seen := map[string]int{}
	for i := 0; i < 300000; i++ {
		reason := "release " + string(rune(0x4e00+i%20000)) + string(rune(0x4e00+i/20000))
		n := holdName(reason)
		if j, ok := seen[n]; ok {
			t.Fatalf("reasons %d and %d are one hold: %s", j, i, n)
		}
		seen[n] = i
	}
	long := holdName(strings.Repeat("release candidate ", 20))
	if len(long) > 64 {
		t.Fatalf("%q is %d bytes: an exception name may have 64", long, len(long))
	}
}

// An option that is given with nothing in it is a mistake, not "no option": dropping it would turn a hold meant to let a class through
// into one that lets nothing through (R1).
func TestHoldRefusesAnEmptyAllowValue(t *testing.T) {
	for _, args := range [][]string{
		{"air26", "--for", "1h", "--reason", "x", "--allow-repo="},
		{"air26", "--for", "1h", "--reason", "x", "--allow-class="},
		{"air26", "--for", "1h", "--reason", "x", "--allow-class", " , "},
		{"air26", "--for", "1h", "--reason", "x", "--allow-class", "release", "--allow-repo", ","},
	} {
		h := newHold("air26")
		code, _, errOut, _ := runHold(h, args...)
		if code != ExitUsage || h.started != nil {
			t.Errorf("%v: code %d started %+v (%s)", args, code, h.started, errOut)
		}
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

// The wait is a deadline on the clock, not a count of readings: a pool that answers slowly does not stretch it (R2).
func TestHoldWaitEndsAtTheDeadlineWhateverTheReadingsCost(t *testing.T) {
	h := newHold("air26")
	h.show = -1
	h.poolCost = 2 * time.Second // every reading takes 2 s, then the command sleeps 1 s
	code, _, _, _ := runHold(h, "air26", "--for", "1h", "--reason", "slow")
	if code == ExitOK {
		t.Fatal("never shown is a failure")
	}
	total := time.Duration(h.polls)*h.poolCost + h.slept
	if total > 10*time.Second+h.poolCost+holdPoll {
		t.Errorf("waited %s of the test's clock for a 10 s wait (%d readings)", total, h.polls)
	}
}

// A reading that fails now and then is not the end of the wait: it is tried again until the deadline, and only then is the last error
// told. (A pool that answered once and then failed once must not make a hold that is in force look like a failure.)
func TestHoldWaitKeepsTryingThroughAFailedReading(t *testing.T) {
	h := newHold("air26")
	h.show = 2
	h.poolErr = func(n int) error {
		if n <= 2 {
			return &client.StatusError{Code: 503, Body: "busy"}
		}
		return nil
	}
	code, out, errOut, _ := runHold(h, "air26", "--for", "1h", "--reason", "flaky")
	if code != ExitOK {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}

	h = newHold("air26")
	h.show = 0
	h.poolErr = func(int) error { return &client.StatusError{Code: 503, Body: "busy"} }
	code, _, errOut, _ = runHold(h, "air26", "--for", "1h", "--reason", "down")
	if code == ExitOK || !strings.Contains(errOut, "busy") || !strings.Contains(errOut, "saved") {
		t.Fatalf("a pool that never answers: code %d %q (the last error and 'saved' are told)", code, errOut)
	}
}

// The reason goes into the output and into holds.log as one line: a control character in it (a newline above all) would write lines of
// its own, so it is refused (R2). The same for what is listed after --allow-*.
func TestHoldRefusesControlCharacters(t *testing.T) {
	for name, args := range map[string][]string{
		"newline in reason": {"air26", "--for", "1h", "--reason", "release\n2026-10-20 hold fake on mlab"},
		"CR in reason":      {"air26", "--for", "1h", "--reason", "a\rb"},
		"ESC in reason":     {"air26", "--for", "1h", "--reason", "a\x1b[31mred"},
		"C1 in reason":      {"air26", "--for", "1h", "--reason", "a\u0085b"},
		"control in repo":   {"air26", "--for", "1h", "--reason", "x", "--allow-repo", "pur\x07dex"},
	} {
		h := newHold("air26")
		code, _, _, log := runHold(h, args...)
		if code != ExitUsage || h.started != nil || log != "" {
			t.Errorf("%s: code %d started %+v log %q", name, code, h.started, log)
		}
	}
	// a reason with spaces, punctuation and non-ASCII text is fine
	h := newHold("air26")
	if code, _, errOut, _ := runHold(h, "air26", "--for", "1h", "--reason", "發版 763: purdex (rc1)"); code != ExitOK {
		t.Fatalf("an ordinary reason: %d %s", code, errOut)
	}
}
