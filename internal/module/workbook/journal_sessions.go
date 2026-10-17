package workbook

import (
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// Session pages (capability journal.sessions.v1, spec §4.2): the journal of a unit is read one leader session at a time. The
// chain of sessions is the leader's, walked back over relay and /clear.

// pageEntryCap bounds one session page.
const pageEntryCap = 1000

// maxChain bounds the walk back over predecessors (a loop or a runaway record cannot hold a request).
const maxChain = 512

// Session links on the wire.
const (
	viaRoot  = "root"
	viaRelay = "relay"
	viaClear = "clear"
)

type journalSessionJSON struct {
	SessionID string `json:"session_id"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"` // 0 for the current session
	Via       string `json:"via"`
	Current   bool   `json:"current"`
}

// chainLink is one session of the leader's chain with how it began.
type chainLink struct {
	sid     string
	startAt int64 // when the link was recorded; the root's is set by the caller
	via     string
}

// relayPredecessor and clearPredecessor are the two readers, looked up when asked (the modules start in any order).
func (m *Module) relayPredecessor() team.PredecessorReader {
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	svc, ok := m.core.Registry.Get(team.PredecessorReaderKey)
	if !ok {
		return nil
	}
	r, _ := svc.(team.PredecessorReader)
	return r
}

func (m *Module) clearPredecessor() agent.PredecessorReader {
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	svc, ok := m.core.Registry.Get(agent.PredecessorReaderKey)
	if !ok {
		return nil
	}
	r, _ := svc.(agent.PredecessorReader)
	return r
}

// predecessorLink is the session `sid` replaced. A relay's record wins over a /clear's for the same session (a relay's
// /clear is recorded first as a plain clear, and the lineage row lands a moment later), as in the conversation module. A
// missing reader is skipped; a read error is logged and treated as none.
func (m *Module) predecessorLink(sid string) (prev string, at int64, via string) {
	if r := m.relayPredecessor(); r != nil {
		p, a, ok, err := r.PredecessorOf(sid)
		if err != nil {
			log.Printf("[workbook] journal: relay predecessor of %s: %v", sid, err)
		} else if ok && p != "" && p != sid {
			return p, a, viaRelay
		}
	}
	if r := m.clearPredecessor(); r != nil {
		p, a, ok, err := r.PredecessorOf(sid)
		if err != nil {
			log.Printf("[workbook] journal: clear predecessor of %s: %v", sid, err)
		} else if ok && p != "" && p != sid {
			return p, a, viaClear
		}
	}
	return "", 0, ""
}

// sessionChain is the leader's sessions oldest first, ending at current. Each link after the first says how it began (the
// record that session replaced the one before it) and when.
func (m *Module) sessionChain(current string) []chainLink {
	rev := []chainLink{}
	seen := map[string]bool{current: true}
	sid := current
	for i := 0; i < maxChain; i++ {
		prev, at, via := m.predecessorLink(sid)
		if prev == "" || seen[prev] {
			break
		}
		rev = append(rev, chainLink{sid: sid, startAt: at, via: via})
		seen[prev] = true
		sid = prev
	}
	rev = append(rev, chainLink{sid: sid, via: viaRoot})
	slices.Reverse(rev)
	return rev
}

// leaderSession is the leader session whose chain the reader asks for: a team's current lead, else the reader itself.
func (m *Module) leaderSession(tgt journalTarget, reader string) string {
	if tgt.kind == UnitTeam {
		if feed := m.teamFeed(); feed != nil {
			if info, found, err := feed.TeamInfo(tgt.id); err != nil {
				log.Printf("[workbook] journal: team %s: %v", tgt.id, err)
			} else if found && info.LeadSessionID != "" {
				return info.LeadSessionID
			}
		}
	}
	return reader
}

// sessionIndex attributes entries to the sessions of a chain.
type sessionIndex struct {
	chain []chainLink
	pos   map[string]int
}

func newSessionIndex(chain []chainLink) sessionIndex {
	pos := make(map[string]int, len(chain))
	for i, c := range chain {
		pos[c.sid] = i
	}
	return sessionIndex{chain: chain, pos: pos}
}

// bySpan is the session whose span holds t (unix ms): the last link that began at or before it; the root takes everything
// before the second link.
func (x sessionIndex) bySpan(t int64) int {
	i := 0
	for j := 1; j < len(x.chain); j++ {
		if x.chain[j].startAt <= t {
			i = j
		}
	}
	return i
}

// workbookID is the wb_entries id a `turn:<id>` or `ev:<id>:<n>` journal entry came from.
func workbookID(entryID string) (int64, bool) {
	rest, ok := strings.CutPrefix(entryID, "turn:")
	if !ok {
		rest, ok = strings.CutPrefix(entryID, "ev:")
		if !ok {
			return 0, false
		}
	}
	num, _, _ := strings.Cut(rest, ":")
	n, err := strconv.ParseInt(num, 10, 64)
	return n, err == nil && n > 0
}

// attribute gives, for each entry of all, the index of its session in the chain: the sid it was written with; else (turn: /
// ev:) the workbook entry's session; else the span its time falls in. A session outside the chain falls through to the span.
func attribute(st *Store, x sessionIndex, all []journal.Entry) ([]int, error) {
	out := make([]int, len(all))
	var need []int64
	for i, e := range all {
		out[i] = -1
		if p, ok := x.pos[e.SID]; ok && e.SID != "" {
			out[i] = p
			continue
		}
		if id, ok := workbookID(e.ID); ok {
			need = append(need, id)
		}
	}
	var sess map[int64]string
	if len(need) > 0 {
		var err error
		if sess, err = st.EntrySessions(need); err != nil {
			return nil, err
		}
	}
	for i, e := range all {
		if out[i] >= 0 {
			continue
		}
		if id, ok := workbookID(e.ID); ok {
			if p, ok := x.pos[sess[id]]; ok {
				out[i] = p
				continue
			}
		}
		out[i] = x.bySpan(e.At.UnixMilli())
	}
	return out, nil
}

// handleJournalSessions: GET /api/journal/sessions?session_id=<reader> → {sessions: […]}, the leader's chain oldest first.
func (m *Module) handleJournalSessions(w http.ResponseWriter, r *http.Request) {
	reader := r.URL.Query().Get("session_id")
	tgt, ok := m.resolveJournal(w, reader)
	if !ok {
		return
	}
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	chain := m.sessionChain(m.leaderSession(tgt, reader))
	out := make([]journalSessionJSON, 0, len(chain))
	for i, c := range chain {
		js := journalSessionJSON{SessionID: c.sid, StartedAt: c.startAt, Via: c.via, Current: i == len(chain)-1}
		if i == 0 {
			if at, found, err := st.FirstTurnAt(c.sid); err != nil {
				log.Printf("[workbook] journal: first turn of %s: %v", c.sid, err)
			} else if found {
				js.StartedAt = at
			}
		}
		if i+1 < len(chain) {
			js.EndedAt = chain[i+1].startAt
		}
		out = append(out, js)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}
