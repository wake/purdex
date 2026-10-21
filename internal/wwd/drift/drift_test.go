package drift

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func TestToolKey(t *testing.T) {
	for need, want := range map[string]string{"tool:node24": "node", "tool:go1.26": "go", "tool:pnpm10": "pnpm", "tool:php84": "php", "tool:xcode16": "xcode"} {
		if got, ok := ToolKey(need); !ok || got != want {
			t.Errorf("%s: %q %v, want %q", need, got, ok, want)
		}
	}
	for _, need := range []string{"os:darwin", "repo:x", "tool:", "tool:24", "node24"} {
		if k, ok := ToolKey(need); ok {
			t.Errorf("%s gave key %q", need, k)
		}
	}
}

func TestKeysForIsTheOSVersionAndTheToolsTheTaskNeeds(t *testing.T) {
	got := KeysFor([]string{"os:darwin", "tool:pnpm10", "tool:node24", "tool:node24", "repo:purdex"})
	if want := []string{"node", "os_version", "pnpm"}; !slices.Equal(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
	if got := KeysFor(nil); !slices.Equal(got, []string{"os_version"}) {
		t.Fatalf("%v", got)
	}
}

func TestChangedNamesTheKeysThatMoved(t *testing.T) {
	stored := map[string]string{"os_version": "26.4", "node": "24.14.0", "go": "1.26.0"}
	keys := []string{"node", "os_version"}
	if got := Changed(stored, map[string]string{"os_version": "26.4", "node": "24.14.0", "go": "9.9"}, keys); len(got) != 0 {
		t.Fatalf("a tool the label does not need moved: %v", got)
	}
	if got := Changed(stored, map[string]string{"os_version": "26.4", "node": "24.15.0"}, keys); !slices.Equal(got, []string{"node"}) {
		t.Fatalf("%v", got)
	}
	if got := Changed(stored, map[string]string{"os_version": "26.5", "node": "24.14.0"}, keys); !slices.Equal(got, []string{"os_version"}) {
		t.Fatalf("%v", got)
	}
	// a tool that is gone has changed, and so has one the stored fingerprint never had
	if got := Changed(stored, map[string]string{"os_version": "26.4"}, keys); !slices.Equal(got, []string{"node"}) {
		t.Fatalf("gone: %v", got)
	}
	if got := Changed(map[string]string{"os_version": "26.4"}, map[string]string{"os_version": "26.4", "node": "24.14.0"}, keys); !slices.Equal(got, []string{"node"}) {
		t.Fatalf("never stored: %v", got)
	}
}

func earned(label, state string, fp map[string]string) wire.EarnedLabel {
	return wire.EarnedLabel{Label: label, State: state, Fingerprint: fp}
}

func needsOf(m map[string][]string) func(string) ([]string, bool) {
	return func(l string) ([]string, bool) { n, ok := m[l]; return n, ok }
}

func TestRevokedPicksTheEarnedLabelsThatDrifted(t *testing.T) {
	fp := map[string]string{"os_version": "26.4", "node": "24.14.0", "pnpm": "10.23.0"}
	rows := []wire.EarnedLabel{
		earned("task:purdex/tsc", wire.EarnedLabelEarned, fp),
		earned("task:purdex/spa", wire.EarnedLabelEarned, fp),
		earned("task:purdex/lint", wire.EarnedLabelQuarantined, fp), // already out
		earned("task:purdex/gone", wire.EarnedLabelEarned, fp),      // the task is no longer known
	}
	needs := needsOf(map[string][]string{
		"task:purdex/tsc":  {"tool:node24"},
		"task:purdex/spa":  {"tool:node24", "tool:pnpm10"},
		"task:purdex/lint": {"tool:node24"},
	})
	now := map[string]string{"os_version": "26.4", "node": "24.14.0", "pnpm": "10.24.0"} // pnpm moved
	got := Revoked(rows, needs, now)
	if len(got) != 1 || got[0].Label != "task:purdex/spa" || !slices.Equal(got[0].Keys, []string{"pnpm"}) {
		t.Fatalf("only the label that needs pnpm: %+v", got)
	}
	now["os_version"] = "26.5" // the OS moves: every label that is earned and known goes (the quarantined and the unknown are left)
	got = Revoked(rows, needs, now)
	if len(got) != 2 || got[0].Label != "task:purdex/spa" || got[1].Label != "task:purdex/tsc" {
		t.Fatalf("%+v", got)
	}
}

type fakeEarned struct {
	mu      sync.Mutex
	rows    []wire.EarnedLabel
	readErr error
	delErr  map[string]error
	deleted []string
}

func (f *fakeEarned) EarnedLabels() ([]wire.EarnedLabel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.rows), f.readErr
}

func (f *fakeEarned) DeleteEarnedLabel(l string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.delErr[l]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, l)
	f.rows = slices.DeleteFunc(f.rows, func(e wire.EarnedLabel) bool { return e.Label == l })
	return nil
}

type fakeCache struct {
	calls    [][2]string
	err      error
	failures int // the first calls fail with this many errors, then it works
}

func (c *fakeCache) DeleteCacheByLabelAndHost(label, host string) (int, error) {
	c.calls = append(c.calls, [2]string{label, host})
	if c.failures > 0 {
		c.failures--
		return 0, errors.New("database is locked")
	}
	return 3, c.err
}

// tasksCalled counts how many times a watcher asked for the tasks.
var tasksCalled int

func newWatcher(e *fakeEarned, c *fakeCache, revoked *int, extra func() []Task) *Watcher {
	tasksCalled = 0
	return NewWatcher(Config{
		Host: "mserv", Earned: e, Cache: c,
		Tasks: func() []Task {
			tasksCalled++
			out := []Task{{Label: "task:purdex/tsc", Needs: []string{"tool:node24"}}, {Label: "task:purdex/spa", Needs: []string{"tool:node24", "tool:pnpm10"}}}
			if extra != nil {
				out = append(out, extra()...)
			}
			return out
		},
		Revoked: func() { *revoked++ },
	})
}

// referenceTask is a task this host (mserv) is the reference for.
func referenceTask(label string, needs ...string) Task {
	return Task{Label: label, Needs: needs, IsReference: true}
}

var fpNow = map[string]string{"os_version": "26.4", "node": "24.14.0", "pnpm": "10.23.0"}

func TestTheWatcherRevokesDropsItsOwnEntriesAndTellsTheDaemonOnce(t *testing.T) {
	e := &fakeEarned{rows: []wire.EarnedLabel{
		earned("task:purdex/tsc", wire.EarnedLabelEarned, fpNow),
		earned("task:purdex/spa", wire.EarnedLabelEarned, fpNow),
	}}
	c := &fakeCache{}
	var revoked int
	w := newWatcher(e, c, &revoked, nil)

	w.OnFacts(fpNow)
	if len(e.deleted) != 0 || revoked != 0 || len(c.calls) != 0 {
		t.Fatalf("nothing moved but the watcher acted: %v %d %v", e.deleted, revoked, c.calls)
	}
	w.OnFacts(map[string]string{"os_version": "26.4", "node": "24.15.0", "pnpm": "10.23.0"}) // node moved: both labels need node
	if !slices.Equal(e.deleted, []string{"task:purdex/spa", "task:purdex/tsc"}) {
		t.Fatalf("revoked %v", e.deleted)
	}
	if revoked != 1 {
		t.Fatalf("the daemon was told %d times, want once", revoked)
	}
	for _, call := range c.calls {
		if call[1] != "mserv" {
			t.Errorf("entries dropped for %v: only this host's own may go", call)
		}
	}
	if len(c.calls) != 2 {
		t.Fatalf("%v", c.calls)
	}
	w.OnFacts(map[string]string{"os_version": "26.4", "node": "24.15.0", "pnpm": "10.23.0"}) // the same record again: nothing is left to revoke
	if revoked != 1 || len(e.deleted) != 2 {
		t.Fatalf("acted twice: %d %v", revoked, e.deleted)
	}
}

// A store that cannot be read revokes nothing on a guess; one that cannot delete one label still lets the others go and the daemon be told.
func TestTheWatcherNeverRevokesOnAGuessAndKeepsGoingPastAFailure(t *testing.T) {
	e := &fakeEarned{rows: []wire.EarnedLabel{earned("task:purdex/tsc", wire.EarnedLabelEarned, fpNow)}, readErr: errors.New("disk I/O error")}
	c := &fakeCache{}
	var revoked int
	w := newWatcher(e, c, &revoked, nil)
	moved := map[string]string{"os_version": "27.0", "node": "24.14.0"}
	w.OnFacts(moved)
	if len(e.deleted) != 0 || revoked != 0 {
		t.Fatalf("revoked without reading the table: %v %d", e.deleted, revoked)
	}
	e.readErr = nil
	e.rows = []wire.EarnedLabel{earned("task:purdex/tsc", wire.EarnedLabelEarned, fpNow), earned("task:purdex/spa", wire.EarnedLabelEarned, fpNow)}
	e.delErr = map[string]error{"task:purdex/spa": errors.New("database is locked")}
	w.OnFacts(moved)
	if !slices.Equal(e.deleted, []string{"task:purdex/tsc"}) || revoked != 1 {
		t.Fatalf("%v %d", e.deleted, revoked)
	}
	e.delErr = nil
	w.OnFacts(moved) // the one that failed is tried again on the next record
	if !slices.Equal(e.deleted, []string{"task:purdex/tsc", "task:purdex/spa"}) || revoked != 2 {
		t.Fatalf("%v %d", e.deleted, revoked)
	}
}

func TestACacheFailureDoesNotKeepALabel(t *testing.T) {
	e := &fakeEarned{rows: []wire.EarnedLabel{earned("task:purdex/tsc", wire.EarnedLabelEarned, fpNow)}}
	c := &fakeCache{err: errors.New("database is locked")}
	var revoked int
	newWatcher(e, c, &revoked, nil).OnFacts(map[string]string{"os_version": "27.0", "node": "24.14.0"})
	if len(e.deleted) != 1 || revoked != 1 {
		t.Fatalf("the label stayed because the cache could not be cleaned: %v %d", e.deleted, revoked)
	}
}

// A reference host earned nothing and loses nothing; its drift drops only the reuse entries it made, and nobody's label is touched.
func TestAReferenceHostsDriftDropsOnlyItsOwnEntries(t *testing.T) {
	e := &fakeEarned{rows: []wire.EarnedLabel{earned("task:purdex/other", wire.EarnedLabelEarned, fpNow)}}
	c := &fakeCache{}
	var revoked int
	// tsc is held as the reference here, so the base list's earned-style tsc is replaced by the reference form
	refs := func() []Task { return []Task{referenceTask("task:purdex/tsc", "tool:node24")} }
	w := newWatcher(e, c, &revoked, refs)

	w.OnFacts(fpNow) // first sight: noted only
	if len(c.calls) != 0 {
		t.Fatalf("%v", c.calls)
	}
	w.OnFacts(map[string]string{"os_version": "26.4", "node": "24.14.0", "pnpm": "11.0.0"}) // a tool the task does not need moved
	if len(c.calls) != 0 {
		t.Fatalf("an unrelated tool dropped entries: %v", c.calls)
	}
	w.OnFacts(map[string]string{"os_version": "26.4", "node": "24.15.0", "pnpm": "11.0.0"}) // node moved
	if len(c.calls) != 1 || c.calls[0] != [2]string{"task:purdex/tsc", "mserv"} {
		t.Fatalf("%v", c.calls)
	}
	if len(e.deleted) != 0 || revoked != 0 {
		t.Fatalf("a reference's drift revoked a label: %v %d", e.deleted, revoked)
	}
	w.OnFacts(map[string]string{"os_version": "26.4", "node": "24.15.0", "pnpm": "11.0.0"}) // stable again: no more calls
	if len(c.calls) != 1 {
		t.Fatalf("%v", c.calls)
	}
}

// The tasks come from files: one record asks for them once, however many earned labels there are.
func TestTheTasksAreAskedForOncePerRecord(t *testing.T) {
	e := &fakeEarned{rows: []wire.EarnedLabel{
		earned("task:purdex/tsc", wire.EarnedLabelEarned, fpNow),
		earned("task:purdex/spa", wire.EarnedLabelEarned, fpNow),
	}}
	var revoked int
	w := newWatcher(e, &fakeCache{}, &revoked, func() []Task { return []Task{referenceTask("task:purdex/ref", "tool:node24")} })
	w.OnFacts(fpNow)
	if tasksCalled != 1 {
		t.Fatalf("the tasks were read %d times for one record", tasksCalled)
	}
	w.OnFacts(map[string]string{"os_version": "27.0", "node": "24.14.0", "pnpm": "10.23.0"}) // both earned labels drift
	if tasksCalled != 2 {
		t.Fatalf("the tasks were read %d times for two records", tasksCalled)
	}
}

// The label is taken away first. If its reuse entries cannot be dropped just now, the label is gone anyway and the drop is tried again on
// every record until it works; a reference label's drift is met again until its entries are dropped.
func TestAFailedCacheCleanupIsRetriedOnTheNextRecords(t *testing.T) {
	e := &fakeEarned{rows: []wire.EarnedLabel{earned("task:purdex/tsc", wire.EarnedLabelEarned, fpNow)}}
	c := &fakeCache{failures: 2}
	var revoked int
	w := newWatcher(e, c, &revoked, nil)
	moved := map[string]string{"os_version": "27.0", "node": "24.14.0"}
	w.OnFacts(moved)
	if len(e.deleted) != 1 || revoked != 1 || len(c.calls) != 1 || !w.pending["task:purdex/tsc"] {
		t.Fatalf("revoked %v, told %d, cache calls %v, pending %v", e.deleted, revoked, c.calls, w.pending)
	}
	w.OnFacts(moved) // the row is gone, but the entries are still to drop: second failure
	if len(c.calls) != 2 || !w.pending["task:purdex/tsc"] {
		t.Fatalf("cache calls %v, pending %v", c.calls, w.pending)
	}
	w.OnFacts(moved) // works now
	if len(c.calls) != 3 || len(w.pending) != 0 {
		t.Fatalf("cache calls %v, pending %v", c.calls, w.pending)
	}
	w.OnFacts(moved) // and is not asked again
	if len(c.calls) != 3 {
		t.Fatalf("%v", c.calls)
	}
	if revoked != 1 {
		t.Fatalf("the daemon was told %d times: a retry of the cleanup is not a new revocation", revoked)
	}

	// the reference side: the noted fingerprint does not move on until the entries are dropped
	c2 := &fakeCache{failures: 1}
	w2 := newWatcher(&fakeEarned{}, c2, new(int), func() []Task { return []Task{referenceTask("task:purdex/ref", "tool:node24")} })
	w2.OnFacts(fpNow)
	w2.OnFacts(map[string]string{"os_version": "26.4", "node": "24.15.0", "pnpm": "10.23.0"}) // drop fails
	w2.OnFacts(map[string]string{"os_version": "26.4", "node": "24.15.0", "pnpm": "10.23.0"}) // the same record: tried again, works
	w2.OnFacts(map[string]string{"os_version": "26.4", "node": "24.15.0", "pnpm": "10.23.0"}) // and is settled
	if len(c2.calls) != 2 {
		t.Fatalf("the reference cleanup was tried %d times, want 2 (one failure, one success)", len(c2.calls))
	}
}

func TestAReferenceThatIsNoLongerOursIsForgotten(t *testing.T) {
	e := &fakeEarned{}
	c := &fakeCache{}
	var revoked int
	held := true
	refs := func() []Task {
		if !held {
			return nil
		}
		return []Task{referenceTask("task:purdex/tsc", "tool:node24")}
	}
	w := newWatcher(e, c, &revoked, refs)
	w.OnFacts(fpNow)
	held = false
	w.OnFacts(fpNow)
	if len(w.seen) != 0 {
		t.Fatalf("%v", w.seen)
	}
	held = true
	w.OnFacts(map[string]string{"os_version": "26.4", "node": "24.15.0"}) // held again with a different fingerprint: first sight, noted only
	if len(c.calls) != 0 {
		t.Fatalf("%v", c.calls)
	}
}

func TestAPendingCleanupIsLetGoOnceTheLabelIsEarnedAgain(t *testing.T) {
	e := &fakeEarned{rows: []wire.EarnedLabel{earned("task:purdex/tsc", wire.EarnedLabelEarned, fpNow)}}
	c := &fakeCache{failures: 1}
	w := newWatcher(e, c, new(int), nil)
	moved := map[string]string{"os_version": "27.0", "node": "24.14.0"}
	w.OnFacts(moved)
	e.rows = []wire.EarnedLabel{earned("task:purdex/tsc", wire.EarnedLabelEarned, moved)} // earned again on the new fingerprint
	w.OnFacts(moved)
	if len(c.calls) != 1 || len(w.pending) != 0 {
		t.Fatalf("cache calls %v, pending %v: the new qualification's entries must not be dropped", c.calls, w.pending)
	}
}

// tool:claude has no version in it: its key is claude, and a task needing it depends on the claude fingerprint (#3908).
func TestToolKeyClaude(t *testing.T) {
	if k, ok := ToolKey("tool:claude"); !ok || k != "claude" {
		t.Errorf("ToolKey = %q %v", k, ok)
	}
	if got := KeysFor([]string{"os:darwin", "tool:claude"}); !reflect.DeepEqual(got, []string{"claude", OSVersionKey}) {
		t.Errorf("KeysFor = %v", got)
	}
}
