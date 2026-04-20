# Probe Chain Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the monolithic CC Detector with a 3-layer Probe Chain (Liveness → Activity → Readiness) that fixes the stuck yellow light problem and unifies CC/Codex process detection.

**Architecture:** New `internal/agent/probe/` package with `Prober` struct orchestrating three layers. Liveness checks process names + optional content fallback. Activity watches screen changes via hash diff with callback notification. Readiness delegates to per-provider `ReadinessChecker` implementations. Agent module integrates via mutex-serialized `activeWatchers` map for hook-event-trumps-activity semantics.

**Tech Stack:** Go / `tmux.Executor` / `hash/fnv` / `sync.Mutex` / `context`

**Spec:** `docs/superpowers/specs/2026-04-13-probe-chain-design.md`

---

## File Structure

### New Files

| File | Responsibility |
|------|---------------|
| `internal/agent/probe/probe.go` | `Prober` struct, `New()`, registration methods, interfaces (`ContentMatcher`, `ReadinessChecker`, `ActivityCallback`) |
| `internal/agent/probe/liveness.go` | `IsAliveFor()` — process name matching + child process + content fallback |
| `internal/agent/probe/liveness_test.go` | Liveness layer tests |
| `internal/agent/probe/activity.go` | `StartWatch()`, `StopWatch()`, `StopAllWatches()` — screen hash diff goroutine |
| `internal/agent/probe/activity_test.go` | Activity layer tests |
| `internal/agent/probe/readiness.go` | `CheckReadiness()` — delegates to registered `ReadinessChecker` |
| `internal/agent/probe/readiness_test.go` | Readiness layer tests |
| `internal/agent/cc/readiness.go` | CC `ReadinessChecker` implementation (from `detectCCSubState`) |
| `internal/agent/cc/content_matcher.go` | CC `ContentMatcher` implementation (from `looksLikeCC`) |
| `internal/agent/codex/readiness.go` | Codex `ReadinessChecker` implementation |

### Modified Files

| File | Changes |
|------|---------|
| `internal/agent/cc/provider.go` | Replace `*Detector` with `*probe.Prober`; rewrite `IsAlive`, `Claim` |
| `internal/agent/cc/operator.go` | `Interrupt()` uses `CheckReadiness`, `Exit()` uses `!IsAliveFor` |
| `internal/agent/codex/provider.go` | `IsAlive` delegates to prober |
| `internal/module/agent/module.go` | Init prober, add `activeWatchers`, `Stop()` cleanup, `OnConfigChange` update |
| `internal/module/agent/handler.go` | `handleEvent` integrates Activity watch start/stop; `handleCheckAlive` uses prober |
| `internal/module/stream/module.go` | Replace `ccDetect` with `*probe.Prober` |
| `internal/module/stream/orchestrator.go` | Replace 4× `ccDetect.Detect()` with `IsAliveFor` + `CheckReadiness` |

### Deleted Files (Step 2)

| File | Reason |
|------|--------|
| `internal/agent/cc/detector.go` | Logic split into probe/liveness + cc/readiness + cc/content_matcher |
| `internal/agent/cc/detector_test.go` | Tests migrated to probe/liveness_test + cc/readiness_test |
| `internal/agent/cc/interfaces.go` | `CCDetector`/`DetectorKey` replaced by prober; `CCOperator`/`CCHistory` keys move to provider.go |
| `internal/agent/codex/detector.go` | `checkPaneProcess`/`isCodexProcess` replaced by prober |

---

## Step 1: Probe Package + Liveness Layer

### Task 1: Prober struct and interfaces

**Files:**
- Create: `internal/agent/probe/probe.go`

- [ ] **Step 1.1: Create probe package with Prober struct and interfaces**

```go
// internal/agent/probe/probe.go
package probe

import (
	"sync"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/tmux"
)

// ContentMatcher is an optional Liveness fallback.
// Providers implement this to detect their agent via screen content
// when process name matching fails (e.g. CC launched via wrapper script).
type ContentMatcher interface {
	LooksLikeAgent(content string) bool
}

// ReadinessChecker determines the detailed status of an agent.
type ReadinessChecker interface {
	CheckReadiness(target string) ReadinessResult
}

// ReadinessResult is the output of a ReadinessChecker.
type ReadinessResult struct {
	Status agent.Status
	Raw    string // captured pane content (debug, optional)
}

// ActivityCallback is called when screen content changes during a watch.
type ActivityCallback func(target string)

// processMatcher holds the known command names for one agent type.
type processMatcher struct {
	commands map[string]bool
}

// Prober provides layered probing: Liveness → Activity → Readiness.
type Prober struct {
	tmux tmux.Executor

	matcherMu sync.RWMutex
	matchers  map[string]*processMatcher  // agentType → matcher
	content   map[string]ContentMatcher   // agentType → optional
	readiness map[string]ReadinessChecker // agentType → checker

	watcherMu sync.Mutex
	watchers  map[string]watchEntry // target → active watcher
}

type watchEntry struct {
	cancel func()
}

// New creates a Prober backed by the given tmux executor.
func New(tmux tmux.Executor) *Prober {
	return &Prober{
		tmux:      tmux,
		matchers:  make(map[string]*processMatcher),
		content:   make(map[string]ContentMatcher),
		readiness: make(map[string]ReadinessChecker),
		watchers:  make(map[string]watchEntry),
	}
}

// RegisterProcessNames registers process names for a given agent type.
func (p *Prober) RegisterProcessNames(agentType string, names []string) {
	cmds := make(map[string]bool, len(names))
	for _, n := range names {
		cmds[n] = true
	}
	p.matcherMu.Lock()
	p.matchers[agentType] = &processMatcher{commands: cmds}
	p.matcherMu.Unlock()
}

// UpdateProcessNames replaces process names for a given agent type.
// Called from OnConfigChange to handle dynamic CC command name updates.
func (p *Prober) UpdateProcessNames(agentType string, names []string) {
	p.RegisterProcessNames(agentType, names)
}

// RegisterContentMatcher registers an optional content-based fallback for Liveness.
func (p *Prober) RegisterContentMatcher(agentType string, m ContentMatcher) {
	p.matcherMu.Lock()
	p.content[agentType] = m
	p.matcherMu.Unlock()
}

// RegisterReadiness registers a ReadinessChecker for a given agent type.
func (p *Prober) RegisterReadiness(agentType string, checker ReadinessChecker) {
	p.matcherMu.Lock()
	p.readiness[agentType] = checker
	p.matcherMu.Unlock()
}
```

- [ ] **Step 1.2: Verify it compiles**

Run: `cd /Users/wake/Workspace/wake/purdex && go build ./internal/agent/probe/`
Expected: no errors

- [ ] **Step 1.3: Commit**

```
feat(probe): add Prober struct and interfaces
```

---

### Task 2: Liveness layer

**Files:**
- Create: `internal/agent/probe/liveness.go`
- Create: `internal/agent/probe/liveness_test.go`

- [ ] **Step 2.1: Write failing tests for Liveness**

```go
// internal/agent/probe/liveness_test.go
package probe_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

// fakeContentMatcher implements probe.ContentMatcher for tests.
type fakeContentMatcher struct {
	result bool
}

func (f *fakeContentMatcher) LooksLikeAgent(string) bool { return f.result }

func TestIsAliveFor_DirectCommand(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude", "cld"})

	fake.SetPaneCommand("sess:", "claude")
	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive when pane command is registered CC command")
	}
}

func TestIsAliveFor_ShellIsDead(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})

	fake.SetPaneCommand("sess:", "zsh")
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected dead when pane command is shell")
	}
}

func TestIsAliveFor_ChildProcess(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})

	fake.SetPaneCommand("sess:", "node")
	fake.SetPaneChildren("sess:", []string{"/usr/local/bin/claude"})
	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive when child process matches (basename)")
	}
}

func TestIsAliveFor_ContentFallback(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})
	p.RegisterContentMatcher("cc", &fakeContentMatcher{result: true})

	fake.SetPaneCommand("sess:", "node")
	fake.SetPaneChildren("sess:", []string{"npm"})
	fake.SetPaneContent("sess:", "❯ prompt here")
	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive via content fallback")
	}
}

func TestIsAliveFor_NoContentMatcherReturnsDead(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})
	// No content matcher registered

	fake.SetPaneCommand("sess:", "node")
	fake.SetPaneChildren("sess:", []string{"npm"})
	fake.SetPaneContent("sess:", "❯ prompt here")
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected dead when no content matcher registered")
	}
}

func TestIsAliveFor_ContentMatcherReturnsFalse(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})
	p.RegisterContentMatcher("cc", &fakeContentMatcher{result: false})

	fake.SetPaneCommand("sess:", "vim")
	fake.SetPaneChildren("sess:", nil)
	fake.SetPaneContent("sess:", "-- INSERT --")
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected dead when content matcher returns false")
	}
}

func TestIsAliveFor_UnknownAgentType(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)

	fake.SetPaneCommand("sess:", "claude")
	if p.IsAliveFor("unknown", "sess:") {
		t.Fatal("expected dead for unregistered agent type")
	}
}

func TestUpdateProcessNames(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})

	fake.SetPaneCommand("sess:", "cld")
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("cld should not be alive before update")
	}

	p.UpdateProcessNames("cc", []string{"claude", "cld"})
	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("cld should be alive after update")
	}
}
```

- [ ] **Step 2.2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/agent/probe/ -v -run TestIsAlive`
Expected: compilation error — `IsAliveFor` not defined

- [ ] **Step 2.3: Implement Liveness**

```go
// internal/agent/probe/liveness.go
package probe

import "strings"

var defaultShells = map[string]bool{
	"zsh": true, "bash": true, "sh": true, "fish": true, "dash": true,
}

// IsAliveFor checks whether the given tmux target is running an agent of the
// specified type. It checks in order: (1) pane foreground command, (2) child
// processes, (3) optional content fallback.
func (p *Prober) IsAliveFor(agentType, target string) bool {
	p.matcherMu.RLock()
	matcher, ok := p.matchers[agentType]
	contentMatcher := p.content[agentType]
	p.matcherMu.RUnlock()
	if !ok {
		return false
	}

	// Layer 1a: foreground command
	cmd, err := p.tmux.PaneCurrentCommand(target)
	if err != nil {
		return false
	}
	cmd = strings.TrimSpace(cmd)
	if matcher.commands[cmd] {
		return true
	}
	if defaultShells[cmd] {
		return false
	}

	// Layer 1b: child processes
	children, err := p.tmux.PaneChildCommands(target)
	if err == nil {
		for _, child := range children {
			base := child
			if idx := strings.LastIndex(child, "/"); idx >= 0 {
				base = child[idx+1:]
			}
			if matcher.commands[base] {
				return true
			}
		}
	}

	// Layer 1c: content fallback (optional)
	if contentMatcher != nil {
		content, err := p.tmux.CapturePaneContent(target, 5)
		if err == nil && contentMatcher.LooksLikeAgent(content) {
			return true
		}
	}

	return false
}
```

- [ ] **Step 2.4: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/agent/probe/ -v`
Expected: all PASS

- [ ] **Step 2.5: Commit**

```
feat(probe): implement Liveness layer with process + content fallback
```

---

### Task 3: CC ContentMatcher

**Files:**
- Create: `internal/agent/cc/content_matcher.go`

- [ ] **Step 3.1: Extract looksLikeCC into ContentMatcher**

```go
// internal/agent/cc/content_matcher.go
package cc

import "strings"

// ccContentMatcher implements probe.ContentMatcher for Claude Code.
type ccContentMatcher struct{}

// NewContentMatcher creates a CC content matcher for Liveness fallback.
func NewContentMatcher() *ccContentMatcher {
	return &ccContentMatcher{}
}

// LooksLikeAgent returns true if the terminal content looks like Claude Code.
func (m *ccContentMatcher) LooksLikeAgent(content string) bool {
	return looksLikeCC(content)
}
```

The existing `looksLikeCC` function in `detector.go` is reused. It will be kept until Step 2 deletes `detector.go`.

- [ ] **Step 3.2: Verify it compiles**

Run: `cd /Users/wake/Workspace/wake/purdex && go build ./internal/agent/cc/`
Expected: no errors

- [ ] **Step 3.3: Commit**

```
feat(cc): extract ContentMatcher from looksLikeCC
```

---

### Task 4: Wire Liveness into agent module + providers

**Files:**
- Modify: `internal/module/agent/module.go`
- Modify: `internal/agent/cc/provider.go`
- Modify: `internal/agent/codex/provider.go`
- Modify: `internal/module/agent/handler.go`
- Modify: `internal/module/agent/fakes_test.go`

- [ ] **Step 4.1: Add prober field to agent Module and wire in Init**

In `internal/module/agent/module.go`, add the import and field:

```go
// Add to imports:
"github.com/wake/purdex/internal/agent/probe"

// Add field to Module struct:
type Module struct {
	// ...existing fields...
	prober *probe.Prober
}
```

Replace the CC provider init block in `Init()`:

```go
// Replace lines 70-82 with:
	// Prober (shared across all providers)
	m.prober = probe.New(c.Tmux)

	// CC provider
	ccDetector := agentcc.NewDetector(c.Tmux, c.Cfg.Detect.CCCommands)
	ccProvider := agentcc.NewProvider(ccDetector, c.Tmux, c.Cfg, &c.CfgMu)
	ccProvider.RegisterServices(c.Registry)
	m.registry.Register(ccProvider)

	// Register CC with prober
	m.prober.RegisterProcessNames("cc", c.Cfg.Detect.CCCommands)
	m.prober.RegisterContentMatcher("cc", agentcc.NewContentMatcher())

	// Listen for config changes to update both detector and prober
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		cmds := c.Cfg.Detect.CCCommands
		c.CfgMu.RUnlock()
		ccDetector.UpdateCommands(cmds)
		m.prober.UpdateProcessNames("cc", cmds)
	})

	// Codex provider
	m.registry.Register(codex.NewProvider())
	m.prober.RegisterProcessNames("codex", []string{"codex"})

	// Expose prober and registry for other modules
	c.Registry.Register("agent.prober", m.prober)
	c.Registry.Register("agent.registry", m.registry)
```

- [ ] **Step 4.2: Replace provider.IsAlive with prober.IsAliveFor in checkAliveAll**

In `internal/module/agent/module.go`, replace lines 294-316 (the `provider, ok := ...` block inside checkAliveAll):

```go
		provider, ok := m.registry.Get(ev.AgentType)
		if !ok {
			continue
		}

		tmuxTarget := ev.TmuxSession + ":"
		if !m.prober.IsAliveFor(ev.AgentType, tmuxTarget) {
			m.mu.Lock()
			delete(m.currentStatus, ev.TmuxSession)
			delete(m.subagents, ev.TmuxSession)
			m.mu.Unlock()

			_ = m.events.Delete(ev.TmuxSession)

			normalized := agentpkg.NormalizedEvent{
				AgentType:    ev.AgentType,
				Status:       string(agentpkg.StatusClear),
				RawEventName: "isAlive:dead",
				BroadcastTs:  time.Now().UnixNano(),
			}
			payload, _ := json.Marshal(normalized)
			m.core.Events.Broadcast(code, "hook", string(payload))
		}
```

Note: `provider` is still needed for the `Get` check (to skip unknown types), but `IsAlive` is now on prober.

- [ ] **Step 4.3: Replace provider.IsAlive in handleCheckAlive**

In `internal/module/agent/handler.go`, replace line 389:

```go
	// Replace:
	// alive := provider.IsAlive(tmuxName + ":")
	// With:
	alive := m.prober.IsAliveFor(ev.AgentType, tmuxName+":")
```

- [ ] **Step 4.4: Run existing tests**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/module/agent/ -v -count=1`
Expected: all existing tests pass (fakeAgentProvider.IsAlive is no longer called for checkAliveAll, but tests should still pass because prober uses FakeExecutor)

- [ ] **Step 4.5: Commit**

```
feat(agent): wire prober Liveness into checkAliveAll and handleCheckAlive
```

---

## Step 2: Readiness Layer + Delete Old Detector

### Task 5: CC ReadinessChecker

**Files:**
- Create: `internal/agent/cc/readiness.go`

- [ ] **Step 5.1: Write failing test**

Add test cases to a new test file:

```go
// internal/agent/cc/readiness_test.go
package cc_test

import (
	"testing"

	cc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/tmux"
)

func TestReadinessChecker(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	checker := cc.NewReadinessChecker(fake)

	tests := []struct {
		name     string
		content  string
		expected agent.Status
	}{
		{"idle prompt", "❯ ", agent.StatusIdle},
		{"running spinner", "⠋ Reading file...", agent.StatusRunning},
		{"waiting permission", "Allow  Deny", agent.StatusWaiting},
		{"idle with status bar", "❯ \n─────────\n  project [Opus 4.6] 100% left", agent.StatusIdle},
		{"empty content", "", agent.StatusRunning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.SetPaneContent("test:", tt.content)
			result := checker.CheckReadiness("test:")
			if result.Status != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, result.Status)
			}
		})
	}
}
```

- [ ] **Step 5.2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/agent/cc/ -v -run TestReadinessChecker`
Expected: compilation error — `NewReadinessChecker` not defined

- [ ] **Step 5.3: Implement CC ReadinessChecker**

```go
// internal/agent/cc/readiness.go
package cc

import (
	"strings"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

// ccReadinessChecker implements probe.ReadinessChecker for Claude Code.
type ccReadinessChecker struct {
	tmux tmux.Executor
}

// NewReadinessChecker creates a CC readiness checker.
func NewReadinessChecker(tmux tmux.Executor) probe.ReadinessChecker {
	return &ccReadinessChecker{tmux: tmux}
}

// CheckReadiness determines CC's current state by parsing terminal content.
func (c *ccReadinessChecker) CheckReadiness(target string) probe.ReadinessResult {
	content, err := c.tmux.CapturePaneContent(target, 5)
	if err != nil {
		return probe.ReadinessResult{Status: agent.StatusRunning}
	}

	if strings.Contains(content, "Allow") && strings.Contains(content, "Deny") {
		return probe.ReadinessResult{Status: agent.StatusWaiting, Raw: content}
	}

	lines := strings.Split(strings.TrimSpace(content), "\n")
	start := len(lines) - 5
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		if strings.HasPrefix(strings.TrimSpace(line), "❯") {
			return probe.ReadinessResult{Status: agent.StatusIdle, Raw: content}
		}
	}

	return probe.ReadinessResult{Status: agent.StatusRunning, Raw: content}
}
```

- [ ] **Step 5.4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/agent/cc/ -v -run TestReadinessChecker`
Expected: all PASS

- [ ] **Step 5.5: Commit**

```
feat(cc): implement ReadinessChecker from detectCCSubState
```

---

### Task 6: Codex ReadinessChecker + Readiness dispatch

**Files:**
- Create: `internal/agent/codex/readiness.go`
- Create: `internal/agent/probe/readiness.go`

- [ ] **Step 6.1: Write Codex ReadinessChecker**

```go
// internal/agent/codex/readiness.go
package codex

import (
	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

type codexReadinessChecker struct {
	tmux tmux.Executor
}

// NewReadinessChecker creates a Codex readiness checker.
func NewReadinessChecker(tmux tmux.Executor) probe.ReadinessChecker {
	return &codexReadinessChecker{tmux: tmux}
}

// CheckReadiness determines Codex's current state.
// Codex has limited screen parsing — default to running.
func (c *codexReadinessChecker) CheckReadiness(target string) probe.ReadinessResult {
	return probe.ReadinessResult{Status: agent.StatusRunning}
}
```

- [ ] **Step 6.2: Write Prober.CheckReadiness dispatch**

```go
// internal/agent/probe/readiness.go
package probe

// CheckReadiness delegates to the registered ReadinessChecker for the given agent type.
// Returns (result, false) if no checker is registered.
func (p *Prober) CheckReadiness(agentType, target string) (ReadinessResult, bool) {
	p.matcherMu.RLock()
	checker, ok := p.readiness[agentType]
	p.matcherMu.RUnlock()
	if !ok {
		return ReadinessResult{}, false
	}
	return checker.CheckReadiness(target), true
}
```

- [ ] **Step 6.3: Write readiness dispatch test**

```go
// internal/agent/probe/readiness_test.go
package probe_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
)

type fakeReadinessChecker struct {
	status agent.Status
}

func (f *fakeReadinessChecker) CheckReadiness(string) probe.ReadinessResult {
	return probe.ReadinessResult{Status: f.status}
}

func TestCheckReadiness_Registered(t *testing.T) {
	p := probe.New(nil)
	p.RegisterReadiness("cc", &fakeReadinessChecker{status: agent.StatusIdle})

	result, ok := p.CheckReadiness("cc", "sess:")
	if !ok {
		t.Fatal("expected ok for registered checker")
	}
	if result.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %s", result.Status)
	}
}

func TestCheckReadiness_Unregistered(t *testing.T) {
	p := probe.New(nil)

	_, ok := p.CheckReadiness("unknown", "sess:")
	if ok {
		t.Fatal("expected not ok for unregistered checker")
	}
}
```

- [ ] **Step 6.4: Run all probe tests**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/agent/probe/ -v`
Expected: all PASS

- [ ] **Step 6.5: Commit**

```
feat(probe): implement Readiness layer + Codex ReadinessChecker
```

---

### Task 7: Rewrite CC Provider + Operator to use Prober

**Files:**
- Modify: `internal/agent/cc/provider.go`
- Modify: `internal/agent/cc/operator.go`
- Modify: `internal/module/agent/module.go`

- [ ] **Step 7.1: Rewrite cc/provider.go**

Replace the entire file:

```go
// internal/agent/cc/provider.go
package cc

import (
	"encoding/json"
	"sync"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/tmux"
)

// Provider implements agent.AgentProvider for Claude Code.
type Provider struct {
	prober   *probe.Prober
	tmuxExec tmux.Executor
	cfg      *config.Config
	cfgMu    *sync.RWMutex
}

// NewProvider creates a CC provider.
func NewProvider(prober *probe.Prober, tmuxExec tmux.Executor, cfg *config.Config, cfgMu *sync.RWMutex) *Provider {
	return &Provider{prober: prober, tmuxExec: tmuxExec, cfg: cfg, cfgMu: cfgMu}
}

func (p *Provider) Type() string        { return "cc" }
func (p *Provider) DisplayName() string { return "Claude Code" }
func (p *Provider) IconHint() string    { return "cc" }

func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "cc"
	}
	if p.prober == nil || ctx.TmuxTarget == "" {
		return false
	}
	return p.prober.IsAliveFor("cc", ctx.TmuxTarget)
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveCCStatus(eventName, rawEvent)
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	if p.prober == nil {
		return false
	}
	return p.prober.IsAliveFor("cc", tmuxTarget)
}

// RegisterServices registers this provider's services into the core service registry.
func (p *Provider) RegisterServices(registry *core.ServiceRegistry) {
	registry.Register(HistoryKey, CCHistoryProvider(p))
	registry.Register(OperatorKey, CCOperator(p))
}
```

- [ ] **Step 7.2: Rewrite cc/operator.go to use prober**

```go
// internal/agent/cc/operator.go
package cc

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/tmux"
)

func (p *Provider) Interrupt(ctx context.Context, tmuxTarget string) error {
	tx := p.tmuxExec
	if err := tx.SendKeysRaw(tmuxTarget, "C-u"); err != nil {
		return fmt.Errorf("send C-u: %w", err)
	}
	if err := tx.SendKeysRaw(tmuxTarget, "C-c"); err != nil {
		return fmt.Errorf("send C-c: %w", err)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			result, ok := p.prober.CheckReadiness("cc", tmuxTarget)
			if ok && result.Status == agent.StatusIdle {
				return nil
			}
		}
	}
}

func (p *Provider) Exit(ctx context.Context, tmuxTarget string) error {
	tx := p.tmuxExec
	if err := tx.SendKeysRaw(tmuxTarget, "-X", "cancel"); err != nil {
		log.Printf("cc: Exit pane-prep cancel (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "Escape"); err != nil {
		log.Printf("cc: Exit pane-prep Escape (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "C-c"); err != nil {
		log.Printf("cc: Exit pane-prep C-c (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "Escape"); err != nil {
		log.Printf("cc: Exit pane-prep Escape2 (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeys(tmuxTarget, "/exit"); err != nil {
		return fmt.Errorf("send /exit: %w", err)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if !p.prober.IsAliveFor("cc", tmuxTarget) {
				return nil
			}
		}
	}
}

func (p *Provider) GetStatus(ctx context.Context, tmuxTarget string) (*StatusInfo, error) {
	tx := p.tmuxExec
	didManualResize := false
	if cols, rows, err := tx.PaneSize(tmuxTarget); err == nil && (cols < 80 || rows < 24) {
		if err := tx.ResizeWindow(tmuxTarget, 80, 24); err != nil {
			return nil, fmt.Errorf("resize pane: %w", err)
		}
		didManualResize = true
		sleepCtx(ctx, 200*time.Millisecond)
	}
	if didManualResize {
		p.cfgMu.RLock()
		sizingMode := "latest"
		if p.cfg.Terminal.GetSizingMode() == "minimal-first" {
			sizingMode = "smallest"
		}
		p.cfgMu.RUnlock()
		defer restoreWindowSizing(tx, tmuxTarget, sizingMode)
	}
	if err := tx.SendKeysRaw(tmuxTarget, "-l", "/"); err != nil {
		return nil, fmt.Errorf("send /: %w", err)
	}
	sleepCtx(ctx, 1*time.Second)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "-l", "status"); err != nil {
		return nil, fmt.Errorf("send status: %w", err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "Enter"); err != nil {
		return nil, fmt.Errorf("send Enter: %w", err)
	}
	var statusInfo StatusInfo
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		sleepCtx(ctx, 500*time.Millisecond)
		if ctx.Err() != nil {
			break
		}
		paneContent, err := tx.CapturePaneContent(tmuxTarget, 200)
		if err != nil {
			lastErr = err
			continue
		}
		info, err := ExtractStatusInfo(paneContent)
		if err == nil {
			statusInfo = info
			break
		}
		lastErr = err
	}
	if statusInfo.SessionID == "" {
		if lastErr != nil {
			return nil, fmt.Errorf("could not extract session ID: %w", lastErr)
		}
		return nil, fmt.Errorf("could not extract session ID")
	}
	return &statusInfo, nil
}

func (p *Provider) Launch(ctx context.Context, tmuxTarget string, cmd string) error {
	return p.tmuxExec.SendKeys(tmuxTarget, cmd)
}

func restoreWindowSizing(tx tmux.Executor, target, windowSizeMode string) {
	if err := tx.ResizeWindowAuto(target); err != nil {
		log.Printf("restoreWindowSizing: ResizeWindowAuto(%s): %v", target, err)
	}
	if err := tx.SetWindowOption(target, "window-size", windowSizeMode); err != nil {
		log.Printf("restoreWindowSizing: SetWindowOption(%s): %v", target, err)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
```

- [ ] **Step 7.3: Update module.go Init to pass prober to CC Provider**

In `internal/module/agent/module.go` Init(), replace the CC provider creation:

```go
	// CC provider (now receives prober instead of detector)
	ccProvider := agentcc.NewProvider(m.prober, c.Tmux, c.Cfg, &c.CfgMu)
	ccProvider.RegisterServices(c.Registry)
	m.registry.Register(ccProvider)

	// Register CC with prober
	m.prober.RegisterProcessNames("cc", c.Cfg.Detect.CCCommands)
	m.prober.RegisterContentMatcher("cc", agentcc.NewContentMatcher())
	m.prober.RegisterReadiness("cc", agentcc.NewReadinessChecker(c.Tmux))

	// Listen for config changes
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		cmds := c.Cfg.Detect.CCCommands
		c.CfgMu.RUnlock()
		m.prober.UpdateProcessNames("cc", cmds)
	})

	// Codex provider
	m.registry.Register(codex.NewProvider())
	m.prober.RegisterProcessNames("codex", []string{"codex"})
	m.prober.RegisterReadiness("codex", codex.NewReadinessChecker(c.Tmux))
```

Remove the `ccDetector` variable entirely — it's no longer created.

- [ ] **Step 7.4: Run tests**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/agent/cc/ ./internal/module/agent/ -v -count=1`
Expected: all PASS

- [ ] **Step 7.5: Commit**

```
refactor(cc): replace Detector with Prober in Provider and Operator
```

---

### Task 8: Rewrite Stream module to use Prober

**Files:**
- Modify: `internal/module/stream/module.go`
- Modify: `internal/module/stream/orchestrator.go`
- Modify: `internal/agent/cc/interfaces.go`

- [ ] **Step 8.1: Update stream/module.go**

```go
// Replace ccDetect field and Init:

import (
	// Keep: agentcc "github.com/wake/purdex/internal/agent/cc" (needed for OperatorKey/CCOperator)
	// Add:
	"github.com/wake/purdex/internal/agent/probe"
)

type StreamModule struct {
	core     *core.Core
	bridge   *bridge.Bridge
	sessions session.SessionProvider
	ccOps    agentcc.CCOperator
	prober   *probe.Prober
	locks    *handoffLocks
}

func (m *StreamModule) Init(c *core.Core) error {
	m.core = c
	m.bridge = bridge.New()
	m.sessions = c.Registry.MustGet(session.RegistryKey).(session.SessionProvider)
	m.ccOps = c.Registry.MustGet(agentcc.OperatorKey).(agentcc.CCOperator)
	m.prober = c.Registry.MustGet("agent.prober").(*probe.Prober)
	m.locks = newHandoffLocks()
	return nil
}
```

Note: `agentcc` import is still needed for `OperatorKey` and `CCOperator`. Only `DetectorKey`/`CCDetector` usage is removed.

- [ ] **Step 8.2: Rewrite orchestrator.go runHandoff**

Replace the Detect calls in `runHandoff`:

```go
// Step 3 (lines 71-76): replace ccDetect.Detect with prober
	broadcast("detecting")
	if !m.prober.IsAliveFor("cc", target) {
		broadcast("failed:no CC running")
		return
	}

// Step 4 (lines 78-87): replace status check with CheckReadiness
	result, _ := m.prober.CheckReadiness("cc", target)
	if result.Status != agent.StatusIdle {
		broadcast("stopping-cc")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.ccOps.Interrupt(ctx, target); err != nil {
			broadcast("failed:interrupt CC: " + err.Error())
			return
		}
	}
```

Add import: `"github.com/wake/purdex/internal/agent"`

- [ ] **Step 8.3: Rewrite orchestrator.go runHandoffToTerm**

Replace the Detect calls in `runHandoffToTerm`:

```go
// Step 4 (lines 197-209): wait for shell = !IsAliveFor
	broadcast("waiting-shell")
	shellDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(shellDeadline) {
		if !m.prober.IsAliveFor("cc", target) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if m.prober.IsAliveFor("cc", target) {
		rollbackMode()
		broadcast("failed:shell did not recover")
		return
	}

// Step 6 (lines 220-235): verify CC started
	ccDeadline := time.Now().Add(15 * time.Second)
	ccStarted := false
	for time.Now().Before(ccDeadline) {
		if m.prober.IsAliveFor("cc", target) {
			result, _ := m.prober.CheckReadiness("cc", target)
			if result.Status == agent.StatusIdle || result.Status == agent.StatusRunning || result.Status == agent.StatusWaiting {
				ccStarted = true
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
```

- [ ] **Step 8.4: Run stream module tests**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/module/stream/ -v -count=1`
Expected: compilation errors — `fakeCCDetector` references need updating

- [ ] **Step 8.5: Update orchestrator_test.go**

Replace `fakeCCDetector` with a fake `Prober`. The test file uses `ccDetect *fakeCCDetector` in test structs. Replace with a real `probe.Prober` backed by `tmux.FakeExecutor`:

For each test case, instead of pre-defining `statuses []agentcc.Status`, set up the `FakeExecutor` pane command/content to make `IsAliveFor` and `CheckReadiness` return the desired results.

This is a mechanical replacement. For each `fakeCCDetector{statuses: []agentcc.Status{...}}`:
- `StatusNormal` / `StatusNotInCC` → set pane command to `"zsh"` (not alive)
- `StatusCCIdle` → set pane command to `"claude"`, pane content to `"❯ "`
- `StatusCCRunning` → set pane command to `"claude"`, pane content to `"⠋ Working..."`
- `StatusCCWaiting` → set pane command to `"claude"`, pane content to `"Allow  Deny"`

For tests that cycle through multiple statuses, update FakeExecutor state between polling iterations by using a goroutine or callback.

- [ ] **Step 8.6: Run stream tests again**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/module/stream/ -v -count=1`
Expected: all PASS

- [ ] **Step 8.7: Commit**

```
refactor(stream): replace CCDetector with Prober in orchestrator
```

---

### Task 9: Delete old Detector and interfaces

**Files:**
- Delete: `internal/agent/cc/detector.go`
- Delete: `internal/agent/cc/detector_test.go`
- Modify: `internal/agent/cc/interfaces.go`
- Delete: `internal/agent/codex/detector.go`

- [ ] **Step 9.1: Remove DetectorKey and CCDetector from interfaces.go**

Rewrite `internal/agent/cc/interfaces.go` — keep only OperatorKey, HistoryKey, and their interfaces:

```go
package cc

import "context"

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

// Registry keys for core.Registry.
const (
	HistoryKey  = "cc.history"
	OperatorKey = "cc.operator"
)
```

- [ ] **Step 9.2: Delete detector.go, detector_test.go, codex/detector.go**

```bash
rm internal/agent/cc/detector.go
rm internal/agent/cc/detector_test.go
rm internal/agent/codex/detector.go
```

- [ ] **Step 9.3: Remove isCodexProcess from codex/provider.go**

The `Claim` method in `codex/provider.go` calls `isCodexProcess`. Since `detector.go` is deleted, inline the check or use prober. For now, since Codex Claim still needs to work without prober in tests:

```go
func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "codex"
	}
	return ctx.ProcessName == "codex"
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	return false // Deprecated: agent module uses prober.IsAliveFor directly
}
```

- [ ] **Step 9.4: Run full test suite**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/... -count=1`
Expected: all PASS

- [ ] **Step 9.5: Commit**

```
refactor: delete cc.Detector and codex.detector, remove CCDetector interface
```

---

## Step 3: Activity Layer

### Task 10: Activity watcher implementation

**Files:**
- Create: `internal/agent/probe/activity.go`
- Create: `internal/agent/probe/activity_test.go`

- [ ] **Step 10.1: Write failing tests**

```go
// internal/agent/probe/activity_test.go
package probe_test

import (
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

func TestStartWatch_DetectsChange(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)

	fake.SetPaneContent("sess:", "initial content")

	var called sync.WaitGroup
	called.Add(1)
	var callbackTarget string

	p.StartWatch("sess:", func(target string) {
		callbackTarget = target
		called.Done()
	})

	// Simulate screen change after a short delay
	time.Sleep(100 * time.Millisecond)
	fake.SetPaneContent("sess:", "new content after user responded")

	called.Wait()
	if callbackTarget != "sess:" {
		t.Fatalf("expected callback with target sess:, got %s", callbackTarget)
	}
}

func TestStartWatch_NoChangeNoCallback(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)

	fake.SetPaneContent("sess:", "static content")

	callbackCalled := false
	p.StartWatch("sess:", func(string) {
		callbackCalled = true
	})

	time.Sleep(600 * time.Millisecond)
	p.StopWatch("sess:")

	if callbackCalled {
		t.Fatal("callback should not be called when content is static")
	}
}

func TestStopWatch_Idempotent(t *testing.T) {
	p := probe.New(nil)
	// Should not panic
	p.StopWatch("nonexistent")
	p.StopWatch("nonexistent")
}

func TestStartWatch_ReplacesExisting(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)

	fake.SetPaneContent("sess:", "content-v1")

	firstCalled := false
	p.StartWatch("sess:", func(string) {
		firstCalled = true
	})

	// Replace with new watcher before change
	var secondCalled sync.WaitGroup
	secondCalled.Add(1)
	p.StartWatch("sess:", func(string) {
		secondCalled.Done()
	})

	// Trigger change
	time.Sleep(100 * time.Millisecond)
	fake.SetPaneContent("sess:", "content-v2")
	secondCalled.Wait()

	if firstCalled {
		t.Fatal("first watcher should have been cancelled by replacement")
	}
}

func TestStopAllWatches(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)

	fake.SetPaneContent("a:", "content-a")
	fake.SetPaneContent("b:", "content-b")

	aCalled := false
	bCalled := false
	p.StartWatch("a:", func(string) { aCalled = true })
	p.StartWatch("b:", func(string) { bCalled = true })

	p.StopAllWatches()

	// Change content after stop — callbacks should NOT fire
	fake.SetPaneContent("a:", "changed-a")
	fake.SetPaneContent("b:", "changed-b")
	time.Sleep(600 * time.Millisecond)

	if aCalled || bCalled {
		t.Fatal("callbacks should not fire after StopAllWatches")
	}
}
```

- [ ] **Step 10.2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/agent/probe/ -v -run TestStartWatch`
Expected: compilation error — `StartWatch` not defined

- [ ] **Step 10.3: Implement Activity layer**

```go
// internal/agent/probe/activity.go
package probe

import (
	"context"
	"hash/fnv"
	"time"
)

const (
	activityPollInterval = 500 * time.Millisecond
	activityCaptureLines = 10
)

// StartWatch begins monitoring the given tmux target for screen changes.
// When a change is detected, cb is called once and the goroutine exits.
// If a watcher already exists for the target, it is stopped first.
func (p *Prober) StartWatch(target string, cb ActivityCallback) {
	p.watcherMu.Lock()
	// Cancel existing watcher for this target
	if existing, ok := p.watchers[target]; ok {
		existing.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.watchers[target] = watchEntry{cancel: cancel}
	p.watcherMu.Unlock()

	go p.activityLoop(ctx, cancel, target, cb)
}

// StopWatch cancels the active watcher for the given target. Idempotent.
func (p *Prober) StopWatch(target string) {
	p.watcherMu.Lock()
	if entry, ok := p.watchers[target]; ok {
		entry.cancel()
		delete(p.watchers, target)
	}
	p.watcherMu.Unlock()
}

// StopAllWatches cancels all active watchers. Used during daemon shutdown.
func (p *Prober) StopAllWatches() {
	p.watcherMu.Lock()
	for target, entry := range p.watchers {
		entry.cancel()
		delete(p.watchers, target)
	}
	p.watcherMu.Unlock()
}

func (p *Prober) activityLoop(ctx context.Context, cancel context.CancelFunc, target string, cb ActivityCallback) {
	defer func() {
		// Clean up own entry (only if it's still ours — a replacement StartWatch
		// may have already overwritten the entry).
		p.watcherMu.Lock()
		if entry, ok := p.watchers[target]; ok && entry.cancel == cancel {
			delete(p.watchers, target)
		}
		p.watcherMu.Unlock()
	}()

	baseline := p.hashCapture(target)
	ticker := time.NewTicker(activityPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := p.hashCapture(target)
			if current != baseline {
				cb(target)
				return
			}
		}
	}
}

func (p *Prober) hashCapture(target string) uint32 {
	content, err := p.tmux.CapturePaneContent(target, activityCaptureLines)
	if err != nil {
		return 0
	}
	h := fnv.New32a()
	h.Write([]byte(content))
	return h.Sum32()
}
```

- [ ] **Step 10.4: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/agent/probe/ -v -count=1`
Expected: all PASS

- [ ] **Step 10.5: Commit**

```
feat(probe): implement Activity layer with screen hash diff
```

---

### Task 11: Integrate Activity into agent module

**Files:**
- Modify: `internal/module/agent/module.go`
- Modify: `internal/module/agent/handler.go`

- [ ] **Step 11.1: Add activeWatchers to Module**

In `internal/module/agent/module.go`:

```go
// Add to Module struct:
	activeWatchers map[string]string  // tmuxSession → agentType
```

Update `New()`:

```go
func New(events *store.AgentEventStore) *Module {
	return &Module{
		events:         events,
		registry:       agentpkg.NewRegistry(),
		currentStatus:  make(map[string]agentpkg.Status),
		subagents:      make(map[string][]string),
		activeWatchers: make(map[string]string),
	}
}
```

Update `Stop()`:

```go
func (m *Module) Stop(_ context.Context) error {
	if m.prober != nil {
		m.prober.StopAllWatches()
	}
	m.mu.Lock()
	m.activeWatchers = make(map[string]string)
	m.mu.Unlock()
	return nil
}
```

- [ ] **Step 11.2: Add Activity integration to handleEvent**

In `internal/module/agent/handler.go`, add the Activity watch logic. Insert after the existing "Update in-memory state" block (after line 99) and before "Build and broadcast":

```go
	// Activity watch management:
	// 1. Any hook event stops an active watcher for this session
	// 2. If new status is waiting, start a new watcher
	if req.TmuxSession != "" {
		m.mu.Lock()
		_, wasWatching := m.activeWatchers[req.TmuxSession]
		delete(m.activeWatchers, req.TmuxSession)
		m.mu.Unlock()
		if wasWatching && m.prober != nil {
			m.prober.StopWatch(req.TmuxSession + ":")
		}

		if result.Valid && result.Status == agentpkg.StatusWaiting && m.prober != nil {
			agentType := req.AgentType
			session := req.TmuxSession
			m.mu.Lock()
			m.activeWatchers[session] = agentType
			m.mu.Unlock()
			m.prober.StartWatch(session+":", m.onActivityDetected(session, agentType))
		}
	}
```

- [ ] **Step 11.3: Add onActivityDetected callback**

Add to `internal/module/agent/handler.go`:

```go
// onActivityDetected returns a callback for when screen activity is detected
// during a waiting state. The callback checks if the watcher is still active
// (a hook event may have already superseded it), then runs Readiness to
// determine the new status.
func (m *Module) onActivityDetected(session, agentType string) func(string) {
	return func(target string) {
		m.mu.Lock()
		if _, active := m.activeWatchers[session]; !active {
			m.mu.Unlock()
			return // hook event already superseded this watcher
		}
		delete(m.activeWatchers, session)
		m.mu.Unlock()

		// Liveness gate
		if m.prober == nil || !m.prober.IsAliveFor(agentType, target) {
			return
		}

		// Readiness check
		result, ok := m.prober.CheckReadiness(agentType, target)
		if !ok {
			return
		}

		// Only broadcast if status actually changed from waiting
		if result.Status == agentpkg.StatusWaiting {
			return
		}

		m.mu.Lock()
		m.currentStatus[session] = result.Status
		m.mu.Unlock()

		normalized := agentpkg.NormalizedEvent{
			AgentType:    agentType,
			Status:       string(result.Status),
			RawEventName: "probe:activity",
			BroadcastTs:  time.Now().UnixNano(),
		}
		m.broadcastToSession(session, normalized)
	}
}
```

- [ ] **Step 11.4: Add import for time in handler.go if not present**

Ensure `"time"` is in the imports of `handler.go`. It's already imported.

- [ ] **Step 11.5: Run all tests**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/... -count=1`
Expected: all PASS

- [ ] **Step 11.6: Commit**

```
feat(agent): integrate Activity watch for yellow light recovery
```

---

### Task 12: Add integration test for yellow light flow

**Files:**
- Modify: `internal/module/agent/handler_test.go`

- [ ] **Step 12.1: Write integration test**

```go
func TestActivityWatch_YellowLightRecovery(t *testing.T) {
	m := newTestModule(t)

	// Set up prober with CC
	fake := tmux.NewFakeExecutor()
	m.prober = probe.New(fake)
	m.prober.RegisterProcessNames("cc", []string{"claude"})
	m.prober.RegisterReadiness("cc", ccpkg.NewReadinessChecker(fake))

	// Register CC provider
	provider := &fakeAgentProvider{
		typeName: "cc",
		derive: func(eventName string, raw json.RawMessage) agentpkg.DeriveResult {
			if eventName == "Notification" {
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusWaiting}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	}
	m.registry.Register(provider)

	// Set up sessions and broadcaster
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "s1", Name: "work"}},
	}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	// Set CC as running in the pane
	fake.SetPaneCommand("work:", "claude")
	fake.SetPaneContent("work:", "Allow  Deny")  // waiting state screen

	// Send asking event → should enter yellow light + start watcher
	body := `{"tmux_session":"work","event_name":"Notification","raw_event":{"type":"notification","notification_type":"permission_prompt"},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	m.handleEvent(w, req)

	// Verify watcher started
	m.mu.Lock()
	_, watching := m.activeWatchers["work"]
	m.mu.Unlock()
	if !watching {
		t.Fatal("expected active watcher after waiting status")
	}

	// Simulate user responding — screen changes to running state
	time.Sleep(100 * time.Millisecond)
	fake.SetPaneContent("work:", "⠋ Processing your request...")

	// Wait for activity detection
	time.Sleep(700 * time.Millisecond)

	// Verify watcher stopped and status updated
	m.mu.Lock()
	_, stillWatching := m.activeWatchers["work"]
	status := m.currentStatus["work"]
	m.mu.Unlock()

	if stillWatching {
		t.Fatal("watcher should have stopped after activity detection")
	}
	if status != agentpkg.StatusRunning {
		t.Fatalf("expected status running after activity, got %s", status)
	}
}

func TestActivityWatch_HookEventSupersedes(t *testing.T) {
	m := newTestModule(t)

	fake := tmux.NewFakeExecutor()
	m.prober = probe.New(fake)
	m.prober.RegisterProcessNames("cc", []string{"claude"})

	provider := &fakeAgentProvider{
		typeName: "cc",
		derive: func(eventName string, raw json.RawMessage) agentpkg.DeriveResult {
			switch eventName {
			case "Notification":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusWaiting}
			case "UserPromptSubmit":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	}
	m.registry.Register(provider)
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "s1", Name: "work"}},
	}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}

	fake.SetPaneCommand("work:", "claude")
	fake.SetPaneContent("work:", "Allow  Deny")

	// Enter waiting state
	body := `{"tmux_session":"work","event_name":"Notification","raw_event":{"type":"notification","notification_type":"permission_prompt"},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	m.handleEvent(w, req)

	// Hook event arrives (UserPromptSubmit) — should supersede watcher
	body2 := `{"tmux_session":"work","event_name":"UserPromptSubmit","raw_event":{},"agent_type":"cc"}`
	req2 := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body2))
	w2 := httptest.NewRecorder()
	m.handleEvent(w2, req2)

	m.mu.Lock()
	_, watching := m.activeWatchers["work"]
	status := m.currentStatus["work"]
	m.mu.Unlock()

	if watching {
		t.Fatal("watcher should have been stopped by hook event")
	}
	if status != agentpkg.StatusRunning {
		t.Fatalf("expected running after UserPromptSubmit, got %s", status)
	}
}
```

- [ ] **Step 12.2: Add required imports to handler_test.go**

```go
import (
	// Add these if not present:
	"github.com/wake/purdex/internal/agent/probe"
	ccpkg "github.com/wake/purdex/internal/agent/cc"
)
```

- [ ] **Step 12.3: Run the integration tests**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/module/agent/ -v -run TestActivityWatch -count=1`
Expected: all PASS

- [ ] **Step 12.4: Run full test suite**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/... -count=1`
Expected: all PASS

- [ ] **Step 12.5: Commit**

```
test(agent): add integration tests for Activity watch yellow light recovery
```
