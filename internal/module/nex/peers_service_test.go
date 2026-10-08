package nex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/peers/execpeers"
)

const (
	pxSidA = "aaaaaaaa-0000-4000-8000-000000000001"
	pxSidB = "bbbbbbbb-0000-4000-8000-000000000002"
	pxSidC = "cccccccc-0000-4000-8000-000000000003"
)

// pxModule is a bare Module over fs: all Rows reads is the store and the
// per-page budget.
func pxModule(fs nexStore) *Module {
	m := &Module{logf: discardLogf, engineOpTimeout: time.Second}
	m.sys.store = fs
	return m
}

func pxRows(t *testing.T, fs nexStore) ([]execpeers.Row, error) {
	t.Helper()
	return (&execPeers{m: pxModule(fs)}).Rows(context.Background())
}

// An idle and a running execution each give exactly one row; a queued one
// that has not reported a session yet is addressed by its resume session id.
// An idle row's pid is the LAST turn's, already gone, so it is not carried.
func TestExecPeersRows_IdleRunningQueued(t *testing.T) {
	fs := &fakeNexStore{listRows: []store.Execution{
		{ID: "E1", State: store.StateIdle, SessionID: pxSidA, Cwd: "/work/a", TitleText: "alpha", Pid: 111},
		{ID: "E2", State: store.StateRunning, SessionID: pxSidB, Cwd: "/work/b", Pid: 222, LiveTurnStarted: true},
		{ID: "E3", State: store.StateQueued, ResumeSessionID: pxSidC, Cwd: "/work/c"},
	}}
	rows, err := pxRows(t, fs)
	require.NoError(t, err)
	assert.Equal(t, []execpeers.Row{
		{ExecutionID: "E1", SessionID: pxSidA, Cwd: "/work/a", State: "idle", Title: "alpha"},
		{ExecutionID: "E2", SessionID: pxSidB, Cwd: "/work/b", State: "running", PID: 222},
		{ExecutionID: "E3", SessionID: pxSidC, Cwd: "/work/c", State: "queued"},
	}, rows)
}

// A running turn whose process has not reported (no live pid yet) carries
// no pid: the row's pid is a live process or nothing.
func TestExecPeersRows_RunningBeforeInitHasNoPID(t *testing.T) {
	fs := &fakeNexStore{listRows: []store.Execution{
		{ID: "E1", State: store.StateRunning, SessionID: pxSidA, Pid: 333, LiveTurnStarted: false},
	}}
	rows, err := pxRows(t, fs)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Zero(t, rows[0].PID)
}

func TestExecPeersRows_SkipsTerminalArchivedAndSessionless(t *testing.T) {
	fs := &fakeNexStore{listRows: []store.Execution{
		{ID: "E1", State: store.StateTerminated, SessionID: pxSidA},
		{ID: "E2", State: store.StateFailed, SessionID: pxSidA},
		{ID: "E3", State: store.StateRejected, SessionID: pxSidA},
		{ID: "E4", State: store.StateIdle, SessionID: pxSidB, ArchivedAt: 5},
		{ID: "E5", State: store.StateIdle},
		{ID: "E6", State: store.StateIdle, SessionID: "AAAAAAAA-0000-4000-8000-000000000009"},
	}}
	rows, err := pxRows(t, fs)
	require.NoError(t, err)
	// Only E6, its session id lowercased: the peers module and the
	// peer_names store key conversations by the lowercase id.
	assert.Equal(t, []execpeers.Row{{ExecutionID: "E6", SessionID: "aaaaaaaa-0000-4000-8000-000000000009", State: "idle"}}, rows)
	for _, o := range fs.allListOpts {
		assert.False(t, o.IncludeArchived, "archived executions are not asked for")
	}
}

// The list is paged; a row on the second page is listed like any other.
func TestExecPeersRows_WalksEveryPage(t *testing.T) {
	fs := &fakeNexStore{}
	const n = ownerScanPageSize + 1
	for i := 1; i <= n; i++ {
		fs.listRows = append(fs.listRows, store.Execution{
			ID: fmt.Sprintf("E%05d", i), State: store.StateIdle,
			SessionID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i),
		})
	}
	rows, err := pxRows(t, fs)
	require.NoError(t, err)
	require.Equal(t, n, len(rows), "every page's rows") // not Len: it prints all 500
	assert.Equal(t, fmt.Sprintf("E%05d", n), rows[n-1].ExecutionID)
	assert.Len(t, fs.allListOpts, 2)
}

// A cursor the store already gave means the walk cannot reach every
// execution: Rows fails whole rather than answer the rows it saw.
func TestExecPeersRows_RepeatedCursorFails(t *testing.T) {
	rs := &repeatCursorStore{}
	rows, err := pxRows(t, rs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `repeated cursor "C"`)
	assert.Nil(t, rows)
	assert.Equal(t, []string{"", "C"}, rs.cursors, "the walk stops at the repeat")
}

func TestExecPeersRows_MidWalkErrorFails(t *testing.T) {
	fs := &fakeNexStore{listErr: errors.New("disk on fire"), listErrAt: 2}
	for i := 1; i <= ownerScanPageSize+1; i++ {
		fs.listRows = append(fs.listRows, store.Execution{
			ID: fmt.Sprintf("E%05d", i), State: store.StateIdle,
			SessionID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i),
		})
	}
	rows, err := pxRows(t, fs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk on fire")
	assert.Nil(t, rows)
}

// Two addressable executions of one conversation would put one address on
// two rows: Rows fails closed instead of picking one, on one page or across
// pages, comparing session ids case-insensitively. A terminated execution of
// that conversation is no peer row, so it is no duplicate.
func TestExecPeersRows_DuplicateSessionIDFails(t *testing.T) {
	idle := func(id, sid string) store.Execution {
		return store.Execution{ID: id, State: store.StateIdle, SessionID: sid}
	}
	page := func(n int) []store.Execution {
		var rows []store.Execution
		for i := 1; i <= n; i++ {
			rows = append(rows, idle(fmt.Sprintf("E%05d", i), fmt.Sprintf("00000000-0000-4000-8000-%012d", i)))
		}
		return rows
	}
	crossPage := page(ownerScanPageSize + 1)
	crossPage[ownerScanPageSize].SessionID = strings.ToUpper(crossPage[0].SessionID)
	cases := map[string]struct {
		rows    []store.Execution
		wantErr bool
	}{
		"same page":  {[]store.Execution{idle("E1", pxSidA), {ID: "E2", State: store.StateRunning, ResumeSessionID: pxSidA}}, true},
		"cross page": {crossPage, true},
		"terminated": {[]store.Execution{{ID: "E1", State: store.StateTerminated, SessionID: pxSidA}, idle("E2", pxSidA)}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rows, err := pxRows(t, &fakeNexStore{listRows: tc.rows})
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "duplicate session id")
			assert.Nil(t, rows)
		})
	}
}

// blockingListStore parks every List until its ctx ends, counting the Lists
// still running.
type blockingListStore struct{ active atomic.Int32 }

func (s *blockingListStore) Get(context.Context, string) (store.Execution, error) {
	return store.Execution{}, store.ErrNotFound
}

func (s *blockingListStore) List(ctx context.Context, _ store.ListOptions) (store.ListPage, error) {
	s.active.Add(1)
	defer s.active.Add(-1)
	<-ctx.Done()
	return store.ListPage{}, ctx.Err()
}

// A page ends with the caller (the inventory's budget) or engineOpTimeout,
// whichever comes first: Rows is never detached from its caller, so a GET
// that gave up leaves no List running behind it.
func TestExecPeersRows_PageEndsWithCallerOrEngineTimeout(t *testing.T) {
	cases := map[string]struct {
		callerTimeout, engineTimeout time.Duration
	}{
		"caller first": {50 * time.Millisecond, 5 * time.Second},
		"engine first": {5 * time.Second, 50 * time.Millisecond},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			bs := &blockingListStore{}
			m := pxModule(bs)
			m.engineOpTimeout = tc.engineTimeout
			ctx, cancel := context.WithTimeout(context.Background(), tc.callerTimeout)
			defer cancel()
			start := time.Now()
			_, err := (&execPeers{m: m}).Rows(ctx)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Less(t, time.Since(start), 2*time.Second, "the page outlived the earlier deadline")
			assert.Zero(t, bs.active.Load(), "a List is still running")
		})
	}
}

// P4a lists executions only: the mailbox last hop is not wired, and Send
// says so instead of pretending to deliver.
func TestExecPeersSend_NotWiredYet(t *testing.T) {
	_, err := (&execPeers{m: pxModule(&fakeNexStore{})}).Send(context.Background(), "E1", execpeers.PeerSend{Text: "hi"})
	require.ErrorIs(t, err, errPeerSendNotWired)
}

// Init publishes the service once the engine assembled, and MailboxEnabled
// is the assembled [nex.peer].enabled.
func TestInitRegistersExecPeers(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("PATH", launchdPath)
			cfg := baseConfig(t)
			cfg.Nex.Peer.Enabled = enabled
			c := newTestCore(&cfg)
			m := New()
			m.logf = discardLogf
			m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
			require.NoError(t, m.Init(c))

			svc, ok := c.Registry.Get(execpeers.RegistryKey)
			require.True(t, ok, "registered under %q", execpeers.RegistryKey)
			ep, ok := svc.(execpeers.ExecPeers)
			require.True(t, ok, "%T implements execpeers.ExecPeers", svc)
			assert.Equal(t, enabled, ep.MailboxEnabled())
		})
	}
}

// An engine that never assembled lists nothing, so nothing is published:
// the peers module then has no execution rows and is otherwise unchanged,
// instead of reporting a listing failure on every request.
func TestInitSoftFailDoesNotRegisterExecPeers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	c := newTestCore(&cfg)
	m := New()
	m.logf = discardLogf
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, engine{}, errors.New("boom"))
	require.NoError(t, m.Init(c))
	_, ok := c.Registry.Get(execpeers.RegistryKey)
	assert.False(t, ok)
}
