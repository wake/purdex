package conversation

import "log"

// predecessorOf answers which session `sid` replaced: a relay's (team.PredecessorReader over session_lineage, kind
// "relay") wins over a recorded /clear (agent.PredecessorReader, kind "clear"). The kind is decided at read time: a
// relay's /clear is recorded first as a plain clear, and the lineage row lands a moment later. A missing reader is
// skipped and a read error is logged and treated as none (a snapshot never fails for want of a link).
func (m *Module) predecessorOf(sid string) *predecessorJSON {
	if m.relayPred != nil {
		prev, at, ok, err := m.relayPred.PredecessorOf(sid)
		if err != nil {
			log.Printf("[conversation] relay predecessor of %s: %v", sid, err)
		} else if ok && prev != "" && prev != sid {
			return &predecessorJSON{SessionID: prev, Kind: "relay", At: at}
		}
	}
	if m.clearPred != nil {
		prev, at, ok, err := m.clearPred.PredecessorOf(sid)
		if err != nil {
			log.Printf("[conversation] clear predecessor of %s: %v", sid, err)
		} else if ok && prev != "" && prev != sid {
			return &predecessorJSON{SessionID: prev, Kind: "clear", At: at}
		}
	}
	return nil
}
