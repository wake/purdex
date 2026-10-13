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

// A client that does not say it reads "queued" (anything older than lease v2
// P2b) is never parked: its row keeps waiting past the deadline as it did, so it
// never meets an answer it cannot read (codex R1 on P2a). (M: park it anyway and
// the row has parked_until.)
func TestParkable_AnOlderClientIsNeverParked(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	req := resources.LeaseRequest{ClientID: cidA, Kind: "test-full", HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026"} // no Parkable
	a := decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", req))
	require.Equal(t, resources.StateWaiting, a.State)
	f.m.store.db.Exec(`UPDATE resource_leases SET deadline_at = ? WHERE id = ?`, f.nowMS()-1, a.ID)
	f.advance(time.Minute)
	f.m.store.RenewLease(a.ID, f.nowMS()+30_000)
	f.m.admissionPass(context.Background(), "")
	r := f.row(a.ID)
	assert.False(t, r.Parkable)
	assert.Zero(t, r.ParkedUntil)
	assert.Zero(t, r.Parks)
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+a.ID+"?wait=0", nil))
	assert.Equal(t, resources.StateWaiting, got.State, "a plain waiting answer, never queued")
}

// The flag is stored from the POST, and a parkable row past its deadline parks.
func TestParkable_TheFlagComesFromThePost(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.post(cidA, "test-full", 0)) // the fixture's post sends parkable
	assert.True(t, f.row(a.ID).Parkable)
	f.m.store.db.Exec(`UPDATE resource_leases SET deadline_at = ? WHERE id = ?`, f.nowMS()-1, a.ID)
	f.m.admissionPass(context.Background(), "")
	assert.NotZero(t, f.row(a.ID).ParkedUntil)
}
