package resourcesmod

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/resources"
)

// nestedFix adds, under the outer pdx (1000 -> zsh 900 -> agent 800), a script
// (1050) that runs the inner pdx (1100), and a sibling pdx (1200) that is not
// below the outer.
func newNestedFix(t *testing.T) *posFix {
	f := newPosFix(t)
	for pid, parent := range map[int]int{1050: pidA, 1100: 1050, 1200: 900} {
		f.table.ppid[pid] = parent
		f.table.start[pid] = attrStart
	}
	for _, pid := range []int{1050, 1100, 1200} {
		f.view.procs[pid] = attrStart
	}
	return f
}

func (f *posFix) postNested(pid int, mode string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/api/resources/leases", resources.LeaseRequest{
		ClientID: newCID(), Kind: "build", HolderPID: pid, HolderStart: attrStartText, Mode: mode, Command: "pnpm run typecheck", Cwd: "/work"})
}

func (f *posFix) leaseCount() int {
	f.t.Helper()
	var n int
	require.NoError(f.t, f.m.store.db.QueryRow(`SELECT COUNT(*) FROM resource_leases`).Scan(&n))
	return n
}

func (f *posFix) nestedRuns(id string) int {
	f.t.Helper()
	var n int
	require.NoError(f.t, f.m.store.db.QueryRow(`SELECT nested_runs FROM resource_leases WHERE id = ?`, id).Scan(&n))
	return n
}

func TestNested_RunUnderRunIsHeldWithoutARow(t *testing.T) {
	f := newNestedFix(t)
	outer := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "pnpm check --fast"))
	require.Equal(t, resources.StateHeld, outer.State)
	rows, positions := f.leaseCount(), len(f.livePositions())
	f.setZone("danger", "flat") // a busy host does not matter: it is the same job

	rec := f.postNested(1100, resources.CallQueue)
	require.Less(t, rec.Code, 300, rec.Body.String())
	got := decodeLease(t, rec)
	assert.Equal(t, resources.StateHeld, got.State)
	assert.True(t, got.Granted)
	assert.Empty(t, got.ID)
	assert.Equal(t, outer.ID, got.Nested)
	assert.Equal(t, rows, f.leaseCount(), "no row for a nested answer")
	assert.Equal(t, positions, len(f.livePositions()))
	assert.Equal(t, 1, f.nestedRuns(outer.ID))

	f.postNested(1100, resources.CallQueue)
	assert.Equal(t, 2, f.nestedRuns(outer.ID))
}

func TestNested_SiblingIsStillQueued(t *testing.T) {
	f := newNestedFix(t)
	outer := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "pnpm check --fast"))
	require.Equal(t, resources.StateHeld, outer.State)
	f.setZone("danger", "flat")

	got := decodeLease(t, f.postNested(1200, resources.CallQueue))
	assert.Equal(t, resources.StateQueued, got.State)
	assert.Equal(t, resources.QueuedOwnRunning, got.QueuedReason)
	assert.Empty(t, got.Nested)
	assert.NotEmpty(t, got.ID)
}

// A held row whose holder is the agent (the mod's foreground lease) never makes
// the session's other commands nested (N3). (M: do not stop before the agent.)
func TestNested_AgentIsNotAnOuter(t *testing.T) {
	f := newNestedFix(t)
	r := baseRow("agent-row", "c-agent-row")
	r.SessionID, r.HolderPID, r.HolderStart = "sid-agent", 800, attrStartText
	r.CreatedAt = f.nowMS()
	mustCreate(t, f.m.store, r)
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET state = 'held', granted_at = ? WHERE id = 'agent-row'`, f.nowMS())
	require.NoError(t, err)

	got := decodeLease(t, f.postNested(1200, resources.CallQueue))
	assert.Empty(t, got.Nested)
}

// An ancestor pid that is held by a row but started at another time is a reused
// pid. (M: drop the start check.)
func TestNested_ReusedPidIsNotAnOuter(t *testing.T) {
	f := newNestedFix(t)
	outer := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "pnpm check --fast"))
	require.Equal(t, resources.StateHeld, outer.State)
	f.table.start[pidA] = attrStart.Add(time.Hour)

	got := decodeLease(t, f.postNested(1100, resources.CallQueue))
	assert.Empty(t, got.Nested)
}

// A row of another session never is the outer.
func TestNested_OtherSessionIsNotAnOuter(t *testing.T) {
	f := newNestedFix(t)
	outer := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "pnpm check --fast"))
	require.Equal(t, resources.StateHeld, outer.State)
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET session_id = 'sid-other' WHERE id = ?`, outer.ID)
	require.NoError(t, err)

	got := decodeLease(t, f.postNested(1100, resources.CallQueue))
	assert.Empty(t, got.Nested)
}

// A replay of a request that already made its row wins over the nested look.
func TestNested_ReplayWins(t *testing.T) {
	f := newNestedFix(t)
	cid := newCID()
	own := decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", resources.LeaseRequest{
		ClientID: cid, Kind: "build", HolderPID: 1100, HolderStart: attrStartText, Mode: resources.CallQueue, Command: "x", Cwd: "/work"}))
	require.NotEmpty(t, own.ID)
	outer := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "pnpm check --fast"))
	_ = outer
	again := decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", resources.LeaseRequest{
		ClientID: cid, Kind: "build", HolderPID: 1100, HolderStart: attrStartText, Mode: resources.CallQueue, Command: "x", Cwd: "/work"}))
	assert.Equal(t, own.ID, again.ID)
	assert.Empty(t, again.Nested)
}

func TestNested_ProbeFailureIsNotReady(t *testing.T) {
	f := newNestedFix(t)
	outer := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "pnpm check --fast"))
	require.Equal(t, resources.StateHeld, outer.State)
	orig := f.m.procSnapshot
	calls := 0
	f.m.procSnapshot = func(ctx context.Context) (*iagent.ProcessSnapshot, error) {
		calls++
		if calls >= 2 { // the session walk reads once; the nested walk is the second
			return nil, errors.New("ps timed out")
		}
		return orig(ctx)
	}
	rec := f.postNested(1100, resources.CallQueue)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.Equal(t, resources.ErrNotReady, apiCode(rec))
}
