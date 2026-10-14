package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	ipeers "github.com/wake/purdex/internal/peers"
)

func TestPeerNames_AssignAcceptsThePoolSource(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	got, err := s.Assign(context.Background(), pnSID, pnRef, "purdex-adam", PeerNameSourcePool, 10)
	require.NoError(t, err)
	require.Equal(t, PeerNameEntry{Name: "purdex-adam", Source: PeerNameSourcePool}, got)
}

func TestPeerNames_RenamePoolChangesTheNameAndKeepsAssignedAt(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	// Two ids sharing a ref, so ByRefs' "earliest assignment" shows whether
	// assigned_at moved.
	const a, b = "cccccccc-0000-4000-8000-000000159958", "cccccccc-0000-4000-8000-000000454406"
	ref := ipeers.RefID(a)
	require.Equal(t, ref, ipeers.RefID(b), "fixture: the two ids must share a ref")
	_, err := s.Assign(ctx, a, ref, "purdex-adam", PeerNameSourcePool, 10)
	require.NoError(t, err)
	_, err = s.Assign(ctx, b, ref, "purdex-emma", PeerNameSourcePool, 20)
	require.NoError(t, err)

	got, ok, err := s.RenamePool(ctx, a, "purdex-adam", "purdex-kenji")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, PeerNameEntry{Name: "purdex-kenji", Source: PeerNameSourcePool}, got)
	refs, err := s.ByRefs(ctx, []string{ref})
	require.NoError(t, err)
	require.Equal(t, map[string]string{ref: "purdex-kenji"}, refs, "a's assigned_at (10) is still the earliest")
}

func TestPeerNames_RenamePoolKeepsALineageRowLineageAndTurnsAnyOtherIntoPool(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	const other = "bbbbbbbb-0000-0000-0000-000000000002"
	_, err := s.Assign(ctx, pnSID, pnRef, "lead-adam", PeerNameSourceLineage, 10)
	require.NoError(t, err)
	_, err = s.Assign(ctx, other, ipeers.RefID(other), "purdex-k3", PeerNameSourceRegistry, 10)
	require.NoError(t, err)

	got, ok, err := s.RenamePool(ctx, pnSID, "lead-adam", "lead-emma")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, PeerNameEntry{Name: "lead-emma", Source: PeerNameSourceLineage}, got)

	got, ok, err = s.RenamePool(ctx, other, "purdex-k3", "purdex-yuki")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, PeerNameEntry{Name: "purdex-yuki", Source: PeerNameSourcePool}, got)
}

// Compare-and-set on the old name: another write that landed first (an
// AdoptLineage, or another rename) wins, and the rename changes nothing.
func TestPeerNames_RenamePoolWithAStaleOldNameChangesNothing(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	_, err := s.Assign(ctx, pnSID, pnRef, "purdex-adam", PeerNameSourcePool, 10)
	require.NoError(t, err)
	_, err = s.AdoptLineage(ctx, pnSID, "lead-omar") // lands first
	require.NoError(t, err)

	got, ok, err := s.RenamePool(ctx, pnSID, "purdex-adam", "purdex-kenji")
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, PeerNameEntry{Name: "lead-omar", Source: PeerNameSourceLineage}, got, "the row as it is stored")
	rows, err := s.Lookup(ctx, []string{pnSID})
	require.NoError(t, err)
	require.Equal(t, "lead-omar", rows[normPeerSID(pnSID)].Name)
}

func TestPeerNames_RenamePoolOfAMissingRowAnswersFalse(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	got, ok, err := s.RenamePool(context.Background(), pnSID, "purdex-adam", "purdex-kenji")
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, PeerNameEntry{}, got)
}

func TestPeerNames_RenamePoolRefusesEmptyInput(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.PeerNames()
	ctx := context.Background()
	_, err := s.Assign(ctx, pnSID, pnRef, "purdex-adam", PeerNameSourcePool, 10)
	require.NoError(t, err)
	for _, c := range [][3]string{{" ", "purdex-adam", "purdex-kenji"}, {pnSID, "", "purdex-kenji"}, {pnSID, "purdex-adam", ""}} {
		_, ok, err := s.RenamePool(ctx, c[0], c[1], c[2])
		require.Error(t, err, "%q", c)
		require.False(t, ok)
	}
	rows, err := s.Lookup(ctx, []string{pnSID})
	require.NoError(t, err)
	require.Equal(t, "purdex-adam", rows[normPeerSID(pnSID)].Name)
}
