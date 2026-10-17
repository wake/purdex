package queue

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The caller may open wwd.db with one connection (other stores of this repo do): a write holds its connection in the transaction,
// and the event for it is read after the commit through another one, so the write must give its connection back first. Mutation
// gate: the connection kept until write returns → Submit never returns and this times out.
func TestSubmitWithASingleConnectionDatabaseDoesNotDeadlockOnPublish(t *testing.T) {
	f := newFixture(t)
	f.db.SetMaxOpenConns(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := f.q.Submit(context.Background(), sub())
		assert.NoError(t, err)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Submit is stuck: the event read waits for the connection the write still holds")
	}
	assert.Len(t, f.ev.all(), 1)
}

// A committed change is announced even when the caller's context ended right after the commit (a client that hung up): the job exists,
// so the event must not be lost. Mutation gate: Get with the request context → the event is dropped.
func TestPublishSurvivesACanceledRequestContext(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	f.ev.reset()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.q.publish(ctx, []string{id})

	evs := f.ev.all()
	require.Len(t, evs, 1)
	assert.Equal(t, id, evs[0].Job.ID)
}
