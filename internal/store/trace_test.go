package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func strPtr(s string) *string { return &s }

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

	traceID := uuid.NewString()

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
				StepID:             "step-1",
				ChainID:            "chain-1",
				Seq:                1,
				Kind:               "decision",
				TmuxSession:        "proj-a",
				PaneID:             "%5",
				AgentType:          "cc",
				FrameID:            "frame-1",
				EventName:          "SessionStart",
				Decision:           "continue",
				Reason:             "ready",
				PayloadJSON:        json.RawMessage(`{"status":"queued"}`),
				BeforeJSON:         json.RawMessage(`{"before":true}`),
				AfterJSON:          json.RawMessage(`{"after":true}`),
				CreatedAt:          101,
				SourceKind:         "hook",
				Action:             "decision:continue",
				ReasonCode:         "ready",
				Outcome:            "emitted",
				ScenarioKey:        "SessionStart",
				ObservedGeneration: 7,
				DecisionPorts:      json.RawMessage(`[{"port":"statusline","decision":"ok"}]`),
				Phase:              "committed",
				Status:             "success",
				WatcherToken:       strPtr("watcher-abc"),
				TraceID:            traceID,
				ReasonText:         "ready to continue",
				Attrs:              json.RawMessage(`{"agent_type":"cc","hook":"post"}`),
				InputRefs:          json.RawMessage(`[{"kind":"event","id":"e1"}]`),
				OutputRefs:         json.RawMessage(`[{"kind":"frame","id":"f1"}]`),
				StateBeforeRef:     "snap:before",
				StateAfterRef:      "snap:after",
				EvidenceRefs:       json.RawMessage(`[{"source":"tmux"}]`),
				StartedAt:          101,
				EndedAt:            101,
				OTelKind:           "internal",
			},
			{
				StepID:             "step-2",
				ChainID:            "chain-1",
				ParentStepID:       "step-1",
				Seq:                2,
				Kind:               "terminal",
				TmuxSession:        "proj-a",
				PaneID:             "%5",
				AgentType:          "cc",
				FrameID:            "frame-1",
				ParentFrameID:      "frame-0",
				EventName:          "Stop",
				Decision:           "done",
				Reason:             "completed",
				PayloadJSON:        json.RawMessage(`{"status":"done"}`),
				CreatedAt:          102,
				SourceKind:         "hook",
				Action:             "terminal:done",
				ReasonCode:         "completed",
				Outcome:            "emitted",
				ScenarioKey:        "Stop",
				ObservedGeneration: 8,
				Phase:              "committed",
				Status:             "success",
				TraceID:            traceID,
				OTelKind:           "internal",
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

	first := got.Steps[0]
	if first.SourceKind != "hook" || first.Action != "decision:continue" || first.ReasonCode != "ready" ||
		first.Outcome != "emitted" || first.ScenarioKey != "SessionStart" || first.Phase != "committed" ||
		first.Status != "success" {
		t.Fatalf("first step lights fields = %+v", first)
	}
	if first.ObservedGeneration != 7 {
		t.Fatalf("first ObservedGeneration = %d, want 7", first.ObservedGeneration)
	}
	if string(first.DecisionPorts) != `[{"port":"statusline","decision":"ok"}]` {
		t.Fatalf("first DecisionPorts = %s", string(first.DecisionPorts))
	}
	if first.WatcherToken == nil || *first.WatcherToken != "watcher-abc" {
		t.Fatalf("first WatcherToken = %v", first.WatcherToken)
	}

	second := got.Steps[1]
	if second.WatcherToken != nil {
		t.Fatalf("second WatcherToken = %v, want nil", second.WatcherToken)
	}
	if string(second.DecisionPorts) != `[]` {
		t.Fatalf("second DecisionPorts = %s, want []", string(second.DecisionPorts))
	}
	if second.Phase != "committed" || second.Status != "success" {
		t.Fatalf("second step lights fields = %+v", second)
	}

	// PR-1b-0 envelope completion: the 11 new fields must round-trip on the
	// fully populated first step and keep well-formed zero-value defaults on
	// the sparsely populated second step.
	if first.TraceID != traceID {
		t.Fatalf("first TraceID = %q, want %q", first.TraceID, traceID)
	}
	if first.ReasonText != "ready to continue" {
		t.Fatalf("first ReasonText = %q", first.ReasonText)
	}
	if string(first.Attrs) != `{"agent_type":"cc","hook":"post"}` {
		t.Fatalf("first Attrs = %s", string(first.Attrs))
	}
	if string(first.InputRefs) != `[{"kind":"event","id":"e1"}]` {
		t.Fatalf("first InputRefs = %s", string(first.InputRefs))
	}
	if string(first.OutputRefs) != `[{"kind":"frame","id":"f1"}]` {
		t.Fatalf("first OutputRefs = %s", string(first.OutputRefs))
	}
	if first.StateBeforeRef != "snap:before" || first.StateAfterRef != "snap:after" {
		t.Fatalf("first state refs = %q/%q", first.StateBeforeRef, first.StateAfterRef)
	}
	if string(first.EvidenceRefs) != `[{"source":"tmux"}]` {
		t.Fatalf("first EvidenceRefs = %s", string(first.EvidenceRefs))
	}
	if first.StartedAt != 101 || first.EndedAt != 101 {
		t.Fatalf("first started/ended = %d/%d", first.StartedAt, first.EndedAt)
	}
	if first.OTelKind != "internal" {
		t.Fatalf("first OTelKind = %q", first.OTelKind)
	}

	if second.TraceID != traceID {
		t.Fatalf("second TraceID = %q, want %q", second.TraceID, traceID)
	}
	if string(second.Attrs) != `{}` {
		t.Fatalf("second Attrs default = %s, want {}", string(second.Attrs))
	}
	if string(second.InputRefs) != `[]` || string(second.OutputRefs) != `[]` || string(second.EvidenceRefs) != `[]` {
		t.Fatalf("second refs default = %s/%s/%s", string(second.InputRefs), string(second.OutputRefs), string(second.EvidenceRefs))
	}
	if second.OTelKind != "internal" {
		t.Fatalf("second OTelKind = %q, want internal", second.OTelKind)
	}
}

// TestTraceStore_LegacyRowZeroValueEnvelopeRoundtrip guards the row-class
// discriminator contract (plan phase-1 #561): a step whose source_kind is
// empty is a legacy row and is exempt from Lights envelope validation, so
// every new spec §3.5 field is allowed to stay at its zero value without
// surfacing SQL NULL errors or triggering validation.
func TestTraceStore_LegacyRowZeroValueEnvelopeRoundtrip(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 10

	record := TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-legacy",
			StartedAt:      10,
			CompletedAt:    20,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "boot",
		},
		Steps: []TraceStep{
			{
				StepID:      "legacy-1",
				ChainID:     "chain-legacy",
				Seq:         1,
				Kind:        "decision",
				TmuxSession: "proj-a",
				PaneID:      "%5",
				AgentType:   "cc",
				EventName:   "Stop",
				Decision:    "done",
				CreatedAt:   11,
				// Intentionally no SourceKind / Phase / Outcome / Action /
				// TraceID: legacy row, discriminator must permit zero-values.
			},
		},
	}

	if err := s.SaveChain(record); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	got, err := s.GetChainRecord("chain-legacy")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got == nil || len(got.Steps) != 1 {
		t.Fatalf("got = %+v", got)
	}
	step := got.Steps[0]
	if step.SourceKind != "" {
		t.Fatalf("legacy row SourceKind must be empty, got %q", step.SourceKind)
	}
	if step.Action != "" || step.Phase != "" || step.Status != "" || step.Outcome != "" {
		t.Fatalf("zero-value lights fields corrupted: %+v", step)
	}
	if step.ObservedGeneration != 0 {
		t.Fatalf("ObservedGeneration = %d, want 0", step.ObservedGeneration)
	}
	if string(step.DecisionPorts) != `[]` {
		t.Fatalf("DecisionPorts default = %s, want []", string(step.DecisionPorts))
	}
	if step.WatcherToken != nil {
		t.Fatalf("WatcherToken = %v, want nil", step.WatcherToken)
	}
	// PR-1b-0 envelope completion: legacy rows keep zero values and
	// well-formed JSON defaults.
	if step.TraceID != "" || step.ReasonText != "" || step.StateBeforeRef != "" || step.StateAfterRef != "" || step.OTelKind != "" {
		t.Fatalf("legacy row envelope fields must be zero, got %+v", step)
	}
	if step.StartedAt != 0 || step.EndedAt != 0 {
		t.Fatalf("legacy row started/ended = %d/%d, want 0/0", step.StartedAt, step.EndedAt)
	}
	if string(step.Attrs) != `{}` {
		t.Fatalf("legacy row Attrs default = %s, want {}", string(step.Attrs))
	}
	if string(step.InputRefs) != `[]` || string(step.OutputRefs) != `[]` || string(step.EvidenceRefs) != `[]` {
		t.Fatalf("legacy row refs default = %s/%s/%s", string(step.InputRefs), string(step.OutputRefs), string(step.EvidenceRefs))
	}
}

// TestTraceStore_LightsRow_MissingRequiredField_Errors covers the negative
// half of the row-class discriminator: once source_kind is set, required
// envelope fields (phase/outcome/action/trace_id) must be non-empty; otherwise
// SaveChain rejects the record with an actionable error.
func TestTraceStore_LightsRow_MissingRequiredField_Errors(t *testing.T) {
	base := func() TraceRecord {
		return TraceRecord{
			Chain: TraceChain{
				ChainID:        "chain-lights",
				StartedAt:      10,
				CompletedAt:    20,
				TerminalStatus: "done",
				TmuxSession:    "proj-a",
				PaneID:         "%5",
				RootAgentType:  "cc",
				RootEventName:  "Stop",
			},
			Steps: []TraceStep{
				{
					StepID:      "lights-1",
					ChainID:     "chain-lights",
					Seq:         1,
					Kind:        "decision",
					TmuxSession: "proj-a",
					PaneID:      "%5",
					AgentType:   "cc",
					EventName:   "Stop",
					Decision:    "done",
					CreatedAt:   11,
					SourceKind:  "hook",
					Action:      "decision:done",
					Outcome:     "emitted",
					Phase:       "committed",
					Status:      "success",
					TraceID:     uuid.NewString(),
				},
			},
		}
	}

	// Sanity: fully-populated lights row persists without error.
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 10
	if err := s.SaveChain(base()); err != nil {
		t.Fatalf("baseline SaveChain: %v", err)
	}

	cases := []struct {
		name     string
		mutate   func(*TraceStep)
		wantHint string
	}{
		{"missing action", func(st *TraceStep) { st.Action = "" }, "action"},
		{"missing outcome", func(st *TraceStep) { st.Outcome = "" }, "outcome"},
		{"missing phase", func(st *TraceStep) { st.Phase = "" }, "phase"},
		{"missing trace_id", func(st *TraceStep) { st.TraceID = "" }, "trace_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestTraceStore(t)
			store.maxChains = 10
			store.maxSteps = 10
			rec := base()
			rec.Chain.ChainID = "chain-lights-" + tc.name
			rec.Steps[0].ChainID = rec.Chain.ChainID
			tc.mutate(&rec.Steps[0])
			err := store.SaveChain(rec)
			if err == nil {
				t.Fatalf("expected lights row validation error")
			}
			if !strings.Contains(err.Error(), tc.wantHint) {
				t.Fatalf("error %q does not mention field %q", err.Error(), tc.wantHint)
			}
		})
	}
}

// TestValidateLightsRow_HybridRow_WithTraceIDButNoSourceKind_Errors covers
// the bidirectional row-class contract: if any Lights envelope field is set
// but source_kind is empty, the row is a hybrid and must be rejected.
func TestValidateLightsRow_HybridRow_WithTraceIDButNoSourceKind_Errors(t *testing.T) {
	step := TraceStep{StepID: "hybrid-1", TraceID: uuid.NewString()}
	if err := validateLightsRow(step); err == nil {
		t.Fatal("expected hybrid row (trace_id set, source_kind empty) to fail validation")
	}
}

func TestValidateLightsRow_HybridRow_WithPhaseButNoSourceKind_Errors(t *testing.T) {
	step := TraceStep{StepID: "hybrid-2", Phase: "committed"}
	if err := validateLightsRow(step); err == nil {
		t.Fatal("expected hybrid row (phase set, source_kind empty) to fail validation")
	}
}

func TestValidateLightsRow_HybridRow_WithActionButNoSourceKind_Errors(t *testing.T) {
	step := TraceStep{StepID: "hybrid-3", Action: "decision:continue"}
	if err := validateLightsRow(step); err == nil {
		t.Fatal("expected hybrid row (action set, source_kind empty) to fail validation")
	}
}

func TestValidateLightsRow_PureLegacyRow_Passes(t *testing.T) {
	step := TraceStep{StepID: "legacy-1", Kind: "decision", Reason: "ok"}
	if err := validateLightsRow(step); err != nil {
		t.Fatalf("pure legacy row must pass: %v", err)
	}
}

// TestTraceStore_InvalidJSON_* covers JSON validation for the envelope JSON
// columns (attrs / *_refs / decision_ports). SaveChain must reject both
// malformed JSON and wrong top-level shapes (array-where-object-expected or
// vice versa) before they reach the database.
func TestTraceStore_InvalidJSON_Attrs_Errors(t *testing.T) {
	rec := newLightsRecordForJSONTest()
	rec.Steps[0].Attrs = json.RawMessage(`{invalid`)
	store := openTestTraceStore(t)
	if err := store.SaveChain(rec); err == nil {
		t.Fatal("expected malformed attrs to be rejected")
	}
}

func TestTraceStore_InvalidJSON_InputRefs_Errors(t *testing.T) {
	rec := newLightsRecordForJSONTest()
	rec.Steps[0].InputRefs = json.RawMessage(`[not json`)
	store := openTestTraceStore(t)
	if err := store.SaveChain(rec); err == nil {
		t.Fatal("expected malformed input_refs to be rejected")
	}
}

func TestTraceStore_WrongShape_AttrsAsArray_Errors(t *testing.T) {
	rec := newLightsRecordForJSONTest()
	rec.Steps[0].Attrs = json.RawMessage(`[]`)
	store := openTestTraceStore(t)
	if err := store.SaveChain(rec); err == nil {
		t.Fatal("expected attrs=array to be rejected (spec: attrs is object)")
	}
}

func TestTraceStore_WrongShape_InputRefsAsObject_Errors(t *testing.T) {
	rec := newLightsRecordForJSONTest()
	rec.Steps[0].InputRefs = json.RawMessage(`{}`)
	store := openTestTraceStore(t)
	if err := store.SaveChain(rec); err == nil {
		t.Fatal("expected input_refs=object to be rejected (spec: *_refs is array)")
	}
}

func TestTraceStore_WrongShape_OutputRefsAsObject_Errors(t *testing.T) {
	rec := newLightsRecordForJSONTest()
	rec.Steps[0].OutputRefs = json.RawMessage(`{}`)
	store := openTestTraceStore(t)
	if err := store.SaveChain(rec); err == nil {
		t.Fatal("expected output_refs=object to be rejected")
	}
}

func TestTraceStore_WrongShape_EvidenceRefsAsObject_Errors(t *testing.T) {
	rec := newLightsRecordForJSONTest()
	rec.Steps[0].EvidenceRefs = json.RawMessage(`{}`)
	store := openTestTraceStore(t)
	if err := store.SaveChain(rec); err == nil {
		t.Fatal("expected evidence_refs=object to be rejected")
	}
}

func TestTraceStore_WrongShape_DecisionPortsAsObject_Errors(t *testing.T) {
	rec := newLightsRecordForJSONTest()
	rec.Steps[0].DecisionPorts = json.RawMessage(`{}`)
	store := openTestTraceStore(t)
	if err := store.SaveChain(rec); err == nil {
		t.Fatal("expected decision_ports=object to be rejected")
	}
}

func TestTraceStore_EmptyJSONFields_DefaultApplied(t *testing.T) {
	rec := newLightsRecordForJSONTest()
	rec.Steps[0].Attrs = nil
	rec.Steps[0].InputRefs = nil
	rec.Steps[0].OutputRefs = nil
	rec.Steps[0].EvidenceRefs = nil
	rec.Steps[0].DecisionPorts = nil
	store := openTestTraceStore(t)
	if err := store.SaveChain(rec); err != nil {
		t.Fatalf("empty JSON fields must default without error: %v", err)
	}
	got, err := store.GetChainRecord(rec.Chain.ChainID)
	if err != nil || got == nil || len(got.Steps) != 1 {
		t.Fatalf("GetChainRecord: rec=%+v err=%v", got, err)
	}
	step := got.Steps[0]
	if string(step.Attrs) != `{}` {
		t.Fatalf("attrs default = %s", string(step.Attrs))
	}
	if string(step.InputRefs) != `[]` || string(step.OutputRefs) != `[]` || string(step.EvidenceRefs) != `[]` {
		t.Fatalf("refs defaults = %s/%s/%s", string(step.InputRefs), string(step.OutputRefs), string(step.EvidenceRefs))
	}
	if string(step.DecisionPorts) != `[]` {
		t.Fatalf("decision_ports default = %s", string(step.DecisionPorts))
	}
}

// newLightsRecordForJSONTest returns a fully-populated Lights record whose
// JSON fields are valid; tests mutate one field at a time to exercise the
// shape / validity rejection paths.
func newLightsRecordForJSONTest() TraceRecord {
	return TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-json",
			StartedAt:      10,
			CompletedAt:    20,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
		},
		Steps: []TraceStep{
			{
				StepID:        "json-step-1",
				ChainID:       "chain-json",
				Seq:           1,
				Kind:          "decision",
				TmuxSession:   "proj-a",
				PaneID:        "%5",
				AgentType:     "cc",
				EventName:     "Stop",
				Decision:      "done",
				CreatedAt:     11,
				SourceKind:    "hook",
				Action:        "decision:done",
				Outcome:       "emitted",
				Phase:         "committed",
				Status:        "success",
				TraceID:       "chain-json",
				Attrs:         json.RawMessage(`{"k":"v"}`),
				InputRefs:     json.RawMessage(`[{"kind":"event"}]`),
				OutputRefs:    json.RawMessage(`[{"kind":"frame"}]`),
				EvidenceRefs:  json.RawMessage(`[{"source":"tmux"}]`),
				DecisionPorts: json.RawMessage(`[{"port":"statusline"}]`),
			},
		},
	}
}

// TestTraceStore_MixedLegacyAndLightsRows confirms legacy + lights rows can
// live in the same chain without the discriminator tripping on the legacy
// neighbour.
func TestTraceStore_MixedLegacyAndLightsRows(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 10

	traceID := uuid.NewString()
	rec := TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-mixed",
			StartedAt:      10,
			CompletedAt:    30,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
		},
		Steps: []TraceStep{
			{
				StepID:      "legacy-1",
				ChainID:     "chain-mixed",
				Seq:         1,
				Kind:        "decision",
				TmuxSession: "proj-a",
				PaneID:      "%5",
				AgentType:   "cc",
				EventName:   "Stop",
				Decision:    "done",
				CreatedAt:   11,
			},
			{
				StepID:       "lights-1",
				ChainID:      "chain-mixed",
				ParentStepID: "legacy-1",
				Seq:          2,
				Kind:         "terminal",
				TmuxSession:  "proj-a",
				PaneID:       "%5",
				AgentType:    "cc",
				EventName:    "Stop",
				Decision:     "done",
				CreatedAt:    12,
				SourceKind:   "hook",
				Action:       "terminal:done",
				Outcome:      "broadcasted",
				Phase:        "committed",
				Status:       "success",
				TraceID:      traceID,
				OTelKind:     "internal",
			},
		},
	}
	if err := s.SaveChain(rec); err != nil {
		t.Fatalf("SaveChain mixed: %v", err)
	}
	got, err := s.GetChainRecord("chain-mixed")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(got.Steps))
	}
	if got.Steps[0].SourceKind != "" {
		t.Fatalf("legacy step SourceKind = %q, want empty", got.Steps[0].SourceKind)
	}
	if got.Steps[1].SourceKind != "hook" || got.Steps[1].TraceID != traceID {
		t.Fatalf("lights step = %+v", got.Steps[1])
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

// TestRebuildLegacyTraceSteps_BlocksCrossChainParent seeds intermediate-schema
// legacy data with a parent_step_id whose target step belongs to a different
// chain_id. The old migration silently nulled such links; the new behaviour
// must abort migration instead so ops can reconcile the data.
func TestRebuildLegacyTraceSteps_BlocksCrossChainParent(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedIntermediateTraceSchema(t, s.db)
	if _, err := s.db.Exec(`
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
	// chain-b has a step whose parent points at a step in chain-a.
	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at
		) VALUES
		('a-1', 'chain-a', NULL, 1, 'root',     'proj-a', '%1', 'cc', 'frame-a', '', 'Stop', '', '', 'null', 'null', 'null', 1),
		('b-1', 'chain-b', 'a-1', 1, 'decision','proj-a', '%1', 'cc', 'frame-b', '', 'Stop', 'continue', 'x', 'null', 'null', 'null', 3)
	`); err != nil {
		t.Fatalf("seed cross-chain parent step: %v", err)
	}

	if _, err := s.Traces(); err == nil {
		t.Fatal("expected Traces migration to fail on cross-chain parent reference")
	}

	// Legacy tables must still exist (migration aborted atomically).
	var name string
	if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='agent_trace_steps'`).Scan(&name); err != nil {
		t.Fatalf("agent_trace_steps missing after aborted migration: %v", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps`).Scan(&count); err != nil {
		t.Fatalf("count legacy steps: %v", err)
	}
	if count != 2 {
		t.Fatalf("legacy step count = %d, want 2", count)
	}
}

// TestRebuildLegacyTraceSteps_PreservesPR1aLightsColumns seeds a legacy table
// carrying the PR-1a Lights columns (source_kind/action/reason_code/outcome/
// scenario_key/observed_generation/decision_ports/phase/status/watcher_token)
// and verifies that PR-1b-0's rebuild preserves their actual values instead of
// resetting them to DEFAULT. Previous rebuild query only copied the original
// 17 columns, which silently corrupted every deployed Lights row whenever the
// migration triggered (e.g. any schema drift that flipped needsStepRebuildTx).
func TestRebuildLegacyTraceSteps_PreservesPR1aLightsColumns(t *testing.T) {
	s := openTestAgentEventStore(t)
	// Seed chains + a "PR-1a schema" steps table: 17 legacy cols + 10 PR-1a
	// Lights cols, but WITHOUT the composite FK and WITHOUT the PR-1b-0
	// envelope cols. This matches what any daemon pinned at PR-1a would see.
	if err := createTraceChainsTable(s.db); err != nil {
		t.Fatalf("create chains: %v", err)
	}
	if _, err := s.db.Exec(`
		CREATE TABLE agent_trace_steps (
			step_id             TEXT PRIMARY KEY,
			chain_id            TEXT NOT NULL,
			parent_step_id      TEXT,
			seq                 INTEGER NOT NULL,
			kind                TEXT NOT NULL DEFAULT '',
			tmux_session        TEXT NOT NULL DEFAULT '',
			pane_id             TEXT NOT NULL DEFAULT '',
			agent_type          TEXT NOT NULL DEFAULT '',
			frame_id            TEXT NOT NULL DEFAULT '',
			parent_frame_id     TEXT NOT NULL DEFAULT '',
			event_name          TEXT NOT NULL DEFAULT '',
			decision            TEXT NOT NULL DEFAULT '',
			reason              TEXT NOT NULL DEFAULT '',
			payload_json        TEXT NOT NULL DEFAULT 'null',
			before_json         TEXT NOT NULL DEFAULT 'null',
			after_json          TEXT NOT NULL DEFAULT 'null',
			created_at          INTEGER NOT NULL DEFAULT 0,
			source_kind         TEXT NOT NULL DEFAULT '',
			action              TEXT NOT NULL DEFAULT '',
			reason_code         TEXT NOT NULL DEFAULT '',
			outcome             TEXT NOT NULL DEFAULT '',
			scenario_key        TEXT NOT NULL DEFAULT '',
			observed_generation INTEGER NOT NULL DEFAULT 0,
			decision_ports      TEXT NOT NULL DEFAULT '[]',
			phase               TEXT NOT NULL DEFAULT '',
			status              TEXT NOT NULL DEFAULT '',
			watcher_token       TEXT,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create PR-1a steps: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at
		) VALUES
		('chain-pr1a', 10, 20, 'done', 'ok', 'proj-a', '%1', 'cc', 'Stop', 'root', 'terminal', 'done', 'ok', 1, 20)
	`); err != nil {
		t.Fatalf("seed chain: %v", err)
	}
	// Populate every PR-1a Lights column with a non-default value so we can
	// detect any DEFAULT-clobber after rebuild.
	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at,
			source_kind, action, reason_code, outcome, scenario_key,
			observed_generation, decision_ports, phase, status, watcher_token
		) VALUES
		('pr1a-1', 'chain-pr1a', NULL, 1, 'terminal', 'proj-a', '%1', 'cc', 'frame-1', 'frame-0',
		 'Stop', 'done', 'completed',
		 '{"x":1}', '{"b":1}', '{"a":1}', 15,
		 'hook', 'terminal:done', 'completed', 'emitted', 'Stop',
		 42, '[{"port":"statusline","decision":"ok"}]', 'committed', 'success', 'watcher-123')
	`); err != nil {
		t.Fatalf("seed step: %v", err)
	}

	// Trigger migrate: missing PR-1b-0 cols make needsStepRebuildTx return true
	// and rebuild the table.
	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces migrate: %v", err)
	}

	store := &TraceStore{db: s.db, maxChains: 10, maxSteps: 10}
	rec, err := store.GetChainRecord("chain-pr1a")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if rec == nil || len(rec.Steps) != 1 {
		t.Fatalf("rec = %+v", rec)
	}
	step := rec.Steps[0]
	if step.SourceKind != "hook" {
		t.Fatalf("source_kind = %q, want hook (rebuild clobbered)", step.SourceKind)
	}
	if step.Action != "terminal:done" {
		t.Fatalf("action = %q, want terminal:done", step.Action)
	}
	if step.ReasonCode != "completed" {
		t.Fatalf("reason_code = %q, want completed", step.ReasonCode)
	}
	if step.Outcome != "emitted" {
		t.Fatalf("outcome = %q, want emitted", step.Outcome)
	}
	if step.ScenarioKey != "Stop" {
		t.Fatalf("scenario_key = %q, want Stop", step.ScenarioKey)
	}
	if step.ObservedGeneration != 42 {
		t.Fatalf("observed_generation = %d, want 42", step.ObservedGeneration)
	}
	if string(step.DecisionPorts) != `[{"port":"statusline","decision":"ok"}]` {
		t.Fatalf("decision_ports = %s", string(step.DecisionPorts))
	}
	if step.Phase != "committed" {
		t.Fatalf("phase = %q, want committed", step.Phase)
	}
	if step.Status != "success" {
		t.Fatalf("status = %q, want success", step.Status)
	}
	if step.WatcherToken == nil || *step.WatcherToken != "watcher-123" {
		t.Fatalf("watcher_token = %v, want watcher-123", step.WatcherToken)
	}
	// trace_id is backfilled from chain_id for Lights rows (source_kind!="")
	// so the row satisfies validateLightsRow post-migration.
	if step.TraceID != "chain-pr1a" {
		t.Fatalf("pr1b0 trace_id backfill = %q, want chain-pr1a", step.TraceID)
	}
	// Other PR-1b-0 cols absent from legacy table fall back to DEFAULTs.
	if step.ReasonText != "" || step.StateBeforeRef != "" || step.StateAfterRef != "" || step.OTelKind != "" {
		t.Fatalf("pr1b0 string cols must default to empty, got %+v", step)
	}
	if step.StartedAt != 0 || step.EndedAt != 0 {
		t.Fatalf("pr1b0 timestamp defaults wrong: %d/%d", step.StartedAt, step.EndedAt)
	}
	if string(step.Attrs) != `{}` || string(step.InputRefs) != `[]` || string(step.OutputRefs) != `[]` || string(step.EvidenceRefs) != `[]` {
		t.Fatalf("pr1b0 json defaults wrong: %s / %s / %s / %s",
			string(step.Attrs), string(step.InputRefs), string(step.OutputRefs), string(step.EvidenceRefs))
	}
}

// TestRebuildLegacyTraceSteps_PreservesAllLightsColumnsEvenIfFullSchemaExists
// covers the robustness scenario where a legacy twin ends up with the full
// PR-1b-0 schema (38 cols). Although this should never happen in practice
// because needsStepRebuildTx also triggers on the missing composite FK, the
// rebuild must still preserve every populated field instead of DEFAULT-ing it.
func TestRebuildLegacyTraceSteps_PreservesAllLightsColumnsEvenIfFullSchemaExists(t *testing.T) {
	s := openTestAgentEventStore(t)
	if err := createTraceChainsTable(s.db); err != nil {
		t.Fatalf("create chains: %v", err)
	}
	// Full PR-1b-0 steps table but without the composite FK, so
	// hasStepParentCompositeFKTx returns false and needsStepRebuildTx triggers
	// a rebuild.
	if _, err := s.db.Exec(`
		CREATE TABLE agent_trace_steps (
			step_id             TEXT PRIMARY KEY,
			chain_id            TEXT NOT NULL,
			parent_step_id      TEXT,
			seq                 INTEGER NOT NULL,
			kind                TEXT NOT NULL DEFAULT '',
			tmux_session        TEXT NOT NULL DEFAULT '',
			pane_id             TEXT NOT NULL DEFAULT '',
			agent_type          TEXT NOT NULL DEFAULT '',
			frame_id            TEXT NOT NULL DEFAULT '',
			parent_frame_id     TEXT NOT NULL DEFAULT '',
			event_name          TEXT NOT NULL DEFAULT '',
			decision            TEXT NOT NULL DEFAULT '',
			reason              TEXT NOT NULL DEFAULT '',
			payload_json        TEXT NOT NULL DEFAULT 'null',
			before_json         TEXT NOT NULL DEFAULT 'null',
			after_json          TEXT NOT NULL DEFAULT 'null',
			created_at          INTEGER NOT NULL DEFAULT 0,
			source_kind         TEXT NOT NULL DEFAULT '',
			action              TEXT NOT NULL DEFAULT '',
			reason_code         TEXT NOT NULL DEFAULT '',
			outcome             TEXT NOT NULL DEFAULT '',
			scenario_key        TEXT NOT NULL DEFAULT '',
			observed_generation INTEGER NOT NULL DEFAULT 0,
			decision_ports      TEXT NOT NULL DEFAULT '[]',
			phase               TEXT NOT NULL DEFAULT '',
			status              TEXT NOT NULL DEFAULT '',
			watcher_token       TEXT,
			trace_id            TEXT NOT NULL DEFAULT '',
			reason_text         TEXT NOT NULL DEFAULT '',
			attrs               TEXT NOT NULL DEFAULT '{}',
			input_refs          TEXT NOT NULL DEFAULT '[]',
			output_refs         TEXT NOT NULL DEFAULT '[]',
			state_before_ref    TEXT NOT NULL DEFAULT '',
			state_after_ref     TEXT NOT NULL DEFAULT '',
			evidence_refs       TEXT NOT NULL DEFAULT '[]',
			started_at          INTEGER NOT NULL DEFAULT 0,
			ended_at            INTEGER NOT NULL DEFAULT 0,
			otel_kind           TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create full steps: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at
		) VALUES
		('chain-full', 10, 20, 'done', 'ok', 'proj-a', '%1', 'cc', 'Stop', 'root', 'terminal', 'done', 'ok', 1, 20)
	`); err != nil {
		t.Fatalf("seed chain: %v", err)
	}
	traceID := uuid.NewString()
	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at,
			source_kind, action, reason_code, outcome, scenario_key,
			observed_generation, decision_ports, phase, status, watcher_token,
			trace_id, reason_text, attrs, input_refs, output_refs,
			state_before_ref, state_after_ref, evidence_refs,
			started_at, ended_at, otel_kind
		) VALUES
		('full-1', 'chain-full', NULL, 1, 'terminal', 'proj-a', '%1', 'cc', 'frame-1', 'frame-0',
		 'Stop', 'done', 'completed',
		 '{"x":1}', '{"b":1}', '{"a":1}', 15,
		 'hook', 'terminal:done', 'completed', 'emitted', 'Stop',
		 42, '[{"port":"statusline","decision":"ok"}]', 'committed', 'success', 'watcher-123',
		 ?, 'reason-text', '{"hook":"post"}', '[{"kind":"event"}]', '[{"kind":"frame"}]',
		 'snap:before', 'snap:after', '[{"source":"tmux"}]',
		 101, 102, 'internal')
	`, traceID); err != nil {
		t.Fatalf("seed step: %v", err)
	}

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces migrate: %v", err)
	}

	store := &TraceStore{db: s.db, maxChains: 10, maxSteps: 10}
	rec, err := store.GetChainRecord("chain-full")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if rec == nil || len(rec.Steps) != 1 {
		t.Fatalf("rec = %+v", rec)
	}
	step := rec.Steps[0]
	// PR-1a cols.
	if step.SourceKind != "hook" || step.Action != "terminal:done" || step.ReasonCode != "completed" ||
		step.Outcome != "emitted" || step.ScenarioKey != "Stop" || step.Phase != "committed" || step.Status != "success" {
		t.Fatalf("pr1a cols wrong: %+v", step)
	}
	if step.ObservedGeneration != 42 {
		t.Fatalf("observed_generation = %d", step.ObservedGeneration)
	}
	if string(step.DecisionPorts) != `[{"port":"statusline","decision":"ok"}]` {
		t.Fatalf("decision_ports = %s", string(step.DecisionPorts))
	}
	if step.WatcherToken == nil || *step.WatcherToken != "watcher-123" {
		t.Fatalf("watcher_token = %v", step.WatcherToken)
	}
	// PR-1b-0 cols.
	if step.TraceID != traceID {
		t.Fatalf("trace_id = %q, want %q", step.TraceID, traceID)
	}
	if step.ReasonText != "reason-text" {
		t.Fatalf("reason_text = %q", step.ReasonText)
	}
	if string(step.Attrs) != `{"hook":"post"}` {
		t.Fatalf("attrs = %s", string(step.Attrs))
	}
	if string(step.InputRefs) != `[{"kind":"event"}]` || string(step.OutputRefs) != `[{"kind":"frame"}]` {
		t.Fatalf("ref cols wrong: %s / %s", string(step.InputRefs), string(step.OutputRefs))
	}
	if step.StateBeforeRef != "snap:before" || step.StateAfterRef != "snap:after" {
		t.Fatalf("state refs = %q / %q", step.StateBeforeRef, step.StateAfterRef)
	}
	if string(step.EvidenceRefs) != `[{"source":"tmux"}]` {
		t.Fatalf("evidence_refs = %s", string(step.EvidenceRefs))
	}
	if step.StartedAt != 101 || step.EndedAt != 102 {
		t.Fatalf("started/ended = %d/%d", step.StartedAt, step.EndedAt)
	}
	if step.OTelKind != "internal" {
		t.Fatalf("otel_kind = %q", step.OTelKind)
	}
}

// TestRebuildLegacyTraceSteps_BackfillsTraceIdForPR1aLightsRows seeds a PR-1a
// schema (17 legacy cols + 10 PR-1a Lights cols, no PR-1b-0 envelope cols) with
// a Lights row (source_kind="hook" …) and a neighbouring legacy row. After
// rebuild the Lights row's trace_id must be backfilled from chain_id so the
// row satisfies validateLightsRow; the pure-legacy row's trace_id must stay
// empty so it keeps round-tripping as a legacy row.
func TestRebuildLegacyTraceSteps_BackfillsTraceIdForPR1aLightsRows(t *testing.T) {
	s := openTestAgentEventStore(t)
	if err := createTraceChainsTable(s.db); err != nil {
		t.Fatalf("create chains: %v", err)
	}
	if _, err := s.db.Exec(`
		CREATE TABLE agent_trace_steps (
			step_id             TEXT PRIMARY KEY,
			chain_id            TEXT NOT NULL,
			parent_step_id      TEXT,
			seq                 INTEGER NOT NULL,
			kind                TEXT NOT NULL DEFAULT '',
			tmux_session        TEXT NOT NULL DEFAULT '',
			pane_id             TEXT NOT NULL DEFAULT '',
			agent_type          TEXT NOT NULL DEFAULT '',
			frame_id            TEXT NOT NULL DEFAULT '',
			parent_frame_id     TEXT NOT NULL DEFAULT '',
			event_name          TEXT NOT NULL DEFAULT '',
			decision            TEXT NOT NULL DEFAULT '',
			reason              TEXT NOT NULL DEFAULT '',
			payload_json        TEXT NOT NULL DEFAULT 'null',
			before_json         TEXT NOT NULL DEFAULT 'null',
			after_json          TEXT NOT NULL DEFAULT 'null',
			created_at          INTEGER NOT NULL DEFAULT 0,
			source_kind         TEXT NOT NULL DEFAULT '',
			action              TEXT NOT NULL DEFAULT '',
			reason_code         TEXT NOT NULL DEFAULT '',
			outcome             TEXT NOT NULL DEFAULT '',
			scenario_key        TEXT NOT NULL DEFAULT '',
			observed_generation INTEGER NOT NULL DEFAULT 0,
			decision_ports      TEXT NOT NULL DEFAULT '[]',
			phase               TEXT NOT NULL DEFAULT '',
			status              TEXT NOT NULL DEFAULT '',
			watcher_token       TEXT,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create PR-1a steps: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at
		) VALUES
		('chain-backfill', 10, 20, 'done', 'ok', 'proj-a', '%1', 'cc', 'Stop', 'root', 'terminal', 'done', 'ok', 2, 20)
	`); err != nil {
		t.Fatalf("seed chain: %v", err)
	}
	// Lights row: source_kind set → trace_id should be backfilled from chain_id.
	// Legacy row: source_kind empty → trace_id must remain empty.
	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at,
			source_kind, action, reason_code, outcome, scenario_key,
			observed_generation, decision_ports, phase, status, watcher_token
		) VALUES
		('lights-row', 'chain-backfill', NULL, 1, 'decision', 'proj-a', '%1', 'cc', 'frame-1', '',
		 'Stop', 'done', 'completed',
		 'null', 'null', 'null', 15,
		 'hook', 'decision:continue', 'ready', 'emitted', 'Stop',
		 1, '[]', 'committed', 'success', NULL),
		('legacy-row', 'chain-backfill', 'lights-row', 2, 'terminal', 'proj-a', '%1', 'cc', 'frame-1', '',
		 'Stop', 'done', '',
		 'null', 'null', 'null', 16,
		 '', '', '', '', '',
		 0, '[]', '', '', NULL)
	`); err != nil {
		t.Fatalf("seed pr1a rows: %v", err)
	}

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces migrate: %v", err)
	}

	store := &TraceStore{db: s.db, maxChains: 10, maxSteps: 10}
	rec, err := store.GetChainRecord("chain-backfill")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if rec == nil || len(rec.Steps) != 2 {
		t.Fatalf("rec = %+v", rec)
	}
	var lights, legacy TraceStep
	for _, step := range rec.Steps {
		switch step.StepID {
		case "lights-row":
			lights = step
		case "legacy-row":
			legacy = step
		}
	}
	if lights.TraceID != "chain-backfill" {
		t.Fatalf("lights trace_id = %q, want chain-backfill (backfilled)", lights.TraceID)
	}
	if err := validateLightsRow(lights); err != nil {
		t.Fatalf("migrated lights row fails validateLightsRow: %v", err)
	}
	if legacy.TraceID != "" {
		t.Fatalf("legacy trace_id = %q, want empty (no backfill)", legacy.TraceID)
	}
	if legacy.SourceKind != "" {
		t.Fatalf("legacy source_kind = %q, want empty", legacy.SourceKind)
	}
}

// TestRebuildLegacyTraceSteps_LegacyRowsTraceIdStaysEmpty is a targeted
// regression guarding the negative half of the backfill: every row in the
// seeded table has source_kind="" so none of them are Lights rows, and the
// rebuild must leave trace_id at the DEFAULT empty string.
func TestRebuildLegacyTraceSteps_LegacyRowsTraceIdStaysEmpty(t *testing.T) {
	s := openTestAgentEventStore(t)
	if err := createTraceChainsTable(s.db); err != nil {
		t.Fatalf("create chains: %v", err)
	}
	if _, err := s.db.Exec(`
		CREATE TABLE agent_trace_steps (
			step_id             TEXT PRIMARY KEY,
			chain_id            TEXT NOT NULL,
			parent_step_id      TEXT,
			seq                 INTEGER NOT NULL,
			kind                TEXT NOT NULL DEFAULT '',
			tmux_session        TEXT NOT NULL DEFAULT '',
			pane_id             TEXT NOT NULL DEFAULT '',
			agent_type          TEXT NOT NULL DEFAULT '',
			frame_id            TEXT NOT NULL DEFAULT '',
			parent_frame_id     TEXT NOT NULL DEFAULT '',
			event_name          TEXT NOT NULL DEFAULT '',
			decision            TEXT NOT NULL DEFAULT '',
			reason              TEXT NOT NULL DEFAULT '',
			payload_json        TEXT NOT NULL DEFAULT 'null',
			before_json         TEXT NOT NULL DEFAULT 'null',
			after_json          TEXT NOT NULL DEFAULT 'null',
			created_at          INTEGER NOT NULL DEFAULT 0,
			source_kind         TEXT NOT NULL DEFAULT '',
			action              TEXT NOT NULL DEFAULT '',
			reason_code         TEXT NOT NULL DEFAULT '',
			outcome             TEXT NOT NULL DEFAULT '',
			scenario_key        TEXT NOT NULL DEFAULT '',
			observed_generation INTEGER NOT NULL DEFAULT 0,
			decision_ports      TEXT NOT NULL DEFAULT '[]',
			phase               TEXT NOT NULL DEFAULT '',
			status              TEXT NOT NULL DEFAULT '',
			watcher_token       TEXT,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create PR-1a steps: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at
		) VALUES
		('chain-legacy-only', 10, 20, 'done', 'ok', 'proj-a', '%1', 'cc', 'Stop', 'root', 'terminal', 'done', 'ok', 1, 20)
	`); err != nil {
		t.Fatalf("seed chain: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at,
			source_kind, action, reason_code, outcome, scenario_key,
			observed_generation, decision_ports, phase, status, watcher_token
		) VALUES
		('legacy-1', 'chain-legacy-only', NULL, 1, 'decision', 'proj-a', '%1', 'cc', 'frame-1', '',
		 'Stop', 'done', 'ok',
		 'null', 'null', 'null', 15,
		 '', '', '', '', '',
		 0, '[]', '', '', NULL)
	`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces migrate: %v", err)
	}

	store := &TraceStore{db: s.db, maxChains: 10, maxSteps: 10}
	rec, err := store.GetChainRecord("chain-legacy-only")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if rec == nil || len(rec.Steps) != 1 {
		t.Fatalf("rec = %+v", rec)
	}
	step := rec.Steps[0]
	if step.TraceID != "" {
		t.Fatalf("trace_id = %q, want empty (legacy row must not be backfilled)", step.TraceID)
	}
	if step.SourceKind != "" {
		t.Fatalf("source_kind = %q, want empty", step.SourceKind)
	}
}

// TestRebuildLegacyTraceSteps_OldSchemaOnlyStillRestores confirms the
// pre-PR-1a legacy schema (step_name/step_index, no Lights cols) still
// rebuilds cleanly with every Lights field left at its DEFAULT — the existing
// TestTraceStore_MigratesLegacy* tests cover behaviour, this adds an explicit
// assertion on the Lights defaults after rebuild.
func TestRebuildLegacyTraceSteps_OldSchemaOnlyStillRestores(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedLegacyTraceSchema(t, s.db)
	seedLegacyTraceData(t, s.db)

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces migrate: %v", err)
	}

	store := &TraceStore{db: s.db, maxChains: 10, maxSteps: 10}
	rec, err := store.GetChainRecord("legacy-chain")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if rec == nil || len(rec.Steps) != 2 {
		t.Fatalf("rec = %+v", rec)
	}
	for _, step := range rec.Steps {
		if step.SourceKind != "" || step.Action != "" || step.Phase != "" || step.Status != "" ||
			step.Outcome != "" || step.ScenarioKey != "" || step.ReasonCode != "" {
			t.Fatalf("legacy schema rebuild must default PR-1a Lights cols, got %+v", step)
		}
		if step.ObservedGeneration != 0 {
			t.Fatalf("observed_generation default = %d", step.ObservedGeneration)
		}
		if string(step.DecisionPorts) != `[]` {
			t.Fatalf("decision_ports default = %s", string(step.DecisionPorts))
		}
		if step.WatcherToken != nil {
			t.Fatalf("watcher_token = %v, want nil", step.WatcherToken)
		}
		if step.TraceID != "" || step.ReasonText != "" || step.OTelKind != "" ||
			step.StateBeforeRef != "" || step.StateAfterRef != "" {
			t.Fatalf("legacy schema rebuild must default PR-1b-0 envelope string cols, got %+v", step)
		}
		if step.StartedAt != 0 || step.EndedAt != 0 {
			t.Fatalf("legacy schema rebuild must default envelope timestamps, got %d/%d", step.StartedAt, step.EndedAt)
		}
		if string(step.Attrs) != `{}` || string(step.InputRefs) != `[]` || string(step.OutputRefs) != `[]` || string(step.EvidenceRefs) != `[]` {
			t.Fatalf("legacy schema rebuild must default PR-1b-0 json cols, got %s / %s / %s / %s",
				string(step.Attrs), string(step.InputRefs), string(step.OutputRefs), string(step.EvidenceRefs))
		}
	}
}

// TestMigrateTraceDB_ResumableIfLegacyTableRemains ensures a second Traces()
// call after a crash-interrupted migration (legacy rename committed, new table
// not yet populated) surfaces the stale state as an error rather than silently
// skipping rebuild.
func TestMigrateTraceDB_ResumableIfLegacyTableRemains(t *testing.T) {
	s := openTestAgentEventStore(t)
	// Simulate a partially-complete migration: new steps table exists (empty)
	// AND agent_trace_steps_legacy remains from an aborted rebuild.
	if err := createTraceChainsTable(s.db); err != nil {
		t.Fatalf("create chains: %v", err)
	}
	if err := createTraceStepsTable(s.db); err != nil {
		t.Fatalf("create steps: %v", err)
	}
	if _, err := s.db.Exec(`
		CREATE TABLE agent_trace_steps_legacy (
			step_id TEXT PRIMARY KEY,
			chain_id TEXT NOT NULL,
			parent_step_id TEXT,
			step_name TEXT NOT NULL,
			payload TEXT NOT NULL DEFAULT 'null',
			step_index INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("seed legacy leftover: %v", err)
	}

	if _, err := s.Traces(); err == nil {
		t.Fatal("expected Traces to fail fast when agent_trace_steps_legacy remains")
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

// ----- AppendSteps tests (PR-1b-1b Task 7 / D10.4) -------------------------
//
// AppendSteps is the TraceWriter's batch sink. Unlike SaveChain — which owns
// an entire chain's lifecycle — AppendSteps inserts steps across arbitrary
// chains and creates minimal chain rows on demand so the Arbitrator never has
// to know whether a given chain already exists in SQLite.

// makeLightsStep returns a canonical Lights step (all five discriminator
// fields populated) for AppendSteps tests. Tests override fields per-case.
func makeLightsStep(stepID, chainID string, seq int, startedAt int64) TraceStep {
	return TraceStep{
		StepID:      stepID,
		ChainID:     chainID,
		Seq:         seq,
		Kind:        "decision",
		TmuxSession: "proj-a",
		PaneID:      "%5",
		AgentType:   "cc",
		EventName:   "Stop",
		CreatedAt:   startedAt,
		SourceKind:  "hook",
		Action:      "decision:ok",
		Outcome:     "emitted",
		Phase:       "committed",
		Status:      "success",
		TraceID:     chainID,
		StartedAt:   startedAt,
		EndedAt:     startedAt,
		OTelKind:    "internal",
	}
}

func TestTraceStore_AppendSteps_NewChain_AutoCreatesChainRow(t *testing.T) {
	s := openTestTraceStore(t)
	step := makeLightsStep("auto-1", "auto-chain", 1, 123)
	if err := s.AppendSteps([]TraceStep{step}); err != nil {
		t.Fatalf("AppendSteps: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_chains WHERE chain_id=?`, "auto-chain").Scan(&count); err != nil {
		t.Fatalf("count chains: %v", err)
	}
	if count != 1 {
		t.Fatalf("auto-created chain rows = %d, want 1", count)
	}
	var startedAt int64
	if err := s.db.QueryRow(`SELECT started_at FROM agent_trace_chains WHERE chain_id=?`, "auto-chain").Scan(&startedAt); err != nil {
		t.Fatalf("chain started_at: %v", err)
	}
	if startedAt != 123 {
		t.Fatalf("chain started_at = %d, want 123", startedAt)
	}

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps WHERE chain_id=?`, "auto-chain").Scan(&count); err != nil {
		t.Fatalf("count steps: %v", err)
	}
	if count != 1 {
		t.Fatalf("steps = %d, want 1", count)
	}
}

func TestTraceStore_AppendSteps_ExistingChain_AppendsSteps_ChainUntouched(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 100

	original := TraceRecord{
		Chain: TraceChain{
			ChainID:          "keeper",
			StartedAt:        50,
			CompletedAt:      60,
			TerminalStatus:   "done",
			TerminalReason:   "finished",
			TmuxSession:      "proj-a",
			PaneID:           "%5",
			RootAgentType:    "cc",
			RootEventName:    "Stop",
			RootReason:       "bootstrap",
			LatestStepKind:   "decision",
			LatestDecision:   "done",
			LatestStepReason: "ok",
		},
		Steps: []TraceStep{makeLightsStep("keeper-1", "keeper", 1, 55)},
	}
	if err := s.SaveChain(original); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	extra := makeLightsStep("keeper-2", "keeper", 2, 200)
	if err := s.AppendSteps([]TraceStep{extra}); err != nil {
		t.Fatalf("AppendSteps: %v", err)
	}

	// Chain summary must be untouched — TerminalStatus, TerminalReason, etc.
	var (
		terminalStatus, terminalReason, latestStepKind string
		startedAt, completedAt                         int64
	)
	if err := s.db.QueryRow(`
		SELECT started_at, completed_at, terminal_status, terminal_reason, latest_step_kind
		FROM agent_trace_chains WHERE chain_id=?`, "keeper").Scan(&startedAt, &completedAt, &terminalStatus, &terminalReason, &latestStepKind); err != nil {
		t.Fatalf("chain summary: %v", err)
	}
	if startedAt != 50 || completedAt != 60 || terminalStatus != "done" || terminalReason != "finished" || latestStepKind != "decision" {
		t.Fatalf("chain summary mutated: started=%d completed=%d terminal=%q/%q latest=%q",
			startedAt, completedAt, terminalStatus, terminalReason, latestStepKind)
	}

	var stepCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps WHERE chain_id=?`, "keeper").Scan(&stepCount); err != nil {
		t.Fatalf("step count: %v", err)
	}
	if stepCount != 2 {
		t.Fatalf("steps = %d, want 2", stepCount)
	}
}

func TestTraceStore_AppendSteps_DuplicateStepID_Ignored(t *testing.T) {
	s := openTestTraceStore(t)
	step := makeLightsStep("dup-1", "dup-chain", 1, 10)
	if err := s.AppendSteps([]TraceStep{step}); err != nil {
		t.Fatalf("first AppendSteps: %v", err)
	}
	// Same step_id with a mutated field; INSERT OR IGNORE must keep first.
	step2 := step
	step2.ReasonText = "mutated"
	if err := s.AppendSteps([]TraceStep{step2}); err != nil {
		t.Fatalf("second AppendSteps (dup): %v", err)
	}

	var reasonText string
	if err := s.db.QueryRow(`SELECT reason_text FROM agent_trace_steps WHERE step_id=?`, "dup-1").Scan(&reasonText); err != nil {
		t.Fatalf("query reason_text: %v", err)
	}
	if reasonText != "" {
		t.Fatalf("reason_text = %q, want first-write-wins (empty)", reasonText)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps WHERE step_id=?`, "dup-1").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows for dup-1 = %d, want 1", count)
	}
}

func TestTraceStore_AppendSteps_MultiChain_SingleTransaction(t *testing.T) {
	s := openTestTraceStore(t)
	steps := []TraceStep{
		makeLightsStep("a-1", "chain-a", 1, 100),
		makeLightsStep("a-2", "chain-a", 2, 101),
		makeLightsStep("b-1", "chain-b", 1, 102),
		makeLightsStep("c-1", "chain-c", 1, 103),
	}
	if err := s.AppendSteps(steps); err != nil {
		t.Fatalf("AppendSteps: %v", err)
	}

	var chainCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_chains WHERE chain_id IN ('chain-a','chain-b','chain-c')`).Scan(&chainCount); err != nil {
		t.Fatalf("chain count: %v", err)
	}
	if chainCount != 3 {
		t.Fatalf("chains = %d, want 3", chainCount)
	}
	var stepCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps WHERE chain_id IN ('chain-a','chain-b','chain-c')`).Scan(&stepCount); err != nil {
		t.Fatalf("step count: %v", err)
	}
	if stepCount != 4 {
		t.Fatalf("steps = %d, want 4", stepCount)
	}
}

func TestTraceStore_AppendSteps_Rollback_OnError(t *testing.T) {
	s := openTestTraceStore(t)
	// First batch seeds two chains cleanly.
	good := []TraceStep{
		makeLightsStep("g-1", "chain-good", 1, 10),
	}
	if err := s.AppendSteps(good); err != nil {
		t.Fatalf("seed good: %v", err)
	}
	// Bad batch: first step OK for a new chain, second step has malformed
	// parent_step_id that violates the composite FK on commit.
	bad := []TraceStep{
		makeLightsStep("rollback-1", "chain-rollback", 1, 20),
		func() TraceStep {
			s := makeLightsStep("rollback-2", "chain-rollback", 2, 21)
			s.ParentStepID = "does-not-exist"
			return s
		}(),
	}
	err := s.AppendSteps(bad)
	if err == nil {
		t.Fatal("expected AppendSteps to error on FK violation")
	}
	// After rollback: chain-rollback must not exist, nor should its steps.
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_chains WHERE chain_id=?`, "chain-rollback").Scan(&count); err != nil {
		t.Fatalf("post-rollback chain count: %v", err)
	}
	if count != 0 {
		t.Fatalf("chain-rollback survived rollback (%d rows)", count)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps WHERE chain_id=?`, "chain-rollback").Scan(&count); err != nil {
		t.Fatalf("post-rollback step count: %v", err)
	}
	if count != 0 {
		t.Fatalf("rollback-chain steps survived rollback (%d rows)", count)
	}
	// Earlier seeded data must be untouched.
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps WHERE step_id=?`, "g-1").Scan(&count); err != nil {
		t.Fatalf("seed verify: %v", err)
	}
	if count != 1 {
		t.Fatalf("g-1 got blown away by rollback (%d)", count)
	}
}

func TestTraceStore_AppendSteps_ConcurrentCallers_NoCorruption(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10000
	s.maxSteps = 100000

	const goroutines = 2
	const perG = 1000
	errCh := make(chan error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			steps := make([]TraceStep, 0, perG)
			chainID := fmt.Sprintf("conc-%d", g)
			for i := 0; i < perG; i++ {
				steps = append(steps, makeLightsStep(fmt.Sprintf("%s-%d", chainID, i), chainID, i+1, int64(i)))
			}
			if err := s.AppendSteps(steps); err != nil {
				errCh <- fmt.Errorf("goroutine %d: %w", g, err)
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent AppendSteps: %v", err)
		}
	}

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps WHERE chain_id LIKE 'conc-%'`).Scan(&total); err != nil {
		t.Fatalf("total count: %v", err)
	}
	if total != goroutines*perG {
		t.Fatalf("concurrent steps = %d, want %d", total, goroutines*perG)
	}
}

func TestTraceStore_AppendSteps_EnvelopeFields_Roundtrip(t *testing.T) {
	s := openTestTraceStore(t)
	traceID := uuid.NewString()
	step := makeLightsStep("env-1", "env-chain", 1, 500)
	step.TraceID = traceID
	step.Attrs = json.RawMessage(`{"agent":"cc","phase":"decision"}`)
	step.InputRefs = json.RawMessage(`[{"kind":"event","id":"e1"}]`)
	step.OutputRefs = json.RawMessage(`[{"kind":"frame","id":"f1"}]`)
	step.EvidenceRefs = json.RawMessage(`[{"source":"tmux"}]`)
	step.DecisionPorts = json.RawMessage(`[{"port":"statusline"}]`)
	step.ReasonText = "ok"
	step.ReasonCode = "Committed"
	step.StateBeforeRef = "snap:before"
	step.StateAfterRef = "snap:after"
	step.Status = "success"
	step.Outcome = "emitted"

	if err := s.AppendSteps([]TraceStep{step}); err != nil {
		t.Fatalf("AppendSteps: %v", err)
	}

	var (
		gotTraceID, gotReasonText, gotReasonCode string
		gotAttrs, gotInputRefs, gotOutputRefs    string
		gotEvidenceRefs, gotDecisionPorts        string
		gotStateBefore, gotStateAfter            string
		gotStatus, gotOutcome                    string
	)
	err := s.db.QueryRow(`
		SELECT trace_id, reason_text, reason_code, attrs, input_refs, output_refs,
		       evidence_refs, decision_ports, state_before_ref, state_after_ref,
		       status, outcome
		FROM agent_trace_steps WHERE step_id=?`, "env-1").Scan(
		&gotTraceID, &gotReasonText, &gotReasonCode,
		&gotAttrs, &gotInputRefs, &gotOutputRefs,
		&gotEvidenceRefs, &gotDecisionPorts,
		&gotStateBefore, &gotStateAfter,
		&gotStatus, &gotOutcome,
	)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotTraceID != traceID {
		t.Fatalf("trace_id = %q, want %q", gotTraceID, traceID)
	}
	if gotReasonText != "ok" || gotReasonCode != "Committed" {
		t.Fatalf("reason = %q/%q", gotReasonText, gotReasonCode)
	}
	if gotAttrs != `{"agent":"cc","phase":"decision"}` {
		t.Fatalf("attrs = %s", gotAttrs)
	}
	if gotInputRefs != `[{"kind":"event","id":"e1"}]` || gotOutputRefs != `[{"kind":"frame","id":"f1"}]` {
		t.Fatalf("refs = %s / %s", gotInputRefs, gotOutputRefs)
	}
	if gotEvidenceRefs != `[{"source":"tmux"}]` || gotDecisionPorts != `[{"port":"statusline"}]` {
		t.Fatalf("evidence/decision = %s / %s", gotEvidenceRefs, gotDecisionPorts)
	}
	if gotStateBefore != "snap:before" || gotStateAfter != "snap:after" {
		t.Fatalf("state refs = %q / %q", gotStateBefore, gotStateAfter)
	}
	if gotStatus != "success" || gotOutcome != "emitted" {
		t.Fatalf("status/outcome = %q / %q", gotStatus, gotOutcome)
	}
}
