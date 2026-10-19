package daemon

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/wire"
)

type fakeEarned struct {
	mu   sync.Mutex
	rows []wire.EarnedLabel
	err  error
}

func (f *fakeEarned) EarnedLabels() ([]wire.EarnedLabel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.rows), f.err
}

func (f *fakeEarned) set(rows ...wire.EarnedLabel) {
	f.mu.Lock()
	f.rows = rows
	f.mu.Unlock()
}

func earnedDaemon(t *testing.T, o Options) *Daemon {
	t.Helper()
	o.HostID, o.Alias, o.Version, o.Token = "h1", "testhost", "v0", testToken
	return New(o)
}

var linuxFacts = wire.Facts{Platform: "linux", Arch: "amd64", OSVersion: "Ubuntu 26.04", Tools: wire.Tools{Node: str("24.14.0")}}

func TestEveryHostAdvertisesItsHostLabel(t *testing.T) {
	d := earnedDaemon(t, Options{})
	d.OnFacts(linuxFacts)
	if got := infoOf(t, d).Labels; !slices.Contains(got, "host:testhost") {
		t.Fatalf("%v", got)
	}
}

// Only an earned label is advertised; a quarantined one stays on record and is not.
func TestEarnedLabelsAreAdvertisedQuarantinedAreNot(t *testing.T) {
	fe := &fakeEarned{}
	fe.set(
		wire.EarnedLabel{Label: "task:purdex/tsc", State: wire.EarnedLabelEarned},
		wire.EarnedLabel{Label: "task:purdex/lint", State: wire.EarnedLabelQuarantined},
	)
	d := earnedDaemon(t, Options{Earned: fe})
	d.OnFacts(linuxFacts)
	got := infoOf(t, d).Labels
	if !slices.Contains(got, "task:purdex/tsc") || slices.Contains(got, "task:purdex/lint") {
		t.Fatalf("%v", got)
	}
}

// A change in the earned set sends a labels event without waiting for the next facts record, and a repeat sends none.
func TestRefreshLabelsAnnouncesAnEarnedChange(t *testing.T) {
	fe := &fakeEarned{}
	d := earnedDaemon(t, Options{Earned: fe})
	ch, cancel := d.Events().Subscribe(false)
	defer cancel()
	d.RefreshLabels() // before the first record: nothing to do
	none(t, ch)

	d.OnFacts(linuxFacts)
	recv(t, ch) // facts
	recv(t, ch) // labels

	fe.set(wire.EarnedLabel{Label: "task:purdex/tsc", State: wire.EarnedLabelEarned})
	d.RefreshLabels()
	ev := labelsEvent(t, recv(t, ch))
	if !slices.Contains(ev.Labels, "task:purdex/tsc") {
		t.Fatalf("%+v", ev)
	}
	d.RefreshLabels()
	none(t, ch)

	fe.set(wire.EarnedLabel{Label: "task:purdex/tsc", State: wire.EarnedLabelQuarantined})
	d.RefreshLabels()
	if ev := labelsEvent(t, recv(t, ch)); slices.Contains(ev.Labels, "task:purdex/tsc") {
		t.Fatalf("a quarantined label is still advertised: %+v", ev)
	}
}

// An unreadable store advertises no earned label (none is invented); the labels of the record still go out.
func TestUnreadableEarnedStoreInventsNothing(t *testing.T) {
	fe := &fakeEarned{err: errTest}
	d := earnedDaemon(t, Options{Earned: fe})
	d.OnFacts(linuxFacts)
	got := infoOf(t, d).Labels
	if !slices.Contains(got, "os:linux") {
		t.Fatalf("%v", got)
	}
	for _, l := range got {
		if len(l) > 5 && l[:5] == "task:" {
			t.Fatalf("invented %s", l)
		}
	}
}

var errTest = errString("store unreadable")

type errString string

func (e errString) Error() string { return string(e) }

// The reference host advertises the task label it holds without a pair; another host does not.
func TestReferenceHostAdvertisesTaskLabelWithoutPair(t *testing.T) {
	tasks := func() []labels.RefTask {
		return []labels.RefTask{{Repo: "purdex", Task: "tsc", Reference: "testhost", Needs: []string{"tool:node24"}}}
	}
	d := earnedDaemon(t, Options{ReferenceTasks: tasks, RepoSource: func() []string { return []string{"purdex"} }})
	d.OnFacts(linuxFacts)
	if got := infoOf(t, d).Labels; !slices.Contains(got, "task:purdex/tsc") {
		t.Fatalf("the reference lacks its label: %v", got)
	}
	other := func() []labels.RefTask {
		return []labels.RefTask{{Repo: "purdex", Task: "tsc", Reference: "air26", Needs: []string{"tool:node24"}}}
	}
	d2 := earnedDaemon(t, Options{ReferenceTasks: other, RepoSource: func() []string { return []string{"purdex"} }})
	d2.OnFacts(linuxFacts)
	if got := infoOf(t, d2).Labels; slices.Contains(got, "task:purdex/tsc") {
		t.Fatalf("a host that is not the reference holds it: %v", got)
	}
}

func TestEarnedLabelsRouteIsLocalOnly(t *testing.T) {
	fe := &fakeEarned{}
	fe.set(wire.EarnedLabel{Label: "task:purdex/tsc", State: wire.EarnedLabelQuarantined, Fingerprint: map[string]string{"node": "24.14.0"}})
	d := earnedDaemon(t, Options{Earned: fe})
	if w := do(d.TCPHandler(), "GET", "/v1/earned-labels", "Bearer "+testToken); w.Code != 404 {
		t.Fatalf("a peer got %d", w.Code)
	}
	w := do(d.SocketHandler(), "GET", "/v1/earned-labels", "")
	if w.Code != 200 {
		t.Fatalf("code %d %s", w.Code, w.Body)
	}
	list, err := wire.Decode[wire.EarnedLabelList](w.Body.Bytes())
	if err != nil || list.Host != "testhost" || len(list.Labels) != 1 || list.Labels[0].State != wire.EarnedLabelQuarantined || list.Labels[0].Fingerprint["node"] != "24.14.0" {
		t.Fatalf("%+v %v", list, err)
	}
	if w := do(d.SocketHandler(), "GET", "/v1/earned-labels?host=air26", ""); w.Code != 400 {
		t.Fatalf("?host= is not forwarded: %d", w.Code)
	}
	empty := earnedDaemon(t, Options{})
	if w := do(empty.SocketHandler(), "GET", "/v1/earned-labels", ""); w.Code != 404 {
		t.Fatalf("no store: %d", w.Code)
	}
}

// A passing read error does not revoke what was earned: the last set that was read stays advertised, and no event announces a loss.
func TestStoreReadErrorKeepsTheLastEarnedSet(t *testing.T) {
	fe := &fakeEarned{}
	fe.set(wire.EarnedLabel{Label: "task:purdex/tsc", State: wire.EarnedLabelEarned})
	d := earnedDaemon(t, Options{Earned: fe})
	ch, cancel := d.Events().Subscribe(false)
	defer cancel()
	d.OnFacts(linuxFacts)
	recv(t, ch)
	recv(t, ch)

	fe.mu.Lock()
	fe.err, fe.rows = errTest, nil
	fe.mu.Unlock()
	d.RefreshLabels()
	none(t, ch)
	if got := infoOf(t, d).Labels; !slices.Contains(got, "task:purdex/tsc") {
		t.Fatalf("the label was revoked by a read error: %v", got)
	}
	// the store answers again, now without the label: that is a real revoke
	fe.mu.Lock()
	fe.err = nil
	fe.mu.Unlock()
	d.RefreshLabels()
	if ev := labelsEvent(t, recv(t, ch)); slices.Contains(ev.Labels, "task:purdex/tsc") {
		t.Fatalf("%+v", ev)
	}
}

// A row that is not a task label is never advertised, whatever the store holds.
func TestOnlyTaskLabelsAreAdvertisedFromTheStore(t *testing.T) {
	fe := &fakeEarned{}
	fe.set(
		wire.EarnedLabel{Label: "os:darwin", State: wire.EarnedLabelEarned},
		wire.EarnedLabel{Label: "repo:secret", State: wire.EarnedLabelEarned},
		wire.EarnedLabel{Label: "task:purdex/tsc", State: wire.EarnedLabelEarned},
	)
	d := earnedDaemon(t, Options{Earned: fe})
	d.OnFacts(wire.Facts{Platform: "linux", Arch: "amd64"})
	got := infoOf(t, d).Labels
	if slices.Contains(got, "os:darwin") || slices.Contains(got, "repo:secret") || !slices.Contains(got, "task:purdex/tsc") {
		t.Fatalf("%v", got)
	}
}

// OnFacts and RefreshLabels never interleave, in the state and in the events: a refresh that is in flight when a newer record arrives
// announces its labels first, then the record, then the record's labels. The refresh's store read is held while OnFacts(B) is called;
// B must wait for it.
func TestRefreshCannotOverwriteANewerRecord(t *testing.T) {
	gate := &gatedEarned{release: make(chan struct{}), entered: make(chan struct{}, 2)}
	d := earnedDaemon(t, Options{Earned: gate})
	ch, cancel := d.Events().Subscribe(false)
	defer cancel()
	a := wire.Facts{Platform: "linux", Arch: "amd64", Tools: wire.Tools{Node: str("24.14.0")}}
	b := wire.Facts{Platform: "linux", Arch: "amd64", Tools: wire.Tools{Node: str("25.0.0")}}
	gate.pass = true
	d.OnFacts(a)
	recv(t, ch) // facts A
	recv(t, ch) // labels A
	gate.pass = false
	gate.rows = []wire.EarnedLabel{{Label: "task:purdex/tsc", State: wire.EarnedLabelEarned}} // the refresh finds a change to announce

	done := make(chan struct{})
	go func() { d.RefreshLabels(); close(done) }()
	<-gate.entered // the refresh is inside its store read, holding the lock with record A
	onB := make(chan struct{})
	go func() { d.OnFacts(b); close(onB) }()
	select {
	case <-onB:
		t.Fatal("OnFacts ran while a refresh was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	none(t, ch) // not even the facts event of B has gone out yet
	close(gate.release)
	<-done
	<-onB
	if e := recv(t, ch); e.Type != wire.EventLabels {
		t.Fatalf("first the refresh's labels, got %s", e.Type)
	}
	if e := recv(t, ch); e.Type != wire.EventFacts {
		t.Fatalf("then the record, got %s", e.Type)
	}
	if ev := labelsEvent(t, recv(t, ch)); !slices.Contains(ev.Labels, "tool:node25") {
		t.Fatalf("then the record's labels: %v", ev.Labels)
	}
	if got := infoOf(t, d).Labels; !slices.Contains(got, "tool:node25") || slices.Contains(got, "tool:node24") {
		t.Fatalf("the older record won: %v", got)
	}
}

type gatedEarned struct {
	pass    bool
	rows    []wire.EarnedLabel
	release chan struct{}
	entered chan struct{}
}

func (g *gatedEarned) EarnedLabels() ([]wire.EarnedLabel, error) {
	if g.pass {
		return nil, nil
	}
	g.entered <- struct{}{}
	<-g.release
	return g.rows, nil
}
