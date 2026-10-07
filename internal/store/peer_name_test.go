package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	ipeers "github.com/wake/purdex/internal/peers"
)

const pnSID = "AAAAAAAA-0000-0000-0000-0000000000A1"

// pnRef is pnSID's ref, derived from the lowercase id the store keys on.
var pnRef = ipeers.RefID(strings.ToLower(pnSID))

func TestPeerNames_AssignStoresLowercaseAndLooksUp(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	got, err := s.Assign(ctx, pnSID, pnRef, "purdex-54-k3", PeerNameSourceRegistry, 10)
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "purdex-54-k3", Source: PeerNameSourceRegistry}, got)

	rows, err := s.Lookup(ctx, []string{pnSID, "bbbbbbbb-0000-0000-0000-000000000002"})
	require.NoError(t, err)
	require.Equal(t, map[string]PeerNameEntry{
		"aaaaaaaa-0000-0000-0000-0000000000a1": {Name: "purdex-54-k3", Source: PeerNameSourceRegistry},
	}, rows)

	refs, err := s.ByRefs(ctx, []string{pnRef, "_zzzzzz"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{pnRef: "purdex-54-k3"}, refs)

	empty, err := s.Lookup(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
	none, err := s.ByRefs(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, none)
}

// The rename test: a name is assigned once. A second Assign for the same
// session — a later pass that saw another registry name — gets the first
// name back and changes nothing (spec §3.2).
func TestPeerNames_AssignKeepsFirstName(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	_, err := s.Assign(ctx, pnSID, pnRef, "first-k3", PeerNameSourceRegistry, 10)
	require.NoError(t, err)
	got, err := s.Assign(ctx, pnSID, pnRef, "second-k3", PeerNameSourceConversationName, 20)
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "first-k3", Source: PeerNameSourceRegistry}, got)
	rows, err := s.Lookup(ctx, []string{pnSID})
	require.NoError(t, err)
	require.Equal(t, "first-k3", rows["aaaaaaaa-0000-0000-0000-0000000000a1"].Name)
}

// Two concurrent first sightings of one session converge on one stored name:
// whichever insert lands first wins, and the other caller is handed it.
func TestPeerNames_ConcurrentAssignConverges(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		sid := fmt.Sprintf("cccccccc-0000-0000-0000-%012d", i)
		var wg sync.WaitGroup
		start := make(chan struct{})
		got := make([]PeerNameEntry, 2)
		errs := make([]error, 2)
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				<-start
				got[g], errs[g] = s.Assign(ctx, sid, ipeers.RefID(sid), fmt.Sprintf("name%d-k3", g), PeerNameSourceRegistry, int64(g))
			}(g)
		}
		close(start)
		wg.Wait()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		require.Equal(t, got[0], got[1], "sid %s: concurrent Assigns disagree", sid)
		rows, err := s.Lookup(ctx, []string{sid})
		require.NoError(t, err)
		require.Equal(t, got[0], rows[sid])
	}
}

func TestPeerNames_AdoptLineageUpgradesAFallbackOnce(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	_, err := s.Assign(ctx, pnSID, pnRef, "purdex-k3", PeerNameSourceRegistry, 10)
	require.NoError(t, err)
	got, err := s.AdoptLineage(ctx, pnSID, "lead-a1")
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "lead-a1", Source: PeerNameSourceLineage}, got)
}

// The never-overwritten test: a lineage name is final. Neither another
// lineage adoption nor an Assign moves it.
func TestPeerNames_LineageRowIsNeverOverwritten(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	_, err := s.Assign(ctx, pnSID, pnRef, "lead-a1", PeerNameSourceLineage, 10)
	require.NoError(t, err)
	got, err := s.AdoptLineage(ctx, pnSID, "other-b2")
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "lead-a1", Source: PeerNameSourceLineage}, got)
	got, err = s.Assign(ctx, pnSID, pnRef, "purdex-k3", PeerNameSourceRegistry, 30)
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "lead-a1", Source: PeerNameSourceLineage}, got)
}

func TestPeerNames_RejectsBadInput(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	_, err := s.Assign(ctx, " ", ipeers.RefID(""), "a-k3", PeerNameSourceRegistry, 1)
	require.Error(t, err)
	_, err = s.Assign(ctx, pnSID, pnRef, "", PeerNameSourceRegistry, 1)
	require.Error(t, err)
	_, err = s.Assign(ctx, pnSID, pnRef, "a-k3", "made-up", 1)
	require.Error(t, err)
	_, err = s.AdoptLineage(ctx, pnSID, "")
	require.Error(t, err)
	_, err = s.AdoptLineage(ctx, pnSID, "a-k3") // no row to upgrade
	require.Error(t, err)
	rows, err := s.Lookup(ctx, []string{pnSID})
	require.NoError(t, err)
	require.Empty(t, rows)
}

// ByRefs answers the earliest assignment among sessions sharing a ref, so a
// lineage upgrade must not move a row's assigned_at: A (t=10) and B (t=20)
// share a ref, and after A adopts a lineage name ByRefs still answers A.
func TestPeerNames_AdoptLineageKeepsAssignedAt(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	// Two ids whose refs collide (found by search; FNV-1a mod 36^6).
	const a, b = "cccccccc-0000-4000-8000-000000159958", "cccccccc-0000-4000-8000-000000454406"
	ref := ipeers.RefID(a)
	require.Equal(t, ref, ipeers.RefID(b), "fixture: the two ids must share a ref")
	_, err := s.Assign(ctx, a, ref, "first-t1", PeerNameSourceRegistry, 10)
	require.NoError(t, err)
	_, err = s.Assign(ctx, b, ref, "second-t1", PeerNameSourceRegistry, 20)
	require.NoError(t, err)
	got, err := s.AdoptLineage(ctx, a, "lead-a1")
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "lead-a1", Source: PeerNameSourceLineage}, got)
	refs, err := s.ByRefs(ctx, []string{ref})
	require.NoError(t, err)
	require.Equal(t, map[string]string{ref: "lead-a1"}, refs, "the earliest assignment is still A's")
}

// The ref is what a relay successor inherits a name by (ByRefs), so it must
// be the one the session id derives — RefID of the lowercase id the row is
// keyed on — whatever the caller passed. A caller with an uppercase id still
// lands under the lowercase id's ref; a wrong, empty or malformed ref is
// refused and nothing is stored.
func TestPeerNames_RefMatchesTheLowercaseSessionID(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	lower := strings.ToLower(pnSID)
	_, err := s.Assign(ctx, pnSID, ipeers.RefID(lower), "purdex-k3", PeerNameSourceRegistry, 10)
	require.NoError(t, err)
	refs, err := s.ByRefs(ctx, []string{ipeers.RefID(lower)})
	require.NoError(t, err)
	require.Equal(t, map[string]string{ipeers.RefID(lower): "purdex-k3"}, refs)

	const other = "bbbbbbbb-0000-0000-0000-000000000002"
	require.NotEqual(t, ipeers.RefID("BBBBBBBB-0000-0000-0000-000000000002"), ipeers.RefID(other), "fixture")
	for _, bad := range []string{"", "_k3m9qz", "k3m9qz", "_ZZZZZZ", ipeers.RefID(strings.ToUpper(other)), ipeers.RefID(other) + "x"} {
		_, err := s.Assign(ctx, other, bad, "other-k3", PeerNameSourceRegistry, 20)
		require.Error(t, err, "ref %q", bad)
	}
	rows, err := s.Lookup(ctx, []string{other})
	require.NoError(t, err)
	require.Empty(t, rows)
}
