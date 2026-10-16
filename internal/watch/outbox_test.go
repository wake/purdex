package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type outboxClock struct{ t time.Time }

func (c *outboxClock) now() time.Time      { return c.t }
func (c *outboxClock) add(d time.Duration) { c.t = c.t.Add(d) }
func newOutboxClock() *outboxClock {
	return &outboxClock{t: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
}

func openTestOutbox(t *testing.T, c *outboxClock) (*Outbox, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "outbox.json")
	o, err := OpenOutbox(path, c.now)
	require.NoError(t, err)
	return o, path
}

var seq int

func rec(kind Kind, c *outboxClock) Record {
	seq++
	return Record{ID: fmt.Sprintf("mlab-%04d", seq), Kind: kind, Host: "mlab", At: c.t, Label: LabelGateGap, Argv: []string{"vitest", "run"}, PeakCores: 4, PeakMB: 2048}
}

func TestOutbox_TheFirstEventOfAKindIsAMessageAndTheNextInTenMinutesIsFolded(t *testing.T) {
	c := newOutboxClock()
	o, _ := openTestOutbox(t, c)
	r1 := rec(KindUnmanagedHeavy, c)
	require.True(t, o.Add(r1))
	c.add(time.Minute)
	require.False(t, o.Add(rec(KindUnmanagedHeavy, c)), "inside the window: folded")
	require.False(t, o.Add(rec(KindUnmanagedHeavy, c)))
	require.Equal(t, 1, o.Pending())
	m, ok := o.Next()
	require.True(t, ok)
	require.Equal(t, []string{r1.ID}, m.EventIDs)
	require.Contains(t, m.Text, "[負載守門]")
	require.Contains(t, m.Text, r1.ID, "the message names the event id")
}

func TestOutbox_FoldingIsPerKind(t *testing.T) {
	c := newOutboxClock()
	o, _ := openTestOutbox(t, c)
	require.True(t, o.Add(rec(KindUnmanagedHeavy, c)))
	require.True(t, o.Add(rec(KindPressureHigh, c)), "another kind has its own window")
	require.Equal(t, 2, o.Pending())
}

func TestOutbox_ASlotOrphanedEventIsNeverFolded(t *testing.T) {
	c := newOutboxClock()
	o, _ := openTestOutbox(t, c)
	for i := 0; i < 3; i++ {
		require.True(t, o.Add(rec(KindSlotOrphaned, c)))
	}
	require.Equal(t, 3, o.Pending())
}

func TestOutbox_FoldedEventsAreCountedIntoOneMessageWhenTheWindowEnds(t *testing.T) {
	c := newOutboxClock()
	o, _ := openTestOutbox(t, c)
	require.True(t, o.Add(rec(KindUnmanagedHeavy, c)))
	f1, f2 := rec(KindUnmanagedHeavy, c), rec(KindUnmanagedHeavy, c)
	c.add(time.Minute)
	o.Add(f1)
	o.Add(f2)
	require.Equal(t, 0, o.FlushFolded(), "the window is still open")
	due, ok := o.NextFold()
	require.True(t, ok)
	require.Equal(t, c.t.Add(9*time.Minute), due)

	c.add(9 * time.Minute)
	require.Equal(t, 1, o.FlushFolded())
	require.Equal(t, 2, o.Pending())
	o.Delivered(headID(t, o))
	m, _ := o.Next()
	require.Equal(t, []string{f1.ID, f2.ID}, m.EventIDs)
	require.Contains(t, m.Text, "2", "the count is in the text")
	require.Equal(t, 0, o.FlushFolded(), "flushed once")
	_, more := o.NextFold()
	require.False(t, more)
}

func headID(t *testing.T, o *Outbox) string {
	t.Helper()
	m, ok := o.Next()
	require.True(t, ok)
	return m.ID
}

func TestOutbox_ARetryIsTheSameMessageAndCountsNothingTwice(t *testing.T) {
	c := newOutboxClock()
	o, _ := openTestOutbox(t, c)
	r1 := rec(KindUnmanagedHeavy, c)
	o.Add(r1)
	m1, _ := o.Next()
	o.Tried(m1.ID)
	o.Tried(m1.ID)
	m2, _ := o.Next()
	require.Equal(t, m1.ID, m2.ID)
	require.Equal(t, m1.Text, m2.Text, "a retry resends the frozen text")
	require.Equal(t, 2, m2.Attempts)
	require.Equal(t, 1, o.Pending())
	// the window did not move on a failure: the next event is still folded
	c.add(time.Minute)
	require.False(t, o.Add(rec(KindUnmanagedHeavy, c)))
}

func TestOutbox_DeliveredRemovesTheMessageAndAdvancesTheLastDeliveredID(t *testing.T) {
	c := newOutboxClock()
	o, _ := openTestOutbox(t, c)
	r1 := rec(KindUnmanagedHeavy, c)
	o.Add(r1)
	require.Equal(t, "", o.LastDelivered())
	o.Delivered(headID(t, o))
	require.Equal(t, 0, o.Pending())
	require.Equal(t, r1.ID, o.LastDelivered())
	o.Delivered("no-such-message") // a message that is not there changes nothing
	require.Equal(t, r1.ID, o.LastDelivered())
}

func TestOutbox_APendingQueueSurvivesARestart(t *testing.T) {
	c := newOutboxClock()
	o, path := openTestOutbox(t, c)
	r1 := rec(KindUnmanagedHeavy, c)
	o.Add(r1)
	c.add(time.Minute)
	f := rec(KindUnmanagedHeavy, c)
	o.Add(f)
	m1, _ := o.Next()
	o.Tried(m1.ID)

	o2, err := OpenOutbox(path, c.now)
	require.NoError(t, err)
	m2, ok := o2.Next()
	require.True(t, ok)
	require.Equal(t, m1.ID, m2.ID)
	require.Equal(t, 1, m2.Attempts)
	c.add(9 * time.Minute)
	require.Equal(t, 1, o2.FlushFolded(), "the folded event and the window survive too")
}

func TestOutbox_AFileThatCannotBeReadIsSetAsideAndTheQueueStartsEmpty(t *testing.T) {
	c := newOutboxClock()
	path := filepath.Join(t.TempDir(), "outbox.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	o, err := OpenOutbox(path, c.now)
	require.Error(t, err, "reported, not fatal")
	require.NotNil(t, o)
	require.Equal(t, 0, o.Pending())
	_, statErr := os.Stat(path + ".bad")
	require.NoError(t, statErr, "the bad file is kept for a look")
}

func TestOutbox_BacklogReplacesThePendingMessagesWithOneSummaryOfTheUndeliveredEventsOnce(t *testing.T) {
	c := newOutboxClock()
	o, _ := openTestOutbox(t, c)
	delivered := rec(KindUnmanagedHeavy, c)
	o.Add(delivered)
	o.Delivered(headID(t, o))
	// three more arrive, the send fails: they wait in the queue
	var later []Record
	for _, k := range []Kind{KindPressureHigh, KindSlotOrphaned, KindSlotLong} {
		r := rec(k, c)
		later = append(later, r)
		o.Add(r)
	}
	require.Equal(t, 3, o.Pending())

	all := append([]Record{delivered}, later...)
	require.True(t, o.Backlog("sess-new", all))
	require.Equal(t, 1, o.Pending(), "the pending messages are covered by the summary")
	m, _ := o.Next()
	require.True(t, m.Backlog)
	require.Equal(t, []string{later[0].ID, later[1].ID, later[2].ID}, m.EventIDs, "only the events after the last delivered one")
	require.Contains(t, m.Text, "pdx watch log --all")
	require.Equal(t, "sess-new", o.BacklogFor())

	require.False(t, o.Backlog("sess-new", all), "once per watcher session")
	require.Equal(t, 1, o.Pending())
}

func TestOutbox_BacklogWithNothingUndeliveredMakesNoMessageButIsRemembered(t *testing.T) {
	c := newOutboxClock()
	o, path := openTestOutbox(t, c)
	require.False(t, o.Backlog("sess-1", nil))
	require.Equal(t, 0, o.Pending())
	o2, err := OpenOutbox(path, c.now)
	require.NoError(t, err)
	require.Equal(t, "sess-1", o2.BacklogFor(), "remembered across a restart")
}

func TestOutbox_TheBacklogListsTheNewestFiveAndCountsTheRest(t *testing.T) {
	c := newOutboxClock()
	o, _ := openTestOutbox(t, c)
	var recs []Record
	for i := 0; i < 8; i++ {
		recs = append(recs, rec(KindUnmanagedHeavy, c))
	}
	require.True(t, o.Backlog("s", recs))
	m, _ := o.Next()
	require.Len(t, m.EventIDs, 8, "every id is in the message")
	for _, r := range recs[3:] {
		require.Contains(t, m.Text, r.ID, "the newest five are listed")
	}
	require.NotContains(t, m.Text, recs[0].ID)
	require.Contains(t, m.Text, "8")
}

func TestInbox_AnEventIsDeliveredOnceAndRemembered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delivered.json")
	in, err := OpenInbox(path)
	require.NoError(t, err)
	require.False(t, in.Seen([]string{"a", "b"}))
	require.False(t, in.Seen(nil), "nothing to dedupe on")
	require.NoError(t, in.Mark([]string{"a", "b"}))
	require.True(t, in.Seen([]string{"a", "b"}))
	require.True(t, in.Seen([]string{"b"}))
	require.False(t, in.Seen([]string{"a", "c"}), "one new id: not a duplicate")

	in2, err := OpenInbox(path)
	require.NoError(t, err)
	require.True(t, in2.Seen([]string{"a", "b"}), "across a restart")
}

func TestInbox_KeepsOnlyTheLatestIDs(t *testing.T) {
	in, err := OpenInbox(filepath.Join(t.TempDir(), "d.json"))
	require.NoError(t, err)
	for i := 0; i < inboxKeep+10; i++ {
		require.NoError(t, in.Mark([]string{fmt.Sprintf("id-%05d", i)}))
	}
	require.False(t, in.Seen([]string{"id-00000"}), "the oldest is forgotten")
	require.True(t, in.Seen([]string{fmt.Sprintf("id-%05d", inboxKeep+9)}))
}
