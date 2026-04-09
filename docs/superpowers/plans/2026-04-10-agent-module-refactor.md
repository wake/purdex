# Agent Module Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract agent-specific logic into a provider pattern with registry, supporting CC and Codex as independent providers with normalized event broadcasting.

**Architecture:** AgentProvider interface with capability-based composition (HookInstaller, HistoryProvider, StreamCapable). Agent module acts as registry + hook event router. Backend derives status and broadcasts NormalizedEvent; frontend is agent-agnostic with only an icon map.

**Tech Stack:** Go (interfaces, type assertion for capabilities), React/Zustand (generic store), Vitest (frontend tests)

**Spec:** `docs/superpowers/specs/2026-04-10-agent-module-design.md`

---

## File Map

### New Files (Create)

| Path | Responsibility |
|------|---------------|
| `internal/agent/provider.go` | AgentProvider + capability interfaces |
| `internal/agent/status.go` | Status enum, NormalizedEvent, DeriveResult |
| `internal/agent/registry.go` | Registry: Get, Claim, All |
| `internal/agent/registry_test.go` | Registry unit tests |
| `internal/agent/cc/provider.go` | CC provider assembly |
| `internal/agent/cc/detector.go` | CC detection (moved from `internal/detect/`) |
| `internal/agent/cc/extract.go` | Session ID / status extraction (moved from `internal/detect/`) |
| `internal/agent/cc/operator.go` | CC operations (moved from `internal/module/cc/`) |
| `internal/agent/cc/history.go` | CC history (moved from `internal/module/cc/`) |
| `internal/agent/cc/hooks.go` | CC HookInstaller (moved from `internal/module/agent/cc_hooks.go`) |
| `internal/agent/cc/status.go` | CC DeriveStatus |
| `internal/agent/cc/status_test.go` | CC status derivation tests |
| `internal/agent/codex/provider.go` | Codex provider assembly |
| `internal/agent/codex/detector.go` | Codex process detection |
| `internal/agent/codex/hooks.go` | Codex HookInstaller |
| `internal/agent/codex/status.go` | Codex DeriveStatus |
| `internal/agent/codex/status_test.go` | Codex status derivation tests |
| `spa/src/lib/agent-icons.ts` | Agent icon + name map |

### Modified Files

| Path | Change |
|------|--------|
| `internal/module/agent/module.go` | Add registry field, init providers, DB replay |
| `internal/module/agent/handler.go` | Normalized event dispatch via registry |
| `internal/module/stream/module.go` | Update imports from `module/cc` → `agent/cc` |
| `internal/module/stream/orchestrator.go` | Update imports from `detect` → `agent/cc` |
| `internal/module/stream/orchestrator_test.go` | Update imports |
| `cmd/tbox/main.go` | Remove `cc.New()`, update agent module init |
| `cmd/tbox/setup.go` | Add `--agent` flag, Codex support |
| `cmd/tbox/hook.go` | Make `--agent` required |
| `spa/src/stores/useAgentStore.ts` | Rewrite for NormalizedEvent |
| `spa/src/stores/useAgentStore.test.ts` | Rewrite tests |
| `spa/src/hooks/useMultiHostEventWs.ts` | Parse NormalizedEvent |
| `spa/src/hooks/useNotificationDispatcher.ts` | Use pre-derived status |
| `spa/src/lib/notification-content.ts` | Read from `detail` instead of `raw_event` |
| `spa/src/components/StatusBar.tsx` | Read model from store |
| `spa/src/components/SessionPanel.tsx` | Agent icon from store |
| `spa/src/components/SortableTab.tsx` | Agent icon integration |
| `spa/src/components/settings/AgentSection.tsx` | Per-agent hook toggles |

### Deleted Files

| Path | Reason |
|------|--------|
| `internal/detect/detector.go` | Moved to `agent/cc/detector.go` |
| `internal/detect/extract.go` | Moved to `agent/cc/extract.go` |
| `internal/detect/detector_test.go` | Moved with detector |
| `internal/module/cc/` (entire dir) | Merged into `agent/cc/` |
| `internal/module/agent/cc_hooks.go` | Moved to `agent/cc/hooks.go` |

---

## Task 1: Agent Interface Layer

**Files:**
- Create: `internal/agent/provider.go`
- Create: `internal/agent/status.go`
- Create: `internal/agent/registry.go`
- Create: `internal/agent/registry_test.go`

- [ ] **Step 1: Create `internal/agent/provider.go`**

```go
package agent

import "encoding/json"

// AgentProvider is the core interface that all agent providers must implement.
type AgentProvider interface {
	Type() string
	DisplayName() string
	IconHint() string
	Claim(ctx ClaimContext) bool
	DeriveStatus(eventName string, rawEvent json.RawMessage) DeriveResult
	IsAlive(tmuxTarget string) bool
}

// ClaimContext provides information for agent detection.
type ClaimContext struct {
	HookEvent   *HookEvent
	ProcessName string
}

// HookEvent is the raw hook event received from tbox hook CLI.
type HookEvent struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
	AgentType   string          `json:"agent_type"`
}

// --- Optional capabilities ---

// HookInstaller can install/remove/check hook configurations for a specific agent.
type HookInstaller interface {
	InstallHooks(tboxPath string) error
	RemoveHooks(tboxPath string) error
	CheckHooks() (HookStatus, error)
}

// HookStatus reports the installation state of hooks for an agent.
type HookStatus struct {
	Installed bool                     `json:"installed"`
	Events    map[string]HookEventInfo `json:"events"`
	Issues    []string                 `json:"issues"`
}

// HookEventInfo describes the state of a single hook event.
type HookEventInfo struct {
	Installed bool   `json:"installed"`
	Command   string `json:"command"`
}

// HistoryProvider can retrieve conversation history for a session.
type HistoryProvider interface {
	GetHistory(cwd string, sessionID string) ([]map[string]any, error)
}

// StreamCapable marks a provider that supports stream mode handoff.
// Reserved for future implementation.
type StreamCapable interface {
	ExtractState(tmuxTarget string) (SessionState, error)
	ExitInteractive(tmuxTarget string) error
	RelayArgs(state SessionState) []string
	ResumeCommand(state SessionState) string
}

// SessionState holds agent session state for stream handoff.
type SessionState struct {
	SessionID string
	Cwd       string
}
```

- [ ] **Step 2: Create `internal/agent/status.go`**

```go
package agent

// Status represents the normalized agent status.
type Status string

const (
	StatusRunning Status = "running"
	StatusWaiting Status = "waiting"
	StatusIdle    Status = "idle"
	StatusError   Status = "error"
	StatusClear   Status = "clear"
)

// DeriveResult is the output of AgentProvider.DeriveStatus.
type DeriveResult struct {
	Status Status
	Valid  bool              // false = event should be ignored
	Model  string            // extracted model name (if any)
	Detail map[string]any    // event-specific data for frontend notifications
}

// NormalizedEvent is broadcast to WS subscribers.
type NormalizedEvent struct {
	AgentType    string         `json:"agent_type"`
	Status       string         `json:"status"`
	Model        string         `json:"model,omitempty"`
	Subagents    []string       `json:"subagents,omitempty"`
	RawEventName string         `json:"raw_event_name"`
	BroadcastTs  int64          `json:"broadcast_ts"`
	Detail       map[string]any `json:"detail,omitempty"`
}
```

- [ ] **Step 3: Create `internal/agent/registry.go`**

```go
package agent

import "sync"

// Registry manages registered agent providers.
type Registry struct {
	mu        sync.RWMutex
	providers []AgentProvider
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a provider. Registration order determines Claim priority.
func (r *Registry) Register(p AgentProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

// Get returns the provider matching the given agent type.
func (r *Registry) Get(agentType string) (AgentProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Type() == agentType {
			return p, true
		}
	}
	return nil, false
}

// Claim asks each provider (in registration order) whether it claims
// the session described by ctx. Used only for process detection path
// (when no hook event with agent_type is available).
func (r *Registry) Claim(ctx ClaimContext) (AgentProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Claim(ctx) {
			return p, true
		}
	}
	return nil, false
}

// All returns all registered providers.
func (r *Registry) All() []AgentProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AgentProvider, len(r.providers))
	copy(out, r.providers)
	return out
}
```

- [ ] **Step 4: Write registry tests `internal/agent/registry_test.go`**

```go
package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/tmux-box/internal/agent"
)

type fakeProvider struct {
	agentType string
	claimFn   func(agent.ClaimContext) bool
}

func (f *fakeProvider) Type() string        { return f.agentType }
func (f *fakeProvider) DisplayName() string { return f.agentType }
func (f *fakeProvider) IconHint() string    { return f.agentType }
func (f *fakeProvider) Claim(ctx agent.ClaimContext) bool {
	if f.claimFn != nil {
		return f.claimFn(ctx)
	}
	return false
}
func (f *fakeProvider) DeriveStatus(string, json.RawMessage) agent.DeriveResult {
	return agent.DeriveResult{}
}
func (f *fakeProvider) IsAlive(string) bool { return true }

func TestRegistryGet(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&fakeProvider{agentType: "cc"})
	r.Register(&fakeProvider{agentType: "codex"})

	p, ok := r.Get("cc")
	if !ok || p.Type() != "cc" {
		t.Fatal("expected cc provider")
	}
	p, ok = r.Get("codex")
	if !ok || p.Type() != "codex" {
		t.Fatal("expected codex provider")
	}
	_, ok = r.Get("unknown")
	if ok {
		t.Fatal("expected no provider for unknown")
	}
}

func TestRegistryClaimPriority(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&fakeProvider{agentType: "cc", claimFn: func(ctx agent.ClaimContext) bool {
		return ctx.ProcessName == "claude"
	}})
	r.Register(&fakeProvider{agentType: "codex", claimFn: func(ctx agent.ClaimContext) bool {
		return ctx.ProcessName == "codex"
	}})

	p, ok := r.Claim(agent.ClaimContext{ProcessName: "codex"})
	if !ok || p.Type() != "codex" {
		t.Fatal("expected codex to claim")
	}
	p, ok = r.Claim(agent.ClaimContext{ProcessName: "claude"})
	if !ok || p.Type() != "cc" {
		t.Fatal("expected cc to claim")
	}
	_, ok = r.Claim(agent.ClaimContext{ProcessName: "bash"})
	if ok {
		t.Fatal("expected no provider to claim bash")
	}
}

func TestRegistryAll(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&fakeProvider{agentType: "cc"})
	r.Register(&fakeProvider{agentType: "codex"})
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(all))
	}
}
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design && go test ./internal/agent/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agent/provider.go internal/agent/status.go internal/agent/registry.go internal/agent/registry_test.go
git commit -m "feat: add agent interface layer (provider, status, registry)"
```

---

## Task 2: CC Provider — Detector + Status

**Files:**
- Create: `internal/agent/cc/detector.go` (move from `internal/detect/detector.go`)
- Create: `internal/agent/cc/extract.go` (move from `internal/detect/extract.go`)
- Create: `internal/agent/cc/status.go`
- Create: `internal/agent/cc/status_test.go`

- [ ] **Step 1: Move detector to `internal/agent/cc/detector.go`**

Copy `internal/detect/detector.go` to `internal/agent/cc/detector.go`. Changes:
- Package declaration: `package detect` → `package cc`
- Import path for tmux: unchanged (`github.com/wake/tmux-box/internal/tmux`)
- All exported types (`Status`, `StatusNormal`, `StatusCCIdle`, etc.) and functions (`New`, `Detect`, `UpdateCommands`) remain exported
- Rename `New` → `NewDetector` to avoid collision with provider `New`

```go
// At top of file, change:
package cc

// Rename constructor:
func NewDetector(executor tmux.Executor, ccCommands []string) *Detector {
```

- [ ] **Step 2: Move extract to `internal/agent/cc/extract.go`**

Copy `internal/detect/extract.go` to `internal/agent/cc/extract.go`. Changes:
- Package declaration: `package detect` → `package cc`
- No other changes needed (no internal imports)

- [ ] **Step 3: Move detector tests**

Copy `internal/detect/detector_test.go` to `internal/agent/cc/detector_test.go`. Changes:
- Package declaration: `package detect_test` → `package cc_test`
- Import path: `github.com/wake/tmux-box/internal/detect` → `github.com/wake/tmux-box/internal/agent/cc`
- Replace `detect.New(` → `cc.NewDetector(`
- Replace all `detect.Status*` → `cc.Status*`

- [ ] **Step 4: Run detector tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design && go test ./internal/agent/cc/... -run TestDetect`
Expected: PASS

- [ ] **Step 5: Create `internal/agent/cc/status.go`**

```go
package cc

import (
	"encoding/json"

	"github.com/wake/tmux-box/internal/agent"
)

// deriveCCStatus maps CC hook events to normalized agent status.
func deriveCCStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	var raw map[string]any
	_ = json.Unmarshal(rawEvent, &raw)

	switch eventName {
	case "SessionStart":
		if raw["source"] == "compact" {
			return agent.DeriveResult{Valid: false}
		}
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusIdle,
			Model:  strVal(raw, "modelName"),
		}

	case "UserPromptSubmit":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusRunning,
		}

	case "Notification":
		nt := strVal(raw, "notification_type")
		var status agent.Status
		switch nt {
		case "permission_prompt", "elicitation_dialog":
			status = agent.StatusWaiting
		case "idle_prompt", "auth_success":
			status = agent.StatusIdle
		default:
			return agent.DeriveResult{Valid: false}
		}
		return agent.DeriveResult{
			Valid:  true,
			Status: status,
			Detail: map[string]any{
				"notification_type": nt,
				"message":           raw["message"],
			},
		}

	case "PermissionRequest":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusWaiting,
			Detail: map[string]any{
				"tool_name": raw["tool_name"],
			},
		}

	case "Stop":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusIdle,
			Model:  strVal(raw, "modelName"),
			Detail: map[string]any{
				"last_assistant_message": raw["last_assistant_message"],
			},
		}

	case "StopFailure":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusError,
			Detail: map[string]any{
				"error_details": raw["error_details"],
				"error":         raw["error"],
			},
		}

	case "SessionEnd":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusClear,
		}

	case "SubagentStart", "SubagentStop":
		// Handled by module layer, not status derivation.
		// Return valid=true so the module can process it, but no status change.
		return agent.DeriveResult{
			Valid:  true,
			Detail: map[string]any{"agent_id": raw["agent_id"]},
		}
	}

	return agent.DeriveResult{Valid: false}
}

func strVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
```

- [ ] **Step 6: Write status tests `internal/agent/cc/status_test.go`**

```go
package cc_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/tmux-box/internal/agent"
	cc "github.com/wake/tmux-box/internal/agent/cc"
)

// testDeriveStatus creates a temporary provider to access deriveCCStatus.
// Since deriveCCStatus is unexported, test via the provider's DeriveStatus.
func deriveViaProvider(eventName string, rawEvent map[string]any) agent.DeriveResult {
	p := cc.NewProvider(nil, nil)
	raw, _ := json.Marshal(rawEvent)
	return p.DeriveStatus(eventName, raw)
}

func TestCCDeriveStatus_SessionStart(t *testing.T) {
	r := deriveViaProvider("SessionStart", map[string]any{"source": "startup"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestCCDeriveStatus_SessionStartCompact(t *testing.T) {
	r := deriveViaProvider("SessionStart", map[string]any{"source": "compact"})
	if r.Valid {
		t.Fatal("compact SessionStart should be ignored")
	}
}

func TestCCDeriveStatus_UserPromptSubmit(t *testing.T) {
	r := deriveViaProvider("UserPromptSubmit", map[string]any{})
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
}

func TestCCDeriveStatus_NotificationPermission(t *testing.T) {
	r := deriveViaProvider("Notification", map[string]any{"notification_type": "permission_prompt"})
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
}

func TestCCDeriveStatus_NotificationIdle(t *testing.T) {
	r := deriveViaProvider("Notification", map[string]any{"notification_type": "idle_prompt"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestCCDeriveStatus_PermissionRequest(t *testing.T) {
	r := deriveViaProvider("PermissionRequest", map[string]any{"tool_name": "Bash"})
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
	if r.Detail["tool_name"] != "Bash" {
		t.Fatalf("expected tool_name Bash in detail")
	}
}

func TestCCDeriveStatus_Stop(t *testing.T) {
	r := deriveViaProvider("Stop", map[string]any{"last_assistant_message": "Done"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestCCDeriveStatus_StopFailure(t *testing.T) {
	r := deriveViaProvider("StopFailure", map[string]any{"error": "OOM"})
	if !r.Valid || r.Status != agent.StatusError {
		t.Fatalf("expected error, got %+v", r)
	}
}

func TestCCDeriveStatus_SessionEnd(t *testing.T) {
	r := deriveViaProvider("SessionEnd", map[string]any{})
	if !r.Valid || r.Status != agent.StatusClear {
		t.Fatalf("expected clear, got %+v", r)
	}
}

func TestCCDeriveStatus_SubagentStart(t *testing.T) {
	r := deriveViaProvider("SubagentStart", map[string]any{"agent_id": "abc"})
	if !r.Valid {
		t.Fatal("SubagentStart should be valid")
	}
	if r.Status != "" {
		t.Fatalf("SubagentStart should not set status, got %s", r.Status)
	}
}

func TestCCDeriveStatus_UnknownEvent(t *testing.T) {
	r := deriveViaProvider("FutureEvent", map[string]any{})
	if r.Valid {
		t.Fatal("unknown event should be invalid")
	}
}

func TestCCDeriveStatus_ModelExtraction(t *testing.T) {
	r := deriveViaProvider("SessionStart", map[string]any{"source": "startup", "modelName": "opus-4"})
	if r.Model != "opus-4" {
		t.Fatalf("expected model opus-4, got %s", r.Model)
	}
}
```

Note: This test creates the provider via `cc.NewProvider(nil, nil)` which will be created in the next step. Write the test file now; it won't compile until Task 2 Step 7.

- [ ] **Step 7: Create `internal/agent/cc/provider.go` (minimal, expanded in Task 3)**

```go
package cc

import (
	"encoding/json"

	"github.com/wake/tmux-box/internal/agent"
	"github.com/wake/tmux-box/internal/tmux"
)

// Provider implements agent.AgentProvider for Claude Code.
type Provider struct {
	detector *Detector
	tmuxExec tmux.Executor
}

// NewProvider creates a CC provider. Pass nil for detector/tmuxExec during testing.
func NewProvider(detector *Detector, tmuxExec tmux.Executor) *Provider {
	return &Provider{detector: detector, tmuxExec: tmuxExec}
}

func (p *Provider) Type() string        { return "cc" }
func (p *Provider) DisplayName() string { return "Claude Code" }
func (p *Provider) IconHint() string    { return "cc" }

func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "cc"
	}
	if p.detector == nil {
		return false
	}
	status := p.detector.Detect(ctx.ProcessName)
	return status != StatusNormal && status != StatusNotInCC
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveCCStatus(eventName, rawEvent)
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	if p.detector == nil {
		return false
	}
	status := p.detector.Detect(tmuxTarget)
	return status == StatusCCIdle || status == StatusCCRunning || status == StatusCCWaiting
}
```

- [ ] **Step 8: Run all cc tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design && go test ./internal/agent/cc/...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/agent/cc/
git commit -m "feat: add CC provider with detector, extract, and status derivation"
```

---

## Task 3: CC Provider — Operator + History + HookInstaller

**Files:**
- Create: `internal/agent/cc/operator.go` (move from `internal/module/cc/operator.go`)
- Create: `internal/agent/cc/history.go` (move from `internal/module/cc/history.go`)
- Create: `internal/agent/cc/hooks.go` (move from `internal/module/agent/cc_hooks.go`)
- Create: `internal/agent/cc/interfaces.go` (re-export interfaces for stream module)
- Modify: `internal/agent/cc/provider.go` (add fields + expose services)

- [ ] **Step 1: Move operator to `internal/agent/cc/operator.go`**

Copy `internal/module/cc/operator.go` to `internal/agent/cc/operator.go`. Changes:
- Package: `package cc` (same name, different path)
- Import: `github.com/wake/tmux-box/internal/detect` → remove (types are now local)
- All `detect.StatusInfo` → `StatusInfo` (local type in extract.go)
- All `detect.StatusNormal` etc. → `StatusNormal` etc. (local type in detector.go)
- Receiver type: `CCModule` → `Provider`
- Field access: `m.detector` → `p.detector`, `m.core.Tmux` → `p.tmuxExec`

Key signature changes:
```go
func (p *Provider) Interrupt(ctx context.Context, tmuxTarget string) error
func (p *Provider) Exit(ctx context.Context, tmuxTarget string) error
func (p *Provider) GetStatus(ctx context.Context, tmuxTarget string) (*StatusInfo, error)
func (p *Provider) Launch(ctx context.Context, tmuxTarget string, cmd string) error
```

- [ ] **Step 2: Move history to `internal/agent/cc/history.go`**

Copy `internal/module/cc/history.go` to `internal/agent/cc/history.go`. Changes:
- Package: `package cc`
- Receiver type: `CCModule` → `Provider`
- Import `github.com/wake/tmux-box/internal/history` stays unchanged

```go
func (p *Provider) GetHistory(cwd string, ccSessionID string) ([]map[string]any, error)
```

- [ ] **Step 3: Create `internal/agent/cc/interfaces.go`**

Re-export interfaces that `stream` module needs. This avoids changing stream module's interface types significantly.

```go
package cc

import "context"

// CCDetector interface for use by stream module.
type CCDetector interface {
	Detect(tmuxTarget string) Status
}

// CCOperator interface for use by stream module.
type CCOperator interface {
	Exit(ctx context.Context, tmuxTarget string) error
	Launch(ctx context.Context, tmuxTarget string, cmd string) error
	Interrupt(ctx context.Context, tmuxTarget string) error
	GetStatus(ctx context.Context, tmuxTarget string) (*StatusInfo, error)
}

// CCHistoryProvider interface for use by agent module.
type CCHistoryProvider interface {
	GetHistory(cwd string, ccSessionID string) ([]map[string]any, error)
}

// Registry keys for core.Registry (same keys as before).
const (
	DetectorKey = "cc.detector"
	HistoryKey  = "cc.history"
	OperatorKey = "cc.operator"
)
```

- [ ] **Step 4: Move hooks to `internal/agent/cc/hooks.go`**

Move CC-specific hook installation logic from `internal/module/agent/cc_hooks.go`. Refactor as HookInstaller implementation on Provider.

```go
package cc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/tmux-box/internal/agent"
)

// ccHookEvents lists all CC hook events that tbox registers.
var ccHookEvents = []string{
	"SessionStart", "UserPromptSubmit", "SubagentStart", "SubagentStop",
	"Stop", "StopFailure", "Notification", "PermissionRequest", "SessionEnd",
}

func (p *Provider) InstallHooks(tboxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	return mergeClaudeHooks(settingsPath, tboxPath, false)
}

func (p *Provider) RemoveHooks(tboxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	return mergeClaudeHooks(settingsPath, tboxPath, true)
}

func (p *Provider) CheckHooks() (agent.HookStatus, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return agent.HookStatus{Issues: []string{"cannot find home dir"}}, err
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return agent.HookStatus{
			Installed: false,
			Events:    map[string]agent.HookEventInfo{},
			Issues:    []string{"settings.json not found"},
		}, nil
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return agent.HookStatus{}, fmt.Errorf("parse settings.json: %w", err)
	}

	hooks, _ := settings["hooks"].(map[string]any)
	events := make(map[string]agent.HookEventInfo, len(ccHookEvents))
	var issues []string
	allInstalled := true

	for _, eventName := range ccHookEvents {
		entries, ok := hooks[eventName]
		if !ok {
			events[eventName] = agent.HookEventInfo{Installed: false}
			issues = append(issues, eventName+" hook not installed")
			allInstalled = false
			continue
		}
		command := findTboxCommand(entries)
		events[eventName] = agent.HookEventInfo{Installed: command != "", Command: command}
		if command == "" {
			issues = append(issues, eventName+" hook: tbox command not found")
			allInstalled = false
		}
	}

	return agent.HookStatus{Installed: allInstalled, Events: events, Issues: issues}, nil
}

// mergeClaudeHooks is the existing mergeHooks logic for ~/.claude/settings.json.
// Moved here from cmd/tbox/setup.go with CC-specific event list.
func mergeClaudeHooks(path, tboxPath string, remove bool) error {
	settings := make(map[string]any)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var hooks map[string]any
	if h, ok := settings["hooks"]; ok {
		hooks, _ = h.(map[string]any)
	}
	if hooks == nil {
		hooks = make(map[string]any)
	}

	for _, event := range ccHookEvents {
		entries := toEntrySlice(hooks[event])
		entries = filterOutAnyTbox(entries)
		if !remove {
			entries = append(entries, makeTboxEntry(tboxPath, "cc", event))
		}
		hooks[event] = entries
	}

	settings["hooks"] = hooks

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Shared helpers for hook entry manipulation (used by both CC and Codex).

func makeTboxEntry(tboxPath, agentType, event string) map[string]any {
	return map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": fmt.Sprintf(`"%s" hook --agent %s %s`, tboxPath, agentType, event),
			},
		},
	}
}

func findTboxCommand(entries any) string {
	arr, ok := entries.([]any)
	if !ok {
		return ""
	}
	for _, entry := range arr {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hooksList, ok := entryMap["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hooksList {
			hookMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hookMap["command"].(string)
			if strings.Contains(strings.ReplaceAll(cmd, `"`, ""), "tbox hook") {
				return cmd
			}
		}
	}
	return ""
}

func toEntrySlice(v any) []any {
	if v == nil {
		return []any{}
	}
	if arr, ok := v.([]any); ok {
		return arr
	}
	return []any{}
}

func filterOutAnyTbox(entries []any) []any {
	result := []any{}
	for _, e := range entries {
		if !entryIsTbox(e) {
			result = append(result, e)
		}
	}
	return result
}

func entryIsTbox(entry any) bool {
	m, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	innerHooks, ok := m["hooks"]
	if !ok {
		return false
	}
	arr, ok := innerHooks.([]any)
	if !ok {
		return false
	}
	for _, h := range arr {
		hookObj, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, ok := hookObj["command"].(string)
		if !ok {
			continue
		}
		if isTboxCommand(cmd) {
			return true
		}
	}
	return false
}

func isTboxCommand(cmd string) bool {
	if strings.Contains(cmd, `/tbox" hook`) || strings.HasPrefix(cmd, `"tbox" hook`) {
		return true
	}
	if strings.Contains(cmd, `/tbox hook`) || strings.HasPrefix(cmd, `tbox hook`) {
		return true
	}
	return false
}
```

- [ ] **Step 5: Update provider.go — add operator/history fields + RegisterServices**

```go
// Add to Provider struct:
type Provider struct {
	detector *Detector
	tmuxExec tmux.Executor
	sessions interface{ ListSessions() ([]session.SessionInfo, error) } // for history handler
}

// Add RegisterServices method for agent module to call during Init:
func (p *Provider) RegisterServices(registry *core.ServiceRegistry) {
	registry.Register(DetectorKey, CCDetector(p))
	registry.Register(HistoryKey, CCHistoryProvider(p))
	registry.Register(OperatorKey, CCOperator(p))
}
```

Update `NewProvider` signature:
```go
func NewProvider(detector *Detector, tmuxExec tmux.Executor) *Provider {
	return &Provider{detector: detector, tmuxExec: tmuxExec}
}
```

- [ ] **Step 6: Run all CC tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design && go test ./internal/agent/cc/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agent/cc/
git commit -m "feat: add CC provider operator, history, hooks, and interfaces"
```

---

## Task 4: Codex Provider

**Files:**
- Create: `internal/agent/codex/provider.go`
- Create: `internal/agent/codex/detector.go`
- Create: `internal/agent/codex/hooks.go`
- Create: `internal/agent/codex/status.go`
- Create: `internal/agent/codex/status_test.go`

- [ ] **Step 1: Create `internal/agent/codex/status.go`**

```go
package codex

import (
	"encoding/json"

	"github.com/wake/tmux-box/internal/agent"
)

func deriveCodexStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	switch eventName {
	case "SessionStart":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle}
	case "UserPromptSubmit":
		return agent.DeriveResult{Valid: true, Status: agent.StatusRunning}
	case "Stop":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle}
	}
	return agent.DeriveResult{Valid: false}
}
```

- [ ] **Step 2: Create `internal/agent/codex/detector.go`**

```go
package codex

import (
	"os/exec"
	"strings"
)

// codexProcessNames lists known process names for the Codex CLI.
var codexProcessNames = []string{"codex"}

// isCodexProcess checks if the given process name is a Codex CLI process.
func isCodexProcess(processName string) bool {
	for _, name := range codexProcessNames {
		if processName == name {
			return true
		}
	}
	return false
}

// checkPaneProcess queries tmux for the current command running in the pane.
func checkPaneProcess(tmuxTarget string) string {
	out, err := exec.Command("tmux", "display-message", "-t", tmuxTarget, "-p", "#{pane_current_command}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

- [ ] **Step 3: Create `internal/agent/codex/hooks.go`**

```go
package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/tmux-box/internal/agent"
)

var codexHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"Stop",
}

func (p *Provider) InstallHooks(tboxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	return mergeCodexHooks(hooksPath, tboxPath, false)
}

func (p *Provider) RemoveHooks(tboxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	return mergeCodexHooks(hooksPath, tboxPath, true)
}

func (p *Provider) CheckHooks() (agent.HookStatus, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return agent.HookStatus{Issues: []string{"cannot find home dir"}}, err
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		return agent.HookStatus{
			Installed: false,
			Events:    map[string]agent.HookEventInfo{},
			Issues:    []string{"hooks.json not found"},
		}, nil
	}

	var hooksFile map[string]any
	if err := json.Unmarshal(data, &hooksFile); err != nil {
		return agent.HookStatus{}, fmt.Errorf("parse hooks.json: %w", err)
	}

	hooks, _ := hooksFile["hooks"].(map[string]any)
	events := make(map[string]agent.HookEventInfo, len(codexHookEvents))
	var issues []string
	allInstalled := true

	for _, eventName := range codexHookEvents {
		entries, ok := hooks[eventName]
		if !ok {
			events[eventName] = agent.HookEventInfo{Installed: false}
			issues = append(issues, eventName+" hook not installed")
			allInstalled = false
			continue
		}
		command := findTboxCommandInCodex(entries)
		events[eventName] = agent.HookEventInfo{Installed: command != "", Command: command}
		if command == "" {
			issues = append(issues, eventName+" hook: tbox command not found")
			allInstalled = false
		}
	}

	return agent.HookStatus{Installed: allInstalled, Events: events, Issues: issues}, nil
}

func mergeCodexHooks(path, tboxPath string, remove bool) error {
	hooksFile := make(map[string]any)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &hooksFile); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var hooks map[string]any
	if h, ok := hooksFile["hooks"]; ok {
		hooks, _ = h.(map[string]any)
	}
	if hooks == nil {
		hooks = make(map[string]any)
	}

	for _, event := range codexHookEvents {
		entries := toCodexEntrySlice(hooks[event])
		entries = filterOutTboxCodex(entries)
		if !remove {
			entries = append(entries, map[string]any{
				"type":    "command",
				"command": fmt.Sprintf(`"%s" hook --agent codex %s`, tboxPath, event),
				"timeout": 5,
			})
		}
		hooks[event] = entries
	}

	hooksFile["hooks"] = hooks

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	out, err := json.MarshalIndent(hooksFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func findTboxCommandInCodex(entries any) string {
	arr, ok := entries.([]any)
	if !ok {
		return ""
	}
	for _, entry := range arr {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := m["command"].(string)
		if strings.Contains(cmd, "tbox hook") {
			return cmd
		}
	}
	return ""
}

func toCodexEntrySlice(v any) []any {
	if v == nil {
		return []any{}
	}
	if arr, ok := v.([]any); ok {
		return arr
	}
	return []any{}
}

func filterOutTboxCodex(entries []any) []any {
	var result []any
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			result = append(result, e)
			continue
		}
		cmd, _ := m["command"].(string)
		if !strings.Contains(cmd, "tbox hook") {
			result = append(result, e)
		}
	}
	return result
}
```

- [ ] **Step 4: Create `internal/agent/codex/provider.go`**

```go
package codex

import (
	"encoding/json"

	"github.com/wake/tmux-box/internal/agent"
)

// Provider implements agent.AgentProvider for Codex.
type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Type() string        { return "codex" }
func (p *Provider) DisplayName() string { return "Codex" }
func (p *Provider) IconHint() string    { return "codex" }

func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "codex"
	}
	return isCodexProcess(ctx.ProcessName)
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveCodexStatus(eventName, rawEvent)
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	cmd := checkPaneProcess(tmuxTarget)
	return isCodexProcess(cmd)
}
```

- [ ] **Step 5: Write Codex status tests `internal/agent/codex/status_test.go`**

```go
package codex_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/tmux-box/internal/agent"
	"github.com/wake/tmux-box/internal/agent/codex"
)

func deriveViaProvider(eventName string) agent.DeriveResult {
	p := codex.NewProvider()
	return p.DeriveStatus(eventName, json.RawMessage(`{}`))
}

func TestCodexDeriveStatus_SessionStart(t *testing.T) {
	r := deriveViaProvider("SessionStart")
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestCodexDeriveStatus_UserPromptSubmit(t *testing.T) {
	r := deriveViaProvider("UserPromptSubmit")
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
}

func TestCodexDeriveStatus_Stop(t *testing.T) {
	r := deriveViaProvider("Stop")
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestCodexDeriveStatus_UnknownEvent(t *testing.T) {
	r := deriveViaProvider("Notification")
	if r.Valid {
		t.Fatal("Codex should not handle Notification")
	}
}
```

- [ ] **Step 6: Run Codex tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design && go test ./internal/agent/codex/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agent/codex/
git commit -m "feat: add Codex provider with status, detector, and hook installer"
```

---

## Task 5: Agent Module Refactor

**Files:**
- Modify: `internal/module/agent/module.go`
- Modify: `internal/module/agent/handler.go`
- Delete: `internal/module/agent/cc_hooks.go`

- [ ] **Step 1: Rewrite `internal/module/agent/module.go`**

Add registry, providers init, in-memory state, DB replay on Start, isAlive on snapshot.

```go
package agent

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wake/tmux-box/internal/agent"
	agentcc "github.com/wake/tmux-box/internal/agent/cc"
	"github.com/wake/tmux-box/internal/agent/codex"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/module/session"
	"github.com/wake/tmux-box/internal/store"
)

// Module is the agent hook event module with provider registry.
type Module struct {
	core      *core.Core
	events    *store.AgentEventStore
	sessions  session.SessionProvider
	registry  *agent.Registry
	uploadDir string

	// In-memory state
	mu            sync.Mutex
	currentStatus map[string]agent.Status   // tmuxSession → current status
	subagents     map[string][]string       // tmuxSession → active subagent IDs
}

// New creates a new agent Module.
func New(events *store.AgentEventStore) *Module {
	return &Module{
		events:        events,
		currentStatus: make(map[string]agent.Status),
		subagents:     make(map[string][]string),
	}
}

func (m *Module) Name() string           { return "agent" }
func (m *Module) Dependencies() []string { return []string{"session"} }

func (m *Module) Init(c *core.Core) error {
	m.core = c
	svc, ok := c.Registry.Get(session.RegistryKey)
	if !ok {
		log.Printf("[agent] warning: session provider not found")
		return nil
	}
	m.sessions = svc.(session.SessionProvider)

	// Expose event store for other modules (e.g. session rename).
	c.Registry.Register("agent.events", m.events)

	if m.uploadDir == "" {
		home, _ := os.UserHomeDir()
		m.uploadDir = filepath.Join(home, "tmp", "tbox-upload")
	}

	// Initialize provider registry
	m.registry = agent.NewRegistry()

	// CC provider
	ccDetector := agentcc.NewDetector(c.Tmux, c.Cfg.Detect.CCCommands)
	ccProvider := agentcc.NewProvider(ccDetector, c.Tmux)
	ccProvider.RegisterServices(c.Registry)
	m.registry.Register(ccProvider)

	// Listen for config changes to update CC detector
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		cmds := c.Cfg.Detect.CCCommands
		c.CfgMu.RUnlock()
		ccDetector.UpdateCommands(cmds)
	})

	// Codex provider
	m.registry.Register(codex.NewProvider())

	// Expose registry for other modules
	c.Registry.Register("agent.registry", m.registry)

	return nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agent/event", m.handleEvent)
	mux.HandleFunc("GET /api/hooks/{agent}/status", m.handleHookStatus)
	mux.HandleFunc("POST /api/hooks/{agent}/setup", m.handleHookSetup)
	mux.HandleFunc("POST /api/agent/check-alive/{session}", m.handleCheckAlive)

	// History (delegates to provider)
	mux.HandleFunc("GET /api/sessions/{code}/history", m.handleHistory)

	// Upload (unchanged)
	mux.HandleFunc("POST /api/agent/upload", m.handleUpload)
	mux.HandleFunc("GET /api/upload/stats", m.handleUploadStats)
	mux.HandleFunc("GET /api/upload/files", m.handleUploadFiles)
	mux.HandleFunc("DELETE /api/upload/files/{session}/{filename}", m.handleDeleteUploadFile)
	mux.HandleFunc("DELETE /api/upload/files/{session}", m.handleDeleteUploadSession)
	mux.HandleFunc("DELETE /api/upload/files", m.handleDeleteAllUploads)
}

func (m *Module) Start(_ context.Context) error {
	// Replay last events from DB to rebuild in-memory state
	m.replayFromDB()

	m.core.Events.OnSubscribe(func(sub *core.EventSubscriber) {
		m.sendSnapshot(sub)
		// Async isAlive check
		go m.checkAliveAll(sub)
	})

	log.Println("[agent] hook event endpoint registered")
	return nil
}

func (m *Module) Stop(_ context.Context) error { return nil }

// replayFromDB rebuilds in-memory currentStatus from DB on daemon startup.
func (m *Module) replayFromDB() {
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("[agent] replay: %v", err)
		return
	}
	for _, ev := range all {
		provider, ok := m.registry.Get(ev.AgentType)
		if !ok {
			continue
		}
		result := provider.DeriveStatus(ev.EventName, ev.RawEvent)
		if result.Valid && result.Status != "" {
			m.mu.Lock()
			m.currentStatus[ev.TmuxSession] = result.Status
			m.mu.Unlock()
		}
	}
}

// sendSnapshot sends latest normalized events for all known sessions.
func (m *Module) sendSnapshot(sub *core.EventSubscriber) {
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("[agent] snapshot: %v", err)
		return
	}
	if len(all) == 0 {
		return
	}

	sessions, err := m.sessions.ListSessions()
	if err != nil {
		log.Printf("[agent] snapshot sessions: %v", err)
		return
	}
	nameToCode := make(map[string]string, len(sessions))
	for _, s := range sessions {
		nameToCode[s.Name] = s.Code
	}

	for _, ev := range all {
		code, ok := nameToCode[ev.TmuxSession]
		if !ok {
			continue
		}
		provider, _ := m.registry.Get(ev.AgentType)
		normalized := m.buildNormalized(ev.TmuxSession, ev.EventName, ev.RawEvent, ev.AgentType, ev.BroadcastTs, provider)
		payload, _ := json.Marshal(normalized)
		event := core.HostEvent{Type: "hook", Session: code, Value: string(payload)}
		data, _ := json.Marshal(event)
		sub.Send(data)
	}
}

// checkAliveAll runs isAlive for all sessions and broadcasts StatusClear for dead ones.
// Runs async with a 5-second timeout budget.
func (m *Module) checkAliveAll(sub *core.EventSubscriber) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	all, err := m.events.ListAll()
	if err != nil {
		return
	}

	sessions, err := m.sessions.ListSessions()
	if err != nil {
		return
	}
	nameToCode := make(map[string]string, len(sessions))
	codeToTmux := make(map[string]string, len(sessions))
	for _, s := range sessions {
		nameToCode[s.Name] = s.Code
		codeToTmux[s.Code] = s.Name + ":"
	}

	for _, ev := range all {
		select {
		case <-ctx.Done():
			return
		default:
		}

		code, ok := nameToCode[ev.TmuxSession]
		if !ok {
			continue
		}
		provider, ok := m.registry.Get(ev.AgentType)
		if !ok {
			continue
		}

		tmuxTarget := ev.TmuxSession + ":"
		if !provider.IsAlive(tmuxTarget) {
			m.mu.Lock()
			delete(m.currentStatus, ev.TmuxSession)
			delete(m.subagents, ev.TmuxSession)
			m.mu.Unlock()

			_ = m.events.Delete(ev.TmuxSession)

			normalized := agent.NormalizedEvent{
				AgentType:    ev.AgentType,
				Status:       string(agent.StatusClear),
				RawEventName: "isAlive:dead",
				BroadcastTs:  time.Now().UnixNano(),
			}
			payload, _ := json.Marshal(normalized)
			m.core.Events.Broadcast(code, "hook", string(payload))
		}
	}
}
```

- [ ] **Step 2: Rewrite `internal/module/agent/handler.go`**

```go
package agent

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wake/tmux-box/internal/agent"
)

// EventRequest is the JSON body expected by POST /api/agent/event.
type EventRequest struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
	AgentType   string          `json:"agent_type"`
}

func (m *Module) handleEvent(w http.ResponseWriter, r *http.Request) {
	var req EventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.TmuxSession == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	broadcastTs := time.Now().UnixNano()

	// Find provider
	provider, _ := m.registry.Get(req.AgentType)

	// Derive status via provider
	var result agent.DeriveResult
	if provider != nil {
		result = provider.DeriveStatus(req.EventName, req.RawEvent)
	}

	// Handle subagent events (transient — broadcast only, don't persist)
	if req.EventName == "SubagentStart" || req.EventName == "SubagentStop" {
		m.handleSubagentEvent(req.TmuxSession, req.EventName, result)
		normalized := m.buildNormalized(req.TmuxSession, req.EventName, req.RawEvent, req.AgentType, broadcastTs, provider)
		m.broadcastToSession(req.TmuxSession, normalized)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// Error guard: when in error state, only whitelisted events can clear it
	if result.Valid && result.Status != "" && result.Status != agent.StatusError {
		m.mu.Lock()
		current := m.currentStatus[req.TmuxSession]
		m.mu.Unlock()
		if current == agent.StatusError {
			canClear := req.EventName == "UserPromptSubmit" ||
				req.EventName == "SessionStart" ||
				req.EventName == "Stop"
			if !canClear {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
				return
			}
		}
	}

	// Store raw event to DB
	if err := m.events.Set(req.TmuxSession, req.EventName, req.RawEvent, req.AgentType, broadcastTs); err != nil {
		log.Printf("[agent] store event: %v", err)
		http.Error(w, `{"error":"store failed"}`, http.StatusInternalServerError)
		return
	}

	// Update in-memory state
	if result.Valid && result.Status != "" {
		m.mu.Lock()
		if result.Status == agent.StatusClear {
			delete(m.currentStatus, req.TmuxSession)
			delete(m.subagents, req.TmuxSession)
		} else {
			m.currentStatus[req.TmuxSession] = result.Status
		}
		m.mu.Unlock()
	}

	// Clear subagents on non-compact SessionStart
	if req.EventName == "SessionStart" && result.Valid {
		m.mu.Lock()
		delete(m.subagents, req.TmuxSession)
		m.mu.Unlock()
	}

	// Build and broadcast normalized event
	normalized := m.buildNormalized(req.TmuxSession, req.EventName, req.RawEvent, req.AgentType, broadcastTs, provider)
	m.broadcastToSession(req.TmuxSession, normalized)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (m *Module) handleSubagentEvent(tmuxSession, eventName string, result agent.DeriveResult) {
	agentID, _ := result.Detail["agent_id"].(string)
	if agentID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if eventName == "SubagentStart" {
		current := m.subagents[tmuxSession]
		for _, id := range current {
			if id == agentID {
				return
			}
		}
		m.subagents[tmuxSession] = append(current, agentID)
	} else { // SubagentStop
		current := m.subagents[tmuxSession]
		filtered := make([]string, 0, len(current))
		for _, id := range current {
			if id != agentID {
				filtered = append(filtered, id)
			}
		}
		if len(filtered) == 0 {
			delete(m.subagents, tmuxSession)
		} else {
			m.subagents[tmuxSession] = filtered
		}
	}
}

func (m *Module) buildNormalized(tmuxSession, eventName string, rawEvent json.RawMessage, agentType string, broadcastTs int64, provider agent.AgentProvider) agent.NormalizedEvent {
	var result agent.DeriveResult
	if provider != nil {
		result = provider.DeriveStatus(eventName, rawEvent)
	}

	m.mu.Lock()
	subs := make([]string, len(m.subagents[tmuxSession]))
	copy(subs, m.subagents[tmuxSession])
	m.mu.Unlock()

	normalized := agent.NormalizedEvent{
		AgentType:    agentType,
		Status:       string(result.Status),
		Model:        result.Model,
		RawEventName: eventName,
		BroadcastTs:  broadcastTs,
		Detail:       result.Detail,
	}
	if len(subs) > 0 {
		normalized.Subagents = subs
	}
	return normalized
}

func (m *Module) broadcastToSession(tmuxSession string, normalized agent.NormalizedEvent) {
	if m.core == nil {
		return
	}
	code := m.resolveSessionCode(tmuxSession)
	if code == "" {
		return
	}
	payload, _ := json.Marshal(normalized)
	m.core.Events.Broadcast(code, "hook", string(payload))
}

func (m *Module) resolveSessionCode(tmuxName string) string {
	if m.sessions == nil {
		return ""
	}
	sessions, err := m.sessions.ListSessions()
	if err != nil {
		log.Printf("[agent] list sessions: %v", err)
		return ""
	}
	for _, s := range sessions {
		if s.Name == tmuxName {
			return s.Code
		}
	}
	return ""
}

// handleHookStatus handles GET /api/hooks/{agent}/status.
func (m *Module) handleHookStatus(w http.ResponseWriter, r *http.Request) {
	agentType := r.PathValue("agent")
	provider, ok := m.registry.Get(agentType)
	if !ok {
		http.Error(w, `{"error":"unknown agent type"}`, http.StatusNotFound)
		return
	}
	installer, ok := provider.(agent.HookInstaller)
	if !ok {
		http.Error(w, `{"error":"agent does not support hooks"}`, http.StatusNotFound)
		return
	}
	status, err := installer.CheckHooks()
	if err != nil {
		http.Error(w, `{"error":"check failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleHookSetup handles POST /api/hooks/{agent}/setup.
func (m *Module) handleHookSetup(w http.ResponseWriter, r *http.Request) {
	agentType := r.PathValue("agent")
	provider, ok := m.registry.Get(agentType)
	if !ok {
		http.Error(w, `{"error":"unknown agent type"}`, http.StatusNotFound)
		return
	}
	installer, ok := provider.(agent.HookInstaller)
	if !ok {
		http.Error(w, `{"error":"agent does not support hooks"}`, http.StatusNotFound)
		return
	}

	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	tboxPath, err := os.Executable()
	if err != nil {
		http.Error(w, `{"error":"cannot find tbox binary"}`, http.StatusInternalServerError)
		return
	}
	tboxPath, _ = filepath.EvalSymlinks(tboxPath)

	switch req.Action {
	case "install":
		if err := installer.InstallHooks(tboxPath); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "setup failed", "detail": err.Error()})
			return
		}
	case "remove":
		if err := installer.RemoveHooks(tboxPath); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "remove failed", "detail": err.Error()})
			return
		}
	default:
		http.Error(w, `{"error":"action must be install or remove"}`, http.StatusBadRequest)
		return
	}

	// Return updated status
	m.handleHookStatus(w, r)
}

// handleHistory handles GET /api/sessions/{code}/history.
func (m *Module) handleHistory(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if m.sessions == nil {
		http.Error(w, `{"error":"no session provider"}`, http.StatusInternalServerError)
		return
	}
	sessions, err := m.sessions.ListSessions()
	if err != nil {
		http.Error(w, `{"error":"list sessions"}`, http.StatusInternalServerError)
		return
	}
	var sess *session.SessionInfo
	for _, s := range sessions {
		if s.Code == code {
			sess = &s
			break
		}
	}
	if sess == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	// Find the right provider for this session's agent type
	ev, _ := m.events.Get(sess.Name)
	if ev == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	provider, ok := m.registry.Get(ev.AgentType)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	histProvider, ok := provider.(agent.HistoryProvider)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	history, err := histProvider.GetHistory(sess.Cwd, sess.CCSessionID)
	if err != nil {
		log.Printf("[agent] history: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// handleCheckAlive handles POST /api/agent/check-alive/{session}.
func (m *Module) handleCheckAlive(w http.ResponseWriter, r *http.Request) {
	sessionCode := r.PathValue("session")

	sessions, err := m.sessions.ListSessions()
	if err != nil {
		http.Error(w, `{"error":"list sessions"}`, http.StatusInternalServerError)
		return
	}
	var tmuxName string
	for _, s := range sessions {
		if s.Code == sessionCode {
			tmuxName = s.Name
			break
		}
	}
	if tmuxName == "" {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	ev, _ := m.events.Get(tmuxName)
	if ev == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"alive": false, "reason": "no event"})
		return
	}

	provider, ok := m.registry.Get(ev.AgentType)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"alive": false, "reason": "unknown agent"})
		return
	}

	alive := provider.IsAlive(tmuxName + ":")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"alive": alive})
}
```

- [ ] **Step 3: Delete `internal/module/agent/cc_hooks.go`**

```bash
rm internal/module/agent/cc_hooks.go
```

- [ ] **Step 4: Run agent module tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design && go test ./internal/module/agent/...`
Expected: PASS (some tests may need updating — fix compilation errors)

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/
git commit -m "refactor: agent module with provider registry and normalized events"
```

---

## Task 6: CLI Changes

**Files:**
- Modify: `cmd/tbox/setup.go`
- Modify: `cmd/tbox/hook.go`
- Modify: `cmd/tbox/main.go`

- [ ] **Step 1: Update `cmd/tbox/hook.go` — make `--agent` required**

Change lines 42-45: after parsing, if `agentType` is empty, print error and exit.

```go
	if agentType == "" {
		fmt.Fprintf(os.Stderr, "tbox hook: --agent flag is required\n")
		os.Exit(1)
	}
```

- [ ] **Step 2: Rewrite `cmd/tbox/setup.go` — add `--agent` flag, delegate to providers**

Replace `runSetup` to parse `--agent` flag and delegate to the right provider's install/remove via HTTP API:

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/wake/tmux-box/internal/config"
)

func runSetup(args []string) {
	var agentType string
	remove := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 < len(args) {
				agentType = args[i+1]
				i++
			}
		case "--remove":
			remove = true
		}
	}

	if agentType == "" {
		fmt.Fprintf(os.Stderr, "tbox setup: --agent flag is required (e.g. --agent cc, --agent codex)\n")
		os.Exit(1)
	}

	cfg, err := config.Load("")
	var baseURL, token string
	if err != nil {
		baseURL = "http://127.0.0.1:7860"
	} else {
		baseURL = fmt.Sprintf("http://%s:%d", cfg.Bind, cfg.Port)
		token = cfg.Token
	}

	action := "install"
	if remove {
		action = "remove"
	}

	body, _ := json.Marshal(map[string]string{"action": action})
	url := fmt.Sprintf("%s/api/hooks/%s/setup", baseURL, agentType)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: cannot reach daemon: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "setup: failed (%d): %s\n", resp.StatusCode, string(respBody))
		os.Exit(1)
	}

	if remove {
		fmt.Printf("tbox hooks for %s removed\n", agentType)
	} else {
		fmt.Printf("tbox hooks for %s installed\n", agentType)
	}
}
```

Remove the old helper functions (`mergeHooks`, `makeTboxEntry`, `toEntrySlice`, `filterOutAnyTbox`, `entryIsTbox`, `isTboxCommand`, `hookEvents` var) — they've been moved to the CC provider.

- [ ] **Step 3: Update `cmd/tbox/main.go` — remove cc module**

Remove the import of `github.com/wake/tmux-box/internal/module/cc` and the line `c.AddModule(cc.New())`. The agent module now handles CC provider initialization internally.

```go
// Remove:
// import "github.com/wake/tmux-box/internal/module/cc"
// c.AddModule(cc.New())
```

- [ ] **Step 4: Verify build**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design && go build ./cmd/tbox/`
Expected: Build succeeds (may fail if stream module still imports old paths — that's Task 7)

- [ ] **Step 5: Commit**

```bash
git add cmd/tbox/
git commit -m "refactor: CLI setup/hook with --agent flag, remove cc module from main"
```

---

## Task 7: Stream Module Migration + Delete Old Modules

**Files:**
- Modify: `internal/module/stream/module.go`
- Modify: `internal/module/stream/orchestrator.go`
- Modify: `internal/module/stream/orchestrator_test.go`
- Delete: `internal/module/cc/` (entire directory)
- Delete: `internal/detect/` (entire directory)

- [ ] **Step 1: Update `internal/module/stream/module.go`**

Change imports from `internal/module/cc` to `internal/agent/cc`:

```go
import (
	// ...
	agentcc "github.com/wake/tmux-box/internal/agent/cc"
	// Remove: "github.com/wake/tmux-box/internal/module/cc"
)

type StreamModule struct {
	// ...
	ccOps    agentcc.CCOperator  // was cc.CCOperator
	ccDetect agentcc.CCDetector  // was cc.CCDetector
	// ...
}

func (m *StreamModule) Dependencies() []string { return []string{"session", "agent"} }
// Changed from {"session", "cc"} → {"session", "agent"} because cc provider
// is now initialized inside agent module.

func (m *StreamModule) Init(c *core.Core) error {
	// ...
	m.ccOps = c.Registry.MustGet(agentcc.OperatorKey).(agentcc.CCOperator)
	m.ccDetect = c.Registry.MustGet(agentcc.DetectorKey).(agentcc.CCDetector)
	// ...
}
```

- [ ] **Step 2: Update `internal/module/stream/orchestrator.go`**

Change imports from `internal/detect` to `internal/agent/cc`:

```go
import (
	// ...
	agentcc "github.com/wake/tmux-box/internal/agent/cc"
	// Remove: "github.com/wake/tmux-box/internal/detect"
)
```

Replace all occurrences:
- `detect.StatusNormal` → `agentcc.StatusNormal`
- `detect.StatusNotInCC` → `agentcc.StatusNotInCC`
- `detect.StatusCCIdle` → `agentcc.StatusCCIdle`
- `detect.StatusCCRunning` → `agentcc.StatusCCRunning`
- `detect.StatusCCWaiting` → `agentcc.StatusCCWaiting`
- `detect.StatusInfo` → `agentcc.StatusInfo`

- [ ] **Step 3: Update `internal/module/stream/orchestrator_test.go`**

Same import replacement as Step 2. Replace all `detect.Status*` → `agentcc.Status*` and `detect.StatusInfo` → `agentcc.StatusInfo`.

- [ ] **Step 4: Delete old modules**

```bash
rm -rf internal/module/cc/
rm -rf internal/detect/
```

- [ ] **Step 5: Verify full build + tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: migrate stream module imports, delete old cc and detect packages"
```

---

## Task 8: Frontend — useAgentStore Rewrite

**Files:**
- Modify: `spa/src/stores/useAgentStore.ts`
- Modify: `spa/src/stores/useAgentStore.test.ts`

- [ ] **Step 1: Rewrite `spa/src/stores/useAgentStore.ts`**

```typescript
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { getActiveSessionInfo } from '../lib/active-session'
import { compositeKey } from '../lib/composite-key'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

export type AgentStatus = 'running' | 'waiting' | 'idle' | 'error'
export type TabIndicatorStyle = 'overlay' | 'replace' | 'inline'

/** Normalized event from backend (replaces AgentHookEvent). */
export interface NormalizedEvent {
  agent_type: string
  status: string             // running | waiting | idle | error | clear
  model?: string
  subagents?: string[]
  raw_event_name: string
  broadcast_ts: number
  detail?: Record<string, unknown>
}

interface AgentState {
  // Backend-derived state
  statuses: Record<string, AgentStatus>
  agentTypes: Record<string, string>
  models: Record<string, string>
  subagents: Record<string, string[]>
  lastEvents: Record<string, NormalizedEvent>  // for notification dispatcher

  // UI state
  unread: Record<string, boolean>
  tabIndicatorStyle: TabIndicatorStyle

  // Actions
  handleNormalizedEvent: (hostId: string, sessionCode: string, event: NormalizedEvent) => void
  markRead: (hostId: string, sessionCode: string) => void
  removeHost: (hostId: string) => void
  setTabIndicatorStyle: (style: TabIndicatorStyle) => void
}

export const useAgentStore = create<AgentState>()(
  persist(
    (set, get) => ({
      statuses: {},
      agentTypes: {},
      models: {},
      subagents: {},
      lastEvents: {},
      unread: {},
      tabIndicatorStyle: 'overlay' as TabIndicatorStyle,

      handleNormalizedEvent: (hostId, sessionCode, event) => {
        const key = compositeKey(hostId, sessionCode)

        if (event.status === 'clear') {
          set((s) => {
            const filterOut = <T,>(rec: Record<string, T>): Record<string, T> => {
              // eslint-disable-next-line @typescript-eslint/no-unused-vars
              const { [key]: _, ...rest } = rec
              return rest
            }
            return {
              statuses: filterOut(s.statuses),
              agentTypes: filterOut(s.agentTypes),
              models: filterOut(s.models),
              subagents: filterOut(s.subagents),
              lastEvents: filterOut(s.lastEvents),
              unread: filterOut(s.unread),
            }
          })
          return
        }

        // Store last event (for notification dispatcher)
        set((s) => ({ lastEvents: { ...s.lastEvents, [key]: event } }))

        // Store agent type
        if (event.agent_type) {
          set((s) => ({ agentTypes: { ...s.agentTypes, [key]: event.agent_type } }))
        }

        // Store model (persist across events)
        if (event.model) {
          set((s) => ({ models: { ...s.models, [key]: event.model! } }))
        }

        // Store subagents
        if (event.subagents) {
          set((s) => ({
            subagents: event.subagents!.length > 0
              ? { ...s.subagents, [key]: event.subagents! }
              : (() => { const { [key]: _, ...rest } = s.subagents; return rest })(),
          }))
        }

        // Store status (skip events with no status, e.g. SubagentStart/Stop)
        const status = event.status as AgentStatus
        if (status && status !== '') {
          set((s) => ({ statuses: { ...s.statuses, [key]: status } }))

          // Mark unread when not focused
          const isActionable = status === 'waiting' || status === 'error' ||
            (status === 'idle' && event.raw_event_name !== 'Notification')
          const activeInfo = getActiveSessionInfo()
          const activeKey = activeInfo ? compositeKey(activeInfo.hostId, activeInfo.sessionCode) : ''
          if (isActionable && activeKey !== key) {
            set((s) => ({ unread: { ...s.unread, [key]: true } }))
          }
        }
      },

      markRead: (hostId, sessionCode) => set((s) => {
        const key = compositeKey(hostId, sessionCode)
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        const { [key]: _, ...rest } = s.unread
        return { unread: rest }
      }),

      removeHost: (hostId) => set((s) => {
        const prefix = `${hostId}:`
        const filterKeys = <T,>(record: Record<string, T>): Record<string, T> => {
          const result: Record<string, T> = {}
          for (const [k, v] of Object.entries(record)) {
            if (!k.startsWith(prefix)) result[k] = v
          }
          return result
        }
        return {
          statuses: filterKeys(s.statuses),
          agentTypes: filterKeys(s.agentTypes),
          models: filterKeys(s.models),
          subagents: filterKeys(s.subagents),
          lastEvents: filterKeys(s.lastEvents),
          unread: filterKeys(s.unread),
        }
      }),

      setTabIndicatorStyle: (style) => set({ tabIndicatorStyle: style }),
    }),
    {
      name: STORAGE_KEYS.AGENT,
      storage: purdexStorage,
      version: 2,  // bumped from 1
      partialize: (state) => ({ tabIndicatorStyle: state.tabIndicatorStyle }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.AGENT, useAgentStore)
```

- [ ] **Step 2: Rewrite `spa/src/stores/useAgentStore.test.ts`**

Update all tests to use `NormalizedEvent` instead of `AgentHookEvent`, and call `handleNormalizedEvent` instead of `handleHookEvent`. Tests should verify the store correctly stores status, agentType, model, subagents, and unread from pre-derived events.

Key test cases:
- Running status from backend → stored correctly
- Waiting status → marks unread
- Clear status → removes all state for session
- Model persists across events
- Subagent tracking from normalized events
- removeHost clears all host data

- [ ] **Step 3: Run frontend tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design/spa && npx vitest run src/stores/useAgentStore.test.ts`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/stores/useAgentStore.ts spa/src/stores/useAgentStore.test.ts
git commit -m "refactor: useAgentStore for normalized events from backend"
```

---

## Task 9: Frontend — WS Event Parsing + Notifications

**Files:**
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`
- Modify: `spa/src/hooks/useNotificationDispatcher.ts`
- Modify: `spa/src/lib/notification-content.ts`
- Modify: `spa/src/components/StatusBar.tsx`

- [ ] **Step 1: Update `spa/src/hooks/useMultiHostEventWs.ts`**

Change the hook event parsing section (~line 125) to use `NormalizedEvent`:

```typescript
// Before:
// import { useAgentStore } from '../stores/useAgentStore'
// ...
// if (event.type === 'hook') {
//   const hookData = JSON.parse(event.value)
//   useAgentStore.getState().handleHookEvent(hostId, event.session, hookData)
// }

// After:
import { useAgentStore, type NormalizedEvent } from '../stores/useAgentStore'
// ...
if (event.type === 'hook') {
  const normalized: NormalizedEvent = JSON.parse(event.value)
  useAgentStore.getState().handleNormalizedEvent(hostId, event.session, normalized)
}
```

- [ ] **Step 2: Update `spa/src/hooks/useNotificationDispatcher.ts`**

Replace `deriveStatus` usage with pre-derived status from the store. Replace `events` subscription with `lastEvents`.

Key changes:
- Remove import of `deriveStatus`
- Subscribe to `lastEvents` instead of `events`
- Use `event.status` instead of `deriveStatus(event.event_name, event.raw_event)`
- Use `event.detail` instead of `event.raw_event` for `buildNotificationContent`
- Use `event.raw_event_name` instead of `event.event_name`

```typescript
// Line 103 change:
// const derived = deriveStatus(event.event_name, event.raw_event)
const derived = event.status

// Line 110 change:
// shouldNotify({ derived, eventName: event.event_name, ... })
shouldNotify({ derived, eventName: event.raw_event_name, ... })

// Line 117 change:
// buildNotificationContent(event.event_name, event.raw_event, sessionName, ...)
buildNotificationContent(event.raw_event_name, event.detail ?? {}, sessionName, ...)
```

Also update `handleNotificationClick` (line 210):
```typescript
// const event = useAgentStore.getState().events[ck]
const event = useAgentStore.getState().lastEvents[ck]
const agentSettings = useNotificationSettingsStore.getState().getSettingsForAgent(event?.agent_type || '')
```

- [ ] **Step 3: Update `spa/src/lib/notification-content.ts`**

The function signature stays the same — it already accepts `rawEvent: Record<string, unknown>`. The caller now passes `event.detail` instead of `event.raw_event`. No changes needed in this file since `detail` contains the same field names (`notification_type`, `tool_name`, `last_assistant_message`, etc.).

- [ ] **Step 4: Update `spa/src/components/StatusBar.tsx`**

Change model reading from `events[key].raw_event.modelName` to `models[key]`:

```typescript
// Before:
// const agentLabel = useAgentStore((s) => agentCk ? s.models[agentCk] ?? null : null)
// This already reads from models map, so likely no change needed.
// Verify and fix if it reads from events.
```

- [ ] **Step 5: Run frontend tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design/spa && npx vitest run`
Expected: PASS (fix any remaining compilation errors from removed types)

- [ ] **Step 6: Commit**

```bash
git add spa/src/hooks/ spa/src/lib/notification-content.ts spa/src/components/StatusBar.tsx
git commit -m "refactor: frontend WS parsing and notifications for normalized events"
```

---

## Task 10: Frontend — Agent Icons + Tab + Settings

**Files:**
- Create: `spa/src/lib/agent-icons.ts`
- Modify: `spa/src/components/SessionPanel.tsx`
- Modify: `spa/src/components/SortableTab.tsx`
- Modify: `spa/src/components/settings/AgentSection.tsx`

- [ ] **Step 1: Create `spa/src/lib/agent-icons.ts`**

```typescript
import { Lightning, Code, Terminal } from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'

export const AGENT_ICONS: Record<string, Icon> = {
  cc: Lightning,
  codex: Code,
}

export const AGENT_NAMES: Record<string, string> = {
  cc: 'Claude Code',
  codex: 'Codex',
}

export const DEFAULT_SESSION_ICON = Terminal
```

- [ ] **Step 2: Update `spa/src/components/SessionPanel.tsx`**

Update `SessionIcon` to show agent icon when an agent is active:

```typescript
import { useAgentStore } from '../stores/useAgentStore'
import { AGENT_ICONS, DEFAULT_SESSION_ICON } from '../lib/agent-icons'
import { compositeKey } from '../lib/composite-key'

function SessionIcon({ mode, code, hostId }: { mode: string; code: string; hostId: string }) {
  const ck = compositeKey(hostId, code)
  const agentType = useAgentStore((s) => s.agentTypes[ck])
  const iconSize = 16

  if (agentType) {
    const AgentIcon = AGENT_ICONS[agentType] ?? DEFAULT_SESSION_ICON
    return <AgentIcon size={iconSize} weight="fill" className="text-text-secondary" />
  }

  // No agent — show mode icon
  switch (mode) {
    case 'stream':
      return <Lightning size={iconSize} weight="fill" className="text-blue-400" />
    default:
      return <Terminal size={iconSize} className="text-text-secondary" />
  }
}
```

- [ ] **Step 3: Update `spa/src/components/SortableTab.tsx`**

The tab already reads `agentStatus` from the store. Add `agentType` for icon rendering:

```typescript
const agentType = useAgentStore((s) => ck ? s.agentTypes[ck] : undefined)
```

Pass `agentType` to `TabStatusDot` if needed for icon rendering.

- [ ] **Step 4: Update `spa/src/components/settings/AgentSection.tsx`**

Add per-agent hook toggles that call the parameterized API:

```typescript
import { AGENT_NAMES } from '../../lib/agent-icons'

const AGENTS = ['cc', 'codex'] as const

function AgentSection() {
  return (
    <div>
      <h3>Agent Hooks</h3>
      {AGENTS.map((agentType) => (
        <HookToggle
          key={agentType}
          agentType={agentType}
          label={AGENT_NAMES[agentType]}
          statusUrl={`/api/hooks/${agentType}/status`}
          setupUrl={`/api/hooks/${agentType}/setup`}
        />
      ))}
    </div>
  )
}
```

The `HookToggle` component uses the existing hook status/setup pattern but parameterized by agent type. Adapt from the existing hook UI in the AgentSection or related component (e.g. `HookModuleCard`).

- [ ] **Step 5: Run all frontend tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design/spa && npx vitest run`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/agent-icons.ts spa/src/components/
git commit -m "feat: agent icons, tab indicators, and per-agent hook settings UI"
```

---

## Task 11: Integration Verification

- [ ] **Step 1: Full Go build**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design && go build ./...`
Expected: No errors

- [ ] **Step 2: Full Go tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design && go test ./...`
Expected: All PASS

- [ ] **Step 3: Frontend lint**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design/spa && pnpm run lint`
Expected: No errors

- [ ] **Step 4: Frontend tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design/spa && npx vitest run`
Expected: All PASS

- [ ] **Step 5: Frontend build**

Run: `cd /Users/wake/Workspace/wake/tmux-box/.claude/worktrees/agent-module-design/spa && pnpm run build`
Expected: Build succeeds

- [ ] **Step 6: Verify old imports are gone**

Run: `grep -r 'internal/module/cc' --include='*.go' . | grep -v '.claude/worktrees'` and `grep -r 'internal/detect' --include='*.go' . | grep -v '.claude/worktrees' | grep -v 'internal/agent/cc'`
Expected: No matches (all old import paths are gone)

- [ ] **Step 7: Verify deleted directories are gone**

Run: `ls internal/module/cc/ 2>&1` and `ls internal/detect/ 2>&1`
Expected: "No such file or directory" for both

- [ ] **Step 8: Final commit (if any fixes needed)**

```bash
git add -A
git commit -m "fix: integration verification fixes"
```
