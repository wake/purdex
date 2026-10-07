package store

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const pnSID = "AAAAAAAA-0000-0000-0000-0000000000A1"

func TestPeerNames_AssignStoresLowercaseAndLooksUp(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	got, err := s.Assign(ctx, pnSID, "_k3m9qz", "purdex-54-k3", PeerNameSourceRegistry, 10)
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "purdex-54-k3", Source: PeerNameSourceRegistry}, got)

	rows, err := s.Lookup(ctx, []string{pnSID, "bbbbbbbb-0000-0000-0000-000000000002"})
	require.NoError(t, err)
	require.Equal(t, map[string]PeerNameEntry{
		"aaaaaaaa-0000-0000-0000-0000000000a1": {Name: "purdex-54-k3", Source: PeerNameSourceRegistry},
	}, rows)

	refs, err := s.ByRefs(ctx, []string{"_k3m9qz", "_zzzzzz"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"_k3m9qz": "purdex-54-k3"}, refs)

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
	_, err := s.Assign(ctx, pnSID, "_k3m9qz", "first-k3", PeerNameSourceRegistry, 10)
	require.NoError(t, err)
	got, err := s.Assign(ctx, pnSID, "_k3m9qz", "second-k3", PeerNameSourceConversationName, 20)
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
				got[g], errs[g] = s.Assign(ctx, sid, "_k3m9qz", fmt.Sprintf("name%d-k3", g), PeerNameSourceRegistry, int64(g))
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
	_, err := s.Assign(ctx, pnSID, "_k3m9qz", "purdex-k3", PeerNameSourceRegistry, 10)
	require.NoError(t, err)
	got, err := s.AdoptLineage(ctx, pnSID, "lead-a1", 20)
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "lead-a1", Source: PeerNameSourceLineage}, got)
}

// The never-overwritten test: a lineage name is final. Neither another
// lineage adoption nor an Assign moves it.
func TestPeerNames_LineageRowIsNeverOverwritten(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	_, err := s.Assign(ctx, pnSID, "_k3m9qz", "lead-a1", PeerNameSourceLineage, 10)
	require.NoError(t, err)
	got, err := s.AdoptLineage(ctx, pnSID, "other-b2", 20)
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "lead-a1", Source: PeerNameSourceLineage}, got)
	got, err = s.Assign(ctx, pnSID, "_k3m9qz", "purdex-k3", PeerNameSourceRegistry, 30)
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "lead-a1", Source: PeerNameSourceLineage}, got)
}

func TestPeerNames_RejectsBadInput(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	_, err := s.Assign(ctx, " ", "_k3m9qz", "a-k3", PeerNameSourceRegistry, 1)
	require.Error(t, err)
	_, err = s.Assign(ctx, pnSID, "_k3m9qz", "", PeerNameSourceRegistry, 1)
	require.Error(t, err)
	_, err = s.Assign(ctx, pnSID, "_k3m9qz", "a-k3", "made-up", 1)
	require.Error(t, err)
	_, err = s.AdoptLineage(ctx, pnSID, "", 1)
	require.Error(t, err)
	_, err = s.AdoptLineage(ctx, pnSID, "a-k3", 1) // no row to upgrade
	require.Error(t, err)
	rows, err := s.Lookup(ctx, []string{pnSID})
	require.NoError(t, err)
	require.Empty(t, rows)
}
