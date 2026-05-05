package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
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

func TestTraceStore_MigratesLegacyStepsWithOrphanChainReference(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedLegacyTraceStepsWithUnrelatedChain(t, s.db)

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

func seedLegacyTraceStepsWithUnrelatedChain(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`PRAGMA foreign_keys = ON`)
	})

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
		('other-chain', 'proj-legacy', '%9', 'cc', 'Stop', 123, 456)
	`); err != nil {
		t.Fatalf("seed unrelated chain: %v", err)
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
	if _, err := db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, step_name, payload, step_index, created_at
		) VALUES
		('legacy-step-1', 'missing-chain', NULL, 'root', 'null', 1, 124)
	`); err != nil {
		t.Fatalf("seed orphan legacy step: %v", err)
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

	if err := createTraceChainsTable(context.Background(), db); err != nil {
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

// TestTraceStore_PruneCascadesStepsToChainsAfterEviction verifies that when
// pruneTraceChains evicts old chains, their steps are also removed via
// ON DELETE CASCADE. Saves more chains than maxChains so prune fires, then
// asserts no orphan steps remain.
//
// Why: without foreign_keys pragma active on the connection that executes the
// prune DELETE, SQLite silently skips the cascade — leaving steps referencing
// deleted chains (orphans). This was the root cause of the 18 GB DB bloat
// (137,550 steps vs 10,000 chains).
func TestTraceStore_PruneCascadesStepsToChainsAfterEviction(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 5
	s.maxSteps = 1000

	// Save 20 chains, each with 3 steps → 60 steps total.
	// After prune: ≤5 chains remain; all surviving steps must reference a
	// surviving chain.
	for i := 0; i < 20; i++ {
		chainID := fmt.Sprintf("cascade-chain-%02d", i)
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:          chainID,
				StartedAt:        int64(i + 1),
				CompletedAt:      int64(i + 2),
				TerminalStatus:   "done",
				TerminalReason:   "ok",
				TmuxSession:      "proj-cascade",
				PaneID:           "%7",
				RootAgentType:    "cc",
				RootEventName:    "Stop",
				RootReason:       "bootstrap",
				LatestStepKind:   "terminal",
				LatestDecision:   "done",
				LatestStepReason: "ok",
			},
			Steps: []TraceStep{
				{StepID: fmt.Sprintf("s%02d-1", i), ChainID: chainID, Seq: 1, Kind: "root", TmuxSession: "proj-cascade", PaneID: "%7", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i*10 + 1)},
				{StepID: fmt.Sprintf("s%02d-2", i), ChainID: chainID, ParentStepID: fmt.Sprintf("s%02d-1", i), Seq: 2, Kind: "decision", TmuxSession: "proj-cascade", PaneID: "%7", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i*10 + 2)},
				{StepID: fmt.Sprintf("s%02d-3", i), ChainID: chainID, ParentStepID: fmt.Sprintf("s%02d-2", i), Seq: 3, Kind: "terminal", TmuxSession: "proj-cascade", PaneID: "%7", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i*10 + 3)},
			},
		}
		if err := s.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}

	var chainCount, stepCount, orphans int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_trace_chains").Scan(&chainCount); err != nil {
		t.Fatalf("count chains: %v", err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_trace_steps").Scan(&stepCount); err != nil {
		t.Fatalf("count steps: %v", err)
	}
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_trace_steps
		WHERE chain_id NOT IN (SELECT chain_id FROM agent_trace_chains)
	`).Scan(&orphans); err != nil {
		t.Fatalf("count orphans: %v", err)
	}

	if chainCount > 5 {
		t.Errorf("chainCount = %d, want ≤5", chainCount)
	}
	if orphans != 0 {
		t.Errorf("orphan steps = %d (stepCount=%d, chainCount=%d): ON DELETE CASCADE did not fire — foreign_keys pragma missing on prune connection", orphans, stepCount, chainCount)
	}
}

// openFileTraceStore opens a file-backed AgentEventStore in t.TempDir() and
// warms the pool to maxConns concurrent connections before returning
// the TraceStore. This exercises the pool-PRAGMA path that :memory: skips.
func openFileTraceStore(t *testing.T) *TraceStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := OpenAgentEvent(path)
	if err != nil {
		t.Fatalf("OpenAgentEvent: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// Warm 4 idle pool connections so subsequent migration/ops fan out.
	ctx := context.Background()
	conns := make([]*sql.Conn, 4)
	for i := range conns {
		c, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatalf("db.Conn warm %d: %v", i, err)
		}
		conns[i] = c
	}
	for _, c := range conns {
		_ = c.Close()
	}

	ts, err := s.Traces()
	if err != nil {
		t.Fatalf("Traces: %v", err)
	}
	ts.maxChains = 10
	ts.maxSteps = 1000
	return ts
}

// TestMigrateTraceDB_RunsOnPinnedConnection verifies that migrateTraceDB
// succeeds against a file-backed pool with FK=1 on all pool connections.
// Before the fix, the FK=OFF PRAGMA on a random pool connection left other
// connections with FK=ON during DDL, breaking schema creation.
func TestMigrateTraceDB_RunsOnPinnedConnection(t *testing.T) {
	s := openFileTraceStore(t)

	// Save a chain with steps to confirm schema is fully functional.
	record := TraceRecord{
		Chain: TraceChain{
			ChainID:        "pinned-chain",
			StartedAt:      1,
			CompletedAt:    2,
			TerminalStatus: "done",
			TmuxSession:    "proj-pinned",
			PaneID:         "%1",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "ok",
		},
		Steps: []TraceStep{
			{StepID: "pinned-s1", ChainID: "pinned-chain", Seq: 1, Kind: "root", TmuxSession: "proj-pinned", PaneID: "%1", AgentType: "cc", EventName: "Stop", CreatedAt: 1},
			{StepID: "pinned-s2", ChainID: "pinned-chain", ParentStepID: "pinned-s1", Seq: 2, Kind: "terminal", TmuxSession: "proj-pinned", PaneID: "%1", AgentType: "cc", EventName: "Stop", CreatedAt: 2},
		},
	}
	if err := s.SaveChain(record); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	got, err := s.GetChainRecord("pinned-chain")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got == nil || len(got.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %v", got)
	}
}

// TestMigrateTraceDB_CascadeRegression_FileBackedPool runs the prune-cascade
// scenario (F3) on a file-backed pool to ensure FK enforcement works across
// pool connections — the scenario that :memory: single-conn tests cannot catch.
func TestMigrateTraceDB_CascadeRegression_FileBackedPool(t *testing.T) {
	s := openFileTraceStore(t)
	s.maxChains = 5
	s.maxSteps = 1000

	for i := 0; i < 20; i++ {
		chainID := fmt.Sprintf("fp-chain-%02d", i)
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:          chainID,
				StartedAt:        int64(i + 1),
				CompletedAt:      int64(i + 2),
				TerminalStatus:   "done",
				TerminalReason:   "ok",
				TmuxSession:      "proj-fp",
				PaneID:           "%8",
				RootAgentType:    "cc",
				RootEventName:    "Stop",
				RootReason:       "ok",
				LatestStepKind:   "terminal",
				LatestDecision:   "done",
				LatestStepReason: "ok",
			},
			Steps: []TraceStep{
				{StepID: fmt.Sprintf("fp%02d-1", i), ChainID: chainID, Seq: 1, Kind: "root", TmuxSession: "proj-fp", PaneID: "%8", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i*10 + 1)},
				{StepID: fmt.Sprintf("fp%02d-2", i), ChainID: chainID, ParentStepID: fmt.Sprintf("fp%02d-1", i), Seq: 2, Kind: "terminal", TmuxSession: "proj-fp", PaneID: "%8", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i*10 + 2)},
			},
		}
		if err := s.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}

	var chainCount, orphans int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_trace_chains").Scan(&chainCount); err != nil {
		t.Fatalf("count chains: %v", err)
	}
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_trace_steps
		WHERE chain_id NOT IN (SELECT chain_id FROM agent_trace_chains)
	`).Scan(&orphans); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if chainCount > 5 {
		t.Errorf("chainCount = %d, want ≤5", chainCount)
	}
	if orphans != 0 {
		t.Errorf("orphan steps = %d after file-backed pool prune: ON DELETE CASCADE did not fire", orphans)
	}
}

// TestMigrateTraceDB_CleansLegacyOrphans verifies that migrateTraceDB
// deletes pre-existing orphan agent_trace_steps (rows whose chain_id has no
// corresponding agent_trace_chains row) accumulated while FK enforcement was
// unreliable.
func TestMigrateTraceDB_CleansLegacyOrphans(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orphan.db")

	// First open: create schema and seed orphan steps via a pinned conn with FK off.
	s1, err := OpenAgentEvent(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}

	ctx := context.Background()
	conn, err := s1.db.Conn(ctx)
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("fk off: %v", err)
	}

	// Ensure trace tables exist before inserting.
	if _, err := s1.Traces(); err != nil {
		t.Fatalf("first Traces: %v", err)
	}

	// Insert 5 orphan steps — chain_id 'ghost-chain' never appears in agent_trace_chains.
	for i := 1; i <= 5; i++ {
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO agent_trace_steps (step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id, agent_type, frame_id, parent_frame_id, event_name, decision, reason, payload_json, before_json, after_json, created_at)
			 VALUES (?, 'ghost-chain', NULL, ?, 'root', '', '', '', '', '', '', '', '', 'null', 'null', 'null', ?)`,
			fmt.Sprintf("ghost-step-%d", i), i, i,
		); err != nil {
			t.Fatalf("insert orphan step %d: %v", i, err)
		}
	}
	_ = conn.Close()
	_ = s1.Close()

	// Second open re-runs migrateTraceDB, which must delete the 5 orphans.
	s2, err := OpenAgentEvent(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()

	if _, err := s2.Traces(); err != nil {
		t.Fatalf("second Traces: %v", err)
	}

	var orphanCount int
	if err := s2.db.QueryRow(`
		SELECT COUNT(*) FROM agent_trace_steps
		WHERE NOT EXISTS (
			SELECT 1 FROM agent_trace_chains
			WHERE agent_trace_chains.chain_id = agent_trace_steps.chain_id
		)
	`).Scan(&orphanCount); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphanCount != 0 {
		t.Errorf("orphan steps after migration = %d, want 0", orphanCount)
	}
}

