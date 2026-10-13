package resourcesmod

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// A poll that names its client renews the row when the client is the row's.
func TestPollClient_MatchingClientRenews(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.post(cidA, "test-full", 0))
	require.Equal(t, resources.StateWaiting, a.State)
	before := f.row(a.ID).LeaseUntil
	f.clock.ms.Add(20_000)
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+a.ID+"?client_id="+cidA, nil))
	assert.Equal(t, resources.StateWaiting, got.State)
	assert.Equal(t, before+20_000, f.row(a.ID).LeaseUntil)
}

// A poll from another client (the row was re-attached by a newer call) is told
// it was replaced and renews nothing. (M: skip the check and the old client
// keeps the row alive.)
func TestPollClient_ADifferentClientIsReplaced(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.post(cidA, "test-full", 0))
	before := f.row(a.ID).LeaseUntil
	f.clock.ms.Add(20_000)
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+a.ID+"?client_id="+cidB, nil))
	assert.Equal(t, resources.StateReplaced, got.State)
	assert.False(t, got.Granted)
	assert.Equal(t, before, f.row(a.ID).LeaseUntil, "nothing renewed")
}

// Without the parameter (an older client) the poll behaves as before.
func TestPollClient_WithoutTheParameterAsBefore(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.post(cidA, "test-full", 0))
	before := f.row(a.ID).LeaseUntil
	f.clock.ms.Add(20_000)
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+a.ID, nil))
	assert.Equal(t, resources.StateWaiting, got.State)
	assert.Equal(t, before+20_000, f.row(a.ID).LeaseUntil)
}

// A malformed client id is a bad request, not a silent mismatch.
func TestPollClient_MalformedIDIsRefused(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	a := decodeLease(t, f.post(cidA, "test-full", 0))
	rec := f.do(http.MethodGet, "/api/resources/leases/"+a.ID+"?client_id=nope", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// The mismatch also holds for a held or ended row: a replaced client is told so
// whatever the row has become, and a granted lease of another client is not its
// own to learn about.
func TestPollClient_ReplacedWinsOverAnyState(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	a := decodeLease(t, f.post(cidA, "test-full", 0)) // granted at once
	require.Equal(t, resources.StateHeld, a.State)
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+a.ID+"?client_id="+cidB, nil))
	assert.Equal(t, resources.StateReplaced, got.State)
}
