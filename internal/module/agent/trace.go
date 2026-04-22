package agent

import (
	"encoding/json"
	"log"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wake/purdex/internal/store"
)

type hookTraceSink struct {
	store   *store.TraceStore
	queue   chan store.TraceRecord
	pending sync.WaitGroup
	worker  sync.WaitGroup
	close   sync.Once
}

func newHookTraceSink(traces *store.TraceStore) *hookTraceSink {
	if traces == nil {
		return nil
	}
	sink := &hookTraceSink{
		store: traces,
		queue: make(chan store.TraceRecord, 256),
	}
	sink.worker.Add(1)
	go func() {
		defer sink.worker.Done()
		for record := range sink.queue {
			if err := sink.store.SaveChain(record); err != nil {
				log.Printf("[agent][trace] save chain %s: %v", record.Chain.ChainID, err)
			}
			sink.pending.Done()
		}
	}()
	return sink
}

func (s *hookTraceSink) Enqueue(record store.TraceRecord) {
	if s == nil {
		return
	}
	s.pending.Add(1)
	select {
	case s.queue <- record:
	default:
		s.pending.Done()
		log.Printf("[agent][trace] drop chain %s: queue full", record.Chain.ChainID)
	}
}

func (s *hookTraceSink) FlushForTest() {
	if s == nil {
		return
	}
	s.pending.Wait()
}

func (s *hookTraceSink) Close() {
	if s == nil {
		return
	}
	s.close.Do(func() {
		s.pending.Wait()
		close(s.queue)
		s.worker.Wait()
	})
}

type hookTraceCollector struct {
	sink             *hookTraceSink
	chain            store.TraceChain
	steps            []store.TraceStep
	nextSeq          int
	triggerStepID    string
	verifyStepID     string
	frameStepID      string
	projectionStepID string
	finished         bool
}

func beginHookTrace(sink *hookTraceSink, req EventRequest) *hookTraceCollector {
	if sink == nil {
		return nil
	}
	collector := &hookTraceCollector{
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
	collector.triggerStepID = collector.append(traceStepInput{
		Kind:      "trigger",
		AgentType: req.AgentType,
		EventName: req.EventName,
		Decision:  "received",
		Reason:    "hook_post",
		Payload:   req,
	})
	return collector
}

// traceStepInput carries the optional fields for a single trace step; zero
// values fall back to hook-path defaults inside append().
type traceStepInput struct {
	// Identity
	ParentStepID  string
	Kind          string
	AgentType     string
	FrameID       string
	ParentFrameID string
	EventName     string
	Decision      string
	Reason        string

	// Payload
	Payload any
	Before  any
	After   any

	// Lights envelope (spec §3.5)
	SourceKind         string
	Action             string
	ReasonCode         string
	Outcome            string
	ScenarioKey        string
	ObservedGeneration int64
	DecisionPorts      string
	Phase              string
	Status             string
	WatcherToken       *string
}

func (c *hookTraceCollector) append(in traceStepInput) string {
	if c == nil {
		return ""
	}
	stepID := uuid.NewString()
	sourceKind := in.SourceKind
	if sourceKind == "" {
		sourceKind = "hook"
	}
	phase := in.Phase
	if phase == "" {
		// Spec §3.5 phase ∈ {proposed, committed, rejected}. Derive from the
		// decision per call site (rejected/skipped/unchanged stay proposed;
		// frame upserts and successful emits commit).
		phase = derivePhaseFromDecision(in.Decision)
	}
	outcome := in.Outcome
	if outcome == "" {
		outcome = deriveOutcomeFromDecision(in.Decision)
	}
	// Status defaults to "success": every appended step represents a
	// completed observation. Status=failure is reserved for execution
	// errors (panic / marshal / DB write) — not for rejected decisions,
	// which spec §3.5 keeps under outcome.
	status := in.Status
	if status == "" {
		status = "success"
	}
	action := in.Action
	if action == "" {
		action = in.Kind + ":" + in.Decision
	}
	scenarioKey := in.ScenarioKey
	if scenarioKey == "" {
		scenarioKey = in.EventName
	}
	reasonCode := in.ReasonCode
	if reasonCode == "" {
		reasonCode = in.Reason
	}
	decisionPorts := in.DecisionPorts
	if decisionPorts == "" {
		decisionPorts = "[]"
	}
	c.steps = append(c.steps, store.TraceStep{
		StepID:             stepID,
		ChainID:            c.chain.ChainID,
		ParentStepID:       in.ParentStepID,
		Seq:                c.nextSeq,
		Kind:               in.Kind,
		TmuxSession:        c.chain.TmuxSession,
		PaneID:             c.chain.PaneID,
		AgentType:          in.AgentType,
		FrameID:            in.FrameID,
		ParentFrameID:      in.ParentFrameID,
		EventName:          in.EventName,
		Decision:           in.Decision,
		Reason:             in.Reason,
		PayloadJSON:        marshalTraceJSON(in.Payload),
		BeforeJSON:         marshalTraceJSON(in.Before),
		AfterJSON:          marshalTraceJSON(in.After),
		CreatedAt:          time.Now().UnixNano(),
		SourceKind:         sourceKind,
		Action:             action,
		ReasonCode:         reasonCode,
		Outcome:            outcome,
		ScenarioKey:        scenarioKey,
		ObservedGeneration: in.ObservedGeneration,
		DecisionPorts:      json.RawMessage(decisionPorts),
		Phase:              phase,
		Status:             status,
		WatcherToken:       in.WatcherToken,
	})
	c.nextSeq++
	c.chain.LatestStepKind = in.Kind
	c.chain.LatestDecision = in.Decision
	c.chain.LatestStepReason = in.Reason
	return stepID
}

func (c *hookTraceCollector) Verify(req EventRequest, decision, reason string, after any) {
	if c == nil {
		return
	}
	c.verifyStepID = c.append(traceStepInput{
		ParentStepID: c.triggerStepID,
		Kind:         "verify",
		AgentType:    req.AgentType,
		EventName:    req.EventName,
		Decision:     decision,
		Reason:       reason,
		Payload:      req,
		After:        after,
	})
}

func (c *hookTraceCollector) Frame(req EventRequest, meta FrameTraceMeta) {
	if c == nil || meta.Decision == "" {
		return
	}
	c.frameStepID = c.append(traceStepInput{
		ParentStepID:  c.verifyStepID,
		Kind:          "frame",
		AgentType:     req.AgentType,
		FrameID:       meta.FrameID,
		ParentFrameID: meta.ParentFrameID,
		EventName:     req.EventName,
		Decision:      meta.Decision,
		Reason:        meta.Reason,
		Payload:       req,
		Before:        meta.Before,
		After:         meta.After,
	})
}

type ProjectionTraceSummary struct {
	Decision string
	Reason   string
	Before   any
	After    any
}

func (c *hookTraceCollector) Projection(req EventRequest, summary ProjectionTraceSummary) {
	if c == nil || summary.Decision == "" {
		return
	}
	parent := c.frameStepID
	if parent == "" {
		parent = c.verifyStepID
	}
	c.projectionStepID = c.append(traceStepInput{
		ParentStepID: parent,
		Kind:         "projection",
		AgentType:    req.AgentType,
		EventName:    req.EventName,
		Decision:     summary.Decision,
		Reason:       summary.Reason,
		Payload:      req,
		Before:       summary.Before,
		After:        summary.After,
	})
}

func (c *hookTraceCollector) Emit(payload any, agentType, eventName, decision, reason string) {
	if c == nil {
		return
	}
	parent := c.projectionStepID
	if parent == "" {
		parent = c.frameStepID
	}
	if parent == "" {
		parent = c.verifyStepID
	}
	c.append(traceStepInput{
		ParentStepID: parent,
		Kind:         "emit",
		AgentType:    agentType,
		EventName:    eventName,
		Decision:     decision,
		Reason:       reason,
		Payload:      payload,
		After:        payload,
	})
}

func (c *hookTraceCollector) Finish(status, reason string) {
	if c == nil || c.finished {
		return
	}
	c.finished = true
	c.chain.CompletedAt = time.Now().UnixNano()
	c.chain.TerminalStatus = status
	c.chain.TerminalReason = reason
	record := store.TraceRecord{
		Chain: c.chain,
		Steps: append([]store.TraceStep(nil), c.steps...),
	}
	c.sink.Enqueue(record)
}

// hookDecisionVocab enumerates every decision literal the agent hook path
// emits (trigger / verify / frame / projection / emit call sites). An
// unrecognised decision maps to an empty outcome — the previous "fall back
// to emitted" default masked classification gaps that this PR's round-2
// review flagged.
var hookDecisionVocab = map[string]struct {
	outcome string
	phase   string
}{
	// trigger
	"received": {outcome: "received", phase: "proposed"},
	// verify
	"accepted": {outcome: "accepted", phase: "proposed"},
	"rejected": {outcome: "rejected", phase: "rejected"},
	// frame_ops
	"created_frame": {outcome: "created_frame", phase: "committed"},
	"updated_frame": {outcome: "updated_frame", phase: "committed"},
	"deleted_frame": {outcome: "deleted_frame", phase: "committed"},
	"skipped":       {outcome: "skipped", phase: "proposed"},
	// projection
	"projection_changed":   {outcome: "projection_changed", phase: "committed"},
	"projection_unchanged": {outcome: "projection_unchanged", phase: "proposed"},
	// emit
	"broadcasted": {outcome: "broadcasted", phase: "committed"},
}

// deriveOutcomeFromDecision maps a known decision literal onto the spec §3.5
// Outcome vocabulary. Unknown decisions return "" so gaps surface instead of
// defaulting to a happy-path value; the empty decision is treated as skipped
// for legacy/no-op call sites.
func deriveOutcomeFromDecision(decision string) string {
	if decision == "" {
		return "skipped"
	}
	if entry, ok := hookDecisionVocab[decision]; ok {
		return entry.outcome
	}
	return ""
}

// derivePhaseFromDecision maps a known decision literal onto Phase ∈
// {proposed, committed, rejected}. Empty / unknown decisions default to
// "proposed" — an observation without committed side-effects.
func derivePhaseFromDecision(decision string) string {
	if entry, ok := hookDecisionVocab[decision]; ok {
		return entry.phase
	}
	return "proposed"
}

func marshalTraceJSON(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage(`{}`)
	}
	buf, err := json.Marshal(v)
	if err != nil || len(buf) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(buf)
}

func summarizeProjectionChange(before, after *SessionProjection) ProjectionTraceSummary {
	beforeSummary := summarizeProjection(before)
	afterSummary := summarizeProjection(after)

	decision := "projection_changed"
	if reflect.DeepEqual(beforeSummary, afterSummary) {
		decision = "projection_unchanged"
	}

	reason := "top_frame_recomputed"
	switch {
	case after == nil || after.TopFrame == nil:
		reason = "frame_removed"
	case before == nil || before.TopFrame == nil:
		reason = "frame_upserted"
	case before.TopFrame.FrameID == after.TopFrame.FrameID:
		reason = "top_frame_stable"
	}

	return ProjectionTraceSummary{
		Decision: decision,
		Reason:   reason,
		Before:   beforeSummary,
		After:    afterSummary,
	}
}

func summarizeProjection(projection *SessionProjection) map[string]any {
	summary := map[string]any{
		"pane_id":          "",
		"primary_frame_id": "",
		"top_frame_id":     "",
		"top_agent_type":   "",
		"subagent_count":   0,
	}
	if projection == nil {
		return summary
	}
	summary["pane_id"] = projection.PaneID
	summary["subagent_count"] = len(projection.Subagents)
	if projection.PrimaryFrame != nil {
		summary["primary_frame_id"] = projection.PrimaryFrame.FrameID
	}
	if projection.TopFrame != nil {
		summary["top_frame_id"] = projection.TopFrame.FrameID
		summary["top_agent_type"] = projection.TopFrame.AgentType
	}
	return summary
}
