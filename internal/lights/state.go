// Package lights derives an agent's light (status, subagent dots and the
// background symbol) from the mod event stream (spec §7, lights v2).
//
// It is pure: no I/O and no clock reads. The caller feeds every event of
// one stream in seq order with the time it received it, and asks for the
// status, the dots and whether the stream is still live.
package lights

import (
	"bytes"
	"cmp"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// Background is the corner symbol for the session's background work (N6).
type Background string

// The background symbols; the empty Background shows nothing.
const (
	BackgroundWorkflow Background = "workflow"
	BackgroundMonitor  Background = "monitor"
	BackgroundSchedule Background = "schedule"
)

// LiveWindow is how long after its last event a stream still drives the
// light. The mod beats every 10 s, so three beats may be lost.
const LiveWindow = 30 * time.Second

// Task is one background task as the mod reports it (from the CC Stop
// hook's background_tasks, which lists only tasks in flight).
type Task struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

// Dot is one subagent dot. StartedAt is the spawn event's at (ms), or the
// heartbeat's at for an agent first seen in a heartbeat.
type Dot struct {
	ID        string
	StartedAt int64
}

// StreamState is the light state of one mod stream. It is not safe for
// concurrent use.
type StreamState struct {
	Stream, SID string
	LastEvent   time.Time       // daemon receive time of the last applied event
	TurnID      string          // main turn in progress
	Asks        map[string]bool // open asks by tool_use_id ("check:<seq>" for a check without one)
	Compacting  bool
	Err         bool
	Ended       bool
	Dots        map[string]Dot // keyed by agent id
	Background  Background
	// StatusEventAt is the daemon receive time of the last event that moved,
	// or could have moved, the light (see apply). A heartbeat, usage,
	// background or agent.spawn does not move it, so a hook edge compared
	// against it is not handed back by a mere beat. Zero until the first.
	StatusEventAt time.Time
}

// NewStreamState returns the empty state of stream: idle, no dots, not
// live until its first event.
func NewStreamState(stream string) *StreamState {
	return &StreamState{Stream: stream, Asks: map[string]bool{}, Dots: map[string]Dot{}}
}

// typeToolApproved is the mod's report that the person approved a
// permission ask (M-U1-6). internal/modevents names it TypeToolApproved in
// a parallel change; lights keeps its own spelling so neither waits on the
// other.
const typeToolApproved = "tool.approved"

// The ask tools: their tool.start waits on the person until their tool.end.
var askTools = map[string]bool{"AskUserQuestion": true, "ExitPlanMode": true}

// The agent.list statuses that keep a dot.
var activeAgent = map[string]bool{"pending": true, "running": true, "waiting": true}

// Apply applies one event received at now. It reports whether Status(),
// DotList(), Background or SID changed; LastEvent always moves and is not
// a change. StatusEventAt moves only with the events apply says touch the
// light. Data that does not decode leaves everything but SID, LastEvent and
// Ended as it was.
func (s *StreamState) Apply(ev modevents.Event, now time.Time) (changed bool) {
	status, bg, sid := s.Status(), s.Background, s.SID
	dots := maps.Clone(s.Dots)

	s.SID = ev.SID
	s.LastEvent = now
	s.Ended = false // any later event reopens an ended stream, as in the registry
	if s.apply(ev) {
		s.StatusEventAt = now
	}

	return status != s.Status() || bg != s.Background || sid != s.SID || !maps.Equal(dots, s.Dots)
}

// apply applies ev and reports whether it touched the inputs of Status(): a
// session boundary, a main turn's start or end, an ask opened or closed, a
// main compaction. Events that merely repeat or refine the state (heartbeat,
// usage, background, agent.spawn), a subagent's turn, a check that allowed, a
// tool that was not waiting on the person and data that does not decode
// report false.
func (s *StreamState) apply(ev modevents.Event) (touched bool) {
	switch ev.Type {
	case modevents.TypeSessionStart, modevents.TypeSessionSwitch:
		var d struct{}
		if decode(ev.Data, &d) {
			s.reset()
			touched = true
		}
	case modevents.TypeSessionEnd:
		var d struct {
			Reason string `json:"reason"`
		}
		if decode(ev.Data, &d) && d.Reason != "clear" && d.Reason != "resume" {
			s.Ended = true
			s.Background = ""
			s.Err = false // spec §7: session.end leaves error
			touched = true
		}
	case modevents.TypeTurnStart:
		var d struct {
			TurnID  string `json:"turn_id"`
			AgentID string `json:"agent_id"`
		}
		if decode(ev.Data, &d) && d.AgentID == "" {
			s.TurnID = d.TurnID
			clear(s.Asks)
			s.Err = false
			touched = true
		}
	case modevents.TypeTurnComplete:
		var d struct {
			Reason  string `json:"reason"`
			AgentID string `json:"agent_id"`
		}
		if !decode(ev.Data, &d) {
			return false
		}
		if d.AgentID != "" {
			delete(s.Dots, d.AgentID)
			return false
		}
		s.TurnID = ""
		clear(s.Asks)
		s.Err = d.Reason == "error"
		touched = true
	case modevents.TypeToolCheck:
		var d struct {
			ToolUseID string `json:"tool_use_id"`
			Decision  string `json:"decision"`
		}
		if decode(ev.Data, &d) && d.Decision == "ask" {
			id := d.ToolUseID
			if id == "" {
				id = "check:" + strconv.FormatInt(ev.Seq, 10)
			}
			s.Asks[id] = true
			touched = true
		}
	case modevents.TypeToolStart:
		var d struct {
			Tool      string `json:"tool"`
			ToolUseID string `json:"tool_use_id"`
		}
		if decode(ev.Data, &d) && askTools[d.Tool] && d.ToolUseID != "" {
			s.Asks[d.ToolUseID] = true
			touched = true
		}
	case modevents.TypeToolEnd, typeToolApproved:
		// An approved permission ask stops waiting on the person at once;
		// the tool itself runs on until its tool.end.
		var d struct {
			ToolUseID string `json:"tool_use_id"`
		}
		if decode(ev.Data, &d) {
			touched = s.Asks[d.ToolUseID]
			delete(s.Asks, d.ToolUseID)
		}
	case modevents.TypeCompactStart, modevents.TypeCompactEnd:
		var d struct {
			Trigger string `json:"trigger"`
			AgentID string `json:"agent_id"`
		}
		if decode(ev.Data, &d) && d.AgentID == "" && d.Trigger != "precompute" {
			s.Compacting = ev.Type == modevents.TypeCompactStart
			touched = true
		}
	case modevents.TypeAgentSpawn:
		var d struct {
			AgentID       string `json:"agent_id"`
			WorkflowRunID string `json:"workflow_run_id"`
		}
		// A workflow's agents never get a dot (N6).
		if decode(ev.Data, &d) && d.AgentID != "" && d.WorkflowRunID == "" {
			if _, ok := s.Dots[d.AgentID]; !ok {
				s.Dots[d.AgentID] = Dot{ID: d.AgentID, StartedAt: ev.At}
			}
		}
	case modevents.TypeBackground:
		var d backgroundData
		if decode(ev.Data, &d) {
			s.Background = BackgroundKind(d.Tasks, d.Crons)
		}
	case modevents.TypeHeartbeat:
		s.reconcile(ev)
	}
	return touched
}

type backgroundData struct {
	Tasks []Task `json:"tasks"`
	Crons int    `json:"crons"`
}

type heartbeatAgent struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// reconcile replaces the mirrored state with the heartbeat's, repairing
// events that were lost (an Esc without turn.complete, a daemon restart).
// A field that is absent or null leaves its part of the state alone, except
// turn_id: the mod omits it exactly when no main turn runs.
func (s *StreamState) reconcile(ev modevents.Event) {
	var d struct {
		TurnID     string            `json:"turn_id"`
		Asks       *[]string         `json:"asks"`
		Compacting *bool             `json:"compacting"`
		Agents     *[]heartbeatAgent `json:"agents"`     // omitted when $.agent.list() fails
		Error      *bool             `json:"error"`      // absent from mods older than U1-2a-1
		Background *backgroundData   `json:"background"` // present only while there is one
	}
	if !decode(ev.Data, &d) {
		return
	}
	s.TurnID = d.TurnID
	if d.Asks != nil {
		clear(s.Asks)
		for _, id := range *d.Asks {
			if id != "" {
				s.Asks[id] = true
			}
		}
	}
	if d.Compacting != nil {
		s.Compacting = *d.Compacting
	}
	if d.Error != nil {
		s.Err = *d.Error
	}
	if d.Agents != nil {
		s.reconcileDots(*d.Agents, ev.At)
	}
	if d.Background != nil {
		s.Background = BackgroundKind(d.Background.Tasks, d.Background.Crons)
	}
}

// reconcileDots keeps a dot for exactly the listed active agents; one not
// dotted yet starts at the heartbeat's at. $.agent.list() never lists
// workflow agents, so every listed active agent is one that gets a dot.
func (s *StreamState) reconcileDots(agents []heartbeatAgent, at int64) {
	active := make(map[string]bool, len(agents))
	for _, a := range agents {
		if a.ID != "" && activeAgent[a.Status] {
			active[a.ID] = true
		}
	}
	maps.DeleteFunc(s.Dots, func(id string, _ Dot) bool { return !active[id] })
	for id := range active {
		if _, ok := s.Dots[id]; !ok {
			s.Dots[id] = Dot{ID: id, StartedAt: at}
		}
	}
}

// reset forgets the conversation: a new session or a /clear or /resume.
func (s *StreamState) reset() {
	s.TurnID = ""
	clear(s.Asks)
	s.Compacting = false
	s.Err = false
	clear(s.Dots)
	s.Background = ""
}

// decode unmarshals raw into v when raw is a JSON object whose fields fit
// v. Unknown fields are ignored.
func decode(raw json.RawMessage, v any) bool {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	return json.Unmarshal(trimmed, v) == nil
}

// Status is the light: clear when ended, then error, waiting, running,
// idle.
func (s *StreamState) Status() agentpkg.Status {
	switch {
	case s.Ended:
		return agentpkg.StatusClear
	case s.Err:
		return agentpkg.StatusError
	case len(s.Asks) > 0:
		return agentpkg.StatusWaiting
	case s.TurnID != "" || s.Compacting:
		return agentpkg.StatusRunning
	default:
		return agentpkg.StatusIdle
	}
}

// Live reports whether the stream drives the light at now: not ended, and
// its last event at most LiveWindow ago.
func (s *StreamState) Live(now time.Time) bool {
	return !s.Ended && !s.LastEvent.IsZero() && now.Sub(s.LastEvent) <= LiveWindow
}

// DotList returns the dots sorted by StartedAt, then ID.
func (s *StreamState) DotList() []Dot {
	out := slices.Collect(maps.Values(s.Dots))
	slices.SortFunc(out, func(a, b Dot) int {
		return cmp.Or(cmp.Compare(a.StartedAt, b.StartedAt), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// BackgroundKind is the symbol for the session's background work: any
// workflow task, else any monitor task, else any cron; shell and subagent
// tasks show nothing (N6). Task status is not filtered: the hook payload
// lists only tasks in flight.
func BackgroundKind(tasks []Task, crons int) Background {
	monitor := false
	for _, t := range tasks {
		switch t.Type {
		case "workflow":
			return BackgroundWorkflow
		case "monitor":
			monitor = true
		}
	}
	switch {
	case monitor:
		return BackgroundMonitor
	case crons > 0:
		return BackgroundSchedule
	default:
		return ""
	}
}
