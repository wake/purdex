package core

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// BG-4 (badge spec §7.2): /ws/host-events?reads=v1 opts into the read-sync frames (session.reads, session.read). Unlike nex.v1
// and agent.v2 it does NOT make the subscriber strict for every frame: only the two read frames are strict, and only
// for the connections that opted in.

func TestFeaturesOf_ReadsV1(t *testing.T) {
	reads := []string{FeatureReadsV1}
	for _, tc := range []struct {
		name, query string
		want        []string
	}{
		{"reads only", "?reads=v1", reads},
		{"all three, fixed order", "?agent=v2&reads=v1&nex=v1", []string{FeatureNexV1, FeatureAgentV2, FeatureReadsV1}},
		{"reads with a ticket", "?ticket=abc&reads=v1", reads},
		{"reads twice", "?reads=v1&reads=v1", nil},
		{"reads=v2", "?reads=v2", nil},
		{"reads empty", "?reads=", nil},
		{"reads bare", "?reads", nil},
		{"a wrong reads leaves nex alone", "?nex=v1&reads=v2", []string{FeatureNexV1}},
		{"a wrong nex leaves reads alone", "?nex=v2&reads=v1", reads},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/ws/host-events"+tc.query, nil)
			assert.Equal(t, tc.want, featuresOf(r))
		})
	}
}

// reads=v1 alone does not make a subscriber strict: every other frame keeps its best effort. Mutation gate: add
// FeatureReadsV1 to the strict set → red.
func TestSubscriber_ReadsV1DoesNotMakeItStrict(t *testing.T) {
	sub := newEventSubscriber(nil, FeatureReadsV1)
	assert.False(t, sub.strict)
	assert.True(t, sub.Wants(FeatureReadsV1))
	assert.True(t, newEventSubscriber(nil, FeatureReadsV1, FeatureNexV1).strict, "nex.v1 still makes it strict")
}

// A full buffer: an ordinary frame is dropped and the connection stays; a read frame closes it (its reconnect brings a fresh
// snapshot). A connection that did not opt in gets neither the frame nor any cost.
func TestReadsV1_OnlyTheReadFramesAreStrict(t *testing.T) {
	eb := NewEventsBroadcaster()
	plain := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(plain)
	fillBuffer(t, plain)
	slow := eb.AddTestSubscriberWith(FeatureReadsV1)
	fillBuffer(t, slow)

	eb.BroadcastEvent(HostEvent{Type: "tmux", Value: "x"}) // best effort for both
	eb.Broadcast("s", "agent.status", `{}`)
	assert.False(t, isDone(slow), "an ordinary frame ended a reads=v1 connection")
	assert.False(t, isDone(plain))

	eb.BroadcastStrictTo(FeatureReadsV1, HostEvent{Type: "session.read", Session: "c1", Value: `{}`})
	assert.True(t, isDone(slow), "a read frame that did not fit must close the opted-in connection")
	assert.False(t, isDone(plain), "a connection that did not opt in must never be closed over a read frame")
	assert.Len(t, plain.send, cap(plain.send))
}

func TestHandleHostEvents_ReadsV1QueryOptsIn(t *testing.T) {
	for query, want := range map[string]bool{"?reads=v1": true, "": false, "?reads=v2": false, "?reads=v1&reads=v1": false} {
		eb := NewEventsBroadcaster()
		subs := make(chan *EventSubscriber, 1)
		eb.OnSubscribe(func(sub *EventSubscriber) { subs <- sub })
		server := httptest.NewServer(http.HandlerFunc(eb.HandleHostEvents))
		_, sub := dialQuery(t, server, subs, query)
		assert.Equal(t, want, sub.Wants(FeatureReadsV1), query)
		assert.False(t, sub.strict, "reads=v1 alone never makes a connection strict (%s)", query)
		server.Close()
	}
}
