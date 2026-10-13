package resourcesmod

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/resources"
)

// fakeAncestry is a process table: parent and start time per pid.
type fakeAncestry struct {
	ppid  map[int]int
	start map[int]time.Time
}

func (f *fakeAncestry) PPID(pid int) (int, error) {
	if p, ok := f.ppid[pid]; ok {
		return p, nil
	}
	return 0, errors.New("no such process")
}

func (f *fakeAncestry) Start(pid int) (time.Time, error) {
	if s, ok := f.start[pid]; ok {
		return s, nil
	}
	return time.Time{}, errors.New("no such process")
}

var attrStart = time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

const attrStartText = "Thu Oct  9 00:00:00 2026" // attrStart in the registry's form

// attrFix is a route fixture whose registry knows one session, agent pid 800.
type attrFix struct {
	*routeFix
	table *fakeAncestry
}

func newAttrFix(t *testing.T) *attrFix {
	f := &attrFix{routeFix: newRouteFix(t, resources.ModeLease), table: &fakeAncestry{ppid: map[int]int{}, start: map[int]time.Time{}}}
	f.m.roots = &fakeRoots{roots: []resources.Root{{SessionID: "sid-agent", PID: 800, ProcStart: attrStartText}}}
	f.m.ancestryOf = func(*iagent.ProcessSnapshot) ancestry { return f.table }
	// pdx (1000) -> zsh (900) -> claude (800) -> launchd (1)
	for pid, parent := range map[int]int{1000: 900, 900: 800, 800: 1} {
		f.table.ppid[pid] = parent
		f.table.start[pid] = attrStart
	}
	return f
}

// The chain pdx -> zsh -> claude resolves to the agent's session.
func TestSessionOfProcess_ChainReachesTheAgent(t *testing.T) {
	f := newAttrFix(t)
	assert.Equal(t, "sid-agent", f.m.sessionOfProcess(context.Background(), 1000))
	assert.Equal(t, "sid-agent", f.m.sessionOfProcess(context.Background(), 800), "the agent itself")
}

// No agent within the hops, an unknown pid, or no registry: no session.
func TestSessionOfProcess_NoAgentNoSession(t *testing.T) {
	f := newAttrFix(t)
	f.table.ppid[2000], f.table.start[2000] = 1, attrStart // a launchd child
	assert.Equal(t, "", f.m.sessionOfProcess(context.Background(), 2000))
	assert.Equal(t, "", f.m.sessionOfProcess(context.Background(), 3000), "not in the table")
	assert.Equal(t, "", f.m.sessionOfProcess(context.Background(), 0))
	f.m.roots = nil
	assert.Equal(t, "", f.m.sessionOfProcess(context.Background(), 1000))
}

// A pid whose start time differs from the registry's is another process that
// reused the number: not matched. (M: skip the start check and it matches.)
func TestSessionOfProcess_ReusedPidIsNotTheAgent(t *testing.T) {
	f := newAttrFix(t)
	f.table.start[800] = attrStart.Add(time.Hour)
	assert.Equal(t, "", f.m.sessionOfProcess(context.Background(), 1000))
	f.table.start[800] = attrStart.Add(400 * time.Millisecond) // the registry's text has second precision
	assert.Equal(t, "sid-agent", f.m.sessionOfProcess(context.Background(), 1000))
	delete(f.table.start, 800)
	assert.Equal(t, "", f.m.sessionOfProcess(context.Background(), 1000), "a start time that cannot be read vouches for nothing")
}

// The walk gives up after 16 hops, and a parent loop in a corrupt table ends it.
func TestSessionOfProcess_HopLimitAndLoops(t *testing.T) {
	f := newAttrFix(t)
	prev := 800
	for i := 1; i <= 17; i++ { // a chain of 17 hops: 5000 -> ... -> 800
		pid := 5000 + i
		f.table.ppid[pid], f.table.start[pid] = prev, attrStart
		prev = pid
	}
	assert.Equal(t, "", f.m.sessionOfProcess(context.Background(), 5017), "17 hops is out of reach")
	assert.Equal(t, "sid-agent", f.m.sessionOfProcess(context.Background(), 5016), "16 hops is not")
	f.table.ppid[6001], f.table.ppid[6002] = 6002, 6001
	f.table.start[6001], f.table.start[6002] = attrStart, attrStart
	assert.Equal(t, "", f.m.sessionOfProcess(context.Background(), 6001))
}

// A process-scope POST with no session_id gets the agent's session on its row;
// one that names a session keeps it; a replay of the same client id is the same
// row even if the lookup would fail the second time.
func TestLeaseCreate_ProcessScopeResolvesTheSession(t *testing.T) {
	f := newAttrFix(t)
	post := func(cid, session string) resources.LeaseResponse {
		rec := f.do(http.MethodPost, "/api/resources/leases", resources.LeaseRequest{
			ClientID: cid, Kind: "test-full", HolderPID: 1000, HolderStart: attrStartText, SessionID: session})
		return decodeLease(t, rec)
	}
	a := post(cidA, "")
	require.NotEmpty(t, a.ID)
	assert.Equal(t, "sid-agent", f.row(a.ID).SessionID)

	b := post(cidB, "sid-named")
	assert.Equal(t, "sid-named", f.row(b.ID).SessionID, "an explicit session is never overridden")

	f.m.roots = nil // the lookup now finds nothing
	again := f.do(http.MethodPost, "/api/resources/leases", resources.LeaseRequest{
		ClientID: cidA, Kind: "test-full", HolderPID: 1000, HolderStart: attrStartText})
	assert.Equal(t, http.StatusOK, again.Code, again.Body.String())
	assert.Equal(t, a.ID, decodeLease(t, again).ID)
}
