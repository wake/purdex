package resourcesmod

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// A boot finds a waiting row that is still parked (an older daemon parked it):
// nobody polls such a row any more, so it ends abandoned; an unparked waiting
// row only gets the boot grace.
func TestBoot_ParkedRowsEndAbandoned(t *testing.T) {
	clock := newFakeClock()
	m := bootModule(t, idleSampler(), clock, func(s *leaseStore, now int64) {
		p := baseRow("parked", "c-parked")
		p.CreatedAt, p.DeadlineAt, p.LeaseUntil = now-60000, now-30000, now+600000
		mustCreate(t, s, p)
		_, err := s.db.Exec(`UPDATE resource_leases SET parked_until = ?, parks = 1 WHERE id = 'parked'`, now+600000)
		require.NoError(t, err)
		w := baseRow("plain", "c-plain")
		w.CreatedAt, w.DeadlineAt, w.LeaseUntil = now-60000, now+60000, now-1000
		mustCreate(t, s, w)
	})
	require.NoError(t, m.Start(context.Background()))
	parked, _ := mustGet(t, m, "parked")
	assert.Equal(t, resources.StateEnded, parked.State)
	assert.Equal(t, resources.EndAbandoned, parked.EndReason)
	plain, _ := mustGet(t, m, "plain")
	assert.Equal(t, resources.StateWaiting, plain.State)
}

// Nothing is ever parked: in lease mode a wait-mode row past its deadline with
// parkable=1 (a client of the P2a/P2b era) stays waiting and unparked.
// (M: put the parking loop back; with parkable=1 it parks the row.)
func TestPass_NothingIsEverParked(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.post(cidA, "test-full", 0)) // the fixture's post sends parkable
	require.Equal(t, resources.StateWaiting, a.State)
	require.True(t, f.row(a.ID).Parkable)
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET deadline_at = ? WHERE id = ?`, f.nowMS()-1, a.ID)
	require.NoError(t, err)
	f.advance(time.Minute)
	require.NoError(t, f.m.store.RenewLease(a.ID, f.nowMS()+30_000))
	f.m.admissionPass(context.Background(), "")
	r := f.row(a.ID)
	assert.Equal(t, resources.StateWaiting, r.State)
	assert.Zero(t, r.ParkedUntil)
	assert.Zero(t, r.Parks)
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+a.ID+"?wait=0", nil))
	assert.Equal(t, resources.StateWaiting, got.State, "a plain waiting answer, never queued")
	assert.Zero(t, got.ParkedUntil)
}

// A second call of a session does not take over an earlier row any more: each
// call is its own request.
func TestPost_NoReattach(t *testing.T) {
	f := newAttrFix(t)
	f.setZone("danger", "flat")
	req := resources.LeaseRequest{ClientID: cidA, Kind: "test-full", HolderPID: 1000, HolderStart: attrStartText, Parkable: true}
	a := decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", req))
	require.Equal(t, resources.StateWaiting, a.State)
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET parked_until = ?, parks = 1 WHERE id = ?`, f.nowMS()+600_000, a.ID)
	require.NoError(t, err)
	req.ClientID = cidB
	b := decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", req))
	assert.NotEqual(t, a.ID, b.ID, "a new row, not the parked one")
	assert.Equal(t, resources.StateWaiting, f.row(a.ID).State)
}

// The agent's pid and start time come with the session (the position keeps them
// to know when its session is gone).
func TestSessionOfProcess_ReturnsTheAgentPidAndStart(t *testing.T) {
	f := newAttrFix(t)
	sid, pid, start := f.m.sessionOfProcess(context.Background(), 1000)
	assert.Equal(t, "sid-agent", sid)
	assert.Equal(t, 800, pid)
	assert.Equal(t, attrStartText, start)
	sid, pid, start = f.m.sessionOfProcess(context.Background(), 2000)
	assert.Equal(t, "", sid)
	assert.Zero(t, pid)
	assert.Equal(t, "", start)
}

// A row that still carries parked_until (written by an older daemon between a
// boot and the next one) is answered as waiting: "queued" now means a queue
// position, which a wait-mode row never has.
func TestResponse_ALegacyParkedRowIsStillJustWaiting(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.post(cidA, "test-full", 0))
	require.Equal(t, resources.StateWaiting, a.State)
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET parked_until = ? WHERE id = ?`, f.nowMS()+600_000, a.ID)
	require.NoError(t, err)
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+a.ID+"?wait=0", nil))
	assert.Equal(t, resources.StateWaiting, got.State)
	assert.Zero(t, got.ParkedUntil)
}
