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
