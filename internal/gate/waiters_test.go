package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Status lists who waits, in order, with how long, what and where (#2941).
func TestStatus_ListsTheWaitersInOrder(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	hold, release := holdSlot(t, g, ClassHeavy, "a")
	eventually(t, "the slot to be held", func() bool { return len(g.Status().Heavy) == 1 })
	dir := t.TempDir()
	first := runAsync(g, context.Background(), Options{Argv: sh("true"), Class: ClassHeavy, Dir: dir})
	eventually(t, "the first to queue", func() bool { return g.Status().WaitHeavy == 1 })
	second := runAsync(g, context.Background(), Options{Argv: []string{"sh", "-c", "exit 0", "--token", "s3cret"}, Class: ClassHeavy})
	eventually(t, "the second to queue", func() bool { return g.Status().WaitHeavy == 2 })

	st := g.Status()
	require.Len(t, st.QueueHeavy, 2)
	assert.Equal(t, 1, st.QueueHeavy[0].Place)
	assert.Equal(t, 2, st.QueueHeavy[1].Place)
	assert.Equal(t, ClassHeavy, st.QueueHeavy[0].Class)
	assert.Equal(t, os.Getpid(), st.QueueHeavy[0].PID, "the gate that waits (this process stands in for it)")
	assert.Equal(t, []string{"sh", "-c", "true"}, st.QueueHeavy[0].Argv)
	assert.Equal(t, dir, st.QueueHeavy[0].Cwd)
	assert.WithinDuration(t, time.Now(), st.QueueHeavy[0].Since, 5*time.Second)
	assert.True(t, st.QueueHeavy[0].Since.Before(st.QueueHeavy[1].Since) || st.QueueHeavy[0].Since.Equal(st.QueueHeavy[1].Since))
	assert.NotContains(t, strings.Join(st.QueueHeavy[1].Argv, " "), "s3cret", "the listing is scrubbed like the holder record")
	assert.Empty(t, st.QueueLight)
	assert.Equal(t, 2, st.WaitHeavy)

	release()
	<-hold
	<-first
	<-second
	assert.Empty(t, g.Status().QueueHeavy)
}

// A ticket made without information (an older gate) is listed with what its name says; a dead one is not listed and not removed.
func TestWaiters_AnEmptyTicketIsListedByItsNameAndADeadOneIsNot(t *testing.T) {
	p := &Pool{Dir: t.TempDir(), Class: ClassLight, Size: 1, Now: time.Now}
	tk, err := p.Enqueue()
	require.NoError(t, err)
	defer tk.Drop()
	w := p.Waiters()
	require.Len(t, w, 1)
	assert.Equal(t, 1, w[0].Place)
	assert.Equal(t, os.Getpid(), w[0].PID)
	assert.Empty(t, w[0].Argv)
	assert.WithinDuration(t, time.Now(), w[0].Since, 5*time.Second)

	dead := filepath.Join(p.queueDir(), "00000000000000000001-999999-000001")
	require.NoError(t, os.WriteFile(dead, nil, 0o644))
	assert.Len(t, p.Waiters(), 1, "a ticket nobody holds is not a waiter")
	_, err = os.Stat(dead)
	assert.NoError(t, err, "and listing does not remove it")
}

func TestWaiters_AMalformedTicketBodyDoesNotHideTheWaiter(t *testing.T) {
	p := &Pool{Dir: t.TempDir(), Class: ClassHeavy, Size: 1, Now: time.Now}
	tk, err := p.Enqueue()
	require.NoError(t, err)
	defer tk.Drop()
	_, err = tk.f.WriteString("{not json")
	require.NoError(t, err)
	w := p.Waiters()
	require.Len(t, w, 1)
	assert.Empty(t, w[0].Argv)
}
