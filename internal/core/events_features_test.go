package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1866 PR1b: a subscriber opts into a frame family when it connects
// (/ws/host-events?nex=v1), and only then is sent its frames. Old SPAs and
// other clients of the same WS never see nex.* at all.

func TestSubscriber_WantsOnlyWhatItOptedInto(t *testing.T) {
	eb := NewEventsBroadcaster()
	plain := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(plain)
	opted := eb.AddTestSubscriberWith(FeatureNexV1)
	defer eb.RemoveTestSubscriber(opted)

	assert.False(t, plain.Wants(FeatureNexV1))
	assert.True(t, opted.Wants(FeatureNexV1))
	assert.False(t, opted.Wants("other.v1"))
	assert.False(t, opted.Wants(""))
}

// An opted-in subscriber's buffer holds a burst — one delta per execution
// flushed in the same coalescing window must not look like a dead client —
// while every other subscriber keeps the 64 it always had.
func TestSubscriber_OptedInGetsTheBurstSizedBuffer(t *testing.T) {
	eb := NewEventsBroadcaster()
	plain := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(plain)
	opted := eb.AddTestSubscriberWith(FeatureNexV1)
	defer eb.RemoveTestSubscriber(opted)

	assert.Equal(t, 64, cap(plain.send))
	assert.Equal(t, 1024, cap(opted.send))
}

// dialQuery connects to /ws/host-events with a raw query string and returns
// the connection and the subscriber the server registered for it.
func dialQuery(t *testing.T, server *httptest.Server, subs <-chan *EventSubscriber, query string) (*websocket.Conn, *EventSubscriber) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/host-events" + query
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	select {
	case sub := <-subs:
		return conn, sub
	case <-time.After(2 * time.Second):
		t.Fatal("the connection was never registered")
		return nil, nil
	}
}

// The upgrade request's nex query parameter is the opt-in: exactly one nex
// value, and it exactly "v1", opts into nex.v1; no parameter, any other
// value, or more than one value — in any order, even v1 twice — does not.
// Other parameters (the ticket) do not matter.
func TestHandleHostEvents_NexV1QueryOptsIn(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		wants       bool
	}{
		{"no query", "", false},
		{"nex=v1", "?nex=v1", true},
		{"nex=v2", "?nex=v2", false},
		{"nex empty", "?nex=", false},
		{"nex bare", "?nex", false},
		{"nex=V1", "?nex=V1", false},
		{"other param", "?foo=v1", false},
		{"v1 then v2", "?nex=v1&nex=v2", false},
		{"v2 then v1", "?nex=v2&nex=v1", false},
		{"v1 twice", "?nex=v1&nex=v1", false},
		{"v1 then empty", "?nex=v1&nex=", false},
		{"with a ticket after", "?nex=v1&ticket=abc123", true},
		{"with a ticket before", "?ticket=abc123&nex=v1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eb := NewEventsBroadcaster()
			subs := make(chan *EventSubscriber, 1)
			eb.OnSubscribe(func(sub *EventSubscriber) { subs <- sub })
			server := httptest.NewServer(http.HandlerFunc(eb.HandleHostEvents))
			defer server.Close()

			_, sub := dialQuery(t, server, subs, tc.query)
			assert.Equal(t, tc.wants, sub.Wants(FeatureNexV1))
			want := 64
			if tc.wants {
				want = 1024
			}
			assert.Equal(t, want, cap(sub.send))
		})
	}
}

// Over real connections: a scoped strict frame reaches the opted-in client
// only. The one without nex gets the best-effort frame broadcast after it as
// its first frame — nothing nex.* came before it — and stays connected.
func TestHandleHostEvents_ScopedFramesReachOnlyOptedInConnections(t *testing.T) {
	eb := NewEventsBroadcaster()
	subs := make(chan *EventSubscriber, 2)
	eb.OnSubscribe(func(sub *EventSubscriber) { subs <- sub })
	server := httptest.NewServer(http.HandlerFunc(eb.HandleHostEvents))
	defer server.Close()
	opted, _ := dialQuery(t, server, subs, "?nex=v1")
	plain, _ := dialQuery(t, server, subs, "")

	eb.BroadcastStrictTo(FeatureNexV1, HostEvent{Type: "nex.execution", Value: `{"bseq":1}`})
	eb.Broadcast("s", "status", "running")

	read := func(conn *websocket.Conn) HostEvent {
		t.Helper()
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
		_, msg, err := conn.ReadMessage()
		require.NoError(t, err)
		var ev HostEvent
		require.NoError(t, json.Unmarshal(msg, &ev))
		return ev
	}
	assert.Equal(t, "nex.execution", read(opted).Type)
	assert.Equal(t, "status", read(opted).Type)
	assert.Equal(t, "status", read(plain).Type, "a connection without nex=v1 was sent a nex frame")
	assert.True(t, eb.HasSubscribers())
}
