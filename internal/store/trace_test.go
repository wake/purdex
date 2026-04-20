package store

import (
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

	record := TraceRecord{
		Chain: TraceChain{
			ChainID:     "chain-1",
			TmuxSession: "proj-a",
			PaneID:      "%5",
			AgentType:   "cc",
			EventName:   "Stop",
			CreatedAt:   100,
			UpdatedAt:   200,
		},
		Steps: []TraceStep{
			{
				StepID:    "step-1",
				ChainID:   "chain-1",
				StepIndex: 0,
				StepName:  "capture",
				Payload:   json.RawMessage(`{"status":"queued"}`),
				CreatedAt: 101,
			},
			{
				StepID:       "step-2",
				ChainID:      "chain-1",
				ParentStepID: "step-1",
				StepIndex:    1,
				StepName:     "derive",
				Payload:      json.RawMessage(`{"status":"done"}`),
				CreatedAt:    102,
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
	if got.Chain.ChainID != record.Chain.ChainID {
		t.Fatalf("chain_id = %q, want %q", got.Chain.ChainID, record.Chain.ChainID)
	}
	if got.Chain.TmuxSession != record.Chain.TmuxSession {
		t.Fatalf("tmux_session = %q, want %q", got.Chain.TmuxSession, record.Chain.TmuxSession)
	}
	if got.Chain.StepCount != len(record.Steps) {
		t.Fatalf("step_count = %d, want %d", got.Chain.StepCount, len(record.Steps))
	}
	if len(got.Steps) != len(record.Steps) {
		t.Fatalf("steps = %d, want %d", len(got.Steps), len(record.Steps))
	}
	if got.Steps[0].StepName != "capture" || string(got.Steps[0].Payload) != `{"status":"queued"}` {
		t.Fatalf("first step = %+v", got.Steps[0])
	}
	if got.Steps[1].ParentStepID != "step-1" {
		t.Fatalf("parent_step_id = %q, want step-1", got.Steps[1].ParentStepID)
	}
}

func TestTraceStore_SaveChain_PreservesParentStepID(t *testing.T) {
	s := openTestTraceStore(t)

	record := TraceRecord{
		Chain: TraceChain{
			ChainID:     "chain-parent",
			TmuxSession: "proj-a",
			PaneID:      "%5",
			AgentType:   "cc",
			EventName:   "SessionStart",
			CreatedAt:   300,
			UpdatedAt:   300,
		},
		Steps: []TraceStep{
			{StepID: "root", ChainID: "chain-parent", StepIndex: 0, StepName: "root", CreatedAt: 301},
			{StepID: "child", ChainID: "chain-parent", ParentStepID: "root", StepIndex: 1, StepName: "child", CreatedAt: 302},
		},
	}

	if err := s.SaveChain(record); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	got, err := s.GetChainRecord("chain-parent")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got == nil {
		t.Fatal("expected record, got nil")
	}
	if got.Steps[1].ParentStepID != "root" {
		t.Fatalf("parent_step_id = %q, want root", got.Steps[1].ParentStepID)
	}
}

func TestTraceStore_ListChains_PaginatesWithCursorAndBefore(t *testing.T) {
	s := openTestTraceStore(t)

	for i := 0; i < 4; i++ {
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:     fmt.Sprintf("chain-%d", i),
				TmuxSession: "proj-a",
				PaneID:      "%5",
				AgentType:   "cc",
				EventName:   "Stop",
				CreatedAt:   int64(100 + i),
				UpdatedAt:   int64(100 + i),
			},
			Steps: []TraceStep{
				{StepID: fmt.Sprintf("step-%d", i), ChainID: fmt.Sprintf("chain-%d", i), StepIndex: 0, StepName: "step", CreatedAt: int64(100 + i)},
			},
		}
		if err := s.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}
	if err := s.SaveChain(TraceRecord{
		Chain: TraceChain{
			ChainID:     "chain-skip",
			TmuxSession: "proj-b",
			PaneID:      "%9",
			AgentType:   "codex",
			EventName:   "Stop",
			CreatedAt:   999,
			UpdatedAt:   999,
		},
		Steps: []TraceStep{{StepID: "skip-step", ChainID: "chain-skip", StepIndex: 0, StepName: "step", CreatedAt: 999}},
	}); err != nil {
		t.Fatalf("SaveChain skip: %v", err)
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

func TestTraceStore_RetentionCapsChainsAtMax(t *testing.T) {
	s := openTestTraceStore(t)

	for i := 0; i < maxChains+1; i++ {
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:     fmt.Sprintf("chain-%05d", i),
				TmuxSession: "proj-a",
				PaneID:      "%5",
				AgentType:   "cc",
				EventName:   "Stop",
				CreatedAt:   int64(i + 1),
				UpdatedAt:   int64(i + 1),
			},
			Steps: []TraceStep{{StepID: fmt.Sprintf("step-%05d", i), ChainID: fmt.Sprintf("chain-%05d", i), StepIndex: 0, StepName: "step", CreatedAt: int64(i + 1)}},
		}
		if err := s.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}

	page, err := s.ListChains(TraceListFilter{Limit: maxChains + 10})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != maxChains {
		t.Fatalf("chains len = %d, want %d", len(page.Chains), maxChains)
	}
	if page.Chains[len(page.Chains)-1].ChainID != "chain-00001" {
		t.Fatalf("oldest chain kept = %q, want chain-00001", page.Chains[len(page.Chains)-1].ChainID)
	}
}

func TestTraceStore_RetentionCapsStepsAtMax(t *testing.T) {
	s := openTestTraceStore(t)

	steps := make([]TraceStep, 0, maxSteps+1)
	for i := 0; i < maxSteps+1; i++ {
		steps = append(steps, TraceStep{
			StepID:    fmt.Sprintf("step-%06d", i),
			ChainID:   "chain-steps",
			StepIndex: i,
			StepName:  "step",
			CreatedAt: int64(i + 1),
		})
	}
	if err := s.SaveChain(TraceRecord{
		Chain: TraceChain{
			ChainID:     "chain-steps",
			TmuxSession: "proj-a",
			PaneID:      "%5",
			AgentType:   "cc",
			EventName:   "Stop",
			CreatedAt:   1,
			UpdatedAt:   1,
		},
		Steps: steps,
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	got, err := s.GetChainRecord("chain-steps")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got == nil {
		t.Fatal("expected record, got nil")
	}
	if len(got.Steps) != maxSteps {
		t.Fatalf("steps len = %d, want %d", len(got.Steps), maxSteps)
	}
	if got.Steps[0].StepID != "step-000001" {
		t.Fatalf("oldest kept step = %q, want step-000001", got.Steps[0].StepID)
	}
}
