package session

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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
