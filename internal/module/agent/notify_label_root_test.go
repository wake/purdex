package agent

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/notifylabel"
	"github.com/wake/purdex/internal/team"
)

// ---- N-2a: the agent module's labeler reads a session's root through the registry and asks the registered overlay for the group ----

type fakeLineage struct {
	mu    sync.Mutex
	roots map[string]string
	err   error
}

func (f *fakeLineage) RootSessionOf(sid string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	if r, ok := f.roots[sid]; ok {
		return r, nil
	}
	return sid, nil // absent from lineage: its own root
}

type fakeOverlay struct {
	mu     sync.Mutex
	groups map[string]string
}

func (o *fakeOverlay) GroupOf(root string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.groups[root]
}

func labelerOn(t *testing.T, c *core.Core) *notifylabel.Labeler {
	t.Helper()
	l := newLabeler(c)
	l.Start()
	t.Cleanup(l.Stop)
	return l
}

func settled(t *testing.T, l *notifylabel.Labeler) {
	t.Helper()
	if !l.WaitIdle(2 * time.Second) {
		t.Fatal("the background read did not finish")
	}
}

// With a lineage resolver and an overlay in the registry, the group the App wrote under the root shows for any session of the chain.
func TestNewLabeler_TheOverlayIsAskedWithTheRootOfTheChain(t *testing.T) {
	c := &core.Core{Registry: core.NewServiceRegistry()}
	c.Registry.Register(team.LineageRootKey, &fakeLineage{roots: map[string]string{"s-new": "s-root"}})
	c.Registry.Register(notifylabel.OverlayKey, &fakeOverlay{groups: map[string]string{"s-root": "My group", "s-new": "WRONG: the session id was used"}})
	l := labelerOn(t, c)

	l.Label("s-new", "dev")
	settled(t, l)
	if got := l.Label("s-new", "dev"); got.Group != "My group" {
		t.Fatalf("group = %q, want the group of the root", got.Group)
	}
	// a session that is not in lineage is its own root
	l.Label("s-alone", "dev2")
	settled(t, l)
	if got := l.Label("s-alone", "dev2"); got.Group != "" {
		t.Fatalf("no group written for it: %q", got.Group)
	}
}

// Neither service has to be there when the labeler is made: the modules start in no fixed order. A service that registers later is
// used by the next read, without a restart.
func TestNewLabeler_ServicesThatRegisterLaterAreUsed(t *testing.T) {
	c := &core.Core{Registry: core.NewServiceRegistry()}
	l := labelerOn(t, c)
	l.Label("s1", "dev")
	settled(t, l)
	if got := l.Label("s1", "dev"); got.Group != "" {
		t.Fatalf("nothing registered: group = %q", got.Group)
	}

	c.Registry.Register(team.LineageRootKey, &fakeLineage{})
	c.Registry.Register(notifylabel.OverlayKey, &fakeOverlay{groups: map[string]string{"s2": "My group"}})
	l.Label("s2", "dev") // a session first read after both are there
	settled(t, l)
	if got := l.Label("s2", "dev"); got.Group != "My group" {
		t.Fatalf("group = %q", got.Group)
	}
}

// A lineage read that fails is no group (and no guess), and does not stop the persona and team parts.
func TestNewLabeler_ALineageErrorMeansNoOverlayGroup(t *testing.T) {
	c := &core.Core{Registry: core.NewServiceRegistry()}
	c.Registry.Register(team.LineageRootKey, &fakeLineage{err: errors.New("lineage is unreadable")})
	c.Registry.Register(notifylabel.OverlayKey, &fakeOverlay{groups: map[string]string{"s1": "WRONG: guessed from the session id"}})
	l := labelerOn(t, c)
	l.Label("s1", "dev")
	settled(t, l)
	if got := l.Label("s1", "dev"); got.Group != "" {
		t.Fatalf("group = %q", got.Group)
	}
}
