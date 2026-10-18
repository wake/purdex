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

// Before any call to the other daemon has completed, nothing is known.
func TestPeerPush_UnknownBeforeTheFirstCall(t *testing.T) {
	p := newPair(t)
	require.Equal(t, fleet.PeerPush{}, p.b.PeerPush())
}
