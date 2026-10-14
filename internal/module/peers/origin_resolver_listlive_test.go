package peers

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// stuckNames is a name store that answers nothing until its context ends (the 10-15 mlab symptom: "virtual names: store
// error ... context deadline exceeded"); every other call goes to the real one beneath.
type stuckNames struct {
	PeerNameStore
	lookups atomic.Int32
}

func (s *stuckNames) Lookup(ctx context.Context, _ []string) (map[string]store.PeerNameEntry, error) {
	s.lookups.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

type countingTitles struct {
	fakeTitles
	snapshots atomic.Int32
}

func (c *countingTitles) Snapshot() ([]store.PeerLabel, error) {
	c.snapshots.Add(1)
	return c.fakeTitles.Snapshot()
}

// #2892: GET /api/team/unattended waited namerTimeout once per live session (16 sessions = 16 s) and read the whole title
// table once per session. A list of origins costs one name resolution and one title read, however many sessions.
func TestOriginResolver_ListLiveOrigins_OneNameResolutionAndOneTitleRead(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	for i := 3; i <= 10; i++ {
		writeRegistryFixture(t, dir, fmt.Sprintf("%d.json", i*10), fmt.Sprintf(`{"pid":%d,"sessionId":"sid-%d","cwd":"/w%d","procStart":"`+targetProcStart+`","version":"2.1.270","messagingSocketPath":"`+dir+`/%d.sock","name":"","status":"idle"}`, i*10, i, i, i*10))
	}
	names := &stuckNames{PeerNameStore: r.m.peerNames}
	titles := &countingTitles{fakeTitles: fakeTitles{"sid-1": "lead-team"}}
	r.m.peerNames, r.m.titles = names, titles

	start := time.Now()
	got, err := r.ListLiveOrigins()
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Fatalf("%d origins, want 10", len(got))
	}
	if took > 3*namerTimeout {
		t.Errorf("10 sessions took %v with a name store that does not answer, want about one namerTimeout (%v)", took, namerTimeout)
	}
	if n := names.lookups.Load(); n > 2 {
		t.Errorf("the name store was asked %d times, want once for the whole list", n)
	}
	if n := titles.snapshots.Load(); n != 1 {
		t.Errorf("the title table was read %d times, want once", n)
	}
	for _, o := range got {
		if o.SessionID == "sid-1" && o.Title != "lead-team" {
			t.Errorf("sid-1 title = %q, want lead-team", o.Title)
		}
		if o.Address != "mlab/"+ipeers.RefID(o.SessionID) {
			t.Errorf("%s address = %q, want the ref form while the name store is stuck", o.SessionID, o.Address)
		}
	}
}

func TestNamesBudget(t *testing.T) {
	for _, c := range []struct {
		n    int
		want time.Duration
	}{{1, namerTimeout}, {2, namerTimeout + 50*time.Millisecond}, {16, namerTimeout + 750*time.Millisecond}, {1000, 3 * namerTimeout}} {
		if got := namesBudget(c.n); got != c.want {
			t.Errorf("namesBudget(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}
