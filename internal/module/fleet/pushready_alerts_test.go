package fleetmod

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/push"
)

// stubReadable is the alert deliverer as the registry holds it; readable is what AlertsReadable answers.
type stubReadable struct{ readable bool }

func (s *stubReadable) AlertsReadable() bool { return s.readable }

// A daemon whose alert deliverer cannot read its own wwd reads no alert, so it must not say it can push: the standby then takes over
// (spec §13.3: a push the primary cannot deliver is delivered by the standby). Once the deliverer reads again, the next answer says so and
// the standby hands back. A host without the deliverer in the registry is as it was.
func TestRoleRoute_PushReadyNeedsTheDelivererToReadItsWWD(t *testing.T) {
	r := newRig(t)
	body := `{"to_host_id":"` + ourID + `","from_alias":"mlab","role":"standby","term":0}`
	ready := func() bool {
		rr := r.post(pathRole, hostP, body)
		var a RoleAnswer
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &a))
		require.NotNil(t, a.PushReady)
		return *a.PushReady
	}
	r.m.core.Registry.Register(push.AlertNotifyKey, &stubNotifier{ready: true})
	require.True(t, ready(), "no deliverer registered: the push module alone decides, as before")

	d := &stubReadable{readable: true}
	r.m.core.Registry.Register(push.AlertsReadableKey, d)
	require.True(t, ready(), "the deliverer reads its wwd")

	d.readable = false
	require.False(t, ready(), "the deliverer cannot read its wwd: nothing can be delivered from here")

	d.readable = true
	require.True(t, ready(), "it reads again: the standby hands back")

	n := &stubNotifier{ready: false}
	r.m.core.Registry.Register(push.AlertNotifyKey, n)
	require.False(t, ready(), "the push module not ready is still not ready, whatever the deliverer reads")
}
