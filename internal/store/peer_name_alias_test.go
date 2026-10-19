package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	ipeers "github.com/wake/purdex/internal/peers"
)

// Name pool v2 (docs/specs/2026-10-19-name-pool-v2-team-v2-spec.md §6.3): a rename keeps the old name as an alias, in the same
// transaction as the CAS.

const (
	alA = "aaaaaaaa-0000-4000-8000-00000000000a"
	alB = "bbbbbbbb-0000-4000-8000-00000000000b"
)

func aliasStore(t *testing.T) *PeerNameStore {
	t.Helper()
	m, _ := openConvStore(t)
	return m.PeerNames()
}

func assignPool(t *testing.T, s *PeerNameStore, sid, name string) {
	t.Helper()
	_, err := s.Assign(context.Background(), sid, ipeers.RefID(sid), name, PeerNameSourcePool, 10)
	require.NoError(t, err)
}

func TestPeerNameAliases_ARenameLeavesTheOldNameAsAnAlias(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "purdex-adam")
	_, ok, err := s.RenamePool(ctx, alA, "purdex-adam", "purdex-hiro")
	require.NoError(t, err)
	require.True(t, ok)
	got, err := s.Aliases(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "purdex-adam", got[0].Alias)
	require.Equal(t, alA, got[0].SessionID)
	require.Equal(t, ipeers.RefID(alA), got[0].Ref)
	require.Positive(t, got[0].CreatedAt)
}

// A lost CAS (a stale old name) changes nothing, the alias table included.
func TestPeerNameAliases_AStaleRenameAddsNoAlias(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "purdex-adam")
	_, ok, err := s.RenamePool(ctx, alA, "purdex-nope", "purdex-hiro")
	require.NoError(t, err)
	require.False(t, ok)
	got, err := s.Aliases(ctx)
	require.NoError(t, err)
	require.Empty(t, got)
}

// A name that is assigned anew (a fresh Assign, or the target of a rename) is no longer an alias.
func TestPeerNameAliases_ANameAssignedAnewIsNoLongerAnAlias(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "purdex-adam")
	_, _, err := s.RenamePool(ctx, alA, "purdex-adam", "purdex-hiro") // alias purdex-adam -> A
	require.NoError(t, err)
	assignPool(t, s, alB, "purdex-adam") // B now holds it for real
	got, err := s.Aliases(ctx)
	require.NoError(t, err)
	require.Empty(t, got, "Assign deletes the alias row of the name it assigns")

	_, _, err = s.RenamePool(ctx, alA, "purdex-hiro", "purdex-wei") // alias purdex-hiro -> A
	require.NoError(t, err)
	_, ok, err := s.RenamePool(ctx, alB, "purdex-adam", "purdex-hiro") // B renames onto the alias name
	require.NoError(t, err)
	require.True(t, ok)
	got, err = s.Aliases(ctx)
	require.NoError(t, err)
	names := map[string]string{}
	for _, a := range got {
		names[a.Alias] = a.SessionID
	}
	require.NotContains(t, names, "purdex-hiro", "a rename onto an alias name takes the name for real")
	require.Equal(t, alB, names["purdex-adam"], "and B's old name is an alias of B")
}

// The same alias renamed twice points at the latest holder (the primary key is the alias).
func TestPeerNameAliases_TheLatestRenameOwnsAnAlias(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "purdex-adam")
	_, _, err := s.RenamePool(ctx, alA, "purdex-adam", "purdex-hiro")
	require.NoError(t, err)
	assignPool(t, s, alB, "purdex-adam") // clears the alias
	_, _, err = s.RenamePool(ctx, alB, "purdex-adam", "purdex-wei")
	require.NoError(t, err)
	got, err := s.Aliases(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, alB, got[0].SessionID)
}

func TestPeerNameAliases_DeleteAndTrim(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "n0")
	for i, n := range []string{"n1", "n2", "n3", "n4"} {
		aliasClock = func() int64 { return int64(100 + i) }
		_, ok, err := s.RenamePool(ctx, alA, "n"+string(rune('0'+i)), n)
		require.NoError(t, err)
		require.True(t, ok)
	}
	t.Cleanup(func() { aliasClock = nil })
	all, err := s.Aliases(ctx)
	require.NoError(t, err)
	require.Len(t, all, 4)
	require.Equal(t, "n0", all[0].Alias, "oldest first")

	n, err := s.TrimAliases(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	left, _ := s.Aliases(ctx)
	require.Equal(t, []string{"n2", "n3"}, []string{left[0].Alias, left[1].Alias}, "the oldest by created_at went first")

	require.NoError(t, s.DeleteAliases(ctx, []string{"n2", "no-such"}))
	left, _ = s.Aliases(ctx)
	require.Len(t, left, 1)
	require.Equal(t, "n3", left[0].Alias)
	require.NoError(t, s.DeleteAliases(ctx, nil))
}

// Undo of a lead rename (spec §5.3 step 3 and the lost close): the name goes back by CAS and the alias that rename added goes.
func TestPeerNameAliases_UndoRenameRestoresTheNameAndDropsTheAlias(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "purdex-adam")
	_, _, err := s.RenamePool(ctx, alA, "purdex-adam", "purdex-hiro")
	require.NoError(t, err)

	ok, err := s.UndoRename(ctx, alA, "purdex-hiro", "purdex-adam")
	require.NoError(t, err)
	require.True(t, ok)
	got, err := s.Lookup(ctx, []string{alA})
	require.NoError(t, err)
	require.Equal(t, "purdex-adam", got[alA].Name)
	al, _ := s.Aliases(ctx)
	require.Empty(t, al)

	// a later rename of the session made the current name something else: the CAS fails and nothing moves
	_, _, err = s.RenamePool(ctx, alA, "purdex-adam", "purdex-wei")
	require.NoError(t, err)
	ok, err = s.UndoRename(ctx, alA, "purdex-hiro", "purdex-adam")
	require.NoError(t, err)
	require.False(t, ok)
	got, _ = s.Lookup(ctx, []string{alA})
	require.Equal(t, "purdex-wei", got[alA].Name)
	al, _ = s.Aliases(ctx)
	require.Len(t, al, 1, "the alias of the later rename stays")
}

// The migration is idempotent (CREATE IF NOT EXISTS): opening a store twice on one file works.
func TestPeerNameAliases_MigrationRunsTwice(t *testing.T) {
	m, _ := openConvStore(t)
	require.NoError(t, migrateMetaDB(m.db))
	require.NoError(t, migrateMetaDB(m.db))
}

// The size cap holds from the first rename on: the rename that adds the 513th alias drops the oldest (spec §6.3, codex review
// of #3435).
func TestPeerNameAliases_ARenameKeepsTheTableAtItsCap(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "n0")
	i := 0
	aliasClock = func() int64 { i++; return int64(i) }
	t.Cleanup(func() { aliasClock = nil })
	prev := "n0"
	for k := 1; k <= MaxPeerNameAliases+3; k++ {
		next := fmt.Sprintf("n%d", k)
		_, ok, err := s.RenamePool(ctx, alA, prev, next)
		require.NoError(t, err)
		require.True(t, ok)
		prev = next
	}
	all, err := s.Aliases(ctx)
	require.NoError(t, err)
	require.Len(t, all, MaxPeerNameAliases)
	require.Equal(t, "n3", all[0].Alias, "the three oldest went")
}

// The old name was taken by another session between the rename and its compensation: the undo refuses (codex attack on #3435).
func TestPeerNameAliases_UndoRenameRefusesWhenTheOldNameWasTakenMeanwhile(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "purdex-adam")
	_, _, err := s.RenamePool(ctx, alA, "purdex-adam", "purdex-hiro")
	require.NoError(t, err)
	assignPool(t, s, alB, "purdex-adam") // B is newly assigned the name A just left

	ok, err := s.UndoRename(ctx, alA, "purdex-hiro", "purdex-adam")
	require.ErrorIs(t, err, ErrOldNameReclaimed)
	require.False(t, ok)
	got, _ := s.Lookup(ctx, []string{alA, alB})
	require.Equal(t, "purdex-hiro", got[alA].Name, "A keeps the name it has")
	require.Equal(t, "purdex-adam", got[alB].Name)
}

// B took the name A left, then renamed away: the alias now points at B. A's undo gets the name back, so no alias may keep
// routing it to B (codex R1 on #3435 at f94c5afca).
func TestPeerNameAliases_UndoRenameDropsAnAliasThatNowPointsAtAnotherSession(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "purdex-adam")
	_, _, err := s.RenamePool(ctx, alA, "purdex-adam", "purdex-hiro")
	require.NoError(t, err)
	assignPool(t, s, alB, "purdex-adam")
	_, ok, err := s.RenamePool(ctx, alB, "purdex-adam", "purdex-wei")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = s.UndoRename(ctx, alA, "purdex-hiro", "purdex-adam")
	require.NoError(t, err)
	require.True(t, ok)
	al, _ := s.Aliases(ctx)
	for _, a := range al {
		require.NotEqual(t, "purdex-adam", a.Alias, "the restored name is a live name, not an alias")
	}
}

// A stale undo (the session was renamed again) is a failed CAS, not a name conflict, even when the old name is taken.
func TestPeerNameAliases_StaleUndoIsNotAReclaimConflict(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "purdex-adam")
	_, _, err := s.RenamePool(ctx, alA, "purdex-adam", "purdex-hiro")
	require.NoError(t, err)
	_, _, err = s.RenamePool(ctx, alA, "purdex-hiro", "purdex-wei")
	require.NoError(t, err)
	assignPool(t, s, alB, "purdex-adam")

	ok, err := s.UndoRename(ctx, alA, "purdex-hiro", "purdex-adam")
	require.NoError(t, err)
	require.False(t, ok)
}

// Retention (spec §6.3): an alias goes once its session's lineage has had no live successor for 30 days. Whoever finds a live
// successor stamps the alias (TouchAliases); an alias counts from the later of its creation and its last stamp (ExpireAliases).
// Mutation gate: ignore the stamp → red; expire on created_at alone → red.
func TestPeerNameAliases_ATouchedAliasOutlivesTheCutoff(t *testing.T) {
	s := aliasStore(t)
	ctx := context.Background()
	assignPool(t, s, alA, "n0")
	for i := 0; i < 4; i++ {
		aliasClock = func() int64 { return int64(100 + i) }
		_, ok, err := s.RenamePool(ctx, alA, fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", i+1))
		require.NoError(t, err)
		require.True(t, ok)
	}
	t.Cleanup(func() { aliasClock = nil })

	require.NoError(t, s.TouchAliases(ctx, []string{"n1", "no-such"}, 5000))
	n, err := s.ExpireAliases(ctx, 1000)
	require.NoError(t, err)
	require.Equal(t, 3, n, "n0, n2, n3 were created before the cutoff and never stamped")
	left, _ := s.Aliases(ctx)
	require.Len(t, left, 1)
	require.Equal(t, "n1", left[0].Alias)

	n, err = s.ExpireAliases(ctx, 5000)
	require.NoError(t, err)
	require.Equal(t, 0, n, "stamped at 5000: not before a cutoff of 5000")
	n, err = s.ExpireAliases(ctx, 5001)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, s.TouchAliases(ctx, nil, 1))
}

// A table made before last_live_at gets the column on the next open, keeps its rows, and counts them from created_at.
func TestPeerNameAliases_MigrationAddsLastLiveAtToAnOlderTable(t *testing.T) {
	m, _ := openConvStore(t)
	for _, q := range []string{
		`DROP TABLE peer_name_aliases`,
		`CREATE TABLE peer_name_aliases (alias TEXT PRIMARY KEY, session_id TEXT NOT NULL, ref TEXT NOT NULL, created_at INTEGER NOT NULL)`,
		`INSERT INTO peer_name_aliases VALUES ('old', 's', '_aaaaaa', 100)`,
	} {
		_, err := m.db.Exec(q)
		require.NoError(t, err)
	}
	require.NoError(t, migrateMetaDB(m.db))
	require.NoError(t, migrateMetaDB(m.db))
	s := m.PeerNames()
	got, err := s.Aliases(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "old", got[0].Alias)
	n, err := s.ExpireAliases(context.Background(), 101)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}
