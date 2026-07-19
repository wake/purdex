package execution

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/tmux"
)

// fakeRelay is a RelayGateway double. It reports "connected" once HasRelay has
// been polled more than connectAfter times, and captures stdin pushes.
type fakeRelay struct {
	mu           sync.Mutex
	connectAfter int
	calls        int
	sent         []relaySend
}

type relaySend struct {
	name string
	data []byte
}

func (f *fakeRelay) HasRelay(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.calls > f.connectAfter
}

func (f *fakeRelay) SubscriberToRelay(name string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, relaySend{name: name, data: append([]byte(nil), data...)})
}

func (f *fakeRelay) sends() []relaySend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]relaySend(nil), f.sent...)
}

// testEncode is a deterministic stand-in for session.EncodeSessionID so tests
// need not import the session package.
func testEncode(tmuxID string) (string, error) {
	return "CODE" + strings.TrimPrefix(tmuxID, "$"), nil
}

func newTestLauncher(t *testing.T, exec tmux.Executor, relay RelayGateway) *RealLauncher {
	t.Helper()
	l := NewRealLauncher(exec, relay, testEncode, func() LaunchConfig {
		return LaunchConfig{Token: "tok-secret", Port: 7860, Bind: "127.0.0.1"}
	})
	l.tokenDir = t.TempDir()
	l.sleep = func(time.Duration) {} // never actually sleep in tests
	return l
}

func TestRealLauncher_CommandAssemblyAndPromptOverStdin(t *testing.T) {
	exec := tmux.NewFakeExecutor()
	relay := &fakeRelay{connectAfter: 0} // connects on first poll
	l := newTestLauncher(t, exec, relay)

	prompt := `fix the bug in "main.go" && rm -rf /  ; echo pwn`
	res, err := l.Launch(context.Background(), LaunchSpec{
		ExecutionID:    "exc_abc",
		SessionName:    "pdx-exec-exc_abc",
		Cwd:            "/repo/here",
		Prompt:         prompt,
		SandboxProfile: "workspace-write",
	})
	require.NoError(t, err)
	require.Equal(t, "CODE0", res.SessionCode) // fake assigns $0 to first session

	// Session created with the deterministic name + cwd.
	require.True(t, exec.HasSession("pdx-exec-exc_abc"))

	// Exactly one SendKeys: the relay command.
	keys := exec.KeysSent()
	require.Len(t, keys, 1)
	call := keys[0]
	require.Equal(t, "pdx-exec-exc_abc:0", call.Target)
	require.Contains(t, call.Keys, "pdx relay --session CODE0 ")
	require.Contains(t, call.Keys, "--daemon ws://127.0.0.1:7860 ")
	require.Contains(t, call.Keys, "--token-file ")
	require.Contains(t, call.Keys, "-- "+claudeBaseCommand)

	// The prompt must NEVER appear on the command line (no keystroke injection).
	require.NotContains(t, call.Keys, "rm -rf")
	require.NotContains(t, call.Keys, "pwn")
	require.NotContains(t, call.Keys, "main.go")

	// The prompt is delivered over relay stdin as a valid stream-json user msg.
	sends := relay.sends()
	require.Len(t, sends, 1)
	require.Equal(t, "CODE0", sends[0].name)
	var msg struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	require.NoError(t, json.Unmarshal(sends[0].data, &msg))
	require.Equal(t, "user", msg.Type)
	require.Equal(t, "user", msg.Message.Role)
	require.Equal(t, prompt, msg.Message.Content)

	// Token file was written then cleaned up.
	entries, _ := os.ReadDir(l.tokenDir)
	require.Empty(t, entries, "token file should be removed after launch")
}

func TestRealLauncher_RelayNeverConnects(t *testing.T) {
	exec := tmux.NewFakeExecutor()
	relay := &fakeRelay{connectAfter: 1_000_000} // never connects in-window
	l := newTestLauncher(t, exec, relay)
	l.connectTimeout = 0 // deadline already passed → single probe then give up

	_, err := l.Launch(context.Background(), LaunchSpec{
		ExecutionID: "exc_to",
		SessionName: "pdx-exec-exc_to",
		Cwd:         "/repo",
		Prompt:      "hi",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "did not connect")

	// No prompt was pushed since the relay never connected.
	require.Empty(t, relay.sends())
	// The (orphan) session was created — reconcile collects it by name (P.9).
	require.True(t, exec.HasSession("pdx-exec-exc_to"))
}

func TestRealLauncher_SessionAlreadyExists(t *testing.T) {
	exec := tmux.NewFakeExecutor()
	require.NoError(t, exec.NewSession("pdx-exec-dup", "/somewhere"))
	relay := &fakeRelay{}
	l := newTestLauncher(t, exec, relay)

	_, err := l.Launch(context.Background(), LaunchSpec{
		ExecutionID: "dup",
		SessionName: "pdx-exec-dup",
		Cwd:         "/repo",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")
	require.Empty(t, exec.KeysSent(), "must not spawn into a pre-existing session")
}

func TestBuildRelayCommand_AppendsPermissionMode(t *testing.T) {
	// The effective profile maps onto a --permission-mode flag appended after the
	// static claude command (P.10).
	cmd := buildRelayCommand("CODEX", "0.0.0.0", 9000, "/tmp/purdex-token-x", "danger-full")
	require.Equal(t,
		"pdx relay --session CODEX --daemon ws://0.0.0.0:9000 --token-file /tmp/purdex-token-x -- "+
			claudeBaseCommand+" --permission-mode bypassPermissions",
		cmd)
}

func TestPermissionModeFlag_Mapping(t *testing.T) {
	cases := map[string]string{
		"read-only":       "--permission-mode plan",
		"ask":             "--permission-mode default",
		"workspace-write": "--permission-mode acceptEdits",
		"danger-full":     "--permission-mode bypassPermissions",
		// Defensive: an empty/invalid profile yields no flag (effective is always a
		// valid enum in production, so this never fires on the live path).
		"":         "",
		"nonsense": "",
	}
	for profile, want := range cases {
		require.Equal(t, want, permissionModeFlag(profile), profile)
	}
}

func TestBuildUserMessage_EscapesPrompt(t *testing.T) {
	raw, err := buildUserMessage(`line1
"quoted" \backslash`)
	require.NoError(t, err)
	// Valid single-line JSON (newline escaped, not literal).
	require.NotContains(t, string(raw), "\n")
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Equal(t, "user", m["type"])
}
