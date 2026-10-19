package fleetmod

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/fleet"
)

// The role answer says whether this daemon's push module is ready (push_ready), so a standby can tell whether the primary can send a
// phone push at all (wwd alerts, WWD-8a-1c). The field is left out when nothing supplies it, as an older daemon's answer is.
func TestPushReady_AnswerCarriesItOnlyWhenSupplied(t *testing.T) {
	p := newPair(t)
	a := p.a.HandleRole(RoleRequest{FromAlias: "mlab", Role: fleet.RoleStandby})
	require.Nil(t, a.PushReady, "no source: the field is absent")
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "push_ready")

	ready := true
	p.a.SetPushReady(func() bool { return ready })
	a = p.a.HandleRole(RoleRequest{FromAlias: "mlab", Role: fleet.RoleStandby})
	require.NotNil(t, a.PushReady)
	require.True(t, *a.PushReady)
	ready = false
	a = p.a.HandleRole(RoleRequest{FromAlias: "mlab", Role: fleet.RoleStandby})
	require.NotNil(t, a.PushReady)
	require.False(t, *a.PushReady, "false is reported, not left out")
	raw, _ = json.Marshal(a)
	require.Contains(t, string(raw), `"push_ready":false`)
}

// A standby remembers what the primary last said about its push, and whether it answered at all.
func TestPeerPush_StandbyRecordsWhatThePrimaryReported(t *testing.T) {
	p := newPair(t)
	p.b.Startup(ctx())
	p.a.Startup(ctx()) // a (air26) is first in order: primary; b (mlab): standby
	require.Equal(t, fleet.RolePrimary, p.a.State().Role)
	require.Equal(t, fleet.RoleStandby, p.b.State().Role)

	require.Equal(t, fleet.PeerPush{Known: true, Answered: true}, p.b.PeerPush(), "startup already asked: an answer without the field")

	ready := true
	p.a.SetPushReady(func() bool { return ready })
	p.b.Beat(ctx())
	got := p.b.PeerPush()
	require.True(t, got.Known && got.Answered)
	require.NotNil(t, got.Ready)
	require.True(t, *got.Ready)

	ready = false
	p.b.Beat(ctx())
	got = p.b.PeerPush()
	require.NotNil(t, got.Ready)
	require.False(t, *got.Ready)

	p.a.SetPushReady(nil) // an older primary: no field
	p.b.Beat(ctx())
	got = p.b.PeerPush()
	require.True(t, got.Known && got.Answered)
	require.Nil(t, got.Ready, "no field: not reported")

	p.ba.setDown(true) // the primary is not reachable
	p.b.Beat(ctx())
	got = p.b.PeerPush()
	require.True(t, got.Known)
	require.False(t, got.Answered)
	require.Nil(t, got.Ready)
}

// What was recorded about the other daemon belongs to the role it was seen from. When this daemon's role changes (a takeover, a step down
// to a primary that calls in) the record is unknown until the next call: unknown makes a standby deliver, so a stale "the primary can
// push" can never leave an alert with nobody to tell it. A call that only repeats the same role does not clear it.
func TestPeerPush_ARoleChangeMakesTheRecordUnknown(t *testing.T) {
	p := newPair(t)
	p.b.Startup(ctx())
	p.a.Startup(ctx())
	yes := true
	p.a.SetPushReady(func() bool { return yes })
	p.b.Beat(ctx())
	require.NotNil(t, p.b.PeerPush().Ready)

	// the primary calling in again, same role: the record stays
	p.b.HandleRole(RoleRequest{FromAlias: "air26", Role: fleet.RolePrimary, Term: p.a.State().Term, Order: []string{"air26", "mlab"}})
	require.Equal(t, fleet.RoleStandby, p.b.State().Role)
	require.NotNil(t, p.b.PeerPush().Ready, "the same role does not clear what was seen")

	// b takes over (air26 not reachable)...
	p.ba.setDown(true)
	p.b.Beat(ctx())
	require.Equal(t, fleet.RolePrimary, p.b.State().Role)
	p.ba.setDown(false)
	// ...and a primary with a higher term calls in: b steps down, and what it saw of air26 under the old role says nothing now
	p.b.HandleRole(RoleRequest{FromAlias: "air26", Role: fleet.RolePrimary, Term: 99, Order: []string{"air26", "mlab"}})
	require.Equal(t, fleet.RoleStandby, p.b.State().Role)
	require.Equal(t, fleet.PeerPush{}, p.b.PeerPush(), "unknown after the role changed")

	p.b.Beat(ctx()) // the next call fills it again
	require.True(t, p.b.PeerPush().Known)
}

// Before any call to the other daemon has completed, nothing is known.
func TestPeerPush_UnknownBeforeTheFirstCall(t *testing.T) {
	p := newPair(t)
	require.Equal(t, fleet.PeerPush{}, p.b.PeerPush())
}
