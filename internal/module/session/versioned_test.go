package session

import (
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func (e *toggleFailExecutor) ListSessions() ([]tmux.TmuxSession, error) {
	e.mu.Lock()
	fail := e.fail
	e.mu.Unlock()
	if fail {
		return nil, errors.New("tmux list exploded")
	}
	return e.Executor.ListSessions()
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

func (e *blockingExecutor) ListSessions() ([]tmux.TmuxSession, error) {
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
	return e.Executor.ListSessions()
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
