package dialogue

// Which turns get a summary after an event was written, and the intake hooks that queue them (plan T9).

import (
	"strings"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
	agentmod "github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// maxSummariesPerEvent: at most this many turns of one event get a summary, the newest ones.
const maxSummariesPerEvent = 3

// summaryPick is one turn to summarise.
type summaryPick struct {
	turn      convmodel.Turn
	no        int
	at        int64 // turn_at: the event's time for the turn it names, else the turn's own end
	seq       int64
	fromEvent bool // at and seq are the event's: this is the turn the Stop is about
}

// pickSummaries decides which of the turns a Write numbered for the first time get a summary (plan T9, the workbook's
// pickTurns): only turns with something to say that ended (or are the turn the event names); a session recorded for the
// first time takes only the event's turn and the ones after it (never the history), or just the newest when the event
// names none; at most the newest three. The turn the event names takes the event's (At, Seq): the push matches on them.
func pickSummaries(ev agentmod.TurnEndEvent, res convturns.TurnsFromResult, wr WriteResult, targetID string) []summaryPick {
	cands := wr.Turns
	if wr.First {
		from := len(cands) - 1 // no named turn (or one not in the list): the newest
		if targetID != "" {
			for i, c := range cands {
				if c.ID == targetID {
					from = i
					break
				}
			}
		}
		if from >= 0 {
			cands = cands[from:]
		}
	}
	inHand := make(map[string]convmodel.Turn, len(res.Turns))
	for _, t := range res.Turns {
		inHand[t.ID] = t
	}
	var picks []summaryPick
	for _, c := range cands {
		t, ok := inHand[c.ID]
		// the event's own turn is summarised from the hook's text when the transcript has nothing of it yet (plan §1-8)
		hookOnly := targetID != "" && t.ID == targetID && strings.TrimSpace(ev.Text) != ""
		if !ok || (!summarisable(t) && !hookOnly) || (t.Outcome == convmodel.OutcomeRunning && t.ID != targetID) {
			continue
		}
		picks = append(picks, summaryPick{turn: t, no: c.No, at: turnOwnEnd(t, ev.At)})
	}
	if len(picks) > maxSummariesPerEvent {
		picks = picks[len(picks)-maxSummariesPerEvent:]
	}
	for i := range picks {
		if (targetID != "" && picks[i].turn.ID == targetID) || (targetID == "" && i == len(picks)-1) {
			picks[i].at, picks[i].seq, picks[i].fromEvent = ev.At, ev.Seq, true
		}
	}
	return picks
}

// turnOwnEnd is when the turn ended, else when it started, else fallback.
func turnOwnEnd(t convmodel.Turn, fallback int64) int64 {
	if t.EndedAt != nil && *t.EndedAt > 0 {
		return *t.EndedAt
	}
	if t.StartedAt > 0 {
		return t.StartedAt
	}
	return fallback
}

// summaryIntake is what intake calls after a Write: it records and queues the summaries of the event's new turns, and
// tells the push wait when an event has been looked at.
type summaryIntake struct {
	st    *Store
	jobs  *channel
	lines *PushLines
	root  team.LineageRootResolver // nil: a session is its own conversation key
	logf  func(format string, args ...any)
}

func (s *summaryIntake) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

func (s *summaryIntake) convKey(sid string) string {
	if s.root != nil {
		if r, err := s.root.RootSessionOf(sid); err == nil && r != "" {
			return r
		}
	}
	return sid
}

// onWritten is intakeDeps.onWritten.
func (s *summaryIntake) onWritten(ev agentmod.TurnEndEvent, res convturns.TurnsFromResult, wr WriteResult, targetID, hookText string) {
	picks := pickSummaries(ev, res, wr, targetID)
	if len(picks) == 0 {
		return
	}
	sid := ev.SessionID
	conv := s.convKey(sid)
	for _, p := range picks {
		id, inserted, err := s.st.InsertPendingSummary(SummaryRow{SessionID: sid, ConvKey: conv, TurnID: p.turn.ID, TurnNo: p.no,
			TurnAt: p.at, TurnSeq: int(p.seq)})
		if err != nil {
			s.log("[dialogue] record the summary of turn %s: %v", p.turn.ID, err)
			continue
		}
		if !inserted {
			continue // the turn has one already
		}
		job := summaryJob{id: id, turn: p.turn}
		if p.fromEvent {
			job.hookText = hookText
		}
		if _, notQueued := s.jobs.enqueue(sid, kindTurnSummary, job); notQueued != "" {
			if _, err := s.st.FinishSummary(id, stateSkipped, notQueued); err != nil {
				s.log("[dialogue] end turn summary %d (skipped, %s): %v", id, notQueued, err)
			}
			s.lines.wake()
		}
	}
}

// onSettled is intakeDeps.onSettled: this event of the session has been looked at (quick, never blocks).
func (s *summaryIntake) onSettled(sid string, at int64) { s.lines.intakeDone(sid, at) }
