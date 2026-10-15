package core

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fleet.v1 (fleet spec §6): ?fleet=v1, exactly once, opts a connection into fleet.changed.
func TestFeaturesOf_FleetV1(t *testing.T) {
	of := func(q string) []string { return featuresOf(httptest.NewRequest("GET", "/ws/host-events"+q, nil)) }
	assert.Equal(t, []string{FeatureFleetV1}, of("?fleet=v1"))
	assert.Empty(t, of("?fleet=v2"))
	assert.Empty(t, of("?fleet=v1&fleet=v1"), "an ambiguous request opts out")
	assert.Empty(t, of(""))
	assert.Equal(t, []string{FeatureNexV1, FeatureAgentV2, FeatureReadsV1, FeatureFleetV1}, of("?nex=v1&agent=v2&reads=v1&fleet=v1"))
}

// Only subscribers that opted in are sent a fleet frame.
func TestBroadcastNewestTo_OnlyOptedIn(t *testing.T) {
	eb := NewEventsBroadcaster()
	plain := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(plain)
	other := eb.AddTestSubscriberWith(FeatureReadsV1)
	defer eb.RemoveTestSubscriber(other)
	fleet := eb.AddTestSubscriberWith(FeatureFleetV1)
	defer eb.RemoveTestSubscriber(fleet)

	eb.BroadcastNewestTo(FeatureFleetV1, HostEvent{Type: "fleet.changed", Value: `{"term":1}`})

	got, ok := fleet.TakeNewest()
	require.True(t, ok)
	assert.JSONEq(t, `{"type":"fleet.changed","session":"","value":"{\"term\":1}"}`, string(got))
	_, ok = plain.TakeNewest()
	assert.False(t, ok, "a plain subscriber got a fleet frame")
	_, ok = other.TakeNewest()
	assert.False(t, ok, "a subscriber of another feature got a fleet frame")
	assert.Empty(t, plain.SendCh())
	assert.Empty(t, other.SendCh())
}

// A subscriber that does not keep up loses the old pushes, keeps the newest, and is not disconnected.
func TestBroadcastNewestTo_SlowSubscriberKeepsOnlyTheNewest(t *testing.T) {
	eb := NewEventsBroadcaster()
	sub := eb.AddTestSubscriberWith(FeatureFleetV1)
	defer eb.RemoveTestSubscriber(sub)

	for _, v := range []string{"1", "2", "3"} {
		eb.BroadcastNewestTo(FeatureFleetV1, HostEvent{Type: "fleet.changed", Value: v})
	}
	got, ok := sub.TakeNewest()
	require.True(t, ok)
	assert.Contains(t, string(got), `"value":"3"`)
	_, ok = sub.TakeNewest()
	assert.False(t, ok, "taken once")
	select {
	case <-sub.Done():
		t.Fatal("a slow subscriber was disconnected")
	default:
	}
	assert.Empty(t, sub.SendCh(), "the fleet frame does not use the shared queue")
}

// A subscriber that also opted into a strict feature is not ended by fleet pushes, however many it misses.
func TestBroadcastNewestTo_NeverEndsAStrictSubscriber(t *testing.T) {
	eb := NewEventsBroadcaster()
	sub := eb.AddTestSubscriberWith(FeatureNexV1, FeatureFleetV1)
	defer eb.RemoveTestSubscriber(sub)
	for i := 0; i < 5000; i++ {
		eb.BroadcastNewestTo(FeatureFleetV1, HostEvent{Type: "fleet.changed"})
	}
	select {
	case <-sub.Done():
		t.Fatal("ended")
	default:
	}
}

func TestHandleInfo_FleetCapabilityFollowsReadiness(t *testing.T) {
	none := infoOf(t, devicesCore(nil))["capabilities"].([]any)
	assert.NotContains(t, none, "fleet.v1")

	ready := &devicesStub{stubModule{name: "fleet"}, map[string]any{"ready": true, "init_error": ""}}
	assert.Contains(t, infoOf(t, devicesCore(ready))["capabilities"], "fleet.v1")

	broken := &devicesStub{stubModule{name: "fleet"}, map[string]any{"ready": false, "init_error": "x"}}
	assert.NotContains(t, infoOf(t, devicesCore(broken))["capabilities"], "fleet.v1")
}
