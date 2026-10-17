package watch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionStore_KeepsTheInProgressMarkAndTheCooldownAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	s, err := OpenSession(path)
	require.NoError(t, err)
	require.Nil(t, s.Get().InProgress)

	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	require.NoError(t, s.Update(func(st *SessionState) {
		st.InProgress = &InProgress{Kind: "start", Since: at, Frame: "f0"}
		st.CooldownUntil = at.Add(30 * time.Minute)
	}))
	again, err := OpenSession(path)
	require.NoError(t, err)
	got := again.Get()
	require.Equal(t, &InProgress{Kind: "start", Since: at, Frame: "f0"}, got.InProgress)
	require.True(t, got.CooldownUntil.Equal(at.Add(30*time.Minute)))

	require.NoError(t, again.Update(func(st *SessionState) { st.InProgress = nil }))
	back, _ := OpenSession(path)
	require.Nil(t, back.Get().InProgress, "cleared and kept cleared")
}

func TestSessionStore_AFileThatCannotBeReadIsSetAsideAndReadsAsNothingInProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	require.NoError(t, os.WriteFile(path, []byte("{nope"), 0o600))
	s, err := OpenSession(path)
	require.Error(t, err, "reported, not fatal")
	require.Nil(t, s.Get().InProgress)
	_, statErr := os.Stat(path + ".corrupt")
	require.NoError(t, statErr, "kept for a look")
}

func TestSessionStore_AFailedWriteChangesNothing(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub"), nil, 0o600))
	s, _ := OpenSession(filepath.Join(dir, "sub", "session.json")) // the directory is a file
	err := s.Update(func(st *SessionState) { st.CooldownUntil = time.Now().Add(time.Hour) })
	require.Error(t, err)
	require.True(t, s.Get().CooldownUntil.IsZero(), "memory says what the file does")
}

func TestCooldownError(t *testing.T) {
	until := time.Date(2026, 10, 17, 12, 30, 0, 0, time.UTC)
	var err error = &CooldownError{Until: until}
	var ce *CooldownError
	require.True(t, errors.As(err, &ce))
	require.True(t, ce.Until.Equal(until))
	require.Contains(t, err.Error(), "2026-10-17")
}

func TestJoinWakePrompt(t *testing.T) {
	got, err := JoinWakePrompt("wake up", "")
	require.NoError(t, err)
	require.Equal(t, "wake up", got, "no prompt: the wake prompt itself, without a trailing space")

	got, err = JoinWakePrompt("wake up", "check disk")
	require.NoError(t, err)
	require.Equal(t, "wake up check disk", got)

	for name, extra := range map[string]string{"a newline": "a\nb", "a control": "x\x1b[0m", "too long": strings.Repeat("a", 4096), "not utf-8": "\xff"} {
		_, err = JoinWakePrompt("wake up", extra)
		require.ErrorIs(t, err, ErrBadPrompt, name)
	}
	_, err = JoinWakePrompt("wake up", strings.Repeat("a", 4096-len("wake up ")))
	require.NoError(t, err, "4 KB in all is the most")
}
