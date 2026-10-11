package nex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"lab.protype.tw/wake/nexen/store"
)

type ownerEnv struct {
	*takebackEnv
	now time.Time
}

func newOwnerEnv(t *testing.T, rows ...store.Execution) *ownerEnv {
	t.Helper()
	env := &ownerEnv{takebackEnv: newTakebackEnv(t), now: time.Unix(1000, 0)}
	env.store.listRows = rows
	env.m.execOwners.now = func() time.Time { return env.now }
	return env
}

func TestLiveWorker_NewestLiveWinsAndFillsRow(t *testing.T) {
	old := row("01A", "idle", false, tS, "", 1)
	newer := row("01B", "running", false, tS, "", 5)
	newer.TranscriptPath = "/x/" + tS + ".jsonl"
	gone := row("01C", "terminated", false, tS, "", 9) // newest, but terminated
	archived := row("01D", "idle", true, tS, "", 10)   // newest of all, archived
	env := newOwnerEnv(t, old, newer, gone, archived)

	o, ok, err := env.m.LiveWorker(context.Background(), tS)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, ExecOwner{ExecID: "01B", Light: "running", TranscriptPath: newer.TranscriptPath, Live: true}, o)
}

func TestLiveWorker_NoneAndError(t *testing.T) {
	env := newOwnerEnv(t, row("01A", "terminated", false, tS, "", 1))
	_, ok, err := env.m.LiveWorker(context.Background(), tS)
	require.NoError(t, err)
	assert.False(t, ok)

	env.store.listErr = errors.New("boom")
	env.now = env.now.Add(time.Minute)
	_, _, err = env.m.LiveWorker(context.Background(), tS)
	assert.Error(t, err, "a failed scan is 'could not tell', not 'none'")
	env.store.listErr = nil
	_, _, err = env.m.LiveWorker(context.Background(), tS)
	assert.NoError(t, err, "an error is not cached")
}

func TestLiveWorker_CacheHitAndExpiry(t *testing.T) {
	env := newOwnerEnv(t, row("01A", "idle", false, tS, "", 1))
	for i := 0; i < 3; i++ {
		_, ok, err := env.m.LiveWorker(context.Background(), tS)
		require.NoError(t, err)
		require.True(t, ok)
	}
	assert.Equal(t, 1, env.store.listCalls)
	env.now = env.now.Add(ownerCacheTTL + time.Millisecond)
	env.store.listRows = []store.Execution{row("01A", "running", false, tS, "", 1)}
	o, _, _ := env.m.LiveWorker(context.Background(), tS)
	assert.Equal(t, 2, env.store.listCalls)
	assert.Equal(t, "running", o.Light)
}

func TestLiveWorker_LightFromDigestElseRow(t *testing.T) {
	env := newOwnerEnv(t, row("01A", "running", false, tS, "", 1))
	// no projector digest: the row state, never waiting
	o, _, _ := env.m.LiveWorker(context.Background(), tS)
	assert.Equal(t, "running", o.Light)

	// a digest with a pending permission is waiting (same projection as the lights)
	p := &projector{pushed: map[string]pushedRow{"01A": {digest: rowDigest{State: "running", PermissionRequest: "p1"}}}}
	env.m.proj = p
	env.now = env.now.Add(time.Minute)
	o, _, _ = env.m.LiveWorker(context.Background(), tS)
	assert.Equal(t, "waiting", o.Light)

	// removed / unparsed digests are not trusted
	p.pushed["01A"] = pushedRow{removed: true}
	env.now = env.now.Add(time.Minute)
	o, _, _ = env.m.LiveWorker(context.Background(), tS)
	assert.Equal(t, "running", o.Light)
}

func TestRowLight(t *testing.T) {
	for state, want := range map[string]string{"running": "running", "queued": "running", "idle": "idle", "failed": "error", "rejected": "error"} {
		assert.Equal(t, want, rowLight(store.State(state)), state)
	}
}
