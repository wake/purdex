package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHolder_MissingFileIsNoneAndUpdatesSurviveARestart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "watch", "holder.json")
	s, err := OpenHolder(p)
	require.NoError(t, err)
	require.Equal(t, HolderNone, s.Get().State)

	at := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	require.NoError(t, s.Update(func(h *Holder) { h.State, h.SessionID, h.Since, h.LastTurn = HolderAwake, "sess-1", at, at }))
	fi, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	r, err := OpenHolder(p)
	require.NoError(t, err)
	require.Equal(t, HolderAwake, r.Get().State)
	require.Equal(t, "sess-1", r.Get().SessionID)
	require.True(t, r.Get().LastTurn.Equal(at))

	require.NoError(t, r.Update(func(h *Holder) { h.State = HolderResting }))
	r2, _ := OpenHolder(p)
	require.Equal(t, HolderResting, r2.Get().State)
	require.Equal(t, "sess-1", r2.Get().SessionID, "resting keeps the session id for the next wake")
}

func TestHolder_AnInvalidStateIsRefusedAndKeepsTheOldOne(t *testing.T) {
	p := filepath.Join(t.TempDir(), "holder.json")
	s, _ := OpenHolder(p)
	require.NoError(t, s.Update(func(h *Holder) { h.State, h.SessionID = HolderAwake, "s" }))
	for name, fn := range map[string]func(*Holder){
		"awake without a session": func(h *Holder) { h.SessionID = "" },
		"none with a session":     func(h *Holder) { h.State = HolderNone },
		"an unknown state":        func(h *Holder) { h.State = "dozing" },
	} {
		require.Error(t, s.Update(fn), name)
		require.Equal(t, HolderAwake, s.Get().State, name)
	}
	require.NoError(t, s.Update(func(h *Holder) {}), "no change writes nothing and is no error")
}

func TestHolder_ACorruptOrInvalidFileIsSetAside(t *testing.T) {
	for name, content := range map[string]string{"not json": "{nope", "awake without session": `{"state":"awake"}`, "unknown state": `{"state":"x"}`} {
		p := filepath.Join(t.TempDir(), "holder.json")
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
		s, err := OpenHolder(p)
		require.Error(t, err, name)
		require.Equal(t, HolderNone, s.Get().State, name)
		_, serr := os.Stat(p + ".corrupt")
		require.NoError(t, serr, name)
	}
}

func TestHolder_AFailedWriteLeavesTheStateAsItWas(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "holder.json")
	s, _ := OpenHolder(p)
	require.NoError(t, s.Update(func(h *Holder) { h.State, h.SessionID = HolderAwake, "s" }))
	old := syncDir
	defer func() { syncDir = old }()
	syncDir = func(string) error { return os.ErrInvalid }
	require.Error(t, s.Update(func(h *Holder) { h.State = HolderResting }))
	// the rename happened but its sync failed: the in-memory state did not move, the caller retries
	require.Equal(t, HolderAwake, s.Get().State)
	ents, _ := os.ReadDir(dir)
	require.Len(t, ents, 1, "no temp file left")
}
