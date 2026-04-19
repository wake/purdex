package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

type verifyDecision struct {
	Accepted bool
	Reason   string
}

var (
	verifyEventFn         = defaultVerifyEvent
	isPidAliveFn          = probe.IsPidAlive
	processStartTimeFn    = probe.ProcessStartTime
	pidAncestorIncludesFn = probe.PidAncestorIncludes
	readProcessInfoFn     = agentpkg.ReadProcessInfo
	resolvePanePIDFn      = resolvePanePID
)

func defaultVerifyEvent(m *Module, req EventRequest) (decision verifyDecision) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[agent][verify] warning: panic while verifying pid=%d pane=%s: %v", req.SenderPID, req.TmuxPaneID, recovered)
			decision = verifyDecision{Accepted: true}
		}
	}()
	return m.verifyEvent(req)
}

func (m *Module) verifyEvent(req EventRequest) verifyDecision {
	if req.SenderUncertain {
		return verifyDecision{Reason: "sender_uncertain"}
	}
	if !isPidAliveFn(req.SenderPID) {
		return verifyDecision{Reason: "pid_dead"}
	}
	actualStart, err := processStartTimeFn(req.SenderPID)
	if err != nil {
		log.Printf("[agent][verify] warning: start_time lookup failed for pid=%d: %v", req.SenderPID, err)
		return verifyDecision{Accepted: true}
	}
	if strings.TrimSpace(actualStart) != strings.TrimSpace(req.SenderStartTime) {
		return verifyDecision{Reason: "pid_reused"}
	}
	if m.tmux == nil {
		log.Printf("[agent][verify] warning: tmux executor unavailable for pid=%d pane=%s", req.SenderPID, req.TmuxPaneID)
		return verifyDecision{Accepted: true}
	}
	panePID, err := resolvePanePIDFn(m.tmux, req.TmuxPaneID)
	if err != nil {
		return verifyDecision{Reason: "pane_unresolvable"}
	}
	if !pidAncestorIncludesFn(req.SenderPID, panePID) {
		return verifyDecision{Reason: "pid_not_in_pane_tree"}
	}
	provider, ok := m.registry.Get(req.AgentType)
	if !ok {
		return verifyDecision{Reason: "identify_mismatch"}
	}
	info, err := readProcessInfoFn(req.SenderPID)
	if err != nil {
		log.Printf("[agent][verify] warning: process lookup failed for pid=%d: %v", req.SenderPID, err)
		return verifyDecision{Accepted: true}
	}
	if !provider.Identify(info) {
		return verifyDecision{Reason: "identify_mismatch"}
	}
	return verifyDecision{Accepted: true}
}

func resolvePanePID(exec tmux.Executor, paneID string) (int, error) {
	pid, err := exec.PanePID(paneID)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(pid))
	if err != nil {
		return 0, fmt.Errorf("parse pane pid %q: %w", pid, err)
	}
	return parsed, nil
}

func writeVerifyRejected(w http.ResponseWriter, req EventRequest, reason string) {
	log.Printf("[agent][verify] rejected pid=%d reason=%s pane=%s", req.SenderPID, reason, req.TmuxPaneID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "rejected",
		"reason": reason,
	})
}
