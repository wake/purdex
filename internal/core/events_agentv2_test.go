package core

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// U1-2b-3: /ws/host-events?agent=v2 opts into the agent snapshot (FeatureAgentV2).
// It is parsed next to nex=v1 — either, both — and, like nex.v1, makes the
// subscriber strict. Every nex.v1 behaviour stays as it was.

// TestFeaturesOf_AgentAndNex: each parameter is read on its own, by the same
// rule: exactly one value, and exactly the feature's version. A repeated or
// other value opts that feature out (not the other one). The order of the
// result is fixed: nex first.
func TestFeaturesOf_AgentAndNex(t *testing.T) {
	nex, agent := []string{FeatureNexV1}, []string{FeatureAgentV2}
	both := []string{FeatureNexV1, FeatureAgentV2}
	for _, tc := range []struct {
		name, query string
		want        []string
	}{
		{"no query", "", nil},
		{"nex only", "?nex=v1", nex},
		{"agent only", "?agent=v2", agent},
		{"both, nex first", "?nex=v1&agent=v2", both},
		{"both, agent first", "?agent=v2&nex=v1", both},
		{"both with a ticket between", "?nex=v1&ticket=abc&agent=v2", both},
		{"agent v2 twice", "?agent=v2&agent=v2", nil},
		{"agent v2 then v1", "?agent=v2&agent=v1", nil},
		{"agent v1 then v2", "?agent=v1&agent=v2", nil},
		{"agent v2 then empty", "?agent=v2&agent=", nil},
		{"agent=v1 is not a version of agent", "?agent=v1", nil},
		{"agent=V2", "?agent=V2", nil},
		{"agent empty", "?agent=", nil},
		{"agent bare", "?agent", nil},
		{"nex=v2 is not a version of nex", "?nex=v2", nil},
		{"nex=v1 is not a version of agent", "?agent=nex", nil},
		{"a wrong agent leaves nex alone", "?nex=v1&agent=v1", nex},
		{"a repeated agent leaves nex alone", "?nex=v1&agent=v2&agent=v2", nex},
		{"a repeated nex leaves agent alone", "?nex=v1&nex=v1&agent=v2", agent},
		{"a wrong nex leaves agent alone", "?nex=v2&agent=v2", agent},
		{"other param", "?foo=v2", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/ws/host-events"+tc.query, nil)
			assert.Equal(t, tc.want, featuresOf(r))
		})
	}
}

// optInKinds are the feature sets that make a subscriber strict.
var optInKinds = []struct {
	name     string
	features []string
}{
	{"nex.v1", []string{FeatureNexV1}},
	{"agent.v2", []string{FeatureAgentV2}},
	{"both", []string{FeatureNexV1, FeatureAgentV2}},
}

// TestStrict_AgentV2DroppedFrameEndsConnection: a frame that does not fit
// ends an agent.v2 subscriber (its client reconnects and gets a fresh
// snapshot), whichever way the frame is sent, exactly as for nex.v1. A
// subscriber that opted into nothing keeps the best effort it always had.
func TestStrict_AgentV2DroppedFrameEndsConnection(t *testing.T) {
	sends := []struct {
		name string
		send func(eb *EventsBroadcaster, sub *EventSubscriber)
	}{
		{"Broadcast", func(eb *EventsBroadcaster, _ *EventSubscriber) { eb.Broadcast("s", "hook", `{}`) }},
		{"BroadcastEvent", func(eb *EventsBroadcaster, _ *EventSubscriber) {
			eb.BroadcastEvent(HostEvent{Type: "tmux", Value: "x"})
		}},
		{"SendStrict", func(eb *EventsBroadcaster, sub *EventSubscriber) {
			eb.SendStrict(sub, HostEvent{Type: "agent.snapshot"})
		}},
		{"TrySend", func(_ *EventsBroadcaster, sub *EventSubscriber) { sub.TrySend([]byte(`{"type":"hook"}`)) }},
		{"Send", func(_ *EventsBroadcaster, sub *EventSubscriber) { sub.Send([]byte(`{"type":"hook"}`)) }},
	}
	for _, kind := range optInKinds {
		for _, s := range sends {
			t.Run(kind.name+"/"+s.name, func(t *testing.T) {
				eb := NewEventsBroadcaster()
				opted := eb.AddTestSubscriberWith(kind.features...)
				defer eb.RemoveTestSubscriber(opted)
				assert.Equal(t, optedInSendBuffer, cap(opted.send), "an opt-in gets the burst-sized buffer")
				fillBuffer(t, opted)

				s.send(eb, opted)

				assert.True(t, isDone(opted), "a frame the subscriber could not take was dropped silently")
				assert.Len(t, drained(t, opted), cap(opted.send), "the frame that did not fit was queued anyway")
			})
		}
	}
	// A subscriber that opted into nothing: best effort, never ended.
	for _, s := range sends {
		if s.name == "SendStrict" {
			continue // strict for whoever it is sent to: not a legacy path
		}
		t.Run("legacy/"+s.name, func(t *testing.T) {
			eb := NewEventsBroadcaster()
			plain := eb.AddTestSubscriber()
			defer eb.RemoveTestSubscriber(plain)
			assert.Equal(t, defaultSendBuffer, cap(plain.send))
			fillBuffer(t, plain)

			s.send(eb, plain)

			assert.False(t, isDone(plain), "a best-effort subscriber was ended")
			assert.True(t, registered(eb, plain))
			assert.Len(t, plain.send, cap(plain.send))
		})
	}
}

// The strict flag follows the opt-in and nothing else.
func TestSubscriber_StrictIffOptedIn(t *testing.T) {
	assert.False(t, newEventSubscriber(nil).strict)
	assert.True(t, newEventSubscriber(nil, FeatureNexV1).strict)
	assert.True(t, newEventSubscriber(nil, FeatureAgentV2).strict)
	assert.True(t, newEventSubscriber(nil, FeatureNexV1, FeatureAgentV2).strict)
}

// queued returns the frames sitting in sub's buffer, oldest first.
func queued(sub *EventSubscriber) []string {
	var out []string
	for {
		select {
		case msg := <-sub.SendCh():
			out = append(out, string(msg))
		default:
			return out
		}
	}
}

// TestAgentV2_DoesNotChangeNexV1: the two opt-ins are independent. A
// subscriber that asked for one is never sent the other's scoped frames, and
// a scoped broadcast never removes a subscriber that did not ask for it, even
// one whose buffer is full.
func TestAgentV2_DoesNotChangeNexV1(t *testing.T) {
	eb := NewEventsBroadcaster()
	nexOnly := eb.AddTestSubscriberWith(FeatureNexV1)
	defer eb.RemoveTestSubscriber(nexOnly)
	agentOnly := eb.AddTestSubscriberWith(FeatureAgentV2)
	defer eb.RemoveTestSubscriber(agentOnly)
	both := eb.AddTestSubscriberWith(FeatureNexV1, FeatureAgentV2)
	defer eb.RemoveTestSubscriber(both)
	plain := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(plain)

	nexFrame := HostEvent{Type: "nex.execution", Value: `{"bseq":1}`}
	agentFrame := HostEvent{Type: "agent.snapshot", Value: `{"seq":0}`}
	eb.BroadcastStrictTo(FeatureNexV1, nexFrame)
	eb.BroadcastStrictTo(FeatureAgentV2, agentFrame)

	assert.Equal(t, []string{marshalled(t, nexFrame)}, queued(nexOnly))
	assert.Equal(t, []string{marshalled(t, agentFrame)}, queued(agentOnly))
	assert.Equal(t, []string{marshalled(t, nexFrame), marshalled(t, agentFrame)}, queued(both))
	assert.Empty(t, queued(plain))

	// HasSubscribersWanting is per feature: the nex safety reconcile must not
	// run for a connection that only wants the agent snapshot.
	eb2 := NewEventsBroadcaster()
	a := eb2.AddTestSubscriberWith(FeatureAgentV2)
	defer eb2.RemoveTestSubscriber(a)
	assert.False(t, eb2.HasSubscribersWanting(FeatureNexV1), "an agent.v2 subscriber counted as consuming nex deltas")
	assert.True(t, eb2.HasSubscribersWanting(FeatureAgentV2))

	// A full agent.v2-only buffer is not a casualty of a nex scoped send, and
	// a full nex.v1-only one is not a casualty of an agent scoped send; one
	// that wants the feature still is.
	eb3 := NewEventsBroadcaster()
	fullAgent := eb3.AddTestSubscriberWith(FeatureAgentV2)
	defer eb3.RemoveTestSubscriber(fullAgent)
	fullNex := eb3.AddTestSubscriberWith(FeatureNexV1)
	defer eb3.RemoveTestSubscriber(fullNex)
	fillBuffer(t, fullAgent)
	fillBuffer(t, fullNex)
	eb3.BroadcastStrictTo(FeatureNexV1, nexFrame)
	assert.False(t, isDone(fullAgent), "a nex scoped send removed an agent.v2 subscriber")
	assert.True(t, isDone(fullNex), "BroadcastStrictTo no longer removes a full subscriber that wants the feature")

	eb4 := NewEventsBroadcaster()
	fullNex2 := eb4.AddTestSubscriberWith(FeatureNexV1)
	defer eb4.RemoveTestSubscriber(fullNex2)
	fillBuffer(t, fullNex2)
	eb4.BroadcastStrictTo(FeatureAgentV2, agentFrame)
	assert.False(t, isDone(fullNex2), "an agent scoped send removed a nex.v1 subscriber")
}

// The upgrade request opts a real connection in, both at once.
func TestHandleHostEvents_AgentV2QueryOptsIn(t *testing.T) {
	for _, tc := range []struct {
		name, query        string
		wantNex, wantAgent bool
	}{
		{"none", "", false, false},
		{"agent=v2", "?agent=v2", false, true},
		{"both", "?nex=v1&agent=v2", true, true},
		{"agent twice", "?agent=v2&agent=v2", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eb := NewEventsBroadcaster()
			subs := make(chan *EventSubscriber, 1)
			eb.OnSubscribe(func(sub *EventSubscriber) { subs <- sub })
			server := httptest.NewServer(http.HandlerFunc(eb.HandleHostEvents))
			defer server.Close()

			_, sub := dialQuery(t, server, subs, tc.query)
			require.NotNil(t, sub)
			assert.Equal(t, tc.wantNex, sub.Wants(FeatureNexV1))
			assert.Equal(t, tc.wantAgent, sub.Wants(FeatureAgentV2))
			assert.Equal(t, tc.wantNex || tc.wantAgent, sub.strict)
		})
	}
}
