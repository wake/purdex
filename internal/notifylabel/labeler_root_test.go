package notifylabel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ---- N-2a: the groups the App wrote back are keyed by the root of a session's relay chain, read in the background ----

// rootSource answers the root of a session; every read is counted and a failure can be switched on.
type rootSource struct {
	mu    sync.Mutex
	roots map[string]string
	err   error
	asked []string
}

func (r *rootSource) read(_ context.Context, sid string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, sid)
	if r.err != nil {
		return "", r.err
	}
	return r.roots[sid], nil
}

func (r *rootSource) set(fn func()) { r.mu.Lock(); fn(); r.mu.Unlock() }

// asks the overlay of the Labeler is called with: what Label looked up
type recordingOverlay struct {
	mu     sync.Mutex
	groups map[string]string
	asked  []string
}

func (o *recordingOverlay) GroupOf(root string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.asked = append(o.asked, root)
	return o.groups[root]
}

func withRoot(src Sources, r *rootSource) Sources {
	src.Root = r.read
	return src
}

func newWithRoot(t *testing.T, f *fakeSources, r *rootSource, o Overlay, opts ...Option) (*Labeler, *clock) {
	t.Helper()
	clk := &clock{t: time.Unix(1_000_000, 0)}
	l := New(withRoot(f.sources(), r), append([]Option{WithClock(clk.Now), WithOverlay(o)}, opts...)...)
	l.Start()
	t.Cleanup(l.Stop)
	return l, clk
}

// The overlay is asked with the session's root, not with the session id.
func TestLabeler_OverlayIsKeyedByTheRoot(t *testing.T) {
	f := baseSources()
	delete(f.groups, "s1")
	r := &rootSource{roots: map[string]string{"s1": "root-1"}}
	o := &recordingOverlay{groups: map[string]string{"root-1": "My group", "s1": "WRONG: the session id was used"}}
	l, _ := newWithRoot(t, f, r, o)
	l.Label("s1", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Group != "My group" {
		t.Fatalf("group = %q: the root's group is the answer", got.Group)
	}
}

// A session whose root is not known yet is not guessed at: before the first read lands, and when the read fails, the overlay says nothing.
func TestLabeler_NoRootNoOverlayLookup(t *testing.T) {
	f := baseSources()
	delete(f.groups, "s1")
	r := &rootSource{roots: map[string]string{"s1": "root-1"}, err: errors.New("lineage is unreadable")}
	o := &recordingOverlay{groups: map[string]string{"root-1": "My group", "s1": "WRONG"}}
	l, _ := newWithRoot(t, f, r, o)
	if got := l.Label("s1", "dev"); got.Group != "" {
		t.Fatalf("cold: group = %q", got.Group)
	}
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Group != "" {
		t.Fatalf("the root could not be read: group = %q", got.Group)
	}
	o.mu.Lock()
	asked := append([]string(nil), o.asked...)
	o.mu.Unlock()
	if len(asked) != 0 {
		t.Fatalf("the overlay was asked about %v with no root known", asked)
	}
}

// A failed read keeps the root known before (as the other sources do); a changed root shows within one ttl.
func TestLabeler_RootKeepsItsLastValueOnAnErrorAndFollowsAChange(t *testing.T) {
	f := baseSources()
	delete(f.groups, "s1")
	r := &rootSource{roots: map[string]string{"s1": "root-1"}}
	o := &recordingOverlay{groups: map[string]string{"root-1": "Old group", "root-2": "New group"}}
	l, clk := newWithRoot(t, f, r, o)
	l.Label("s1", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Group != "Old group" {
		t.Fatalf("group = %q", got.Group)
	}

	r.set(func() { r.err = errors.New("busy") })
	clk.advance(defaultTTL + time.Second)
	l.Label("s1", "dev") // queues a reread, which fails
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Group != "Old group" {
		t.Fatalf("after a failed read: group = %q", got.Group)
	}

	r.set(func() { r.err = nil; r.roots["s1"] = "root-2" }) // the relay moved the session to another chain
	clk.advance(defaultTTL + time.Second)
	l.Label("s1", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Group != "New group" {
		t.Fatalf("after the root changed: group = %q", got.Group)
	}
}

// A read that finds no root (the lineage service is not there) with no error is "not known": the root known before stays.
func TestLabeler_AnEmptyRootKeepsTheLastKnownOne(t *testing.T) {
	f := baseSources()
	delete(f.groups, "s1")
	r := &rootSource{roots: map[string]string{"s1": "root-1"}}
	o := &recordingOverlay{groups: map[string]string{"root-1": "Old group"}}
	l, clk := newWithRoot(t, f, r, o)
	l.Label("s1", "dev")
	settle(t, l)
	r.set(func() { r.roots["s1"] = "" })
	clk.advance(defaultTTL + time.Second)
	l.Label("s1", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Group != "Old group" {
		t.Fatalf("group = %q: an unknown root must not erase the known one", got.Group)
	}
}

// A team still beats the overlay when roots are in use, and the overlay is not asked when a team has answered.
func TestLabeler_TeamStillBeatsTheOverlayWithRoots(t *testing.T) {
	f := baseSources() // s1 is in a team: "Purdex A team"
	r := &rootSource{roots: map[string]string{"s1": "root-1"}}
	o := &recordingOverlay{groups: map[string]string{"root-1": "My group"}}
	l, _ := newWithRoot(t, f, r, o)
	l.Label("s1", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Group != "Purdex A team" {
		t.Fatalf("group = %q", got.Group)
	}
	o.mu.Lock()
	n := len(o.asked)
	o.mu.Unlock()
	if n != 0 {
		t.Fatalf("the overlay was asked %d times though a team had answered", n)
	}
}

// The root is read by the background goroutine only: Label itself never calls the source.
func TestLabeler_LabelDoesNotReadTheRoot(t *testing.T) {
	f := baseSources()
	delete(f.groups, "s1")
	r := &rootSource{roots: map[string]string{"s1": "root-1"}}
	o := &recordingOverlay{groups: map[string]string{"root-1": "My group"}}
	l, _ := newWithRoot(t, f, r, o)
	l.Label("s1", "dev")
	settle(t, l)
	r.mu.Lock()
	before := len(r.asked)
	r.mu.Unlock()
	for i := 0; i < 50; i++ {
		l.Label("s1", "dev")
	}
	r.mu.Lock()
	after := len(r.asked)
	r.mu.Unlock()
	if after != before {
		t.Fatalf("Label called the root source %d times on a warm entry", after-before)
	}
}

// Without a Root source the session id is the key, as it was before N-2 (a labeler made for a host with no lineage).
func TestLabeler_WithoutARootSourceTheSessionIDIsTheKey(t *testing.T) {
	f := baseSources()
	delete(f.groups, "s1")
	o := &recordingOverlay{groups: map[string]string{"s1": "My group"}}
	l, _ := newTest(t, f, WithOverlay(o))
	l.Label("s1", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Group != "My group" {
		t.Fatalf("group = %q", got.Group)
	}
}
