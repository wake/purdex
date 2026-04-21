# Agent Hook Trace Monitor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a hook-only trace rail that records each daemon hook decision chain into SQLite and expose a dev-only settings monitor that renders the chain as a hierarchical block tree with a selected-step inspector.

**Architecture:** Build the trace chain in memory during `handleEvent`, then flush one completed record to SQLite through a dedicated trace sink instead of issuing per-step `Exec` calls on the hook hot path. Persist chain metadata and ordered steps transactionally so every stored chain has a terminal status plus latest-step summary. On the SPA side, expose typed read-only monitor DTOs and render the monitor in three focused UI blocks: chain list, step tree, and step inspector with projection summary.

**Tech Stack:** Go, `modernc.org/sqlite`, existing `internal/store` migration pattern, existing `internal/module/agent` routing pattern, React 19, Vite, Testing Library, Vitest.

**Spec:** `docs/superpowers/specs/2026-04-20-agent-trace-monitor-design.md`

---

## File Structure

**Create:**
- `internal/store/trace.go` — trace schema, transactional save/read methods, retention
- `internal/store/trace_test.go` — migration, persistence, tree ordering, retention tests
- `internal/module/agent/trace.go` — in-memory hook trace collector + async sink
- `internal/module/agent/trace_test.go` — hook-path instrumentation tests
- `internal/module/agent/monitor.go` — read-only monitor handlers + DTO mapping
- `internal/module/agent/monitor_test.go` — monitor API tests
- `spa/src/components/settings/tmux-agent-monitor/ChainList.tsx` — left column chain list
- `spa/src/components/settings/tmux-agent-monitor/StepTree.tsx` — middle column ordered block tree
- `spa/src/components/settings/tmux-agent-monitor/StepInspector.tsx` — right column selected-step inspector + projection summary
- `spa/src/components/settings/TmuxAgentMonitorSection.tsx` — section shell, state orchestration, fetching
- `spa/src/components/settings/TmuxAgentMonitorSection.test.tsx` — section integration tests

**Modify:**
- `internal/store/agent_event.go` — expose `Traces()` from the shared DB handle
- `internal/module/agent/module.go` — wire trace sink, register monitor routes
- `internal/module/agent/handler.go` — start/finish collector, append verify/projection/emit steps
- `internal/module/agent/frame_ops.go` — return frame mutation metadata only, not trace serialization
- `spa/src/lib/host-api.ts` — typed monitor DTOs and fetch helpers
- `spa/src/lib/register-modules.tsx` — register monitor section behind a real dev gate
- `spa/src/locales/en.json` — monitor labels
- `spa/src/locales/zh-TW.json` — monitor labels
- `CHANGELOG.md` — add unreleased entry

**Do not modify in this phase:**
- `internal/module/agent/sweep.go`
- activity watcher logic
- tab icon / final display semantics
- probe / sweep trace schema

---

## Task 1: Add Transactional Trace Storage

**Files:**
- Create: `internal/store/trace.go`
- Test: `internal/store/trace_test.go`
- Modify: `internal/store/agent_event.go`

- [ ] **Step 1: Write the failing storage tests**

Create `internal/store/trace_test.go` with:

```go
package store

import "testing"

func TestTraceStore_SaveChainPersistsSummaryAndSteps(t *testing.T) {
	store := openTestAgentEventStore(t)
	traces, err := store.Traces()
	if err != nil {
		t.Fatalf("Traces: %v", err)
	}

	record := TraceRecord{
		Chain: TraceChain{
			ChainID:          "chain-1",
			StartedAt:        100,
			CompletedAt:      200,
			TerminalStatus:   "completed",
			TerminalReason:   "emit_broadcasted",
			TmuxSession:      "work",
			PaneID:           "%7",
			RootAgentType:    "codex",
			RootEventName:    "UserPromptSubmit",
			RootReason:       "hook_post",
			LatestStepKind:   "emit",
			LatestDecision:   "broadcasted",
			LatestStepReason: "session_code_resolved",
		},
		Steps: []TraceStep{
			{
				StepID:       "step-trigger",
				ChainID:      "chain-1",
				ParentStepID: "",
				Seq:          1,
				Kind:         "trigger",
				TmuxSession:  "work",
				PaneID:       "%7",
				AgentType:    "codex",
				EventName:    "UserPromptSubmit",
				Decision:     "received",
				Reason:       "hook_post",
				PayloadJSON:  `{"tmux_session":"work"}`,
				BeforeJSON:   `{}`,
				AfterJSON:    `{}`,
				CreatedAt:    100,
			},
			{
				StepID:       "step-verify",
				ChainID:      "chain-1",
				ParentStepID: "step-trigger",
				Seq:          2,
				Kind:         "verify",
				TmuxSession:  "work",
				PaneID:       "%7",
				AgentType:    "codex",
				EventName:    "UserPromptSubmit",
				Decision:     "accepted",
				Reason:       "verify_passed",
				PayloadJSON:  `{}`,
				BeforeJSON:   `{}`,
				AfterJSON:    `{"decision":"accepted"}`,
				CreatedAt:    110,
			},
		},
	}

	if err := traces.SaveChain(record); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	page, err := traces.ListChains(TraceListFilter{Session: "work", Limit: 10})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != 1 {
		t.Fatalf("len(page.Chains) = %d, want 1", len(page.Chains))
	}
	if page.Chains[0].LatestStepKind != "emit" {
		t.Fatalf("LatestStepKind = %q, want emit", page.Chains[0].LatestStepKind)
	}

	got, err := traces.GetChainRecord("chain-1")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got == nil || len(got.Steps) != 2 {
		t.Fatalf("GetChainRecord = %+v, want 2 steps", got)
	}
	if got.Steps[1].ParentStepID != "step-trigger" {
		t.Fatalf("ParentStepID = %q, want step-trigger", got.Steps[1].ParentStepID)
	}
}

func TestTraceStore_RetentionCapsChainsAndSteps(t *testing.T) {
	store := openTestAgentEventStore(t)
	traces, err := store.Traces()
	if err != nil {
		t.Fatalf("Traces: %v", err)
	}
	traces.maxChains = 2
	traces.maxSteps = 3

	for i := 0; i < 3; i++ {
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:          "chain-" + string(rune('a'+i)),
				StartedAt:        int64(100 + i),
				CompletedAt:      int64(110 + i),
				TerminalStatus:   "completed",
				TerminalReason:   "emit_broadcasted",
				TmuxSession:      "work",
				PaneID:           "%7",
				RootAgentType:    "cc",
				RootEventName:    "Stop",
				RootReason:       "hook_post",
				LatestStepKind:   "emit",
				LatestDecision:   "broadcasted",
				LatestStepReason: "session_code_resolved",
			},
			Steps: []TraceStep{
				{StepID: "s1-" + string(rune('a'+i)), ChainID: "chain-" + string(rune('a'+i)), Seq: 1, Kind: "trigger", CreatedAt: int64(100 + i)},
				{StepID: "s2-" + string(rune('a'+i)), ChainID: "chain-" + string(rune('a'+i)), Seq: 2, Kind: "emit", ParentStepID: "s1-" + string(rune('a'+i)), CreatedAt: int64(101 + i)},
			},
		}
		if err := traces.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}

	page, err := traces.ListChains(TraceListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != 1 {
		t.Fatalf("len(page.Chains) = %d, want 1 after step-cap prune", len(page.Chains))
	}
	if page.Chains[0].ChainID != "chain-c" {
		t.Fatalf("remaining chain = %q, want chain-c", page.Chains[0].ChainID)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store -run 'TraceStore' -count=1`

Expected: FAIL with missing `TraceRecord`, `SaveChain`, `GetChainRecord`, and retention fields.

- [ ] **Step 3: Implement trace schema and types**

Create `internal/store/trace.go` with the core types and migrations:

```go
package store

import (
	"encoding/base64"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

type TraceChain struct {
	ChainID          string
	StartedAt        int64
	CompletedAt      int64
	TerminalStatus   string
	TerminalReason   string
	TmuxSession      string
	PaneID           string
	RootAgentType    string
	RootEventName    string
	RootReason       string
	LatestStepKind   string
	LatestDecision   string
	LatestStepReason string
}

type TraceStep struct {
	StepID        string
	ChainID       string
	ParentStepID  string
	Seq           int
	Kind          string
	TmuxSession   string
	PaneID        string
	AgentType     string
	FrameID       string
	ParentFrameID string
	EventName     string
	Decision      string
	Reason        string
	PayloadJSON   string
	BeforeJSON    string
	AfterJSON     string
	CreatedAt     int64
}

type TraceRecord struct {
	Chain TraceChain
	Steps []TraceStep
}

type TraceListFilter struct {
	Session   string
	Pane      string
	AgentType string
	EventName string
	Limit     int
	Cursor    string
	Before    int64
}

type TraceChainPage struct {
	Chains      []TraceChain
	NextCursor  string
}

type TraceStore struct {
	db        *sql.DB
	maxChains int
	maxSteps  int
}

func migrateTraceDB(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS agent_trace_chains (
		chain_id TEXT PRIMARY KEY,
		started_at INTEGER NOT NULL,
		completed_at INTEGER NOT NULL,
		terminal_status TEXT NOT NULL DEFAULT '',
		terminal_reason TEXT NOT NULL DEFAULT '',
		tmux_session TEXT NOT NULL DEFAULT '',
		pane_id TEXT NOT NULL DEFAULT '',
		root_agent_type TEXT NOT NULL DEFAULT '',
		root_event_name TEXT NOT NULL DEFAULT '',
		root_reason TEXT NOT NULL DEFAULT '',
		latest_step_kind TEXT NOT NULL DEFAULT '',
		latest_decision TEXT NOT NULL DEFAULT '',
		latest_step_reason TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS agent_trace_steps (
		step_id TEXT PRIMARY KEY,
		chain_id TEXT NOT NULL,
		parent_step_id TEXT NOT NULL DEFAULT '',
		seq INTEGER NOT NULL,
		kind TEXT NOT NULL,
		tmux_session TEXT NOT NULL DEFAULT '',
		pane_id TEXT NOT NULL DEFAULT '',
		agent_type TEXT NOT NULL DEFAULT '',
		frame_id TEXT NOT NULL DEFAULT '',
		parent_frame_id TEXT NOT NULL DEFAULT '',
		event_name TEXT NOT NULL DEFAULT '',
		decision TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		payload_json TEXT NOT NULL DEFAULT '{}',
		before_json TEXT NOT NULL DEFAULT '{}',
		after_json TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL,
		FOREIGN KEY(chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE
	)`); err != nil {
		return err
	}
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_chains_started ON agent_trace_chains(started_at DESC, chain_id DESC)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_chains_session ON agent_trace_chains(tmux_session, started_at DESC, chain_id DESC)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_chains_pane ON agent_trace_chains(pane_id, started_at DESC, chain_id DESC)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_steps_chain_seq ON agent_trace_steps(chain_id, seq ASC)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_steps_created ON agent_trace_steps(created_at ASC)`)
	return nil
}
```

- [ ] **Step 4: Implement transactional save, read helpers, and dual retention**

Append to `internal/store/trace.go`:

```go
func (s *AgentEventStore) Traces() (*TraceStore, error) {
	if err := migrateTraceDB(s.db); err != nil {
		return nil, err
	}
	return &TraceStore{
		db:        s.db,
		maxChains: 10_000,
		maxSteps:  100_000,
	}, nil
}

func (s *TraceStore) SaveChain(record TraceRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO agent_trace_chains
		(chain_id, started_at, completed_at, terminal_status, terminal_reason, tmux_session, pane_id, root_agent_type, root_event_name, root_reason, latest_step_kind, latest_decision, latest_step_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.Chain.ChainID, record.Chain.StartedAt, record.Chain.CompletedAt, record.Chain.TerminalStatus, record.Chain.TerminalReason,
		record.Chain.TmuxSession, record.Chain.PaneID, record.Chain.RootAgentType, record.Chain.RootEventName, record.Chain.RootReason,
		record.Chain.LatestStepKind, record.Chain.LatestDecision, record.Chain.LatestStepReason,
	); err != nil {
		return err
	}

	for _, step := range record.Steps {
		if _, err := tx.Exec(`INSERT INTO agent_trace_steps
			(step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id, agent_type, frame_id, parent_frame_id, event_name, decision, reason, payload_json, before_json, after_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			step.StepID, step.ChainID, step.ParentStepID, step.Seq, step.Kind, step.TmuxSession, step.PaneID, step.AgentType, step.FrameID, step.ParentFrameID, step.EventName, step.Decision, step.Reason, step.PayloadJSON, step.BeforeJSON, step.AfterJSON, step.CreatedAt,
		); err != nil {
			return err
		}
	}

	if err := s.pruneByChainLimit(tx); err != nil {
		return err
	}
	if err := s.pruneByStepLimit(tx); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *TraceStore) pruneByChainLimit(tx *sql.Tx) error {
	if s.maxChains <= 0 {
		return nil
	}
	_, err := tx.Exec(`
		DELETE FROM agent_trace_chains
		WHERE chain_id IN (
			SELECT chain_id FROM agent_trace_chains
			ORDER BY started_at DESC, chain_id DESC
			LIMIT -1 OFFSET ?
		)
	`, s.maxChains)
	return err
}

func (s *TraceStore) pruneByStepLimit(tx *sql.Tx) error {
	if s.maxSteps <= 0 {
		return nil
	}
	for {
		var total int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps`).Scan(&total); err != nil {
			return err
		}
		if total <= s.maxSteps {
			return nil
		}
		var oldestChainID string
		if err := tx.QueryRow(`SELECT chain_id FROM agent_trace_chains ORDER BY started_at ASC, chain_id ASC LIMIT 1`).Scan(&oldestChainID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM agent_trace_chains WHERE chain_id = ?`, oldestChainID); err != nil {
			return err
		}
	}
}

func (s *TraceStore) ListChains(filter TraceListFilter) (TraceChainPage, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	beforeStartedAt := filter.Before
	beforeChainID := ""
	if beforeStartedAt == 0 && filter.Cursor != "" {
		var ok bool
		beforeStartedAt, beforeChainID, ok = decodeTraceCursor(filter.Cursor)
		if !ok {
			return TraceChainPage{}, fmt.Errorf("invalid trace cursor")
		}
	}

	query := `SELECT chain_id, started_at, completed_at, terminal_status, terminal_reason, tmux_session, pane_id, root_agent_type, root_event_name, root_reason, latest_step_kind, latest_decision, latest_step_reason
		FROM agent_trace_chains WHERE 1=1`
	args := []any{}
	if filter.Session != "" {
		query += ` AND tmux_session = ?`
		args = append(args, filter.Session)
	}
	if filter.Pane != "" {
		query += ` AND pane_id = ?`
		args = append(args, filter.Pane)
	}
	if filter.AgentType != "" {
		query += ` AND root_agent_type = ?`
		args = append(args, filter.AgentType)
	}
	if filter.EventName != "" {
		query += ` AND root_event_name = ?`
		args = append(args, filter.EventName)
	}
	if beforeStartedAt > 0 {
		query += ` AND (started_at < ? OR (started_at = ? AND chain_id < ?))`
		args = append(args, beforeStartedAt, beforeStartedAt, beforeChainID)
	}
	query += ` ORDER BY started_at DESC, chain_id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return TraceChainPage{}, err
	}
	defer rows.Close()

	var chains []TraceChain
	for rows.Next() {
		var chain TraceChain
		if err := rows.Scan(
			&chain.ChainID,
			&chain.StartedAt,
			&chain.CompletedAt,
			&chain.TerminalStatus,
			&chain.TerminalReason,
			&chain.TmuxSession,
			&chain.PaneID,
			&chain.RootAgentType,
			&chain.RootEventName,
			&chain.RootReason,
			&chain.LatestStepKind,
			&chain.LatestDecision,
			&chain.LatestStepReason,
		); err != nil {
			return TraceChainPage{}, err
		}
		chains = append(chains, chain)
	}
	if err := rows.Err(); err != nil {
		return TraceChainPage{}, err
	}

	nextCursor := ""
	if len(chains) > limit {
		last := chains[limit-1]
		nextCursor = encodeTraceCursor(last.StartedAt, last.ChainID)
		chains = chains[:limit]
	}
	return TraceChainPage{Chains: chains, NextCursor: nextCursor}, nil
}

func (s *TraceStore) GetChainRecord(chainID string) (*TraceRecord, error) {
	row := s.db.QueryRow(`SELECT chain_id, started_at, completed_at, terminal_status, terminal_reason, tmux_session, pane_id, root_agent_type, root_event_name, root_reason, latest_step_kind, latest_decision, latest_step_reason
		FROM agent_trace_chains WHERE chain_id = ?`, chainID)

	var chain TraceChain
	if err := row.Scan(
		&chain.ChainID,
		&chain.StartedAt,
		&chain.CompletedAt,
		&chain.TerminalStatus,
		&chain.TerminalReason,
		&chain.TmuxSession,
		&chain.PaneID,
		&chain.RootAgentType,
		&chain.RootEventName,
		&chain.RootReason,
		&chain.LatestStepKind,
		&chain.LatestDecision,
		&chain.LatestStepReason,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	rows, err := s.db.Query(`SELECT step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id, agent_type, frame_id, parent_frame_id, event_name, decision, reason, payload_json, before_json, after_json, created_at
		FROM agent_trace_steps WHERE chain_id = ? ORDER BY seq ASC`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []TraceStep
	for rows.Next() {
		var step TraceStep
		if err := rows.Scan(
			&step.StepID,
			&step.ChainID,
			&step.ParentStepID,
			&step.Seq,
			&step.Kind,
			&step.TmuxSession,
			&step.PaneID,
			&step.AgentType,
			&step.FrameID,
			&step.ParentFrameID,
			&step.EventName,
			&step.Decision,
			&step.Reason,
			&step.PayloadJSON,
			&step.BeforeJSON,
			&step.AfterJSON,
			&step.CreatedAt,
		); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &TraceRecord{Chain: chain, Steps: steps}, nil
}

func encodeTraceCursor(startedAt int64, chainID string) string {
	raw := strconv.FormatInt(startedAt, 10) + ":" + chainID
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func decodeTraceCursor(cursor string) (int64, string, bool) {
	buf, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", false
	}
	parts := strings.SplitN(string(buf), ":", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	startedAt, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return startedAt, parts[1], true
}
```

- [ ] **Step 5: Run the storage tests to verify they pass**

Run: `go test ./internal/store -run 'TraceStore' -count=1`

Expected: PASS.

- [ ] **Step 6: Run formatting and broader store tests**

Run: `gofmt -w internal/store/trace.go internal/store/trace_test.go internal/store/agent_event.go && go test ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/store/trace.go internal/store/trace_test.go internal/store/agent_event.go
git commit -m "feat(agent-store): add transactional hook trace store"
```

---

## Task 2: Add In-Memory Hook Trace Collector and Async Sink

**Files:**
- Create: `internal/module/agent/trace.go`
- Test: `internal/module/agent/trace_test.go`
- Modify: `internal/module/agent/module.go`
- Modify: `internal/module/agent/handler.go`
- Modify: `internal/module/agent/frame_ops.go`

- [ ] **Step 1: Write the failing instrumentation tests**

Create `internal/module/agent/trace_test.go` with:

```go
package agent

import (
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
)

func TestHandleEvent_PersistsCompletedHookTrace(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.registerProvider("codex")
	env.registerSession("work", "session-code-1")

	verifyEventFn = func(_ *Module, _ EventRequest) verifyDecision {
		return verifyDecision{Accepted: true}
	}
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1200, ExePath: "/usr/local/bin/codex"}, nil
	}
	t.Cleanup(func() {
		verifyEventFn = defaultVerifyEvent
		readProcessInfoFn = agentpkg.ReadProcessInfo
	})

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"UserPromptSubmit",
		"raw_event":{},
		"agent_type":"codex",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`))
	w := httptest.NewRecorder()

	env.module.handleEvent(w, req)
	env.traceSink.FlushForTest()

	page, err := env.traces.ListChains(store.TraceListFilter{Session: "work", Limit: 10})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != 1 {
		t.Fatalf("len(page.Chains) = %d, want 1", len(page.Chains))
	}
	if page.Chains[0].TerminalStatus != "completed" {
		t.Fatalf("TerminalStatus = %q, want completed", page.Chains[0].TerminalStatus)
	}
	if page.Chains[0].LatestStepKind != "emit" {
		t.Fatalf("LatestStepKind = %q, want emit", page.Chains[0].LatestStepKind)
	}

	record, err := env.traces.GetChainRecord(page.Chains[0].ChainID)
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got := len(record.Steps); got < 5 {
		t.Fatalf("len(record.Steps) = %d, want >= 5", got)
	}
	if record.Steps[1].ParentStepID != record.Steps[0].StepID {
		t.Fatalf("verify parent = %q, want trigger step id", record.Steps[1].ParentStepID)
	}
}

func TestHandleEvent_VerifyRejectStillPersistsTerminalChain(t *testing.T) {
	env := newHandlerTestEnv(t)
	verifyEventFn = func(_ *Module, _ EventRequest) verifyDecision {
		return verifyDecision{Accepted: false, Reason: "pid_not_in_pane_tree"}
	}
	t.Cleanup(func() {
		verifyEventFn = defaultVerifyEvent
	})

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"Stop",
		"raw_event":{},
		"agent_type":"cc"
	}`))
	w := httptest.NewRecorder()

	env.module.handleEvent(w, req)
	env.traceSink.FlushForTest()

	page, err := env.traces.ListChains(store.TraceListFilter{Session: "work", Limit: 10})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != 1 {
		t.Fatalf("len(page.Chains) = %d, want 1", len(page.Chains))
	}
	if page.Chains[0].TerminalStatus != "completed" {
		t.Fatalf("TerminalStatus = %q, want completed", page.Chains[0].TerminalStatus)
	}
	if page.Chains[0].LatestDecision != "rejected" {
		t.Fatalf("LatestDecision = %q, want rejected", page.Chains[0].LatestDecision)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/module/agent -run 'Persist.*HookTrace|VerifyReject' -count=1`

Expected: FAIL with missing trace sink / collector wiring.

- [ ] **Step 3: Wire the module with a trace sink**

In `internal/module/agent/module.go`, extend the module:

```go
type Module struct {
	traces    *store.TraceStore
	traceSink *hookTraceSink
}
```

Update `New`:

```go
var traces *store.TraceStore
if events != nil {
	frames, _ = events.Frames()
	traces, _ = events.Traces()
}

m := &Module{
	traces: traces,
}
if traces != nil {
	m.traceSink = newHookTraceSink(traces)
}
```

- [ ] **Step 4: Implement the collector and sink**

Create `internal/module/agent/trace.go` with:

```go
package agent

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wake/purdex/internal/store"
)

type hookTraceSink struct {
	store *store.TraceStore
	queue chan store.TraceRecord
	wg    sync.WaitGroup
}

func newHookTraceSink(traces *store.TraceStore) *hookTraceSink {
	s := &hookTraceSink{
		store: traces,
		queue: make(chan store.TraceRecord, 256),
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for record := range s.queue {
			if err := s.store.SaveChain(record); err != nil {
				log.Printf("[agent][trace] save chain: %v", err)
			}
		}
	}()
	return s
}

func (s *hookTraceSink) Enqueue(record store.TraceRecord) {
	if s == nil {
		return
	}
	select {
	case s.queue <- record:
	default:
		log.Printf("[agent][trace] drop chain %s: queue full", record.Chain.ChainID)
	}
}

func (s *hookTraceSink) FlushForTest() {
	if s == nil {
		return
	}
	for len(s.queue) > 0 {
		time.Sleep(5 * time.Millisecond)
	}
}

type hookTraceCollector struct {
	sink         *hookTraceSink
	chain        store.TraceChain
	steps        []store.TraceStep
	nextSeq      int
	triggerStep  string
	verifyStep   string
	frameStep    string
	projectStep  string
	finished     bool
}

func beginHookTrace(sink *hookTraceSink, req EventRequest) *hookTraceCollector {
	if sink == nil {
		return nil
	}
	c := &hookTraceCollector{
		sink: sink,
		chain: store.TraceChain{
			ChainID:       uuid.NewString(),
			StartedAt:     time.Now().UnixNano(),
			TmuxSession:   req.TmuxSession,
			PaneID:        req.TmuxPaneID,
			RootAgentType: req.AgentType,
			RootEventName: req.EventName,
			RootReason:    "hook_post",
		},
		nextSeq: 1,
	}
	c.triggerStep = c.append("", "trigger", req.AgentType, "", "", req.EventName, "received", "hook_post", req, nil, nil)
	return c
}

func (c *hookTraceCollector) append(parentStepID, kind, agentType, frameID, parentFrameID, eventName, decision, reason string, payload, before, after any) string {
	if c == nil {
		return ""
	}
	stepID := uuid.NewString()
	c.steps = append(c.steps, store.TraceStep{
		StepID:        stepID,
		ChainID:       c.chain.ChainID,
		ParentStepID:  parentStepID,
		Seq:           c.nextSeq,
		Kind:          kind,
		TmuxSession:   c.chain.TmuxSession,
		PaneID:        c.chain.PaneID,
		AgentType:     agentType,
		FrameID:       frameID,
		ParentFrameID: parentFrameID,
		EventName:     eventName,
		Decision:      decision,
		Reason:        reason,
		PayloadJSON:   mustJSON(payload),
		BeforeJSON:    mustJSON(before),
		AfterJSON:     mustJSON(after),
		CreatedAt:     time.Now().UnixNano(),
	})
	c.nextSeq++
	c.chain.LatestStepKind = kind
	c.chain.LatestDecision = decision
	c.chain.LatestStepReason = reason
	return stepID
}
```

Continue the same file with step helpers:

```go
type ProjectionTraceSummary struct {
	Decision string
	Reason   string
	Before   any
	After    any
}

func (c *hookTraceCollector) Verify(req EventRequest, decision, reason string, after any) {
	c.verifyStep = c.append(c.triggerStep, "verify", req.AgentType, "", "", req.EventName, decision, reason, req, nil, after)
}

func (c *hookTraceCollector) Frame(req EventRequest, meta FrameTraceMeta) {
	c.frameStep = c.append(c.verifyStep, "frame", req.AgentType, meta.FrameID, meta.ParentFrameID, req.EventName, meta.Decision, meta.Reason, req, meta.Before, meta.After)
}

func (c *hookTraceCollector) Projection(req EventRequest, summary ProjectionTraceSummary) {
	parent := c.frameStep
	if parent == "" {
		parent = c.verifyStep
	}
	c.projectStep = c.append(parent, "projection", req.AgentType, "", "", req.EventName, summary.Decision, summary.Reason, req, summary.Before, summary.After)
}

func (c *hookTraceCollector) Emit(normalized normalizedEvent, decision, reason string) {
	parent := c.projectStep
	if parent == "" {
		parent = c.verifyStep
	}
	c.append(parent, "emit", normalized.AgentType, "", "", normalized.RawEventName, decision, reason, normalized, nil, normalized)
}

func (c *hookTraceCollector) Finish(status, reason string) {
	if c == nil || c.finished {
		return
	}
	c.finished = true
	c.chain.CompletedAt = time.Now().UnixNano()
	c.chain.TerminalStatus = status
	c.chain.TerminalReason = reason
	c.sink.Enqueue(store.TraceRecord{Chain: c.chain, Steps: c.steps})
}

func mustJSON(v any) string {
	if v == nil {
		return `{}`
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return `{}`
	}
	return string(buf)
}

func summarizeProjectionChange(before, after *SessionProjection) ProjectionTraceSummary {
	return ProjectionTraceSummary{
		Decision: "projection_changed",
		Reason:   "frame_upserted",
		Before:   summarizeProjection(before),
		After:    summarizeProjection(after),
	}
}

func summarizeProjection(p *SessionProjection) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return map[string]any{
		"pane_id":          p.TmuxPaneID,
		"primary_frame_id": p.PrimaryFrame.FrameID,
		"top_frame_id":     p.TopFrame.FrameID,
		"top_agent_type":   p.TopFrame.AgentType,
		"subagent_count":   len(p.Subagents),
	}
}
```

- [ ] **Step 5: Move trace decision points to the real authority points**

In `internal/module/agent/frame_ops.go`, introduce a narrow metadata return instead of trace writes:

```go
type FrameTraceMeta struct {
	FrameID       string
	ParentFrameID string
	Decision      string
	Reason        string
	Before        any
	After         any
}

func (m *Module) applyFrameEvent(req EventRequest, result agentpkg.DeriveResult, broadcastTs int64) (*SessionProjection, FrameTraceMeta, error)
```

Rules for this change:

- `applyFrameEvent` returns frame mutation details only.
- Do not emit projection trace from `projectPane` inside `applyFrameEvent`.
- `handler.go` becomes the only place that appends `projection` and `emit` steps.

In `internal/module/agent/handler.go`, wire the collector:

```go
trace := beginHookTrace(m.traceSink, req)
traceFinished := false
defer func() {
	if !traceFinished {
		trace.Finish("aborted", "handler_return")
	}
}()

decision := verifyEventFn(m, req)
if !decision.Accepted {
	trace.Verify(req, "rejected", decision.Reason, map[string]any{"reason": decision.Reason})
	trace.Finish("completed", "verify_rejected")
	traceFinished = true
	writeVerifyRejected(w, req, decision.Reason)
	return
}
trace.Verify(req, "accepted", "verify_passed", map[string]any{"decision": "accepted"})

projection, frameMeta, err := m.applyFrameEvent(req, result, broadcastTs)
if err != nil {
	trace.Finish("aborted", "frame_apply_failed")
	traceFinished = true
	http.Error(w, "apply frame event failed", http.StatusInternalServerError)
	return
}
trace.Frame(req, frameMeta)

sessionProjection, err := m.projectionForSession(req.TmuxSession)
if err != nil {
	trace.Finish("aborted", "projection_failed")
	traceFinished = true
	http.Error(w, "projection failed", http.StatusInternalServerError)
	return
}
trace.Projection(req, summarizeProjectionChange(projection, sessionProjection))
```

For emit, instrument the real broadcast point:

```go
normalized := buildProjectionNormalized(sessionProjection, req.AgentType, req.EventName, broadcastTs, result)
if sessionCode == "" {
	trace.Emit(normalized, "skipped", "session_code_missing")
	trace.Finish("completed", "emit_skipped")
	traceFinished = true
	return
}

m.broadcastToSession(sessionCode, normalized)
trace.Emit(normalized, "broadcasted", "session_code_resolved")
trace.Finish("completed", "emit_broadcasted")
traceFinished = true
```

- [ ] **Step 6: Run the targeted instrumentation tests**

Run: `go test ./internal/module/agent -run 'Persist.*HookTrace|VerifyReject' -count=1`

Expected: PASS.

- [ ] **Step 7: Run broader agent tests and format touched files**

Run: `gofmt -w internal/module/agent/trace.go internal/module/agent/trace_test.go internal/module/agent/module.go internal/module/agent/handler.go internal/module/agent/frame_ops.go && go test ./internal/module/agent -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/module/agent/trace.go internal/module/agent/trace_test.go internal/module/agent/module.go internal/module/agent/handler.go internal/module/agent/frame_ops.go
git commit -m "feat(agent): persist hook trace chains"
```

---

## Task 3: Expose Typed Monitor APIs

**Files:**
- Create: `internal/module/agent/monitor.go`
- Test: `internal/module/agent/monitor_test.go`
- Modify: `internal/module/agent/module.go`

- [ ] **Step 1: Write the failing monitor API tests**

Create `internal/module/agent/monitor_test.go` with:

```go
package agent

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/wake/purdex/internal/store"
)

func TestHandleMonitorChains_ReturnsPageWithLatestStepSummary(t *testing.T) {
	env := newHandlerTestEnv(t)
	err := env.traces.SaveChain(store.TraceRecord{
		Chain: store.TraceChain{
			ChainID:          "chain-1",
			StartedAt:        100,
			CompletedAt:      120,
			TerminalStatus:   "completed",
			TerminalReason:   "emit_broadcasted",
			TmuxSession:      "work",
			PaneID:           "%7",
			RootAgentType:    "codex",
			RootEventName:    "UserPromptSubmit",
			RootReason:       "hook_post",
			LatestStepKind:   "emit",
			LatestDecision:   "broadcasted",
			LatestStepReason: "session_code_resolved",
		},
	})
	if err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/agent/monitor/chains?session=work&limit=10", nil)
	w := httptest.NewRecorder()
	env.module.handleMonitorChains(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Chains []struct {
			ChainID        string `json:"chain_id"`
			LatestStepKind string `json:"latest_step_kind"`
		} `json:"chains"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(body.Chains) != 1 || body.Chains[0].LatestStepKind != "emit" {
		t.Fatalf("body = %+v, want emit summary", body)
	}
}

func TestHandleMonitorChain_ReturnsOrderedStepTree(t *testing.T) {
	env := newHandlerTestEnv(t)
	if err := env.traces.SaveChain(store.TraceRecord{
		Chain: store.TraceChain{
			ChainID:        "chain-2",
			StartedAt:      100,
			CompletedAt:    130,
			TerminalStatus: "completed",
		},
		Steps: []store.TraceStep{
			{StepID: "s1", ChainID: "chain-2", Seq: 1, Kind: "trigger", CreatedAt: 100},
			{StepID: "s2", ChainID: "chain-2", ParentStepID: "s1", Seq: 2, Kind: "verify", CreatedAt: 110},
			{StepID: "s3", ChainID: "chain-2", ParentStepID: "s2", Seq: 3, Kind: "emit", CreatedAt: 120},
		},
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/agent/monitor/chains/chain-2", nil)
	req.SetPathValue("id", "chain-2")
	w := httptest.NewRecorder()
	env.module.handleMonitorChain(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		StepTree []struct {
			Step struct {
				StepID string `json:"step_id"`
				Kind   string `json:"kind"`
			} `json:"step"`
			Children []struct {
				Step struct {
					Kind string `json:"kind"`
				} `json:"step"`
			} `json:"children"`
		} `json:"step_tree"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(body.StepTree) != 1 || body.StepTree[0].Step.Kind != "trigger" {
		t.Fatalf("body = %+v, want trigger root", body)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/module/agent -run 'HandleMonitor' -count=1`

Expected: FAIL because the handlers and DTOs do not exist yet.

- [ ] **Step 3: Implement monitor DTOs and tree builder**

Create `internal/module/agent/monitor.go` with:

```go
package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/store"
)

type MonitorChainSummary struct {
	ChainID          string `json:"chain_id"`
	StartedAt        int64  `json:"started_at"`
	CompletedAt      int64  `json:"completed_at"`
	TerminalStatus   string `json:"terminal_status"`
	TerminalReason   string `json:"terminal_reason"`
	TmuxSession      string `json:"tmux_session"`
	PaneID           string `json:"pane_id"`
	RootAgentType    string `json:"root_agent_type"`
	RootEventName    string `json:"root_event_name"`
	RootReason       string `json:"root_reason"`
	LatestStepKind   string `json:"latest_step_kind"`
	LatestDecision   string `json:"latest_decision"`
	LatestStepReason string `json:"latest_step_reason"`
}

type MonitorStepNode struct {
	Step     store.TraceStep    `json:"step"`
	Children []*MonitorStepNode `json:"children"`
}

type MonitorProjectionSummary struct {
	TmuxSession       string `json:"tmux_session"`
	PaneID            string `json:"pane_id"`
	PrimaryFrameID    string `json:"primary_frame_id"`
	TopFrameID        string `json:"top_frame_id"`
	TopAgentType      string `json:"top_agent_type"`
	LatestChainID     string `json:"latest_chain_id"`
}

func buildStepTree(steps []store.TraceStep) []*MonitorStepNode {
	nodes := make(map[string]*MonitorStepNode, len(steps))
	for _, step := range steps {
		stepCopy := step
		nodes[step.StepID] = &MonitorStepNode{Step: stepCopy, Children: []*MonitorStepNode{}}
	}
	roots := make([]*MonitorStepNode, 0)
	for _, step := range steps {
		node := nodes[step.StepID]
		if step.ParentStepID == "" {
			roots = append(roots, node)
			continue
		}
		parent := nodes[step.ParentStepID]
		if parent == nil {
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
	}
	return roots
}

func parseBefore(raw string) int64 {
	if raw == "" {
		return 0
	}
	n, _ := strconv.ParseInt(raw, 10, 64)
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func encodeCursor(startedAt int64, chainID string) string {
	raw := strconv.FormatInt(startedAt, 10) + ":" + chainID
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(raw string) (int64, string, bool) {
	buf, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return 0, "", false
	}
	parts := strings.SplitN(string(buf), ":", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	startedAt, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return startedAt, parts[1], true
}

func (m *Module) monitorProjectionSummary(session, pane string) (*MonitorProjectionSummary, error) {
	var projection *SessionProjection
	var err error
	switch {
	case session != "":
		projection, err = m.projectionForSession(session)
	case pane != "":
		projection, err = m.projectPane(pane)
	default:
		return nil, fmt.Errorf("missing session or pane")
	}
	if err != nil {
		return nil, err
	}

	page, err := m.traces.ListChains(store.TraceListFilter{Session: session, Pane: pane, Limit: 1})
	if err != nil {
		return nil, err
	}
	latestChainID := ""
	if len(page.Chains) > 0 {
		latestChainID = page.Chains[0].ChainID
	}
	if projection == nil {
		return &MonitorProjectionSummary{
			TmuxSession:   session,
			PaneID:        pane,
			LatestChainID: latestChainID,
		}, nil
	}
	return &MonitorProjectionSummary{
		TmuxSession:    session,
		PaneID:         projection.TmuxPaneID,
		PrimaryFrameID: projection.PrimaryFrame.FrameID,
		TopFrameID:     projection.TopFrame.FrameID,
		TopAgentType:   projection.TopFrame.AgentType,
		LatestChainID:  latestChainID,
	}, nil
}
```

- [ ] **Step 4: Implement handlers against typed DTOs**

Continue `internal/module/agent/monitor.go`:

```go
func (m *Module) handleMonitorChains(w http.ResponseWriter, r *http.Request) {
	if m.traces == nil {
		http.Error(w, `{"error":"trace store unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := m.traces.ListChains(store.TraceListFilter{
		Session:   r.URL.Query().Get("session"),
		Pane:      r.URL.Query().Get("pane"),
		AgentType: r.URL.Query().Get("agent_type"),
		EventName: r.URL.Query().Get("event_name"),
		Limit:     limit,
		Cursor:    r.URL.Query().Get("cursor"),
		Before:    parseBefore(r.URL.Query().Get("before")),
	})
	if err != nil {
		http.Error(w, `{"error":"list chains failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"chains":      page.Chains,
		"next_cursor": page.NextCursor,
	})
}

func (m *Module) handleMonitorChain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	record, err := m.traces.GetChainRecord(id)
	if err != nil {
		http.Error(w, `{"error":"get chain failed"}`, http.StatusInternalServerError)
		return
	}
	if record == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{
		"chain":     record.Chain,
		"step_tree": buildStepTree(record.Steps),
	})
}

func (m *Module) handleMonitorProjection(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	pane := r.URL.Query().Get("pane")
	summary, err := m.monitorProjectionSummary(session, pane)
	if err != nil {
		http.Error(w, `{"error":"projection failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"projection": summary})
}
```

- [ ] **Step 5: Register the routes**

In `internal/module/agent/module.go`, add:

```go
mux.HandleFunc("GET /api/agent/monitor/chains", m.handleMonitorChains)
mux.HandleFunc("GET /api/agent/monitor/chains/{id}", m.handleMonitorChain)
mux.HandleFunc("GET /api/agent/monitor/projection", m.handleMonitorProjection)
```

- [ ] **Step 6: Run the monitor API tests**

Run: `go test ./internal/module/agent -run 'HandleMonitor' -count=1`

Expected: PASS.

- [ ] **Step 7: Run broader agent tests and format touched files**

Run: `gofmt -w internal/module/agent/monitor.go internal/module/agent/monitor_test.go internal/module/agent/module.go && go test ./internal/module/agent -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/module/agent/monitor.go internal/module/agent/monitor_test.go internal/module/agent/module.go
git commit -m "feat(agent): add typed hook trace monitor api"
```

---

## Task 4: Add Typed SPA Monitor UI with Split Components

**Files:**
- Modify: `spa/src/lib/host-api.ts`
- Create: `spa/src/components/settings/tmux-agent-monitor/ChainList.tsx`
- Create: `spa/src/components/settings/tmux-agent-monitor/StepTree.tsx`
- Create: `spa/src/components/settings/tmux-agent-monitor/StepInspector.tsx`
- Create: `spa/src/components/settings/TmuxAgentMonitorSection.tsx`
- Test: `spa/src/components/settings/TmuxAgentMonitorSection.test.tsx`

- [ ] **Step 1: Write the failing section test**

Create `spa/src/components/settings/TmuxAgentMonitorSection.test.tsx` with:

```tsx
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import * as api from '../../lib/host-api'
import { TmuxAgentMonitorSection } from './TmuxAgentMonitorSection'

describe('TmuxAgentMonitorSection', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('renders chain list, tree, selected step inspector, and projection summary', async () => {
    vi.spyOn(api, 'fetchAgentMonitorChains').mockResolvedValue({
      chains: [{
        chain_id: 'chain-1',
        started_at: 100,
        completed_at: 120,
        terminal_status: 'completed',
        terminal_reason: 'emit_broadcasted',
        tmux_session: 'work',
        pane_id: '%7',
        root_agent_type: 'codex',
        root_event_name: 'UserPromptSubmit',
        root_reason: 'hook_post',
        latest_step_kind: 'emit',
        latest_decision: 'broadcasted',
        latest_step_reason: 'session_code_resolved',
      }],
      next_cursor: '',
    })

    vi.spyOn(api, 'fetchAgentMonitorChain').mockResolvedValue({
      chain: {
        chain_id: 'chain-1',
        started_at: 100,
        completed_at: 120,
        terminal_status: 'completed',
        terminal_reason: 'emit_broadcasted',
        tmux_session: 'work',
        pane_id: '%7',
        root_agent_type: 'codex',
        root_event_name: 'UserPromptSubmit',
        root_reason: 'hook_post',
        latest_step_kind: 'emit',
        latest_decision: 'broadcasted',
        latest_step_reason: 'session_code_resolved',
      },
      step_tree: [{
        step: {
          step_id: 's1',
          chain_id: 'chain-1',
          parent_step_id: '',
          seq: 1,
          kind: 'trigger',
          tmux_session: 'work',
          pane_id: '%7',
          agent_type: 'codex',
          frame_id: '',
          parent_frame_id: '',
          event_name: 'UserPromptSubmit',
          decision: 'received',
          reason: 'hook_post',
          payload_json: '{"tmux_session":"work"}',
          before_json: '{}',
          after_json: '{}',
          created_at: 100,
        },
        children: [{
          step: {
            step_id: 's2',
            chain_id: 'chain-1',
            parent_step_id: 's1',
            seq: 2,
            kind: 'verify',
            tmux_session: 'work',
            pane_id: '%7',
            agent_type: 'codex',
            frame_id: '',
            parent_frame_id: '',
            event_name: 'UserPromptSubmit',
            decision: 'accepted',
            reason: 'verify_passed',
            payload_json: '{}',
            before_json: '{}',
            after_json: '{"decision":"accepted"}',
            created_at: 101,
          },
          children: [],
        }],
      }],
    })

    vi.spyOn(api, 'fetchAgentMonitorProjection').mockResolvedValue({
      projection: {
        tmux_session: 'work',
        pane_id: '%7',
        primary_frame_id: 'frame-cc',
        top_frame_id: 'frame-codex',
        top_agent_type: 'codex',
        latest_chain_id: 'chain-1',
      },
    })

    render(<TmuxAgentMonitorSection />)

    await waitFor(() => expect(screen.getByText('UserPromptSubmit')).toBeTruthy())
    fireEvent.click(screen.getByText('verify'))

    await waitFor(() => expect(screen.getByText('verify_passed')).toBeTruthy())
    expect(screen.getByTestId('monitor-step-payload').textContent).toContain('{}')
    expect(screen.getByTestId('monitor-projection-summary').textContent).toContain('frame-codex')
  })
})
```

- [ ] **Step 2: Run the section test to verify it fails**

Run: `cd spa && npx vitest run src/components/settings/TmuxAgentMonitorSection.test.tsx`

Expected: FAIL because the section and typed helpers do not exist yet.

- [ ] **Step 3: Add typed host API helpers**

Append to `spa/src/lib/host-api.ts`:

```ts
export interface AgentMonitorChainSummary {
  chain_id: string
  started_at: number
  completed_at: number
  terminal_status: string
  terminal_reason: string
  tmux_session: string
  pane_id: string
  root_agent_type: string
  root_event_name: string
  root_reason: string
  latest_step_kind: string
  latest_decision: string
  latest_step_reason: string
}

export interface AgentMonitorStep {
  step_id: string
  chain_id: string
  parent_step_id: string
  seq: number
  kind: string
  tmux_session: string
  pane_id: string
  agent_type: string
  frame_id: string
  parent_frame_id: string
  event_name: string
  decision: string
  reason: string
  payload_json: string
  before_json: string
  after_json: string
  created_at: number
}

export interface AgentMonitorStepNode {
  step: AgentMonitorStep
  children: AgentMonitorStepNode[]
}

export interface AgentMonitorProjectionSummary {
  tmux_session: string
  pane_id: string
  primary_frame_id: string
  top_frame_id: string
  top_agent_type: string
  latest_chain_id: string
}

export async function fetchAgentMonitorChains(hostId: string, query = new URLSearchParams()): Promise<{ chains: AgentMonitorChainSummary[]; next_cursor: string }> {
  const qs = query.toString()
  const res = await hostFetch(hostId, `/api/agent/monitor/chains${qs ? `?${qs}` : ''}`)
  if (!res.ok) throw new Error(`fetchAgentMonitorChains failed: ${res.status}`)
  return res.json()
}

export async function fetchAgentMonitorChain(hostId: string, chainId: string): Promise<{ chain: AgentMonitorChainSummary; step_tree: AgentMonitorStepNode[] }> {
  const res = await hostFetch(hostId, `/api/agent/monitor/chains/${chainId}`)
  if (!res.ok) throw new Error(`fetchAgentMonitorChain failed: ${res.status}`)
  return res.json()
}

export async function fetchAgentMonitorProjection(hostId: string, query: URLSearchParams): Promise<{ projection: AgentMonitorProjectionSummary | null }> {
  const res = await hostFetch(hostId, `/api/agent/monitor/projection?${query.toString()}`)
  if (!res.ok) throw new Error(`fetchAgentMonitorProjection failed: ${res.status}`)
  return res.json()
}
```

- [ ] **Step 4: Implement split monitor components**

Create `spa/src/components/settings/tmux-agent-monitor/ChainList.tsx`:

```tsx
import type { AgentMonitorChainSummary } from '../../../lib/host-api'

interface ChainListProps {
  chains: AgentMonitorChainSummary[]
  selectedChainId: string
  onSelect: (chainId: string) => void
}

export function ChainList({ chains, selectedChainId, onSelect }: ChainListProps) {
  return (
    <div className='w-72 border border-border-default rounded-md overflow-hidden'>
      <div className='px-3 py-2 border-b border-border-subtle text-text-muted'>傳遞鏈</div>
      <div className='divide-y divide-border-subtle'>
        {chains.map((chain) => (
          <button
            key={chain.chain_id}
            type='button'
            onClick={() => onSelect(chain.chain_id)}
            className={chain.chain_id === selectedChainId ? 'w-full text-left px-3 py-2 bg-surface-hover' : 'w-full text-left px-3 py-2 hover:bg-surface-hover'}
          >
            <div className='text-text-primary'>{chain.root_event_name}</div>
            <div className='text-xs text-text-muted'>{chain.root_agent_type} · {chain.tmux_session} · {chain.pane_id}</div>
            <div className='text-xs text-text-muted'>{chain.latest_step_kind} · {chain.latest_decision}</div>
          </button>
        ))}
      </div>
    </div>
  )
}
```

Create `spa/src/components/settings/tmux-agent-monitor/StepTree.tsx`:

```tsx
import type { AgentMonitorStep, AgentMonitorStepNode } from '../../../lib/host-api'

interface StepTreeProps {
  nodes: AgentMonitorStepNode[]
  selectedStepId: string
  onSelect: (step: AgentMonitorStep) => void
}

function StepNode({ node, selectedStepId, onSelect, depth }: StepTreeProps & { node: AgentMonitorStepNode; depth: number }) {
  return (
    <div style={{ paddingLeft: `${depth * 16}px` }}>
      <button
        type='button'
        onClick={() => onSelect(node.step)}
        className={node.step.step_id === selectedStepId ? 'w-full text-left border border-border-default rounded-md px-3 py-2 bg-surface-hover' : 'w-full text-left border border-border-subtle rounded-md px-3 py-2'}
      >
        <div className='text-text-primary'>{node.step.kind}</div>
        <div className='text-xs text-text-muted'>{node.step.decision} · {node.step.reason}</div>
        <div className='text-xs text-text-muted'>{node.step.agent_type || '-'} · {node.step.frame_id || '-'} · {node.step.parent_frame_id || '-'}</div>
      </button>
      <div className='mt-2 space-y-2'>
        {node.children.map((child) => (
          <StepNode key={child.step.step_id} node={child} selectedStepId={selectedStepId} onSelect={onSelect} nodes={[]} depth={depth + 1} />
        ))}
      </div>
    </div>
  )
}

export function StepTree({ nodes, selectedStepId, onSelect }: StepTreeProps) {
  return (
    <div className='flex-1 border border-border-default rounded-md p-3 space-y-2'>
      {nodes.map((node) => (
        <StepNode key={node.step.step_id} node={node} selectedStepId={selectedStepId} onSelect={onSelect} nodes={[]} depth={0} />
      ))}
    </div>
  )
}
```

Create `spa/src/components/settings/tmux-agent-monitor/StepInspector.tsx`:

```tsx
import type { AgentMonitorProjectionSummary, AgentMonitorStep } from '../../../lib/host-api'

interface StepInspectorProps {
  selectedStep: AgentMonitorStep | null
  projection: AgentMonitorProjectionSummary | null
  error: string
}

export function StepInspector({ selectedStep, projection, error }: StepInspectorProps) {
  return (
    <div className='w-[28rem] border border-border-default rounded-md p-3 space-y-3'>
      <div>
        <div className='text-xs text-text-muted'>選取步驟</div>
        <pre data-testid='monitor-step-payload' className='text-xs whitespace-pre-wrap'>{selectedStep?.payload_json ?? '{}'}</pre>
        <pre className='text-xs whitespace-pre-wrap'>{selectedStep?.before_json ?? '{}'}</pre>
        <pre className='text-xs whitespace-pre-wrap'>{selectedStep?.after_json ?? '{}'}</pre>
      </div>
      <div>
        <div className='text-xs text-text-muted'>Projection</div>
        <pre data-testid='monitor-projection-summary' className='text-xs whitespace-pre-wrap'>{JSON.stringify(projection, null, 2)}</pre>
      </div>
      {error ? <div className='text-red-400'>{error}</div> : null}
    </div>
  )
}
```

Create `spa/src/components/settings/TmuxAgentMonitorSection.tsx`:

```tsx
import { useEffect, useMemo, useState } from 'react'
import {
  type AgentMonitorChainSummary,
  type AgentMonitorProjectionSummary,
  type AgentMonitorStep,
  type AgentMonitorStepNode,
  fetchAgentMonitorChain,
  fetchAgentMonitorChains,
  fetchAgentMonitorProjection,
} from '../../lib/host-api'
import { useHostStore } from '../../stores/useHostStore'
import { ChainList } from './tmux-agent-monitor/ChainList'
import { StepInspector } from './tmux-agent-monitor/StepInspector'
import { StepTree } from './tmux-agent-monitor/StepTree'

function firstStep(nodes: AgentMonitorStepNode[]): AgentMonitorStep | null {
  return nodes[0]?.step ?? null
}

export function TmuxAgentMonitorSection() {
  const hostId = useHostStore((s) => s.hostOrder[0] ?? '')
  const [chains, setChains] = useState<AgentMonitorChainSummary[]>([])
  const [selectedChainId, setSelectedChainId] = useState('')
  const [stepTree, setStepTree] = useState<AgentMonitorStepNode[]>([])
  const [selectedStep, setSelectedStep] = useState<AgentMonitorStep | null>(null)
  const [projection, setProjection] = useState<AgentMonitorProjectionSummary | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!hostId) return
    fetchAgentMonitorChains(hostId).then((res) => {
      setChains(res.chains)
      if (res.chains[0]) setSelectedChainId(res.chains[0].chain_id)
    }).catch((err) => setError(String(err)))
  }, [hostId])

  useEffect(() => {
    if (!hostId || !selectedChainId) return
    fetchAgentMonitorChain(hostId, selectedChainId).then(async (res) => {
      setStepTree(res.step_tree)
      setSelectedStep(firstStep(res.step_tree))
      if (res.chain.tmux_session) {
        const projectionRes = await fetchAgentMonitorProjection(hostId, new URLSearchParams({ session: res.chain.tmux_session }))
        setProjection(projectionRes.projection)
      } else {
        setProjection(null)
      }
    }).catch((err) => setError(String(err)))
  }, [hostId, selectedChainId])

  const selectedChain = useMemo(() => chains.find((chain) => chain.chain_id === selectedChainId) ?? null, [chains, selectedChainId])

  return (
    <div className='flex h-full gap-4 text-sm'>
      <ChainList chains={chains} selectedChainId={selectedChainId} onSelect={setSelectedChainId} />
      <div className='flex-1 space-y-3'>
        <div className='text-xs text-text-muted'>
          {selectedChain ? `${selectedChain.root_event_name} · ${selectedChain.terminal_status}` : 'No chain selected'}
        </div>
        <StepTree nodes={stepTree} selectedStepId={selectedStep?.step_id ?? ''} onSelect={setSelectedStep} />
      </div>
      <StepInspector selectedStep={selectedStep} projection={projection} error={error} />
    </div>
  )
}
```

- [ ] **Step 5: Run the section test**

Run: `cd spa && npx vitest run src/components/settings/TmuxAgentMonitorSection.test.tsx`

Expected: PASS.

- [ ] **Step 6: Run broader SPA tests for touched files**

Run: `cd spa && npx vitest run src/components/settings/TmuxAgentMonitorSection.test.tsx src/components/settings/SettingsSidebar.test.tsx`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add spa/src/lib/host-api.ts spa/src/components/settings/tmux-agent-monitor/ChainList.tsx spa/src/components/settings/tmux-agent-monitor/StepTree.tsx spa/src/components/settings/tmux-agent-monitor/StepInspector.tsx spa/src/components/settings/TmuxAgentMonitorSection.tsx spa/src/components/settings/TmuxAgentMonitorSection.test.tsx
git commit -m "feat(spa): add tmux agent hook monitor ui"
```

---

## Task 5: Register the Monitor Behind a Real Dev Gate

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Register the section with a real dev gate**

In `spa/src/lib/register-modules.tsx`, add the import:

```tsx
import { TmuxAgentMonitorSection } from '../components/settings/TmuxAgentMonitorSection'
```

Then add a dedicated gate:

```tsx
const showTmuxAgentMonitor = import.meta.env.DEV || caps.devUpdateEnabled

if (showTmuxAgentMonitor) {
  registerSettingsSection({
    id: 'tmux-agent-monitor',
    label: 'settings.section.tmux_agent_monitor',
    order: 21,
    component: TmuxAgentMonitorSection,
  })
}
```

Keep the existing `dev-environment` registration unchanged.

- [ ] **Step 2: Add locale strings**

In `spa/src/locales/en.json`, add:

```json
  "settings.section.tmux_agent_monitor": "Tmux Agent Monitor",
  "settings.monitor.chains": "Chains",
  "settings.monitor.step_tree": "Step Tree",
  "settings.monitor.inspect": "Inspect",
  "settings.monitor.selected_step": "Selected Step",
  "settings.monitor.projection": "Projection",
```

In `spa/src/locales/zh-TW.json`, add:

```json
  "settings.section.tmux_agent_monitor": "Tmux Agent Monitor",
  "settings.monitor.chains": "傳遞鏈",
  "settings.monitor.step_tree": "步驟樹",
  "settings.monitor.inspect": "檢視",
  "settings.monitor.selected_step": "選取步驟",
  "settings.monitor.projection": "Projection",
```

- [ ] **Step 3: Add changelog entry**

Append under the current unreleased heading in `CHANGELOG.md`:

```md
- **Hook trace monitor (dev-only)**: daemon 新增 hook-only trace rail，將 trigger / verify / frame / projection / emit 傳遞鏈持久化到 SQLite；settings 新增 Tmux Agent Monitor 頁面，可檢視階層 step tree、selected-step JSON inspector 與 projection summary。
```

- [ ] **Step 4: Run targeted verification**

Run: `cd spa && npx vitest run src/components/settings/TmuxAgentMonitorSection.test.tsx src/components/settings/SettingsSidebar.test.tsx src/components/SettingsPage.test.tsx`

Expected: PASS.

- [ ] **Step 5: Run final lint, typecheck, and daemon tests**

Run: `cd spa && pnpm run lint && npx tsc --noEmit && cd .. && go test ./internal/store ./internal/module/agent -count=1`

Expected: PASS.

If `pnpm run lint` reports unrelated pre-existing issues, confirm touched files directly with:

```bash
cd spa && pnpm exec eslint src/lib/host-api.ts src/components/settings/TmuxAgentMonitorSection.tsx src/components/settings/tmux-agent-monitor/ChainList.tsx src/components/settings/tmux-agent-monitor/StepTree.tsx src/components/settings/tmux-agent-monitor/StepInspector.tsx src/lib/register-modules.tsx
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/register-modules.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json CHANGELOG.md
git commit -m "feat(settings): register tmux agent monitor"
```

---

## Spec Coverage Check

- Hook-only trace persistence with SQLite: Task 1
- `trigger / verify / frame / projection / emit` chain capture: Task 2
- Projection trace anchored at `projectionForSession` and emit trace anchored at the real broadcast decision: Task 2
- `cursor` / `before` chain list API plus latest-step summary: Task 1 + Task 3
- Ordered step tree and selected-step inspector: Task 3 + Task 4
- Projection summary typed DTO instead of raw daemon model: Task 3 + Task 4
- Dev-only settings monitor behind an actual dev gate: Task 5

## Placeholder Scan

- No `TODO` / `TBD`
- All new files and modified files are named explicitly
- All test steps include concrete commands and expected outcomes
- No step depends on raw `any` / `unknown` monitor payloads
