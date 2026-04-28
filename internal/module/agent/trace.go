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
			RootEventName: req.PurdexName,
			RootReason:    "hook_post",
		},
		nextSeq: 1,
	}
	collector.triggerStepID = collector.append(
		"",
		"trigger",
		req.AgentType,
		"",
		"",
		req.PurdexName,
		"received",
		"hook_post",
		req,
		nil,
		nil,
	)
	return collector
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
		PayloadJSON:   marshalTraceJSON(payload),
		BeforeJSON:    marshalTraceJSON(before),
		AfterJSON:     marshalTraceJSON(after),
		CreatedAt:     time.Now().UnixNano(),
	})
	c.nextSeq++
	c.chain.LatestStepKind = kind
	c.chain.LatestDecision = decision
	c.chain.LatestStepReason = reason
	return stepID
}

func (c *hookTraceCollector) Verify(req EventRequest, decision, reason string, after any) {
	if c == nil {
		return
	}
	c.verifyStepID = c.append(
		c.triggerStepID,
		"verify",
		req.AgentType,
		"",
		"",
		req.PurdexName,
		decision,
		reason,
		req,
		nil,
		after,
	)
}

func (c *hookTraceCollector) Frame(req EventRequest, meta FrameTraceMeta) {
	if c == nil || meta.Decision == "" {
		return
	}
	// Phase 3: when daemon_restart_recovery hits, merge matched_agent_type
	// into the `after` payload so Inspector / Phase 5 reparent can detect
	// divergence between hook event AgentType (req.AgentType) and the
	// agent family the live process tree confirmed (meta.MatchedAgentType).
	// Equal values mean rebuild confirmed the hook owner; divergent values
	// mean the pane has a different alive agent (e.g. cc parent of a codex
	// hook) — diagnostic signal for proxy collapse failures.
	// (PR #638 codex review round 2 #3 fix.)
	after := meta.After
	if meta.MatchedAgentType != "" {
		if afterMap, ok := after.(map[string]any); ok {
			merged := make(map[string]any, len(afterMap)+1)
			for k, v := range afterMap {
				merged[k] = v
			}
			merged["matched_agent_type"] = meta.MatchedAgentType
			after = merged
		}
	}
	c.frameStepID = c.append(
		c.verifyStepID,
		"frame",
		req.AgentType,
		meta.FrameID,
		meta.ParentFrameID,
		req.PurdexName,
		meta.Decision,
		meta.Reason,
		req,
		meta.Before,
		after,
	)
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
	c.projectionStepID = c.append(
		parent,
		"projection",
		req.AgentType,
		"",
		"",
		req.PurdexName,
		summary.Decision,
		summary.Reason,
		req,
		summary.Before,
		summary.After,
	)
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
	c.append(
		parent,
		"emit",
		agentType,
		"",
		"",
		eventName,
		decision,
		reason,
		payload,
		nil,
		payload,
	)
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
