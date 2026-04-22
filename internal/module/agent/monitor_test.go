package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

func TestHandleMonitorChains_ReturnsPageWithLatestStepSummary(t *testing.T) {
	m := newTestModule(t)
	if err := m.traces.SaveChain(store.TraceRecord{
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
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agent/monitor/chains?session=work&limit=10", nil)
	w := httptest.NewRecorder()

	m.handleMonitorChains(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Chains []struct {
			ChainID          string `json:"chain_id"`
			LatestStepKind   string `json:"latest_step_kind"`
			LatestDecision   string `json:"latest_decision"`
			LatestStepReason string `json:"latest_step_reason"`
		} `json:"chains"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(body.Chains) != 1 {
		t.Fatalf("len(body.Chains) = %d, want 1", len(body.Chains))
	}
	if body.Chains[0].ChainID != "chain-1" {
		t.Fatalf("ChainID = %q, want chain-1", body.Chains[0].ChainID)
	}
	if body.Chains[0].LatestStepKind != "emit" {
		t.Fatalf("LatestStepKind = %q, want emit", body.Chains[0].LatestStepKind)
	}
	if body.Chains[0].LatestDecision != "broadcasted" {
		t.Fatalf("LatestDecision = %q, want broadcasted", body.Chains[0].LatestDecision)
	}
	if body.Chains[0].LatestStepReason != "session_code_resolved" {
		t.Fatalf("LatestStepReason = %q, want session_code_resolved", body.Chains[0].LatestStepReason)
	}
}

func TestHandleMonitorChain_ReturnsOrderedStepTree(t *testing.T) {
	m := newTestModule(t)
	if err := m.traces.SaveChain(store.TraceRecord{
		Chain: store.TraceChain{
			ChainID:          "chain-2",
			StartedAt:        100,
			CompletedAt:      130,
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
		Steps: []store.TraceStep{
			{
				StepID:      "s1",
				ChainID:     "chain-2",
				Seq:         1,
				Kind:        "trigger",
				TmuxSession: "work",
				PaneID:      "%7",
				AgentType:   "codex",
				EventName:   "UserPromptSubmit",
				Decision:    "received",
				Reason:      "hook_post",
				PayloadJSON: json.RawMessage(`{"prompt":"hi"}`),
				BeforeJSON:  json.RawMessage(`null`),
				AfterJSON:   json.RawMessage(`{"accepted":true}`),
				CreatedAt:   100,
			},
			{
				StepID:       "s2",
				ChainID:      "chain-2",
				ParentStepID: "s1",
				Seq:          2,
				Kind:         "verify",
				TmuxSession:  "work",
				PaneID:       "%7",
				AgentType:    "codex",
				EventName:    "UserPromptSubmit",
				Decision:     "accepted",
				Reason:       "verify_passed",
				PayloadJSON:  json.RawMessage(`{"tmux_session":"work"}`),
				BeforeJSON:   json.RawMessage(`null`),
				AfterJSON:    json.RawMessage(`{"accepted":true}`),
				CreatedAt:    110,
			},
			{
				StepID:       "s3",
				ChainID:      "chain-2",
				ParentStepID: "s2",
				Seq:          3,
				Kind:         "emit",
				TmuxSession:  "work",
				PaneID:       "%7",
				AgentType:    "codex",
				EventName:    "UserPromptSubmit",
				Decision:     "broadcasted",
				Reason:       "session_code_resolved",
				PayloadJSON:  json.RawMessage(`{"status":"running"}`),
				BeforeJSON:   json.RawMessage(`null`),
				AfterJSON:    json.RawMessage(`{"status":"running"}`),
				CreatedAt:    120,
			},
		},
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agent/monitor/chains/chain-2", nil)
	req.SetPathValue("id", "chain-2")
	w := httptest.NewRecorder()

	m.handleMonitorChain(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Chain struct {
			ChainID string `json:"chain_id"`
		} `json:"chain"`
		StepTree []struct {
			Step struct {
				StepID       string `json:"step_id"`
				Kind         string `json:"kind"`
				PayloadJSON  string `json:"payload_json"`
				BeforeJSON   string `json:"before_json"`
				AfterJSON    string `json:"after_json"`
				Decision     string `json:"decision"`
				Reason       string `json:"reason"`
				ParentStepID string `json:"parent_step_id"`
			} `json:"step"`
			Children []struct {
				Step struct {
					StepID string `json:"step_id"`
					Kind   string `json:"kind"`
				} `json:"step"`
				Children []struct {
					Step struct {
						StepID string `json:"step_id"`
						Kind   string `json:"kind"`
					} `json:"step"`
				} `json:"children"`
			} `json:"children"`
		} `json:"step_tree"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if body.Chain.ChainID != "chain-2" {
		t.Fatalf("ChainID = %q, want chain-2", body.Chain.ChainID)
	}
	if len(body.StepTree) != 1 {
		t.Fatalf("len(body.StepTree) = %d, want 1", len(body.StepTree))
	}
	root := body.StepTree[0]
	if root.Step.StepID != "s1" || root.Step.Kind != "trigger" {
		t.Fatalf("root step = %+v, want s1 trigger", root.Step)
	}
	if root.Step.PayloadJSON != `{"prompt":"hi"}` {
		t.Fatalf("PayloadJSON = %q, want trigger payload", root.Step.PayloadJSON)
	}
	if len(root.Children) != 1 || root.Children[0].Step.StepID != "s2" || root.Children[0].Step.Kind != "verify" {
		t.Fatalf("children = %+v, want verify child", root.Children)
	}
	if len(root.Children[0].Children) != 1 || root.Children[0].Children[0].Step.StepID != "s3" || root.Children[0].Children[0].Step.Kind != "emit" {
		t.Fatalf("grandchildren = %+v, want emit grandchild", root.Children[0].Children)
	}
}

func TestHandleMonitorProjection_ReturnsTypedSummary(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%7", "work")
	m.tmux = fakeTmux

	if _, err := m.frames.Upsert(store.Frame{
		FrameID:          "frame-1",
		PaneID:           "%7",
		AgentType:        "codex",
		PID:              1001,
		PPID:             1,
		ProcessStartTime: "Sun Apr 20 01:30:00 2026",
		Status:           agentpkg.StatusRunning,
		StartedAt:        100,
		LastSeenAt:       120,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	if err := m.traces.SaveChain(store.TraceRecord{
		Chain: store.TraceChain{
			ChainID:          "chain-latest",
			StartedAt:        120,
			CompletedAt:      130,
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
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agent/monitor/projection?session=work", nil)
	w := httptest.NewRecorder()

	m.handleMonitorProjection(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Projection struct {
			TmuxSession    string `json:"tmux_session"`
			PaneID         string `json:"pane_id"`
			PrimaryFrameID string `json:"primary_frame_id"`
			TopFrameID     string `json:"top_frame_id"`
			TopAgentType   string `json:"top_agent_type"`
			LatestChainID  string `json:"latest_chain_id"`
		} `json:"projection"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if body.Projection.TmuxSession != "work" {
		t.Fatalf("TmuxSession = %q, want work", body.Projection.TmuxSession)
	}
	if body.Projection.PaneID != "%7" {
		t.Fatalf("PaneID = %q, want %%7", body.Projection.PaneID)
	}
	if body.Projection.PrimaryFrameID != "frame-1" {
		t.Fatalf("PrimaryFrameID = %q, want frame-1", body.Projection.PrimaryFrameID)
	}
	if body.Projection.TopFrameID != "frame-1" {
		t.Fatalf("TopFrameID = %q, want frame-1", body.Projection.TopFrameID)
	}
	if body.Projection.TopAgentType != "codex" {
		t.Fatalf("TopAgentType = %q, want codex", body.Projection.TopAgentType)
	}
	if body.Projection.LatestChainID != "chain-latest" {
		t.Fatalf("LatestChainID = %q, want chain-latest", body.Projection.LatestChainID)
	}
}

// TestMonitorStep_IncludesAllEnvelopeFields verifies monitorStepFromStore
// copies every PR-1b-0 envelope field from store.TraceStep onto MonitorStep
// and that the JSON shape (field names + `omitempty` semantics) round-trips
// through encoding/json unchanged. Spec §3.5 / plan PR-1b-1c D6.
func TestMonitorStep_IncludesAllEnvelopeFields(t *testing.T) {
	watcher := "w-1"
	storeStep := store.TraceStep{
		StepID:             "step-envelope",
		ChainID:            "chain-envelope",
		Seq:                1,
		Kind:               "apply",
		TmuxSession:        "work",
		PaneID:             "%1",
		AgentType:          "codex",
		EventName:          "UserPromptSubmit",
		Decision:           "committed",
		Reason:             "arb_apply",
		PayloadJSON:        json.RawMessage(`{"k":"v"}`),
		BeforeJSON:         json.RawMessage(`{"before":true}`),
		AfterJSON:          json.RawMessage(`{"after":true}`),
		CreatedAt:          1000,
		SourceKind:         "hook",
		Action:             "frame_enter",
		ReasonCode:         "hook_pre",
		Outcome:            "committed",
		ScenarioKey:        "codex:UserPromptSubmit",
		ObservedGeneration: 42,
		DecisionPorts:      json.RawMessage(`[{"name":"frame_enter"}]`),
		Phase:              "committed",
		Status:             "active",
		WatcherToken:       &watcher,
		TraceID:            "trace-xyz",
		ReasonText:         "hook pre accepted",
		Attrs:              json.RawMessage(`{"a":1}`),
		InputRefs:          json.RawMessage(`["ref-in"]`),
		OutputRefs:         json.RawMessage(`["ref-out"]`),
		StateBeforeRef:     "frame-before",
		StateAfterRef:      "frame-after",
		EvidenceRefs:       json.RawMessage(`["ev-1"]`),
		StartedAt:          900,
		EndedAt:            1100,
		OTelKind:           "INTERNAL",
	}

	ms := monitorStepFromStore(storeStep)
	raw, err := json.Marshal(ms)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	wantStrings := map[string]string{
		"source_kind":      "hook",
		"action":           "frame_enter",
		"reason_code":      "hook_pre",
		"outcome":          "committed",
		"scenario_key":     "codex:UserPromptSubmit",
		"phase":            "committed",
		"status":           "active",
		"watcher_token":    "w-1",
		"trace_id":         "trace-xyz",
		"reason_text":      "hook pre accepted",
		"state_before_ref": "frame-before",
		"state_after_ref":  "frame-after",
		"otel_kind":        "INTERNAL",
	}
	for k, v := range wantStrings {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("missing field %s in JSON: %s", k, string(raw))
		}
		if gv != v {
			t.Fatalf("%s = %v, want %q", k, gv, v)
		}
	}

	wantNumbers := map[string]float64{
		"observed_generation": 42,
		"started_at":          900,
		"ended_at":            1100,
	}
	for k, v := range wantNumbers {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("missing numeric field %s in JSON: %s", k, string(raw))
		}
		if fv, ok := gv.(float64); !ok || fv != v {
			t.Fatalf("%s = %v, want %v", k, gv, v)
		}
	}

	// JSON raw fields must round-trip as parsed JSON (not double-encoded
	// strings) so SPA consumers can read them without a second parse.
	wantJSON := map[string]string{
		"decision_ports": `[{"name":"frame_enter"}]`,
		"attrs":          `{"a":1}`,
		"input_refs":     `["ref-in"]`,
		"output_refs":    `["ref-out"]`,
		"evidence_refs":  `["ev-1"]`,
	}
	for k, want := range wantJSON {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("missing JSON field %s in JSON: %s", k, string(raw))
		}
		reMarshal, err := json.Marshal(gv)
		if err != nil {
			t.Fatalf("remarshal %s: %v", k, err)
		}
		if string(reMarshal) != want {
			t.Fatalf("%s = %s, want %s", k, string(reMarshal), want)
		}
	}
}

// TestMonitorStep_NormalizeEmptyJSON_ToNull verifies that when the store
// returns an empty/nil json.RawMessage for payload/before/after, the Monitor
// API normalises the value to the JSON string "null" (existing behaviour of
// monitorRawJSON). New RawMessage envelope fields stay omitted via omitempty
// so they do not appear in the response at all (consumer-friendly default).
func TestMonitorStep_NormalizeEmptyJSON_ToNull(t *testing.T) {
	step := store.TraceStep{
		StepID:      "empty",
		ChainID:     "chain-empty",
		Seq:         1,
		Kind:        "trigger",
		TmuxSession: "work",
		PaneID:      "%1",
		// All envelope + legacy JSON fields left nil.
	}

	ms := monitorStepFromStore(step)
	if ms.PayloadJSON != "null" {
		t.Fatalf("PayloadJSON = %q, want %q", ms.PayloadJSON, "null")
	}
	if ms.BeforeJSON != "null" {
		t.Fatalf("BeforeJSON = %q, want %q", ms.BeforeJSON, "null")
	}
	if ms.AfterJSON != "null" {
		t.Fatalf("AfterJSON = %q, want %q", ms.AfterJSON, "null")
	}

	raw, err := json.Marshal(ms)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Legacy JSON fields are non-omitempty strings — they must appear as
	// "null" so SPA consumers can rely on the field existing.
	for _, field := range []string{"payload_json", "before_json", "after_json"} {
		v, ok := got[field]
		if !ok {
			t.Fatalf("field %s missing from JSON (want present=\"null\"): %s", field, string(raw))
		}
		if v != "null" {
			t.Fatalf("%s = %v, want \"null\"", field, v)
		}
	}

	// New RawMessage envelope fields carry omitempty and must be absent
	// when the source step has nothing to surface — they would otherwise
	// leak a bare "null" into every legacy (non-Lights) row.
	for _, field := range []string{
		"decision_ports", "attrs", "input_refs", "output_refs", "evidence_refs",
	} {
		if _, ok := got[field]; ok {
			t.Fatalf("empty envelope field %s should be omitted; JSON = %s", field, string(raw))
		}
	}

	// Likewise, scalar envelope strings/ints stay absent when unset.
	for _, field := range []string{
		"source_kind", "action", "reason_code", "outcome", "scenario_key",
		"observed_generation", "phase", "status", "watcher_token",
		"trace_id", "reason_text", "state_before_ref", "state_after_ref",
		"started_at", "ended_at", "otel_kind",
	} {
		if _, ok := got[field]; ok {
			t.Fatalf("empty envelope field %s should be omitted; JSON = %s", field, string(raw))
		}
	}
}

// TestMonitorChainSummary_IncludesSchemaVersion confirms chain summary
// responses stamp schema_version = "1.0.0-lights-1b" so the SPA can detect a
// store rollback (defender recommendation in PR-1b-1c plan D6).
func TestMonitorChainSummary_IncludesSchemaVersion(t *testing.T) {
	m := newTestModule(t)
	if err := m.traces.SaveChain(store.TraceRecord{
		Chain: store.TraceChain{
			ChainID:     "chain-sv",
			StartedAt:   200,
			CompletedAt: 210,
			TmuxSession: "work",
			PaneID:      "%1",
		},
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	// /chains list endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/agent/monitor/chains?session=work", nil)
	w := httptest.NewRecorder()
	m.handleMonitorChains(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", w.Code)
	}
	var listBody struct {
		Chains []struct {
			SchemaVersion string `json:"schema_version"`
			ChainID       string `json:"chain_id"`
		} `json:"chains"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("list Unmarshal: %v", err)
	}
	if len(listBody.Chains) != 1 {
		t.Fatalf("len(listBody.Chains) = %d, want 1", len(listBody.Chains))
	}
	if listBody.Chains[0].SchemaVersion != "1.0.0-lights-1b" {
		t.Fatalf("list schema_version = %q, want %q", listBody.Chains[0].SchemaVersion, "1.0.0-lights-1b")
	}

	// /chains/{id} detail endpoint
	req = httptest.NewRequest(http.MethodGet, "/api/agent/monitor/chains/chain-sv", nil)
	req.SetPathValue("id", "chain-sv")
	w = httptest.NewRecorder()
	m.handleMonitorChain(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", w.Code)
	}
	var detailBody struct {
		Chain struct {
			SchemaVersion string `json:"schema_version"`
			ChainID       string `json:"chain_id"`
		} `json:"chain"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("detail Unmarshal: %v", err)
	}
	if detailBody.Chain.SchemaVersion != "1.0.0-lights-1b" {
		t.Fatalf("detail schema_version = %q, want %q", detailBody.Chain.SchemaVersion, "1.0.0-lights-1b")
	}
	if detailBody.Chain.ChainID != "chain-sv" {
		t.Fatalf("detail chain_id = %q, want chain-sv", detailBody.Chain.ChainID)
	}
}

// TestMonitorProjection_UnchangedShape is a regression covering the existing
// non-envelope fields on MonitorStep and MonitorProjectionSummary — none of
// those names or types may change as part of T9.
func TestMonitorProjection_UnchangedShape(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%1", "work")
	m.tmux = fakeTmux

	if _, err := m.frames.Upsert(store.Frame{
		FrameID:          "frame-regression",
		PaneID:           "%1",
		AgentType:        "codex",
		PID:              2001,
		PPID:             1,
		ProcessStartTime: "Sun Apr 20 01:30:00 2026",
		Status:           agentpkg.StatusRunning,
		StartedAt:        100,
		LastSeenAt:       110,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	if err := m.traces.SaveChain(store.TraceRecord{
		Chain: store.TraceChain{
			ChainID:          "chain-regression",
			StartedAt:        100,
			CompletedAt:      120,
			TerminalStatus:   "completed",
			TerminalReason:   "emit_broadcasted",
			TmuxSession:      "work",
			PaneID:           "%1",
			RootAgentType:    "codex",
			RootEventName:    "UserPromptSubmit",
			RootReason:       "hook_post",
			LatestStepKind:   "emit",
			LatestDecision:   "broadcasted",
			LatestStepReason: "session_code_resolved",
		},
		Steps: []store.TraceStep{
			{
				StepID:      "reg-1",
				ChainID:     "chain-regression",
				Seq:         1,
				Kind:        "trigger",
				TmuxSession: "work",
				PaneID:      "%1",
				AgentType:   "codex",
				EventName:   "UserPromptSubmit",
				Decision:    "received",
				Reason:      "hook_post",
				PayloadJSON: json.RawMessage(`{"prompt":"hi"}`),
				CreatedAt:   100,
			},
		},
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	// projection endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/agent/monitor/projection?session=work", nil)
	w := httptest.NewRecorder()
	m.handleMonitorProjection(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("projection status = %d, want 200", w.Code)
	}
	var proj struct {
		Projection struct {
			TmuxSession    string `json:"tmux_session"`
			PaneID         string `json:"pane_id"`
			PrimaryFrameID string `json:"primary_frame_id"`
			TopFrameID     string `json:"top_frame_id"`
			TopAgentType   string `json:"top_agent_type"`
			LatestChainID  string `json:"latest_chain_id"`
		} `json:"projection"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &proj); err != nil {
		t.Fatalf("projection Unmarshal: %v", err)
	}
	if proj.Projection.TmuxSession != "work" ||
		proj.Projection.PaneID != "%1" ||
		proj.Projection.PrimaryFrameID != "frame-regression" ||
		proj.Projection.TopFrameID != "frame-regression" ||
		proj.Projection.TopAgentType != "codex" ||
		proj.Projection.LatestChainID != "chain-regression" {
		t.Fatalf("projection shape regressed: %+v", proj.Projection)
	}

	// detail endpoint — step level legacy shape still present
	req = httptest.NewRequest(http.MethodGet, "/api/agent/monitor/chains/chain-regression", nil)
	req.SetPathValue("id", "chain-regression")
	w = httptest.NewRecorder()
	m.handleMonitorChain(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", w.Code)
	}
	var detail struct {
		Chain struct {
			ChainID          string `json:"chain_id"`
			StartedAt        int64  `json:"started_at"`
			TerminalStatus   string `json:"terminal_status"`
			LatestStepKind   string `json:"latest_step_kind"`
			LatestDecision   string `json:"latest_decision"`
			LatestStepReason string `json:"latest_step_reason"`
			StepCount        int    `json:"step_count"`
		} `json:"chain"`
		StepTree []struct {
			Step struct {
				StepID      string `json:"step_id"`
				ChainID     string `json:"chain_id"`
				Seq         int    `json:"seq"`
				Kind        string `json:"kind"`
				TmuxSession string `json:"tmux_session"`
				PaneID      string `json:"pane_id"`
				AgentType   string `json:"agent_type"`
				EventName   string `json:"event_name"`
				Decision    string `json:"decision"`
				Reason      string `json:"reason"`
				PayloadJSON string `json:"payload_json"`
				BeforeJSON  string `json:"before_json"`
				AfterJSON   string `json:"after_json"`
				CreatedAt   int64  `json:"created_at"`
			} `json:"step"`
		} `json:"step_tree"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("detail Unmarshal: %v", err)
	}
	// SaveChain / normalizeTraceRecord overwrites latest_* from the last
	// step when present; we assert against the post-normalize values which
	// mirror the single step we wrote above.
	if detail.Chain.ChainID != "chain-regression" ||
		detail.Chain.TerminalStatus != "completed" ||
		detail.Chain.LatestStepKind != "trigger" ||
		detail.Chain.LatestDecision != "received" ||
		detail.Chain.LatestStepReason != "hook_post" ||
		detail.Chain.StepCount != 1 {
		t.Fatalf("chain summary shape regressed: %+v", detail.Chain)
	}
	if len(detail.StepTree) != 1 {
		t.Fatalf("len(StepTree) = %d, want 1", len(detail.StepTree))
	}
	step := detail.StepTree[0].Step
	if step.StepID != "reg-1" ||
		step.ChainID != "chain-regression" ||
		step.Seq != 1 ||
		step.Kind != "trigger" ||
		step.TmuxSession != "work" ||
		step.PaneID != "%1" ||
		step.AgentType != "codex" ||
		step.EventName != "UserPromptSubmit" ||
		step.Decision != "received" ||
		step.Reason != "hook_post" ||
		step.PayloadJSON != `{"prompt":"hi"}` ||
		step.BeforeJSON != "null" ||
		step.AfterJSON != "null" ||
		step.CreatedAt != 100 {
		t.Fatalf("step legacy shape regressed: %+v", step)
	}
}
