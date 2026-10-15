package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var st0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func TestStateStoreSurvivesRestart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "watch", "state.json")
	s, err := OpenState(p)
	require.NoError(t, err)
	require.True(t, s.Sighting("pnpm build", st0))
	require.False(t, s.Sighting("pnpm build", st0.Add(time.Hour)))
	require.Equal(t, st0, s.OrphanSince("heavy-1 1.2", st0))
	require.Equal(t, st0, s.OrphanSince("heavy-1 1.2", st0.Add(time.Minute)))
	s.MarkReported(KindSlotOrphaned, "heavy-1 1.2")
	require.NoError(t, s.Flush())

	r, err := OpenState(p)
	require.NoError(t, err)
	require.False(t, r.Sighting("pnpm build", st0.Add(2*time.Hour)), "a signature seen before the restart is not new")
	require.Equal(t, st0, r.OrphanSince("heavy-1 1.2", st0.Add(time.Hour)), "orphan time is not reset by a restart")
	require.True(t, r.Reported(KindSlotOrphaned, "heavy-1 1.2"))
	require.False(t, r.Reported(KindSlotLong, "heavy-1 1.2"))
	sig := r.Signatures()["pnpm build"]
	require.Equal(t, 3, sig.Count)
	require.Equal(t, st0, sig.First)
	require.Equal(t, st0.Add(2*time.Hour), sig.Last)
}

func TestStateStoreKeepDropsTheRest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s, _ := OpenState(p)
	s.OrphanSince("a", st0)
	s.OrphanSince("b", st0)
	s.MarkReported(KindSlotLong, "a")
	s.MarkReported(KindSlotLong, "b")
	s.KeepOrphans(map[string]bool{"a": true})
	s.KeepReported(map[string]bool{"b": true})
	require.NoError(t, s.Flush())
	r, _ := OpenState(p)
	require.Equal(t, st0.Add(time.Hour), r.OrphanSince("b", st0.Add(time.Hour)), "b was dropped, so it starts again")
	require.Equal(t, st0, r.OrphanSince("a", st0.Add(time.Hour)))
	require.False(t, r.Reported(KindSlotLong, "a"))
	require.True(t, r.Reported(KindSlotLong, "b"))
}

func TestStateStoreFlushOnlyWhenDirty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s, _ := OpenState(p)
	require.NoError(t, s.Flush())
	_, err := os.Stat(p)
	require.True(t, os.IsNotExist(err), "nothing changed, nothing written")
	s.Sighting("x", st0)
	require.NoError(t, s.Flush())
	fi1, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi1.Mode().Perm())
	require.NoError(t, os.Remove(p))
	require.NoError(t, s.Flush())
	_, err = os.Stat(p)
	require.True(t, os.IsNotExist(err), "a second flush with no change writes nothing")
	s.KeepOrphans(map[string]bool{}) // no orphan to drop: not a change
	require.NoError(t, s.Flush())
	_, err = os.Stat(p)
	require.True(t, os.IsNotExist(err))
}

func TestStateStoreCorruptFileIsSetAside(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	require.NoError(t, os.WriteFile(p, []byte("{not json"), 0o600))
	s, err := OpenState(p)
	require.Error(t, err, "the caller logs it")
	require.NotNil(t, s, "but the store is usable")
	require.True(t, s.Sighting("x", st0))
	_, err = os.Stat(p + ".corrupt")
	require.NoError(t, err)
}

// "new" means the first sighting on this host, however long ago (spec §12): a signature is never forgotten.
func TestStateStoreNeverForgetsASignature(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s, _ := OpenState(p)
	s.Sighting("old", st0)
	require.NoError(t, s.Flush())
	r, _ := OpenState(p)
	require.False(t, r.Sighting("old", st0.Add(400*24*time.Hour)))
}

func TestStateStoreAtomicNoTempLeft(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenState(filepath.Join(dir, "state.json"))
	s.Sighting("x", st0)
	require.NoError(t, s.Flush())
	ents, _ := os.ReadDir(dir)
	require.Len(t, ents, 1)
}

func TestStateStoreFlushKeepsDirtyWhenTheDirectorySyncFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s, _ := OpenState(p)
	old := syncDir
	defer func() { syncDir = old }()
	syncDir = func(string) error { return os.ErrInvalid }
	s.Sighting("x", st0)
	require.Error(t, s.Flush())
	syncDir = old
	require.NoError(t, os.Remove(p))
	require.NoError(t, s.Flush(), "still dirty: the next pass writes it again")
	_, err := os.Stat(p)
	require.NoError(t, err)
}
