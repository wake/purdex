package agent

import (
	"encoding/json"
	"net/http"
	"strconv"

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
	StepCount        int    `json:"step_count"`
}

type MonitorStep struct {
	StepID        string `json:"step_id"`
	ChainID       string `json:"chain_id"`
	ParentStepID  string `json:"parent_step_id,omitempty"`
	Seq           int    `json:"seq"`
	Kind          string `json:"kind"`
	TmuxSession   string `json:"tmux_session"`
	PaneID        string `json:"pane_id"`
	AgentType     string `json:"agent_type"`
	FrameID       string `json:"frame_id"`
	ParentFrameID string `json:"parent_frame_id,omitempty"`
	EventName     string `json:"event_name"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	PayloadJSON   string `json:"payload_json"`
	BeforeJSON    string `json:"before_json"`
	AfterJSON     string `json:"after_json"`
	CreatedAt     int64  `json:"created_at"`
}

type MonitorStepNode struct {
	Step     MonitorStep        `json:"step"`
	Children []*MonitorStepNode `json:"children"`
}

type MonitorProjectionSummary struct {
	TmuxSession    string `json:"tmux_session"`
	PaneID         string `json:"pane_id"`
	PrimaryFrameID string `json:"primary_frame_id"`
	TopFrameID     string `json:"top_frame_id"`
	TopAgentType   string `json:"top_agent_type"`
	LatestChainID  string `json:"latest_chain_id"`
}

func (m *Module) handleMonitorChains(w http.ResponseWriter, r *http.Request) {
	if m.traces == nil {
		writeMonitorError(w, http.StatusServiceUnavailable, "trace store unavailable")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := m.traces.ListChains(store.TraceListFilter{
		TmuxSession: r.URL.Query().Get("session"),
		PaneID:      r.URL.Query().Get("pane"),
		AgentType:   r.URL.Query().Get("agent_type"),
		EventName:   r.URL.Query().Get("event_name"),
		Limit:       limit,
		Cursor:      r.URL.Query().Get("cursor"),
		Before:      parseMonitorBool(r.URL.Query().Get("before")),
	})
	if err != nil {
		writeMonitorError(w, http.StatusInternalServerError, "list chains failed")
		return
	}

	chains := make([]MonitorChainSummary, 0, len(page.Chains))
	for _, chain := range page.Chains {
		chains = append(chains, monitorChainSummaryFromStore(chain))
	}

	writeMonitorJSON(w, map[string]any{
		"chains":      chains,
		"next_cursor": page.NextCursor,
	})
}

func (m *Module) handleMonitorChain(w http.ResponseWriter, r *http.Request) {
	if m.traces == nil {
		writeMonitorError(w, http.StatusServiceUnavailable, "trace store unavailable")
		return
	}

	record, err := m.traces.GetChainRecord(r.PathValue("id"))
	if err != nil {
		writeMonitorError(w, http.StatusInternalServerError, "get chain failed")
		return
	}
	if record == nil {
		writeMonitorError(w, http.StatusNotFound, "trace chain not found")
		return
	}

	writeMonitorJSON(w, map[string]any{
		"chain":     monitorChainSummaryFromStore(record.Chain),
		"step_tree": buildStepTree(record.Steps),
	})
}

func (m *Module) handleMonitorProjection(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	pane := r.URL.Query().Get("pane")
	if session == "" && pane == "" {
		writeMonitorError(w, http.StatusBadRequest, "session or pane is required")
		return
	}

	summary, err := m.monitorProjectionSummary(session, pane)
	if err != nil {
		writeMonitorError(w, http.StatusInternalServerError, "projection failed")
		return
	}

	writeMonitorJSON(w, map[string]any{"projection": summary})
}

func buildStepTree(steps []store.TraceStep) []*MonitorStepNode {
	nodes := make(map[string]*MonitorStepNode, len(steps))
	roots := make([]*MonitorStepNode, 0)

	for _, step := range steps {
		stepNode := &MonitorStepNode{
			Step:     monitorStepFromStore(step),
			Children: []*MonitorStepNode{},
		}
		nodes[step.StepID] = stepNode
	}

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

func (m *Module) monitorProjectionSummary(session, pane string) (*MonitorProjectionSummary, error) {
	latestChainID, err := m.latestMonitorChainID(session, pane)
	if err != nil {
		return nil, err
	}

	var projection *SessionProjection
	switch {
	case session != "":
		projection, err = m.projectionForSession(session)
	case pane != "":
		projection, err = m.projectPane(pane)
	}
	if err != nil {
		return nil, err
	}
	if projection == nil {
		if latestChainID == "" {
			return nil, nil
		}
		return &MonitorProjectionSummary{
			TmuxSession:   session,
			PaneID:        pane,
			LatestChainID: latestChainID,
		}, nil
	}

	summary := &MonitorProjectionSummary{
		TmuxSession:   session,
		PaneID:        projection.PaneID,
		LatestChainID: latestChainID,
	}
	if projection.PrimaryFrame != nil {
		summary.PrimaryFrameID = projection.PrimaryFrame.FrameID
	}
	if projection.TopFrame != nil {
		summary.TopFrameID = projection.TopFrame.FrameID
		summary.TopAgentType = projection.TopFrame.AgentType
	}
	return summary, nil
}

func (m *Module) latestMonitorChainID(session, pane string) (string, error) {
	if m.traces == nil {
		return "", nil
	}
	page, err := m.traces.ListChains(store.TraceListFilter{
		TmuxSession: session,
		PaneID:      pane,
		Limit:       1,
	})
	if err != nil {
		return "", err
	}
	if len(page.Chains) == 0 {
		return "", nil
	}
	return page.Chains[0].ChainID, nil
}

func monitorChainSummaryFromStore(chain store.TraceChain) MonitorChainSummary {
	return MonitorChainSummary{
		ChainID:          chain.ChainID,
		StartedAt:        chain.StartedAt,
		CompletedAt:      chain.CompletedAt,
		TerminalStatus:   chain.TerminalStatus,
		TerminalReason:   chain.TerminalReason,
		TmuxSession:      chain.TmuxSession,
		PaneID:           chain.PaneID,
		RootAgentType:    chain.RootAgentType,
		RootEventName:    chain.RootEventName,
		RootReason:       chain.RootReason,
		LatestStepKind:   chain.LatestStepKind,
		LatestDecision:   chain.LatestDecision,
		LatestStepReason: chain.LatestStepReason,
		StepCount:        chain.StepCount,
	}
}

func monitorStepFromStore(step store.TraceStep) MonitorStep {
	return MonitorStep{
		StepID:        step.StepID,
		ChainID:       step.ChainID,
		ParentStepID:  step.ParentStepID,
		Seq:           step.Seq,
		Kind:          step.Kind,
		TmuxSession:   step.TmuxSession,
		PaneID:        step.PaneID,
		AgentType:     step.AgentType,
		FrameID:       step.FrameID,
		ParentFrameID: step.ParentFrameID,
		EventName:     step.EventName,
		Decision:      step.Decision,
		Reason:        step.Reason,
		PayloadJSON:   monitorRawJSON(step.PayloadJSON),
		BeforeJSON:    monitorRawJSON(step.BeforeJSON),
		AfterJSON:     monitorRawJSON(step.AfterJSON),
		CreatedAt:     step.CreatedAt,
	}
}

func monitorRawJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}

func parseMonitorBool(raw string) bool {
	switch raw {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	default:
		return false
	}
}

func writeMonitorJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeMonitorError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	writeMonitorJSON(w, map[string]string{"error": msg})
}
