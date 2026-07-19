package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

// claudeBaseCommand is the M0 headless agent invocation the relay wraps. It
// mirrors the working default stream preset (config.go) — `--verbose` is
// required alongside `--output-format stream-json` in print mode. The issue
// prompt is NOT a command-line argument: it is delivered over relay stdin as a
// stream-json message (buildUserMessage), so nothing user-controlled ever
// reaches this shell line.
const claudeBaseCommand = "claude -p --verbose --input-format stream-json --output-format stream-json"

const (
	defaultConnectTimeout = 15 * time.Second
	defaultPollInterval   = 500 * time.Millisecond
)

// RelayGateway is the narrow bridge port the launcher needs: probe whether a
// relay has registered for a session code, and push a message to the relay's
// stdin. Satisfied by *bridge.Bridge (HasRelay / SubscriberToRelay). Injected so
// execution never imports the stream module or the bridge concretely.
type RelayGateway interface {
	HasRelay(name string) bool
	SubscriberToRelay(name string, data []byte)
}

// LaunchConfig is the daemon connection snapshot the relay command needs. It is
// read fresh per launch (token/port/bind can change via config reload).
type LaunchConfig struct {
	Token string
	Port  int
	Bind  string
}

// RealLauncher is the production Launcher: it creates the named tmux session,
// spawns `pdx relay -- claude -p …` inside it, waits for the relay to connect,
// then pushes the issue prompt over relay stdin. It reuses the exact primitives
// the stream handoff uses (NewSession, SendKeys, token file, relay-connect
// polling) but assembles a from-zero launch rather than a resume/handoff.
type RealLauncher struct {
	tmux   tmux.Executor
	relay  RelayGateway
	encode func(tmuxID string) (string, error) // session.EncodeSessionID, injected
	cfg    func() LaunchConfig

	tokenDir       string
	connectTimeout time.Duration
	pollInterval   time.Duration
	now            func() time.Time
	sleep          func(time.Duration)
	logf           func(format string, args ...any)
}

// NewRealLauncher builds a RealLauncher. encode is session.EncodeSessionID; cfg
// returns the current daemon connection snapshot (token/port/bind).
func NewRealLauncher(exec tmux.Executor, relay RelayGateway, encode func(string) (string, error), cfg func() LaunchConfig) *RealLauncher {
	return &RealLauncher{
		tmux:           exec,
		relay:          relay,
		encode:         encode,
		cfg:            cfg,
		tokenDir:       os.TempDir(),
		connectTimeout: defaultConnectTimeout,
		pollInterval:   defaultPollInterval,
		now:            time.Now,
		sleep:          time.Sleep,
		logf:           log.Printf,
	}
}

var _ Launcher = (*RealLauncher)(nil)

// Launch performs the from-zero start for one execution and blocks until the
// relay connects (or errors). On any error before connect the created session
// may be orphaned; the caller marks the execution failed and reconcile (P.9)
// collects the orphan by session_name.
func (l *RealLauncher) Launch(ctx context.Context, spec LaunchSpec) (LaunchResult, error) {
	if l.tmux.HasSession(spec.SessionName) {
		return LaunchResult{}, fmt.Errorf("launch: tmux session %q already exists", spec.SessionName)
	}
	if err := l.tmux.NewSession(spec.SessionName, spec.Cwd); err != nil {
		return LaunchResult{}, fmt.Errorf("launch: create session %q: %w", spec.SessionName, err)
	}

	// Derive the deeplink session_code from the freshly-created tmux id.
	code, err := l.sessionCode(spec.SessionName)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("launch: derive session code: %w", err)
	}

	cfg := l.cfg()
	tokenFile := filepath.Join(l.tokenDir, "purdex-token-"+spec.ExecutionID)
	if err := os.WriteFile(tokenFile, []byte(cfg.Token), 0o600); err != nil {
		return LaunchResult{}, fmt.Errorf("launch: write token file: %w", err)
	}
	// The relay reads the token file within seconds of startup; removing it when
	// Launch returns is safe and covers all exit paths.
	defer os.Remove(tokenFile)

	relayCmd := buildRelayCommand(code, cfg.Bind, cfg.Port, tokenFile, spec.SandboxProfile)
	target := spec.SessionName + ":0"
	if err := l.tmux.SendKeys(target, relayCmd); err != nil {
		return LaunchResult{}, fmt.Errorf("launch: send relay command: %w", err)
	}

	if !l.waitRelayConnect(code) {
		return LaunchResult{}, fmt.Errorf("launch: relay for %q did not connect within %s", code, l.connectTimeout)
	}

	// Push the issue prompt over relay stdin as a single stream-json user
	// message. The daemon is the sole initial writer (M0 deeplink attach is
	// observe-only, spec §9), so there is no multi-writer race here.
	msg, err := buildUserMessage(spec.Prompt)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("launch: build prompt message: %w", err)
	}
	l.relay.SubscriberToRelay(code, msg)

	return LaunchResult{SessionCode: code}, nil
}

// sessionCode looks up the tmux id for the just-created session and encodes it
// into the 6-char deeplink code.
func (l *RealLauncher) sessionCode(sessionName string) (string, error) {
	sessions, err := l.tmux.ListSessions()
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}
	for _, s := range sessions {
		if s.Name == sessionName {
			return l.encode(s.ID)
		}
	}
	return "", fmt.Errorf("session %q not found after create", sessionName)
}

// waitRelayConnect polls the bridge until the relay for code registers or the
// connect timeout elapses.
func (l *RealLauncher) waitRelayConnect(code string) bool {
	deadline := l.now().Add(l.connectTimeout)
	for {
		if l.relay.HasRelay(code) {
			return true
		}
		if !l.now().Before(deadline) {
			return false
		}
		l.sleep(l.pollInterval)
	}
}

// buildRelayCommand assembles the SendKeys line. Only fixed, daemon-controlled
// tokens (session code, bind/port, token-file path, the static agent command)
// appear here — never the issue prompt.
func buildRelayCommand(code, bind string, port int, tokenFile, sandboxProfile string) string {
	cmd := fmt.Sprintf("pdx relay --session %s --daemon ws://%s:%d --token-file %s -- %s",
		code, bind, port, tokenFile, claudeBaseCommand)
	if flag := permissionModeFlag(sandboxProfile); flag != "" {
		cmd += " " + flag
	}
	return cmd
}

// permissionModeFlag maps a sandbox_profile onto a `claude --permission-mode`
// flag. M0 P.6 is a passthrough stub returning "" (no partial-order clamp, no
// mapping table); the effective-profile clamp + flag mapping lands in P.10.
func permissionModeFlag(sandboxProfile string) string { return "" }

// buildUserMessage marshals the issue prompt into the stream-json user message
// Claude Code's `--input-format stream-json` consumes. json.Marshal escapes the
// prompt, so arbitrary issue text cannot break the framing.
func buildUserMessage(prompt string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": prompt,
		},
	})
}
