package queue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The caller may open wwd.db with one connection (other stores of this repo do): a write holds its connection in the transaction,
// and the connection goes back before the events are published. Mutation gate: the connection kept until write returns → a
// publisher that touches the queue (Get) never returns and this times out.
func TestSubmitWithASingleConnectionDatabaseDoesNotDeadlockOnPublish(t *testing.T) {
	f := newFixture(t)
	f.db.SetMaxOpenConns(1)
	f.q.pub = func(ev wire.JobEvent) {
		_, err := f.q.Get(context.Background(), ev.Job.ID) // a subscriber that reads back needs a connection
		assert.NoError(t, err)
		f.ev.publish(ev)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := f.q.Submit(context.Background(), sub())
		assert.NoError(t, err)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Submit is stuck: the publisher waits for the connection the write still holds")
	}
	assert.Len(t, f.ev.all(), 1)
}

// The event of a change is the job as that change left it, read inside the transaction: another submission that commits between
// this one's commit and its publish must not turn this event into the newer state (it would announce "superseded" twice and "queued"
// never). Mutation gate: the job read after the commit → the first event says superseded.
func TestEventIsTheStateTheChangeLeftNotALaterOne(t *testing.T) {
	f := newFixture(t)
	secondDone := make(chan struct{})
	f.q.afterCommit = func() {
		f.q.afterCommit = nil
		// a second submission with the same key commits between the first one's commit and its publish; its events must wait their turn
		go func() {
			defer close(secondDone)
			_, err := f.q.Submit(context.Background(), sub())
			assert.NoError(t, err)
		}()
		require.Eventually(t, func() bool {
			var n int
			_ = f.db.QueryRow(`SELECT count(*) FROM jobs WHERE state = 'superseded'`).Scan(&n)
			return n == 1 // the second submission has committed
		}, 10*time.Second, 5*time.Millisecond)
	}
	r, err := f.q.Submit(context.Background(), sub())
	require.NoError(t, err)
	first := r.ID
	<-secondDone

	states := map[string][]wire.JobState{}
	for _, e := range f.ev.all() {
		states[e.Job.ID] = append(states[e.Job.ID], e.Job.State)
	}
	// the job is announced queued (its own submission) and then superseded (by the second), each exactly once and in commit order
	assert.Equal(t, []wire.JobState{wire.StateQueued, wire.StateSuperseded}, states[first])
	assert.Len(t, f.ev.all(), 3, "the first job queued, the second queued, the first superseded")
}

// A committed change is announced with the request's context already canceled: the payload was built in the transaction, so a client
// that hung up right after the commit loses nothing.
func TestEventIsPublishedEvenWhenTheRequestContextEndsAfterTheCommit(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.q.afterCommit = func() { cancel() }
	r, err := f.q.Submit(ctx, sub())
	require.NoError(t, err)
	evs := f.ev.all()
	require.Len(t, evs, 1)
	assert.Equal(t, r.ID, evs[0].Job.ID)
	assert.Equal(t, wire.StateQueued, evs[0].Job.State)
}

// A subscriber that panics must not turn a stored job into a failed request (the client would submit it again): the panic is caught.
// Mutation gate: no recover → Submit panics.
func TestAPanickingPublisherDoesNotFailASubmit(t *testing.T) {
	f := newFixture(t)
	f.q.pub = func(wire.JobEvent) { panic("subscriber bug") }
	r, err := f.q.Submit(context.Background(), sub())
	require.NoError(t, err)
	_, err = f.q.Get(context.Background(), r.ID)
	require.NoError(t, err, "the job is stored")
}

// A peer reads through GetForPeers: an ad-hoc job is not found, whatever its id (spec 4.4, 8.3, 11). The local Get still sees it.
// Mutation gate: the adhoc condition dropped → the peer read returns the job.
func TestGetForPeersNeverReturnsAnAdHocJob(t *testing.T) {
	f := newFixture(t)
	normal := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	adhoc := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey, s.AdHoc = "", true })
	ctx := context.Background()

	j, err := f.q.GetForPeers(ctx, normal)
	require.NoError(t, err)
	assert.Equal(t, normal, j.ID)

	_, err = f.q.GetForPeers(ctx, adhoc)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = f.q.Get(ctx, adhoc)
	assert.NoError(t, err, "the local read sees it")
}

// Tickets are served in the order they were taken, whatever order the holders ask for their turn in. Mutation gate: await returns at
// once → the order is the order of arrival.
func TestSequencerServesTicketsInTheOrderTheyWereTaken(t *testing.T) {
	s := newSequencer()
	tk := []uint64{s.ticket(), s.ticket(), s.ticket()}
	var mu sync.Mutex
	var order []uint64
	var wg sync.WaitGroup
	for _, i := range []int{2, 0, 1} { // asked in the reverse of the order taken
		wg.Add(1)
		go func(t uint64) {
			defer wg.Done()
			s.await(t)
			mu.Lock()
			order = append(order, t)
			mu.Unlock()
			s.done(t)
		}(tk[i])
		time.Sleep(20 * time.Millisecond) // let each one reach await before the next asks
	}
	wg.Wait()
	assert.Equal(t, []uint64{0, 1, 2}, order)
}

// A COMMIT that fails after the ticket was taken (here: the request's context ended in between) hands the ticket back and stores
// nothing, so the writes after it still publish. Mutation gate: the ticket of a failed commit never passed on → the next Submit hangs.
func TestACommitThatFailsAfterTheTicketPassesTheTurnOn(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.q.afterTicket = func() { f.q.afterTicket = nil; cancel() }
	_, err := f.q.Submit(ctx, sub(func(s *wire.JobSubmission) { s.SupersedeKey = "" }))
	require.Error(t, err)
	var n int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&n))
	assert.Zero(t, n, "the failed commit stored nothing")

	done := make(chan error, 1)
	go func() {
		_, err := f.q.Submit(context.Background(), sub(func(s *wire.JobSubmission) { s.SupersedeKey = "" }))
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the Submit after a failed commit never returned: its ticket was not passed on")
	}
	assert.Len(t, f.ev.all(), 1)
}

// A write that fails (here: an id that exists already) takes no ticket or hands it back, so the writes after it still publish. Mutation
// gate: the ticket of a failed commit never passed on → the Submit after it hangs.
func TestAFailedWriteDoesNotStopTheEventsAfterIt(t *testing.T) {
	f := newFixture(t)
	f.q.newI = func() string { return "j_FIXED" }
	_, err := f.q.Submit(context.Background(), sub(func(s *wire.JobSubmission) { s.SupersedeKey = "" }))
	require.NoError(t, err)
	_, err = f.q.Submit(context.Background(), sub(func(s *wire.JobSubmission) { s.SupersedeKey = "" })) // same id: the insert fails
	require.Error(t, err)

	f.q.newI = func() string { return newJobID(f.clk.Now()) }
	done := make(chan error, 1)
	go func() {
		_, err := f.q.Submit(context.Background(), sub(func(s *wire.JobSubmission) { s.SupersedeKey = "" }))
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("a Submit after a failed one never returned: the failed write's turn was not passed on")
	}
	assert.Len(t, f.ev.all(), 2, "one event for the first job and one for the third; none for the failed one")
}
