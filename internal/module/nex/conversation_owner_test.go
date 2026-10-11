package nex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/convfeed"
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

func TestExecutionOwns(t *testing.T) {
	other := "11111111-2222-4333-8444-555555555555"
	cases := map[string]struct {
		e    store.Execution
		want bool
	}{
		"own session":                  {row("a", "idle", false, tS, "", 1), true},
		"own session, other case":      {row("a", "idle", false, strings.ToUpper(tS), "", 1), true},
		"resume only, first turn open": {row("a", "idle", false, "", tS, 1), true},
		"resume only, other case":      {row("a", "idle", false, "", strings.ToUpper(tS), 1), true},
		"resumed, has its own id":      {row("a", "idle", false, other, tS, 1), false},
		"other session":                {row("a", "idle", false, other, "", 1), false},
		"neither":                      {row("a", "idle", false, "", "", 1), false},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, executionOwns(c.e, tS), name)
	}
	assert.False(t, executionOwns(row("a", "idle", false, "", "", 1), ""))
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
	assert.Equal(t, convfeed.ExecOwner{ExecID: "01B", Light: "running", TranscriptPath: newer.TranscriptPath, Live: true, At: env.now}, o)
}

func TestLiveWorker_ResumeOnlyMatchIsNotTheOwner(t *testing.T) {
	other := "11111111-2222-4333-8444-555555555555"
	// the newest row resumed S but is already running as another session; the older one is S's own
	resumed := row("01B", "running", false, other, tS, 9)
	own := row("01A", "idle", false, tS, "", 1)
	env := newOwnerEnv(t, own, resumed)
	o, ok, err := env.m.LiveWorker(context.Background(), tS)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "01A", o.ExecID)

	env = newOwnerEnv(t, resumed)
	_, ok, err = env.m.LiveWorker(context.Background(), tS)
	require.NoError(t, err)
	assert.False(t, ok, "only a resume-only match: not the owner")

	env = newOwnerEnv(t, row("01C", "idle", false, "", strings.ToUpper(tS), 3)) // first turn not reported yet
	o, ok, _ = env.m.LiveWorker(context.Background(), strings.ToUpper(tS))
	require.True(t, ok)
	assert.Equal(t, "01C", o.ExecID)
}

func TestLiveWorker_NoneAndErrorNegativeCache(t *testing.T) {
	env := newOwnerEnv(t, row("01A", "terminated", false, tS, "", 1))
	_, ok, err := env.m.LiveWorker(context.Background(), tS)
	require.NoError(t, err)
	assert.False(t, ok)

	env.store.listErr = errors.New("boom")
	env.now = env.now.Add(time.Minute)
	before := env.store.listCalls
	for i := 0; i < 3; i++ {
		_, _, err = env.m.LiveWorker(context.Background(), tS)
		assert.Error(t, err, "a failed scan is 'could not tell', not 'none'")
	}
	assert.Equal(t, before+1, env.store.listCalls, "the error is remembered briefly")

	env.store.listErr = nil
	env.now = env.now.Add(errCacheTTL + time.Millisecond)
	_, _, err = env.m.LiveWorker(context.Background(), tS)
	assert.NoError(t, err, "the error expires after errCacheTTL")
}

func TestLiveWorker_PartialScanIsAnError(t *testing.T) {
	var rows []store.Execution
	for i := 0; i < ownerScanPageSize+1; i++ {
		rows = append(rows, row(fmt.Sprintf("01%05d", i), "idle", false, tS, "", int64(i)))
	}
	env := newOwnerEnv(t, rows...)
	env.store.listErr, env.store.listErrAt = errors.New("page 2 failed"), 2
	_, ok, err := env.m.LiveWorker(context.Background(), tS)
	assert.Error(t, err)
	assert.False(t, ok, "rows found before the failure do not make an owner")
}

func TestLiveWorker_CacheHoldsIdentityOnly(t *testing.T) {
	env := newOwnerEnv(t, row("01A", "running", false, tS, "", 1))
	_, ok, err := env.m.LiveWorker(context.Background(), tS)
	require.NoError(t, err)
	require.True(t, ok)
	// a digest arrives: the very next call (cache hit) shows it
	p := &projector{pushed: map[string]pushedRow{"01A": {digest: rowDigest{State: "running", PermissionRequest: "p1"}}}}
	env.m.proj = p
	env.now = env.now.Add(time.Second)
	o, _, _ := env.m.LiveWorker(context.Background(), strings.ToUpper(tS)) // key is normalised
	assert.Equal(t, 1, env.store.listCalls)
	assert.Equal(t, "waiting", o.Light)
	assert.Equal(t, env.now, o.At, "At is when the light was read")

	p.pushed["01A"] = pushedRow{digest: rowDigest{State: "idle"}}
	o, _, _ = env.m.LiveWorker(context.Background(), tS)
	assert.Equal(t, "idle", o.Light)
	assert.Equal(t, 1, env.store.listCalls)

	env.now = env.now.Add(ownerCacheTTL + time.Millisecond)
	_, _, _ = env.m.LiveWorker(context.Background(), tS)
	assert.Equal(t, 2, env.store.listCalls, "identity expires after the TTL")
}

func TestLiveWorker_LightFromDigestElseRow(t *testing.T) {
	cases := map[string]string{"running": "running", "queued": "running", "idle": "idle", "failed": "error", "rejected": "error",
		"paused": "unknown", "terminating": "unknown", "": "unknown"}
	for state, want := range cases {
		env := newOwnerEnv(t, row("01A", state, false, tS, "", 1))
		o, ok, err := env.m.LiveWorker(context.Background(), tS)
		require.NoError(t, err, state)
		require.True(t, ok, state)
		assert.Equal(t, want, o.Light, "row state %q", state)
	}
	// digests: removed / unparsed are not trusted; unknown states and clear are unknown, never idle or waiting
	env := newOwnerEnv(t, row("01A", "running", false, tS, "", 1))
	p := &projector{pushed: map[string]pushedRow{}}
	env.m.proj = p
	check := func(r pushedRow, want string) {
		p.pushed["01A"] = r
		o, _, _ := env.m.LiveWorker(context.Background(), tS)
		assert.Equal(t, want, o.Light)
	}
	check(pushedRow{removed: true}, "running")
	check(pushedRow{unparsed: true}, "running")
	check(pushedRow{digest: rowDigest{State: "paused"}}, "unknown")
	check(pushedRow{digest: rowDigest{State: "paused", PermissionRequest: "p"}}, "unknown")
	check(pushedRow{digest: rowDigest{State: "idle", Archived: true}}, "unknown")
	check(pushedRow{digest: rowDigest{State: "failed"}}, "error")
}

func TestLiveWorker_ConcurrentMissesShareOneScan(t *testing.T) {
	env := newOwnerEnv(t, row("01A", "idle", false, tS, "", 1))
	env.store.listGate = make(chan struct{})
	entered := make(chan struct{})
	env.store.listEntered = entered
	var wg sync.WaitGroup
	res := make([]bool, 8)
	for i := range res {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, _ := env.m.LiveWorker(context.Background(), tS)
			res[i] = ok
		}()
	}
	<-entered
	time.Sleep(50 * time.Millisecond) // let the others join the flight
	close(env.store.listGate)
	wg.Wait()
	for i, ok := range res {
		assert.True(t, ok, i)
	}
	assert.Equal(t, 1, env.store.listCalls)
}

func TestLiveWorker_CallerContextBoundsTheWait(t *testing.T) {
	env := newOwnerEnv(t, row("01A", "idle", false, tS, "", 1))
	env.store.listGate = make(chan struct{})
	t.Cleanup(func() { close(env.store.listGate) })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, ok, err := env.m.LiveWorker(ctx, tS)
	assert.Error(t, err)
	assert.False(t, ok)
	assert.Less(t, time.Since(start), time.Second)
}

func TestExecOwnerCache_SweepsAtMostOncePerInterval(t *testing.T) {
	now := time.Unix(1000, 0)
	c := &execOwnerCache{now: func() time.Time { return now }}
	c.put("a", ownerCacheEntry{at: now})
	now = now.Add(ownerCacheTTL + time.Second)
	c.put("b", ownerCacheEntry{at: now})
	assert.Len(t, c.m, 2, "an expired entry stays until the next sweep")
	now = now.Add(ownerSweepEvery)
	c.put("c", ownerCacheEntry{at: now})
	assert.Len(t, c.m, 1, "the sweep removed the expired ones")
}

func TestExecOwnerCache_NegativeEntryCountsFromScanEnd(t *testing.T) {
	now := time.Unix(1000, 0)
	c := &execOwnerCache{now: func() time.Time { return now }}
	scans := 0
	slowFail := func(context.Context) ownerCacheEntry {
		scans++
		now = now.Add(errCacheTTL + 500*time.Millisecond) // the scan outlasts the negative TTL
		return ownerCacheEntry{err: errors.New("stuck store")}
	}
	_, err := c.do(context.Background(), "k", slowFail)
	assert.NoError(t, err)
	_, hit := c.get("k")
	assert.True(t, hit, "a negative entry from a slow scan is still cached right after it ends")
	now = now.Add(errCacheTTL - time.Millisecond)
	_, hit = c.get("k")
	assert.True(t, hit, "still cached until errCacheTTL after the end")
	now = now.Add(2 * time.Millisecond)
	_, hit = c.get("k")
	assert.False(t, hit, "expires errCacheTTL after the scan end")
	assert.Equal(t, 1, scans)
}
