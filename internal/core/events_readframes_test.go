package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedServer upgrades each request and hands the connection to script, then closes it.
func scriptedServer(t *testing.T, script func(c *websocket.Conn)) *websocket.Conn {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		script(c)
	}))
	t.Cleanup(srv.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

// #2590: the read waits for progress, not for a total time. A server that keeps sending, slowly, for longer than the idle
// window and then closes is a pass; the old fixed deadline failed it. Mutation gate: set the deadline once, before the loop
// → red.
func TestReadFrames_ProgressRenewsTheWindow(t *testing.T) {
	conn := scriptedServer(t, func(c *websocket.Conn) {
		for i := 0; i < 10; i++ {
			c.WriteMessage(websocket.TextMessage, []byte(`{"type":"tick"}`))
			time.Sleep(200 * time.Millisecond)
		}
		c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	})
	// 2 s of frames against a 1 s idle window
	types, err := readFrames(conn, time.Second, time.Minute)
	require.Error(t, err)
	assert.False(t, errors.Is(err, errNoProgress), "a server that kept sending was judged stuck: %v", err)
	assert.False(t, errors.Is(err, errTooLong))
	assert.Len(t, types, 10)
}

// No frame and no close for the idle window is the failure this helper exists to keep: the server that never closes.
// Mutation gate: never time out → red (the read hangs until the connection dies).
func TestReadFrames_SilenceIsNoProgress(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	conn := scriptedServer(t, func(c *websocket.Conn) {
		c.WriteMessage(websocket.TextMessage, []byte(`{"type":"one"}`))
		<-release // alive, silent, never closing
	})
	start := time.Now()
	types, err := readFrames(conn, 300*time.Millisecond, time.Minute)
	require.ErrorIs(t, err, errNoProgress)
	assert.Equal(t, []string{"one"}, types)
	assert.Less(t, time.Since(start), 10*time.Second)
}

// A server that sends forever never trips the idle window; the total limit is the backstop.
func TestReadFrames_ASendForeverServerHitsTheTotalLimit(t *testing.T) {
	conn := scriptedServer(t, func(c *websocket.Conn) {
		for {
			if err := c.WriteMessage(websocket.TextMessage, []byte(`{"type":"x"}`)); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	_, err := readFrames(conn, time.Second, 400*time.Millisecond)
	require.ErrorIs(t, err, errTooLong)
}

// The connection's own ending is returned as it is, with the frames read before it.
func TestReadFrames_ACloseIsReturnedNotAFailure(t *testing.T) {
	conn := scriptedServer(t, func(c *websocket.Conn) {
		c.WriteMessage(websocket.TextMessage, []byte(`{"type":"a"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"type":"b"}`))
	})
	types, err := readFrames(conn, 5*time.Second, time.Minute)
	require.Error(t, err)
	assert.False(t, errors.Is(err, errNoProgress) || errors.Is(err, errTooLong), "%v", err)
	assert.Equal(t, []string{"a", "b"}, types)
}
