package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/execstat"
)

func gaugeValue(name string) (int64, bool) {
	for _, g := range execstat.Gauges() {
		if g.Name == name {
			return g.Value, true
		}
	}
	return 0, false
}

func eventTotal(name string) int64 {
	for _, s := range execstat.Events() {
		if s.Name == name {
			return s.Total
		}
	}
	return 0
}

// Each host-events connection is one subscribe event and one level on the subscribers gauge, and closing it takes the level
// back down.
func TestHostEventsSubscribersDenominator(t *testing.T) {
	execstat.ResetTags()
	t.Cleanup(execstat.ResetTags)

	eb := NewEventsBroadcaster()
	subs := make(chan *EventSubscriber, 2)
	eb.OnSubscribe(func(sub *EventSubscriber) { subs <- sub })
	server := httptest.NewServer(http.HandlerFunc(eb.HandleHostEvents))
	defer server.Close()

	c1, _ := dialQuery(t, server, subs, "")
	c2, _ := dialQuery(t, server, subs, "")
	assert.Equal(t, int64(2), eventTotal("host-events-subscribe"))
	v, ok := gaugeValue("host-events-subscribers")
	require.True(t, ok)
	assert.Equal(t, int64(2), v)

	c1.Close()
	c2.Close()
	require.Eventually(t, func() bool {
		v, _ := gaugeValue("host-events-subscribers")
		return v == 0
	}, 3*time.Second, 20*time.Millisecond, "the gauge must fall back when the connections end")
	assert.Equal(t, int64(2), eventTotal("host-events-subscribe"), "ending a connection is not a subscribe")
}
