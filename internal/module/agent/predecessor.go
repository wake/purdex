package agent

import (
	"encoding/json"
	"log"
)

// PredecessorReaderKey is the service-registry key of the PredecessorReader.
const PredecessorReaderKey = "agent.predecessor"

// PredecessorReader answers which session a Claude Code session replaced by a `/clear` (manual or a relay's), from
// the record kept when the clear's SessionStart was accepted. ok is false when none was recorded; at is unix ms.
type PredecessorReader interface {
	PredecessorOf(sessionID string) (prev string, at int64, ok bool, err error)
}

type predecessorReader struct{ m *Module }

// PredecessorReaderOf returns the module's reader (nil-safe: a module without a frame store answers none).
func PredecessorReaderOf(m *Module) PredecessorReader { return predecessorReader{m} }

func (r predecessorReader) PredecessorOf(sid string) (string, int64, bool, error) {
	if r.m == nil || r.m.frames == nil || sid == "" {
		return "", 0, false, nil
	}
	return r.m.frames.PredecessorOf(sid)
}

// lastRunFreshMs is how long after a run ended its last_agent_run still counts as "the session this /clear replaced".
const lastRunFreshMs = 120_000

// hookSource is the "source" of a SessionStart payload ("" when absent or unparseable).
func hookSource(req EventRequest) string {
	var raw struct {
		Source string `json:"source"`
	}
	_ = json.Unmarshal(req.RawEvent, &raw)
	return raw.Source
}

// notePredecessor records the session a granted `source=clear` SessionStart replaced, and returns its id ("" when the
// start is not a clear or the predecessor is unknown). prev is the id the frame held before the start overwrote it
// (FrameTraceMeta.PrevSessionID); when it is empty — the SessionEnd already removed the frame — the session's
// last_agent_run (same tmux code, ended within 120 s) answers instead. nowMs is the event's own time. A store error
// is logged and never fails the hook.
func (m *Module) notePredecessor(req EventRequest, prov Provenance, prev string, nowMs int64) string {
	if m == nil || m.frames == nil || prov.AgentType != "cc" || prov.SessionID == "" || hookSource(req) != "clear" {
		return ""
	}
	code := ""
	if prev == "" {
		c, _ := m.resolveSessionCodeFromHook(req)
		if c == "" {
			log.Printf("[agent] clear_predecessor_unknown: session=%s reason=no_code", prov.SessionID)
			return ""
		}
		run, err := m.frames.LastRun(c)
		if err != nil {
			log.Printf("[agent] clear_predecessor_last_run: session=%s err=%v", prov.SessionID, err)
			return ""
		}
		if run == nil || run.SessionID == "" || nowMs-run.EndedAt > lastRunFreshMs || run.EndedAt-nowMs > lastRunFreshMs {
			log.Printf("[agent] clear_predecessor_unknown: session=%s code=%s reason=no_fresh_last_run", prov.SessionID, c)
			return ""
		}
		// The run must be this agent's own: the same agent type and the same tmux generation (a code reused after a
		// tmux restart, or a codex that just ended on this code, is somebody else's session).
		if run.AgentType != prov.AgentType || prov.TmuxInstance == "" || run.TmuxInstance != prov.TmuxInstance {
			log.Printf("[agent] clear_predecessor_unknown: session=%s code=%s reason=last_run_not_ours", prov.SessionID, c)
			return ""
		}
		prev, code = run.SessionID, c
	}
	if prev == "" || prev == prov.SessionID {
		return ""
	}
	if err := m.frames.RecordPredecessor(prov.SessionID, prev, nowMs, code); err != nil {
		log.Printf("[agent] clear_predecessor_write_failed: session=%s err=%v", prov.SessionID, err)
		return ""
	}
	return prev
}
