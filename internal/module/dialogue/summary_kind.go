package dialogue

import (
	"fmt"
	"time"

	"github.com/wake/purdex/internal/convmodel"
)

// recapEvery: this many turn summaries done since the last recap queue the next one (spec §4).
const recapEvery = 30

// summaryJob is a turn-summary job's payload: the row it settles, the turn it is about and the Stop hook's own text.
type summaryJob struct {
	id       int64
	turn     convmodel.Turn
	hookText string
}

// summaryKind is the turn-summary job kind (kindHandler). build and apply are the model call and its validated storage;
// after puts the block into the dialogue file and tells the push wait; finish ends a row that got no answer.
type summaryKind struct {
	st    *Store
	w     *Writer
	lines *PushLines
	// recap is called when SinceRecap reaches recapEvery after a summary (T10 queues the recap there); nil: nothing.
	recap func(sid string)
	logf  func(format string, args ...any)
}

var _ kindHandler = (*summaryKind)(nil)

func (k *summaryKind) log(format string, args ...any) {
	if k.logf != nil {
		k.logf(format, args...)
	}
}

func (k *summaryKind) wake() {
	if k.lines != nil {
		k.lines.wake()
	}
}

func payloadOf(j job) (summaryJob, error) {
	p, ok := j.payload.(summaryJob)
	if !ok {
		return summaryJob{}, fmt.Errorf("dialogue: a turn-summary job with payload %T", j.payload)
	}
	return p, nil
}

func (k *summaryKind) build(j job) (callInput, error) {
	p, err := payloadOf(j)
	if err != nil {
		return callInput{}, err
	}
	titles, err := k.st.RecentTitles(j.sid, 3)
	if err != nil {
		return callInput{}, err
	}
	prompt, err := buildSummaryPrompt(p.turn, titles, p.hookText)
	if err != nil {
		return callInput{}, err
	}
	return callInput{System: turnSummaryPrompt, Prompt: prompt}, nil
}

// apply stores the answer: the row's pending -> done change is the gate, so a second call for the same answer (a retry)
// or a row that was ended meanwhile is left alone and returns nil.
func (k *summaryKind) apply(j job, text string, u usage) error {
	p, err := payloadOf(j)
	if err != nil {
		return err
	}
	out, err := parseSummary(text)
	if err != nil {
		return err // wraps errFormat
	}
	out = repairSummary(out)
	applied, err := k.st.SetSummaryDone(p.id, out.Title, out.Push, out.Summary, out.Skip, u)
	if err != nil {
		return err
	}
	if !applied { // no longer pending: nothing to write, unless the row is gone altogether
		if _, found, err := k.st.SummaryByID(p.id); err != nil {
			return err
		} else if !found {
			return errGone
		}
	}
	return nil
}

// after runs once per stored answer: the summary goes into the dialogue file, the push wait looks again, and a recap is
// queued when enough summaries are done since the last one. Best-effort: it only logs.
func (k *summaryKind) after(j job) {
	p, err := payloadOf(j)
	if err != nil {
		k.log("[dialogue] %v", err)
		return
	}
	row, found, err := k.st.SummaryByID(p.id)
	if err != nil || !found {
		k.log("[dialogue] read back turn summary %d: found=%v err=%v", p.id, found, err)
		k.wake()
		return
	}
	if row.State != stateDone {
		k.wake() // skipped by the model (or ended by someone else meanwhile): nothing to put in the file
		return
	}
	if err := k.w.AppendMarker(row.SessionID, summaryBlock(row)); err != nil {
		k.log("[dialogue] put turn summary %d into the dialogue file: %v", row.ID, err)
	}
	k.wake()
	if k.recap == nil {
		return
	}
	if n, err := k.st.SinceRecap(row.SessionID); err != nil {
		k.log("[dialogue] read since_recap of session %s: %v", row.SessionID, err)
	} else if n >= recapEvery {
		k.recap(row.SessionID)
	}
}

func (k *summaryKind) finish(j job, state, reason string) {
	p, err := payloadOf(j)
	if err != nil {
		k.log("[dialogue] %v", err)
		return
	}
	if _, err := k.st.FinishSummary(p.id, state, reason); err != nil {
		k.log("[dialogue] end turn summary %d (%s, %s): %v", p.id, state, reason, err)
	}
	k.wake()
}

// summaryBlock is a done summary as it stands in the dialogue file: "<turn-summary N · HH:MM | title>" and the text.
// The time is the turn's, in local time.
func summaryBlock(r SummaryRow) string {
	return fmt.Sprintf("<turn-summary %d · %s | %s>\n%s\n", r.TurnNo, time.UnixMilli(r.TurnAt).Format("15:04"), oneLine(r.Title), r.Summary)
}

// placedBlocks are the blocks a rebuild puts back right after a turn: its done summaries (PR 2's recap joins them here).
func placedBlocks(st *Store, sid, turnID string) []string {
	rows, err := st.SummariesByTurnID(sid, turnID)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range rows {
		out = append(out, summaryBlock(r))
	}
	return out
}
