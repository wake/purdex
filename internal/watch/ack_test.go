package watch

import (
	"encoding/json"
	"errors"
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

// A batch is written whole or not at all: a write that fails leaves the file as it was, so a restart does not find half of an ack.
func TestAckStore_AFailedBatchLeavesTheFileAsItWas(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made unwritable for root")
	}
	c := newOutboxClock()
	s, dir := openAcks(t, c)
	known := func(string) bool { return true }
	_, err := s.Ack([]string{"mlab-1"}, known, Ack{Note: "first"})
	require.NoError(t, err)
	before, err := os.ReadFile(filepath.Join(dir, ackFile))
	require.NoError(t, err)

	require.NoError(t, os.Chmod(dir, 0o500)) // no new file can be made in it
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err = s.Ack([]string{"mlab-2", "mlab-3"}, known, Ack{Note: "second"})
	require.Error(t, err)
	require.NoError(t, os.Chmod(dir, 0o700))
	after, err := os.ReadFile(filepath.Join(dir, ackFile))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "the file is as it was")
	again, err := OpenAcks(dir, c.now)
	require.NoError(t, err)
	require.False(t, again.Handled("mlab-2"))
	require.False(t, s.Handled("mlab-2"), "and so is memory")
	require.True(t, again.Handled("mlab-1"))
}

func TestHandledRecord_CarriesTheAckInTheLogJSON(t *testing.T) {
	c := newOutboxClock()
	r := rec(KindPressureHigh, c)
	got := HandledRecord(r, nil)
	require.False(t, got.Handled)
	got = HandledRecord(r, &Ack{Note: "ok", At: c.t, By: "mlab/w"})
	require.True(t, got.Handled)
	require.Equal(t, "ok", got.Note)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"handled":true`)
	require.Contains(t, string(raw), `"note":"ok"`)
	require.Contains(t, string(raw), `"id":"`+r.ID+`"`, "the record's own fields are still there, flat")
}

// A rename that went through and a directory sync that failed after it is a committed ack: the file has it, so memory has it too, and a
// later ack does not rewrite the file without it.
func TestAckStore_ARenameThatWentThroughIsCommittedEvenIfTheDirectorySyncFailed(t *testing.T) {
	c := newOutboxClock()
	s, dir := openAcks(t, c)
	known := func(string) bool { return true }
	old := syncDir
	syncDir = func(string) error { return errors.New("sync failed") }
	t.Cleanup(func() { syncDir = old })

	res, err := s.Ack([]string{"mlab-1"}, known, Ack{Note: "first"})
	require.NoError(t, err, "the ack is on disk: not a failure")
	require.Equal(t, []AckResult{{ID: "mlab-1", Outcome: AckHandled}}, res)
	require.True(t, s.Handled("mlab-1"))

	syncDir = old
	_, err = s.Ack([]string{"mlab-1", "mlab-2"}, known, Ack{Note: "second"})
	require.NoError(t, err)
	again, err := OpenAcks(dir, c.now)
	require.NoError(t, err)
	a, ok := again.Get("mlab-1")
	require.True(t, ok, "the first ack is still in the file")
	require.Equal(t, "first", a.Note, "and its note is the first")
	require.True(t, again.Handled("mlab-2"))
}

// A line of the file that is not an ack is not dropped quietly: the file is kept as acks.jsonl.bad, the acks that could be read go
// on, and the open says so.
func TestOpenAcks_ADamagedLineKeepsTheFileAsBadAndTheRestGoesOn(t *testing.T) {
	c := newOutboxClock()
	dir := t.TempDir()
	good := `{"id":"mlab-1","note":"a","at":"` + c.t.Format(time.RFC3339) + `"}`
	good2 := `{"id":"mlab-2","note":"b","at":"` + c.t.Format(time.RFC3339) + `"}`
	for name, body := range map[string]string{
		"a torn last line":    good + "\n" + `{"id":"mlab-2","no`,
		"a bad line in front": "not json\n" + good + "\n" + good2 + "\n",
	} {
		require.NoError(t, os.RemoveAll(dir))
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ackFile), []byte(body), 0o600))
		s, err := OpenAcks(dir, c.now)
		require.Error(t, err, name)
		require.True(t, s.Handled("mlab-1"), name)
		kept, gerr := filepath.Glob(filepath.Join(dir, ackFile+".bad*"))
		require.NoError(t, gerr, name)
		require.Len(t, kept, 1, name)
		raw, rerr := os.ReadFile(kept[0])
		require.NoError(t, rerr, name)
		require.Equal(t, body, string(raw), "the damaged file is kept as it was: "+name)

		_, err = s.Ack([]string{"mlab-9"}, func(string) bool { return true }, Ack{Note: "n"})
		require.NoError(t, err, name)
		again, err := OpenAcks(dir, c.now)
		require.NoError(t, err, "the rewritten file is whole: "+name)
		require.True(t, again.Handled("mlab-1") && again.Handled("mlab-9"), name)
	}
}

// A second damage does not overwrite the evidence of the first: every damaged file is kept under a name of its own.
func TestOpenAcks_EveryDamagedFileIsKeptUnderAUniqueName(t *testing.T) {
	c := newOutboxClock()
	dir := t.TempDir()
	for i, body := range []string{"first damage\n", "second damage\n", "third damage\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ackFile), []byte(body), 0o600))
		_, err := OpenAcks(dir, c.now) // the same clock: the names must not depend on it alone
		require.Error(t, err, i)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ackFile+".bad*"))
	require.NoError(t, err)
	require.Len(t, matches, 3)
	var bodies []string
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		require.NoError(t, err)
		bodies = append(bodies, string(raw))
	}
	require.ElementsMatch(t, []string{"first damage\n", "second damage\n", "third damage\n"}, bodies)
}
