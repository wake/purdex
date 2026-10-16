package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func openAcks(t *testing.T, c *outboxClock) (*AckStore, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenAcks(dir, c.now)
	require.NoError(t, err)
	return s, dir
}

func TestAckStore_AnEventIsPendingUntilAcked_AndTheNoteAndWhoAreKept(t *testing.T) {
	c := newOutboxClock()
	s, dir := openAcks(t, c)
	known := func(id string) bool { return id == "mlab-1" || id == "mlab-2" }
	require.False(t, s.Handled("mlab-1"))

	res, err := s.Ack([]string{"mlab-1", "nope"}, known, Ack{Note: "已查，是 vitest", By: "mlab/watcher", FromHost: "a26"})
	require.NoError(t, err)
	require.Equal(t, []AckResult{{ID: "mlab-1", Outcome: AckHandled}, {ID: "nope", Outcome: AckNotFound}}, res)
	a, ok := s.Get("mlab-1")
	require.True(t, ok)
	require.Equal(t, "已查，是 vitest", a.Note)
	require.Equal(t, "mlab/watcher", a.By)
	require.Equal(t, "a26", a.FromHost)
	require.Equal(t, c.t, a.At)
	require.False(t, s.Handled("nope"), "an unknown id is not recorded")

	again, err := OpenAcks(dir, c.now)
	require.NoError(t, err)
	b, ok := again.Get("mlab-1")
	require.True(t, ok, "kept in the file")
	require.Equal(t, a, b)
}

func TestAckStore_AckingAgainChangesNothing(t *testing.T) {
	c := newOutboxClock()
	s, dir := openAcks(t, c)
	known := func(string) bool { return true }
	_, err := s.Ack([]string{"mlab-1"}, known, Ack{Note: "first", By: "x"})
	require.NoError(t, err)
	c.add(time.Hour)
	res, err := s.Ack([]string{"mlab-1"}, known, Ack{Note: "second", By: "y"})
	require.NoError(t, err)
	require.Equal(t, []AckResult{{ID: "mlab-1", Outcome: AckAlready}}, res)
	a, _ := s.Get("mlab-1")
	require.Equal(t, "first", a.Note, "the note is not replaced")
	require.Equal(t, "x", a.By)
	raw, err := os.ReadFile(filepath.Join(dir, ackFile))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(raw), "\n"), "and nothing is appended")
}

func TestAckStore_AFailedWriteAcksNothing(t *testing.T) {
	c := newOutboxClock()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub"), nil, 0o600))
	s, _ := OpenAcks(filepath.Join(dir, "sub"), c.now) // the directory is a file
	_, err := s.Ack([]string{"mlab-1"}, func(string) bool { return true }, Ack{Note: "n"})
	require.Error(t, err)
	require.False(t, s.Handled("mlab-1"), "not handled in memory either: a status must not say what the file does not")
}

func TestAckStore_AnOldAckIsForgottenWhenTheFileGrows(t *testing.T) {
	c := newOutboxClock()
	s, dir := openAcks(t, c)
	_, err := s.Ack([]string{"mlab-old"}, func(string) bool { return true }, Ack{Note: "n"})
	require.NoError(t, err)
	c.add(40 * 24 * time.Hour)
	_, err = s.Ack([]string{"mlab-new"}, func(string) bool { return true }, Ack{Note: "n"})
	require.NoError(t, err)
	s.Prune()
	again, err := OpenAcks(dir, c.now)
	require.NoError(t, err)
	require.False(t, again.Handled("mlab-old"), "the event it was about is out of the log by now")
	require.True(t, again.Handled("mlab-new"))
}

func TestValidateNote(t *testing.T) {
	require.NoError(t, ValidateNote("已處理：vitest 在 worktree 跑"))
	for name, n := range map[string]string{
		"empty":      "",
		"blank":      "   ",
		"a newline":  "line one\nline two",
		"a control":  "x\x1b[31m",
		"too long":   strings.Repeat("a", 501),
		"not utf-8":  "\xff\xfe",
		"a tab only": "\t",
	} {
		require.Error(t, ValidateNote(n), name)
	}
	require.NoError(t, ValidateNote(strings.Repeat("a", 500)), "500 bytes is the most")
}

func TestViewOf_ShowsTheStatusNoteAndWho(t *testing.T) {
	c := newOutboxClock()
	r := rec(KindUnmanagedHeavy, c)
	r.Argv = []string{"vitest", "run"}
	r.Holders = []int{7, 8}
	v := ViewOf(r, nil)
	require.Equal(t, "pending", v.Status)
	require.Equal(t, "vitest run", v.Command)
	require.Empty(t, v.Note)
	require.Equal(t, r.ID, v.ID)
	require.Equal(t, []int{7, 8}, v.LockPIDs)

	v = ViewOf(r, &Ack{Note: "ok", At: c.t, By: "mlab/w", FromHost: "a26"})
	require.Equal(t, "handled", v.Status)
	require.Equal(t, "ok", v.Note)
	require.Equal(t, "mlab/w", v.HandledBy)
	require.Equal(t, "a26", v.HandledFromHost)
	require.NotNil(t, v.HandledAt)
}

func TestCountUnhandled(t *testing.T) {
	c := newOutboxClock()
	a, b, d := rec(KindUnmanagedHeavy, c), rec(KindPressureHigh, c), rec(KindSlotLong, c)
	acks := map[string]Ack{b.ID: {Note: "n"}}
	require.Equal(t, 2, CountUnhandled([]Record{a, b, d}, acks))
	require.Equal(t, 0, CountUnhandled(nil, acks))
}
