package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
)

func openTestTraceStore(t *testing.T) *TraceStore {
	t.Helper()
	events := openTestAgentEventStore(t)
	traces, err := events.Traces()
	if err != nil {
		t.Fatalf("traces: %v", err)
	}
	return traces
}

func TestTraceStore_SaveAndGetChainRecord(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 10

	record := TraceRecord{
		Chain: TraceChain{
			ChainID:          "chain-1",
			StepCount:        99,
			StartedAt:        100,
			CompletedAt:      200,
			TerminalStatus:   "done",
			TerminalReason:   "completed",
			TmuxSession:      "proj-a",
			PaneID:           "%5",
			RootAgentType:    "cc",
			RootEventName:    "SessionStart",
			RootReason:       "bootstrap",
			LatestStepKind:   "decision",
			LatestDecision:   "continue",
			LatestStepReason: "ready",
		},
		Steps: []TraceStep{
			{
				StepID:      "step-1",
				ChainID:     "chain-1",
				Seq:         1,
				Kind:        "decision",
				TmuxSession: "proj-a",
				PaneID:      "%5",
				AgentType:   "cc",
				FrameID:     "frame-1",
				EventName:   "SessionStart",
				Decision:    "continue",
				Reason:      "ready",
				PayloadJSON: json.RawMessage(`{"status":"queued"}`),
				BeforeJSON:  json.RawMessage(`{"before":true}`),
				AfterJSON:   json.RawMessage(`{"after":true}`),
				CreatedAt:   101,
			},
			{
				StepID:        "step-2",
				ChainID:       "chain-1",
				ParentStepID:  "step-1",
				Seq:           2,
				Kind:          "terminal",
				TmuxSession:   "proj-a",
				PaneID:        "%5",
				AgentType:     "cc",
				FrameID:       "frame-1",
				ParentFrameID: "frame-0",
				EventName:     "Stop",
				Decision:      "done",
				Reason:        "completed",
				PayloadJSON:   json.RawMessage(`{"status":"done"}`),
				CreatedAt:     102,
			},
		},
	}

	if err := s.SaveChain(record); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	got, err := s.GetChainRecord("chain-1")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got == nil {
		t.Fatal("expected record, got nil")
	}
	if got.Chain.StepCount != 2 {
		t.Fatalf("step_count = %d, want 2", got.Chain.StepCount)
	}
	if got.Chain.TerminalStatus != "done" {
		t.Fatalf("terminal_status = %q, want done", got.Chain.TerminalStatus)
	}
	if got.Chain.RootAgentType != "cc" || got.Chain.LatestDecision != "done" || got.Chain.LatestStepKind != "terminal" {
		t.Fatalf("chain summary = %+v", got.Chain)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(got.Steps))
	}
	if got.Steps[0].Seq != 1 || got.Steps[0].Kind != "decision" {
		t.Fatalf("first step = %+v", got.Steps[0])
	}
	if got.Steps[1].ParentStepID != "step-1" {
		t.Fatalf("parent_step_id = %q, want step-1", got.Steps[1].ParentStepID)
	}
	if string(got.Steps[0].BeforeJSON) != `{"before":true}` || string(got.Steps[0].AfterJSON) != `{"after":true}` {
		t.Fatalf("payload fields = %+v", got.Steps[0])
	}
}

func TestTraceStore_MigratesLegacySchemaAndReadsListChains(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedLegacyTraceSchema(t, s.db)
	seedLegacyTraceData(t, s.db)

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces: %v", err)
	}
	store := &TraceStore{db: s.db, maxChains: 10, maxSteps: 10}

	page, err := store.ListChains(TraceListFilter{
		TmuxSession: "proj-legacy",
		PaneID:      "%9",
		AgentType:   "cc",
		EventName:   "Stop",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != 1 {
		t.Fatalf("chains = %d, want 1", len(page.Chains))
	}
	if page.Chains[0].StartedAt != 123 {
		t.Fatalf("started_at = %d, want 123", page.Chains[0].StartedAt)
	}
	if page.Chains[0].StepCount != 2 {
		t.Fatalf("step_count = %d, want 2", page.Chains[0].StepCount)
	}
}

func TestTraceStore_MigratesLegacyChainsWithoutStepsTable(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedLegacyTraceChainsOnly(t, s.db)

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces: %v", err)
	}

	store := &TraceStore{db: s.db, maxChains: 10, maxSteps: 10}
	page, err := store.ListChains(TraceListFilter{
		TmuxSession: "proj-legacy",
		PaneID:      "%9",
		AgentType:   "cc",
		EventName:   "Stop",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != 1 {
		t.Fatalf("chains = %d, want 1", len(page.Chains))
	}
	if page.Chains[0].StepCount != 0 {
		t.Fatalf("step_count = %d, want 0", page.Chains[0].StepCount)
	}
}

func TestTraceStore_MigratesLegacyStepsWithoutChainsTable(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedLegacyTraceStepsOnly(t, s.db)

	if _, err := s.Traces(); err == nil {
		t.Fatal("expected Traces to fail fast")
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps`).Scan(&count); err != nil {
		t.Fatalf("count legacy steps: %v", err)
	}
	if count != 1 {
		t.Fatalf("legacy step count = %d, want 1", count)
	}
}

func TestTraceStore_MigratesLegacyStepSchemaAndBlocksCrossChainParent(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedIntermediateTraceSchema(t, s.db)
	seedIntermediateTraceData(t, s.db)

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces: %v", err)
	}

	_, err := s.db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at
		) VALUES
		('b-1', 'chain-b', 'a-1', 1, 'decision', 'proj-a', '%1', 'cc', 'frame-b', '', 'Stop', 'continue', 'needs-parent', 'null', 'null', 'null', 3)
	`)
	if err == nil {
		t.Fatal("expected cross-chain parent insert to fail")
	}
}

func TestTraceStore_ListChains_PaginatesWithCursorAndBefore(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 20

	for i := 0; i < 4; i++ {
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:          fmt.Sprintf("chain-%d", i),
				StartedAt:        int64(100 + i),
				CompletedAt:      int64(200 + i),
				TerminalStatus:   "done",
				TerminalReason:   "ok",
				TmuxSession:      "proj-a",
				PaneID:           "%5",
				RootAgentType:    "cc",
				RootEventName:    "Stop",
				RootReason:       "bootstrap",
				LatestStepKind:   "terminal",
				LatestDecision:   "done",
				LatestStepReason: "ok",
			},
			Steps: []TraceStep{
				{
					StepID:      fmt.Sprintf("step-%d", i),
					ChainID:     fmt.Sprintf("chain-%d", i),
					Seq:         1,
					Kind:        "terminal",
					TmuxSession: "proj-a",
					PaneID:      "%5",
					AgentType:   "cc",
					EventName:   "Stop",
					Decision:    "done",
					Reason:      "ok",
					CreatedAt:   int64(100 + i),
				},
			},
		}
		if err := s.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}

	page1, err := s.ListChains(TraceListFilter{
		TmuxSession: "proj-a",
		PaneID:      "%5",
		AgentType:   "cc",
		EventName:   "Stop",
		Limit:       2,
	})
	if err != nil {
		t.Fatalf("ListChains page1: %v", err)
	}
	if len(page1.Chains) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1.Chains))
	}
	if page1.Chains[0].ChainID != "chain-3" || page1.Chains[1].ChainID != "chain-2" {
		t.Fatalf("page1 chains = [%s, %s]", page1.Chains[0].ChainID, page1.Chains[1].ChainID)
	}
	if page1.NextCursor == "" {
		t.Fatal("expected next cursor")
	}

	page2, err := s.ListChains(TraceListFilter{
		TmuxSession: "proj-a",
		PaneID:      "%5",
		AgentType:   "cc",
		EventName:   "Stop",
		Limit:       2,
		Cursor:      page1.NextCursor,
		Before:      true,
	})
	if err != nil {
		t.Fatalf("ListChains page2: %v", err)
	}
	if len(page2.Chains) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2.Chains))
	}
	if page2.Chains[0].ChainID != "chain-1" || page2.Chains[1].ChainID != "chain-0" {
		t.Fatalf("page2 chains = [%s, %s]", page2.Chains[0].ChainID, page2.Chains[1].ChainID)
	}
}

func TestTraceStore_RetentionDropsOldestWholeChainWhenStepCapExceeded(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 3

	first := TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-a",
			StartedAt:      1,
			CompletedAt:    2,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "a",
		},
		Steps: []TraceStep{
			{StepID: "a-1", ChainID: "chain-a", Seq: 1, Kind: "root", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 1},
			{StepID: "a-2", ChainID: "chain-a", ParentStepID: "a-1", Seq: 2, Kind: "decision", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 2},
		},
	}
	second := TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-b",
			StartedAt:      3,
			CompletedAt:    4,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "b",
		},
		Steps: []TraceStep{
			{StepID: "b-1", ChainID: "chain-b", Seq: 1, Kind: "root", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 3},
			{StepID: "b-2", ChainID: "chain-b", ParentStepID: "b-1", Seq: 2, Kind: "decision", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 4},
		},
	}

	if err := s.SaveChain(first); err != nil {
		t.Fatalf("SaveChain first: %v", err)
	}
	if err := s.SaveChain(second); err != nil {
		t.Fatalf("SaveChain second: %v", err)
	}

	gotA, err := s.GetChainRecord("chain-a")
	if err != nil {
		t.Fatalf("GetChainRecord a: %v", err)
	}
	if gotA != nil {
		t.Fatalf("expected chain-a to be evicted, got %+v", gotA.Chain)
	}

	gotB, err := s.GetChainRecord("chain-b")
	if err != nil {
		t.Fatalf("GetChainRecord b: %v", err)
	}
	if gotB == nil {
		t.Fatal("expected chain-b to remain")
	}
	if len(gotB.Steps) != 2 {
		t.Fatalf("chain-b steps = %d, want 2", len(gotB.Steps))
	}
}

func TestTraceStore_RetentionDropsOldestWholeChainWhenChainCapExceeded(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 2
	s.maxSteps = 100

	for i := 0; i < 3; i++ {
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:        fmt.Sprintf("chain-%d", i),
				StartedAt:      int64(i + 1),
				CompletedAt:    int64(i + 2),
				TerminalStatus: "done",
				TmuxSession:    "proj-a",
				PaneID:         "%5",
				RootAgentType:  "cc",
				RootEventName:  "Stop",
				RootReason:     fmt.Sprintf("reason-%d", i),
			},
			Steps: []TraceStep{
				{StepID: fmt.Sprintf("step-%d", i), ChainID: fmt.Sprintf("chain-%d", i), Seq: 1, Kind: "terminal", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i + 1)},
			},
		}
		if err := s.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}

	got0, err := s.GetChainRecord("chain-0")
	if err != nil {
		t.Fatalf("GetChainRecord chain-0: %v", err)
	}
	if got0 != nil {
		t.Fatalf("expected chain-0 to be evicted, got %+v", got0.Chain)
	}
}

func TestTraceStore_RejectsCrossChainParentStep(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 10

	if err := s.SaveChain(TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-a",
			StartedAt:      1,
			CompletedAt:    2,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "root",
		},
		Steps: []TraceStep{
			{StepID: "a-1", ChainID: "chain-a", Seq: 1, Kind: "root", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 1},
		},
	}); err != nil {
		t.Fatalf("SaveChain chain-a: %v", err)
	}

	err := s.SaveChain(TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-b",
			StartedAt:      3,
			CompletedAt:    4,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "root",
		},
		Steps: []TraceStep{
			{StepID: "b-1", ChainID: "chain-b", ParentStepID: "a-1", Seq: 1, Kind: "decision", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 3},
		},
	})
	if err == nil {
		t.Fatal("expected cross-chain parent step to fail")
	}
}

func seedLegacyTraceSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`
		CREATE TABLE agent_trace_chains (
			chain_id     TEXT PRIMARY KEY,
			tmux_session TEXT NOT NULL,
			pane_id      TEXT NOT NULL,
			agent_type   TEXT NOT NULL,
			event_name   TEXT NOT NULL,
			created_at   INTEGER NOT NULL,
			updated_at   INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy chains: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE agent_trace_steps (
			step_id        TEXT PRIMARY KEY,
			chain_id       TEXT NOT NULL,
			parent_step_id TEXT,
			step_name      TEXT NOT NULL,
			payload        TEXT NOT NULL DEFAULT 'null',
			step_index     INTEGER NOT NULL,
			created_at     INTEGER NOT NULL,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create legacy steps: %v", err)
	}
}

func seedLegacyTraceChainsOnly(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`
		CREATE TABLE agent_trace_chains (
			chain_id     TEXT PRIMARY KEY,
			tmux_session TEXT NOT NULL,
			pane_id      TEXT NOT NULL,
			agent_type   TEXT NOT NULL,
			event_name   TEXT NOT NULL,
			created_at   INTEGER NOT NULL,
			updated_at   INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy chains: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, tmux_session, pane_id, agent_type, event_name, created_at, updated_at
		) VALUES
		('legacy-chain', 'proj-legacy', '%9', 'cc', 'Stop', 123, 456)
	`); err != nil {
		t.Fatalf("seed legacy chain: %v", err)
	}
}

func seedLegacyTraceStepsOnly(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`PRAGMA foreign_keys = ON`)
	})

	if _, err := db.Exec(`
		CREATE TABLE agent_trace_steps (
			step_id        TEXT PRIMARY KEY,
			chain_id       TEXT NOT NULL,
			parent_step_id TEXT,
			step_name      TEXT NOT NULL,
			payload        TEXT NOT NULL DEFAULT 'null',
			step_index     INTEGER NOT NULL,
			created_at     INTEGER NOT NULL,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create legacy steps: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, step_name, payload, step_index, created_at
		) VALUES
		('legacy-step-1', 'legacy-chain', NULL, 'root', 'null', 1, 124)
	`); err != nil {
		t.Fatalf("seed legacy step: %v", err)
	}
}

func seedLegacyTraceData(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, tmux_session, pane_id, agent_type, event_name, created_at, updated_at
		) VALUES
		('legacy-chain', 'proj-legacy', '%9', 'cc', 'Stop', 123, 456)
	`); err != nil {
		t.Fatalf("seed legacy chain: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, step_name, payload, step_index, created_at
		) VALUES
		('legacy-step-1', 'legacy-chain', NULL, 'root', 'null', 1, 124),
		('legacy-step-2', 'legacy-chain', 'legacy-step-1', 'terminal', 'null', 2, 125)
	`); err != nil {
		t.Fatalf("seed legacy steps: %v", err)
	}
}

func seedIntermediateTraceSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	if err := createTraceChainsTable(db); err != nil {
		t.Fatalf("create chains: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE agent_trace_steps (
			step_id         TEXT PRIMARY KEY,
			chain_id        TEXT NOT NULL,
			parent_step_id  TEXT,
			seq             INTEGER NOT NULL,
			kind            TEXT NOT NULL DEFAULT '',
			tmux_session    TEXT NOT NULL DEFAULT '',
			pane_id         TEXT NOT NULL DEFAULT '',
			agent_type      TEXT NOT NULL DEFAULT '',
			frame_id        TEXT NOT NULL DEFAULT '',
			parent_frame_id TEXT NOT NULL DEFAULT '',
			event_name      TEXT NOT NULL DEFAULT '',
			decision        TEXT NOT NULL DEFAULT '',
			reason          TEXT NOT NULL DEFAULT '',
			payload_json    TEXT NOT NULL DEFAULT 'null',
			before_json     TEXT NOT NULL DEFAULT 'null',
			after_json      TEXT NOT NULL DEFAULT 'null',
			created_at      INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create intermediate steps: %v", err)
	}
}

func seedIntermediateTraceData(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at
		) VALUES
		('chain-a', 1, 2, 'done', 'ok', 'proj-a', '%1', 'cc', 'Stop', 'root', 'terminal', 'done', 'ok', 1, 2),
		('chain-b', 3, 4, 'done', 'ok', 'proj-a', '%1', 'cc', 'Stop', 'root', 'terminal', 'done', 'ok', 1, 4)
	`); err != nil {
		t.Fatalf("seed chains: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at
		) VALUES
		('a-1', 'chain-a', NULL, 1, 'root', 'proj-a', '%1', 'cc', 'frame-a', '', 'Stop', '', '', 'null', 'null', 'null', 1)
	`); err != nil {
		t.Fatalf("seed step: %v", err)
	}
}
