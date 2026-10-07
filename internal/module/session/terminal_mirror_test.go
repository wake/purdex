package session

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/tmux"
)

func TestIsMirrorRequest(t *testing.T) {
	cases := map[string]bool{
		"/ws/terminal/x":             false,
		"/ws/terminal/x?mirror=1":    true,
		"/ws/terminal/x?mirror=0":    false,
		"/ws/terminal/x?mirror=":     false,
		"/ws/terminal/x?mirror=true": false,
		"/ws/terminal/x?mirror=11":   false,
	}
	for url, want := range cases {
		r := httptest.NewRequest("GET", url, nil)
		assert.Equal(t, want, isMirrorRequest(r), url)
	}
}

func TestTerminalRelaySetup_NonMirrorUnchanged(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	cases := []struct {
		mode        string
		args        []string
		wantOnStart bool
	}{
		{"auto", []string{"attach-session", "-t", "dev"}, true},
		{"", []string{"attach-session", "-t", "dev"}, true},
		{"minimal-first", []string{"attach-session", "-t", "dev"}, true},
		{"terminal-first", []string{"attach-session", "-t", "dev", "-f", "ignore-size"}, false},
	}
	for _, c := range cases {
		args, onStart := terminalRelaySetup(fake, "dev", c.mode, false)
		assert.Equal(t, c.args, args, c.mode)
		assert.Equal(t, c.wantOnStart, onStart != nil, c.mode)
	}
}

func TestNewTerminalRelay_WindowSizeOnlyForMirror(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetWindowSize(132, 43)

	normal := newTerminalRelay(fake, "dev", "auto", false)
	assert.Nil(t, normal.WindowSize)

	mirror := newTerminalRelay(fake, "dev", "auto", true)
	require.NotNil(t, mirror.WindowSize)
	assert.Nil(t, mirror.OnStart)
	c, r, err := mirror.WindowSize(context.Background())
	require.NoError(t, err)
	assert.Equal(t, [2]uint16{132, 43}, [2]uint16{c, r})
}

// The mirror PTY is window size + status rows: tmux sizes the window from even
// a lone ignore-size client, and a client that is exactly window-high loses the
// status row from the window (observed: 150x44 client -> 150x43 window).
func TestNewTerminalRelay_PTYSizeIsWindowPlusStatusRows(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetWindowSize(150, 44)

	assert.Nil(t, newTerminalRelay(fake, "dev", "auto", false).PTYSize, "non-mirror keeps client-driven sizing")

	mirror := newTerminalRelay(fake, "dev", "auto", true)
	require.NotNil(t, mirror.PTYSize)
	c, r, err := mirror.PTYSize(context.Background())
	require.NoError(t, err)
	assert.Equal(t, [2]uint16{150, 45}, [2]uint16{c, r})

	fake.SetStatusRows(0)
	_, r, _ = mirror.PTYSize(context.Background())
	assert.Equal(t, uint16(44), r, "status off adds nothing")

	fake.SetStatusRows(2)
	_, r, _ = mirror.PTYSize(context.Background())
	assert.Equal(t, uint16(46), r)

	fake.SetWindowSizeErr(assert.AnError)
	_, _, err = mirror.PTYSize(context.Background())
	assert.Error(t, err, "an unknown size must surface, never default")
}

func TestTerminalRelaySetup_MirrorIgnoresSizeNoOnStart(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	for _, mode := range []string{"auto", "", "minimal-first", "terminal-first", "bogus"} {
		args, onStart := terminalRelaySetup(fake, "dev", mode, true)
		assert.Equal(t, []string{"attach-session", "-t", "dev", "-f", "ignore-size"}, args, mode)
		assert.Nil(t, onStart, mode)
	}
	assert.Empty(t, fake.AutoResizeCalls())
	assert.Empty(t, fake.SetWindowOptionCalls())
}
