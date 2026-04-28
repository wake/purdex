package agent

import (
	"encoding/json"
	"log"
	"time"
)

// pathHintBroadcaster is the seam used by EmitPathHint. Production wires
// *core.EventsBroadcaster (Module.core.Events); tests pass a stub.
type pathHintBroadcaster interface {
	Broadcast(session, eventType, value string)
}

// EmitPathHint walks the extract → dedup → buffer → broadcast pipeline.
// Pure helper — no Module dependency, so tests can drive it without spinning
// up the full agent module.
func EmitPathHint(
	b pathHintBroadcaster,
	dedup *PathHintDedupCache,
	buf *PathHintRingBuffer,
	rawEvent json.RawMessage,
	eventName, agentID, sessionCode string,
	now time.Time,
) {
	hint, basename, ok := ExtractPathHint(rawEvent, eventName, agentID, sessionCode, now)
	if !ok {
		return
	}
	if !dedup.Mark(hint.SessionCode, hint.Dir, basename, hint.Timestamp) {
		return
	}
	buf.Push(hint)
	payload, err := json.Marshal(hint)
	if err != nil {
		log.Printf("[agent] path_hint marshal failed: %v", err)
		return
	}
	b.Broadcast(sessionCode, "agent.path_hint", string(payload))
}
