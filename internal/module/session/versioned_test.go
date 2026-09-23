package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/tmux"
)

var epochRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// toggleFailExecutor fails ListSessions while fail is set, then delegates.
type toggleFailExecutor struct {
	tmux.Executor
	mu   sync.Mutex
	fail bool
}

func (e *toggleFailExecutor) setFail(v bool) {
	e.mu.Lock()
	e.fail = v
	e.mu.Unlock()
}

func (e *toggleFailExecutor) ListSessions(ctx context.Context) ([]tmux.TmuxSession, error) {
	e.mu.Lock()
	fail := e.fail
	e.mu.Unlock()
	if fail {
		return nil, errors.New("tmux list exploded")
	}
	return e.Executor.ListSessions(ctx)
}

// blockingExecutor numbers every ListSessions call (the read ordinal) and
// parks the first one until release is closed, signalling entered[k] as each
// call k (1-based) starts. The seam for "is a second versioned read allowed
// to start while the first is still reading?".
type blockingExecutor struct {
	tmux.Executor
	mu      sync.Mutex
	n       int
	entered [2]chan struct{}
	release chan struct{}
}

func newBlockingExecutor(inner tmux.Executor) *blockingExecutor {
	return &blockingExecutor{
		Executor: inner,
		entered:  [2]chan struct{}{make(chan struct{}), make(chan struct{})},
		release:  make(chan struct{}),
	}
}

func (e *blockingExecutor) ListSessions(ctx context.Context) ([]tmux.TmuxSession, error) {
	e.mu.Lock()
	e.n++
	k := e.n
	e.mu.Unlock()
	if k <= len(e.entered) {
		close(e.entered[k-1])
	}
	if k == 1 {
		<-e.release
	}
	return e.Executor.ListSessions(ctx)
}

func TestVersionedList_SeqIncreasesEpochStable(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("a", "/tmp")

	v1, err := mod.versionedList()
	require.NoError(t, err)
	v2, err := mod.versionedList()
	require.NoError(t, err)

	assert.Equal(t, uint64(1), v1.Seq)
	assert.Equal(t, uint64(2), v2.Seq)
	assert.Equal(t, v1.Epoch, v2.Epoch)
	assert.Regexp(t, epochRe, v1.Epoch)
	require.Len(t, v1.Sessions, 1)
	assert.Equal(t, "a", v1.Sessions[0].Name)
}

func TestVersionedList_EpochDiffersBetweenModules(t *testing.T) {
	a, _, _ := newTestModule(t)
	b, _, _ := newTestModule(t)
	va, err := a.versionedList()
	require.NoError(t, err)
	vb, err := b.versionedList()
	require.NoError(t, err)
	assert.NotEqual(t, va.Epoch, vb.Epoch)
}

func TestVersionedList_ErrorDoesNotConsumeSeq(t *testing.T) {
	mod, _, fake := newTestModule(t)
	failing := &toggleFailExecutor{Executor: fake, fail: true}
	mod.tmux = failing

	_, err := mod.versionedList()
	require.Error(t, err)

	failing.setFail(false)
	v, err := mod.versionedList()
	require.NoError(t, err)
	assert.Equal(t, uint64(1), v.Seq, "a failed read must not take a seq")
}

func TestVersionedList_EmptyIsNonNil(t *testing.T) {
	mod, _, _ := newTestModule(t)
	v, err := mod.versionedList()
	require.NoError(t, err)
	require.NotNil(t, v.Sessions)
	data, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"sessions":[]`)
}

// Versioned reads are serialized (spec §3.3 rule 1): while one read is inside
// tmux, a second caller must not start its read. Removing snapMu turns this red.
func TestVersionedList_ReadsAreSerialized(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("a", "/tmp")
	blk := newBlockingExecutor(fake)
	mod.tmux = blk

	var va, vb VersionedSessions
	var errA, errB error
	doneA := make(chan struct{})
	doneB := make(chan struct{})

	go func() {
		defer close(doneA)
		va, errA = mod.versionedList()
	}()
	select {
	case <-blk.entered[0]:
	case <-time.After(2 * time.Second):
		t.Fatal("A never entered ListSessions")
	}

	go func() {
		defer close(doneB)
		vb, errB = mod.versionedList()
	}()

	select {
	case <-blk.entered[1]:
		close(blk.release)
		t.Fatal("B entered ListSessions while A's versioned read was still in flight")
	case <-time.After(100 * time.Millisecond):
	}

	close(blk.release)
	<-doneA
	<-doneB
	require.NoError(t, errA)
	require.NoError(t, errB)
	assert.Less(t, va.Seq, vb.Seq, "the read that went first must carry the smaller seq")
}

// ordinalExecutor numbers every ListSessions call (the read ordinal k) and
// answers a single session named r<k>, so any list handed out says which
// read produced it.
type ordinalExecutor struct {
	tmux.Executor
	mu sync.Mutex
	n  int
	// onRead, if set, runs inside read k before it returns.
	onRead func(k int)
}

func (e *ordinalExecutor) ListSessions(ctx context.Context) ([]tmux.TmuxSession, error) {
	e.mu.Lock()
	e.n++
	k := e.n
	hook := e.onRead
	e.mu.Unlock()
	if hook != nil {
		hook(k)
	}
	return []tmux.TmuxSession{{ID: "$0", Name: fmt.Sprintf("r%d", k), Cwd: "/tmp"}}, nil
}

func (e *ordinalExecutor) reads() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.n
}

// versionedPayload is one versioned list as a client received it.
type versionedPayload struct {
	via     string
	epoch   string
	seq     uint64
	ordinal int
}

func ordinalOf(t *testing.T, list []SessionInfo) int {
	t.Helper()
	require.Len(t, list, 1)
	k, err := strconv.Atoi(strings.TrimPrefix(list[0].Name, "r"))
	require.NoError(t, err, "unexpected session name %q", list[0].Name)
	return k
}

// crossChannelHarness drives every versioned channel of one module and
// collects what a client would have received.
type crossChannelHarness struct {
	t   *testing.T
	mod *SessionModule
	mux *http.ServeMux
	sub *core.EventSubscriber
	ex  *ordinalExecutor
}

func newCrossChannelHarness(t *testing.T) *crossChannelHarness {
	mod, _, fake := newTestModule(t)
	ex := &ordinalExecutor{Executor: fake}
	mod.tmux = ex
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	sub := mod.core.Events.AddTestSubscriber()
	t.Cleanup(func() { mod.core.Events.RemoveTestSubscriber(sub) })
	return &crossChannelHarness{t: t, mod: mod, mux: mux, sub: sub, ex: ex}
}

func (h *crossChannelHarness) fresh() versionedPayload {
	v := getFresh(h.t, h.mux)
	return versionedPayload{via: "fresh", epoch: v.Epoch, seq: v.Seq, ordinal: ordinalOf(h.t, v.Sessions)}
}

// plain runs an unversioned GET /api/sessions and returns the read ordinal
// of the list it answered with.
func (h *crossChannelHarness) plain() int {
	w := getList(h.t, h.mux, "/api/sessions")
	require.Equal(h.t, http.StatusOK, w.Code)
	var list []SessionInfo
	require.NoError(h.t, json.Unmarshal(w.Body.Bytes(), &list))
	return ordinalOf(h.t, list)
}

// frame runs one push path and returns the single sessions frame it sent.
func (h *crossChannelHarness) frame(via string, push func()) versionedPayload {
	push()
	frames := drainSessionFrames(h.t, h.sub)
	require.Len(h.t, frames, 1, "%s must push exactly one sessions frame", via)
	var list []SessionInfo
	require.NoError(h.t, json.Unmarshal([]byte(frames[0].Value), &list))
	return versionedPayload{via: via, epoch: frames[0].Epoch, seq: frames[0].Seq, ordinal: ordinalOf(h.t, list)}
}

func (h *crossChannelHarness) broadcast() versionedPayload {
	expireDebounce(h.mod)
	return h.frame("broadcastSessions", h.mod.broadcastSessions)
}

func (h *crossChannelHarness) tick() versionedPayload {
	return h.frame("tickNormal", h.mod.tickNormal)
}

func (h *crossChannelHarness) snapshot() versionedPayload {
	return h.frame("subscribe", func() { h.mod.sendSessionsSnapshot(h.sub) })
}

// Spec §3.3 rules 4–5: every channel draws from one counter, and each seq is
// paired with exactly the read that took it. With only versioned reads in
// play, seq ↔ read ordinal must be a monotone bijection covering every read —
// a list re-used under a new seq duplicates an ordinal, and a read whose list
// is discarded (seq taken by one read, list from another) leaves a gap.
func TestVersioned_SharedCounterAndSeqBelongsToRead(t *testing.T) {
	h := newCrossChannelHarness(t)

	got := []versionedPayload{
		h.fresh(),
		h.broadcast(),
		h.tick(),
		h.snapshot(),
		h.fresh(),
		h.broadcast(),
		h.tick(),
		h.snapshot(),
		h.fresh(),
	}

	epoch := got[0].epoch
	seenSeq := map[uint64]string{}
	seenOrd := map[int]string{}
	for _, p := range got {
		assert.Equal(t, epoch, p.epoch, "%s: one epoch per process", p.via)
		assert.NotZero(t, p.seq, "%s: seq 0 is never sent", p.via)
		if prev, dup := seenSeq[p.seq]; dup {
			t.Errorf("seq %d sent by both %s and %s", p.seq, prev, p.via)
		}
		seenSeq[p.seq] = p.via
		if prev, dup := seenOrd[p.ordinal]; dup {
			t.Errorf("read r%d handed out by both %s and %s", p.ordinal, prev, p.via)
		}
		seenOrd[p.ordinal] = p.via
	}

	sorted := append([]versionedPayload(nil), got...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].seq < sorted[j].seq })
	for i := 1; i < len(sorted); i++ {
		assert.Less(t, sorted[i-1].ordinal, sorted[i].ordinal,
			"seq order must be read order: %s(seq %d, r%d) vs %s(seq %d, r%d)",
			sorted[i-1].via, sorted[i-1].seq, sorted[i-1].ordinal,
			sorted[i].via, sorted[i].seq, sorted[i].ordinal)
	}
	assert.Equal(t, len(got), h.ex.reads(), "every tmux read must reach a client under its own seq")
}

// A warm plain-GET cache never leaks into a versioned payload: right after a
// plain GET answered with read r<k>, each versioned channel must hand out a
// newer read, never r<k> under a new seq.
func TestVersioned_WarmPlainCacheNeverLeaks(t *testing.T) {
	h := newCrossChannelHarness(t)
	h.tick() // prime tickNormal's hash so later ticks see a change

	channels := []func() versionedPayload{h.fresh, h.broadcast, h.tick, h.snapshot}
	var lastSeq uint64
	for _, ch := range channels {
		k := h.plain()
		p := ch()
		assert.Greater(t, p.ordinal, k, "%s re-used the cached plain list r%d", p.via, k)
		assert.Greater(t, p.seq, lastSeq, "%s: seq must keep increasing", p.via)
		lastSeq = p.seq
	}
}

// Spec §3.3 rules 1 and 5 under concurrency: while a push path is inside its
// tmux read, a concurrent ?fresh=1 is given the chance to run to completion.
// If the push took its seq separately from its read (e.g. read through the
// plain cache, then stamped), the fetch would slip in between and the push
// would pair an older list with a newer seq. With the read under snapMu the
// fetch must wait, and so reads after the push.
func TestVersioned_ConcurrentFetchCannotSplitReadFromSeq(t *testing.T) {
	for _, tc := range []struct {
		name string
		push func(h *crossChannelHarness) versionedPayload
	}{
		{"broadcastSessions", (*crossChannelHarness).broadcast},
		{"tickNormal", (*crossChannelHarness).tick},
		{"subscribe", (*crossChannelHarness).snapshot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCrossChannelHarness(t)
			h.tick() // prime tickNormal's hash so the next tick broadcasts

			var body []byte
			fetchDone := make(chan struct{})
			var once sync.Once
			h.ex.mu.Lock()
			h.ex.onRead = func(int) {
				once.Do(func() {
					go func() {
						defer close(fetchDone)
						// No require/t.Fatal off the test goroutine: decode below.
						req := httptest.NewRequest(http.MethodGet, "/api/sessions?fresh=1", nil)
						w := httptest.NewRecorder()
						h.mux.ServeHTTP(w, req)
						body = w.Body.Bytes()
					}()
					// Give the fetch every chance to finish inside this read.
					select {
					case <-fetchDone:
					case <-time.After(200 * time.Millisecond):
					}
				})
			}
			h.ex.mu.Unlock()

			pushed := tc.push(h)
			select {
			case <-fetchDone:
			case <-time.After(2 * time.Second):
				t.Fatal("concurrent fetch never finished")
			}
			var v VersionedSessions
			require.NoError(t, json.Unmarshal(body, &v), string(body))
			fetched := versionedPayload{via: "fresh", epoch: v.Epoch, seq: v.Seq, ordinal: ordinalOf(t, v.Sessions)}
			require.NotZero(t, fetched.seq)
			require.NotEqual(t, pushed.seq, fetched.seq)

			if pushed.seq < fetched.seq {
				assert.Less(t, pushed.ordinal, fetched.ordinal, "smaller seq must be the earlier read")
			} else {
				assert.Greater(t, pushed.ordinal, fetched.ordinal,
					"%s: seq %d carries read r%d, but fetch seq %d carries newer read r%d",
					pushed.via, pushed.seq, pushed.ordinal, fetched.seq, fetched.ordinal)
			}
		})
	}
}

func TestVersionedList_RotatesEpochAtMaxSeq(t *testing.T) {
	mod, _, _ := newTestModule(t)
	v1, err := mod.versionedList()
	require.NoError(t, err)

	mod.snapMu.Lock()
	mod.snapSeq = maxSeq
	mod.snapMu.Unlock()

	v2, err := mod.versionedList()
	require.NoError(t, err)
	assert.NotEqual(t, v1.Epoch, v2.Epoch, "exhausting the counter must draw a new epoch")
	assert.Regexp(t, epochRe, v2.Epoch)
	assert.Equal(t, uint64(1), v2.Seq)

	v3, err := mod.versionedList()
	require.NoError(t, err)
	assert.Equal(t, v2.Epoch, v3.Epoch)
	assert.Equal(t, uint64(2), v3.Seq)
}
