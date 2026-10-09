package workbook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/peers"
)

// pick is one ended turn the catch-up decided to record.
type pick struct {
	turnID string
	at     int64
	seq    int64
	turn   convmodel.Turn
}

// OnTurnEnd is the agent module's turn-end subscriber (spec §4.2). It runs on the subscriber's own goroutine; an event
// means "this session has new ended turns", not "this turn ended", so a dropped or a retried event is harmless (plan D2).
func (e *Engine) OnTurnEnd(ev agent.TurnEndEvent) { e.catchUp(ev, tries{}) }

// tries counts how often one event has been put back: for a busy transcript cache, and for a transcript that has not yet
// caught up with the hook.
type tries struct{ busy, settle int }

// again is a catch-up to run later.
type again struct {
	after time.Duration
	next  tries
}

func (e *Engine) catchUp(ev agent.TurnEndEvent, try tries) {
	e.life.RLock()
	defer e.life.RUnlock()
	if e.stopped || ev.SessionID == "" {
		return
	}
	// The subscriber is one goroutine but a busy re-queue comes from a timer: two catch-ups of one conversation must not
	// interleave between their insert and their enqueue, or a newer turn would queue before an older one.
	e.intakeMu.Lock()
	defer e.intakeMu.Unlock()
	conv, err := e.convKey(ev.SessionID)
	if err != nil {
		e.d.Logf("[workbook] conversation of a session: %v", err)
		return
	}
	picks, later := e.pickTurns(ev, try)
	if later != nil {
		e.d.After(later.after, func() { e.catchUp(ev, later.next) })
		return
	}
	if len(picks) == 0 {
		return
	}
	// who the session was at this moment, read once per event and never under a lock
	entry := Entry{ConvKey: conv, HostID: e.d.HostID, Provider: "claude", SessionID: ev.SessionID, Ref: peers.RefID(ev.SessionID), PromptVer: PromptVersion}
	if e.d.Seats != nil {
		if seat, err := e.d.Seats.SeatOf(ev.SessionID); err != nil {
			e.d.Logf("[workbook] seat of a session: %v", err)
		} else {
			entry.TeamID, entry.Role = seat.TeamID, seat.Role
		}
	}
	for _, p := range picks {
		row := entry
		row.TurnID, row.TurnAt, row.TurnSeq = p.turnID, p.at, p.seq
		id, inserted, err := e.d.Store.InsertPending(row)
		if err != nil {
			e.d.Logf("[workbook] record a turn: %v", err)
			continue
		}
		if !inserted {
			continue
		}
		if !e.capable(ev.SessionID) { // nothing waits for a mod that is not there
			if _, err := e.d.Store.Finish(id, StateSkipped, ReasonNoMod, Output{}); err != nil {
				e.d.Logf("[workbook] skip a turn: %v", err)
			}
			e.notifyLine(id, false)
			continue
		}
		e.enqueue(&job{conv: conv, entryID: id, session: ev.SessionID, kind: JobTurn, attempt: 1, turn: p.turn})
	}
}

// pickTurns decides which ended turns of the session to record (plan D1 / D2). later is set when the event should be
// handled again in a moment: the transcript cache was busy, or the transcript has not caught up with the hook yet.
func (e *Engine) pickTurns(ev agent.TurnEndEvent, try tries) (picks []pick, later *again) {
	if e.d.Turns == nil {
		return e.fallbackPick(ev), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	turns, err := e.d.Turns.LastTurns(ctx, "claude", ev.SessionID, catchUpWindow)
	switch {
	case errors.Is(err, convfeed.ErrBusy) && try.busy < busyRetries:
		return nil, &again{busyDelay, tries{try.busy + 1, try.settle}}
	case err != nil:
		return e.fallbackPick(ev), nil
	}
	cursor, _, has, err := e.d.Store.NewestTurn(ev.SessionID)
	if err != nil {
		e.d.Logf("[workbook] read the newest recorded turn: %v", err)
		return nil, nil
	}
	// the turns newer than the newest recorded one; a cursor outside the window means the whole window
	from := 0
	if has {
		for i, t := range turns {
			if t.ID == cursor {
				from = i + 1
			}
		}
	}
	// The Stop hook comes before Claude Code has finished writing the turn: the duration row (so the turn still reads as
	// running) and sometimes the last assistant row. Wait a little for the file to catch up with the hook's own words; only
	// then use those words.
	forced := try.settle >= settleRetries
	turns, caughtUp := adoptEvent(turns, ev, from, forced)
	if !caughtUp {
		return nil, &again{settleDelay, tries{try.busy, try.settle + 1}}
	}
	if forced {
		e.d.Logf("[workbook] the transcript had not caught up with a Stop after %d ms; the hook's own words are used", settleRetries*int(settleDelay/time.Millisecond))
	}
	var ended []convmodel.Turn
	for _, t := range turns[from:] {
		if t.Outcome != convmodel.OutcomeRunning {
			ended = append(ended, t)
		}
	}
	if !has && len(ended) > 1 { // no record yet: no history backfill, only the newest ended turn
		ended = ended[len(ended)-1:]
	}
	var keep []convmodel.Turn
	for _, t := range ended {
		if summarisable(t) {
			keep = append(keep, t)
		}
	}
	if len(keep) > catchUpKeep {
		keep = keep[len(keep)-catchUpKeep:]
	}
	for i, t := range keep {
		p := pick{turnID: t.ID, turn: t, at: turnEndedAt(t, ev.At)}
		if i == len(keep)-1 { // the newest one is the turn this event is about
			p.at, p.seq = ev.At, ev.Seq
		}
		picks = append(picks, p)
	}
	return picks, nil
}

// adoptEvent makes the turn this Stop event is about an ended turn, whatever the transcript shows yet (spec §4.2, found
// by the real-session gate): Claude Code writes a turn's last assistant row and its duration row after the Stop hook
// fires, so the turn can read as running, and even without its last words. The hook's last_assistant_message names the
// turn: the newest turn newer than `from` (the cursor) whose last words match it is that turn (one text may be a prefix of
// the other: the hook's is bounded). With no text to compare (a failed turn, a tool-only one) a running turn that has
// done something counts. When nothing matches and the newest turn is running with no assistant words at all, the file has
// not caught up: caughtUp is false until `force`, then the hook's words become that turn's words. The turns slice is not
// changed; a turn that is changed is copied.
func adoptEvent(turns []convmodel.Turn, ev agent.TurnEndEvent, from int, force bool) (out []convmodel.Turn, caughtUp bool) {
	last := len(turns) - 1
	if last < from {
		return turns, true // nothing newer than the newest recorded turn
	}
	out = append([]convmodel.Turn(nil), turns...)
	want := strings.TrimSpace(ev.Text)
	if want == "" {
		if t := out[last]; t.Outcome == convmodel.OutcomeRunning && summarisable(t) {
			out[last].Outcome = convmodel.OutcomeDone
		}
		return out, true
	}
	for i := last; i >= from; i-- {
		if said := lastWords(out[i]); said != "" && (strings.HasPrefix(said, want) || strings.HasPrefix(want, said)) {
			if out[i].Outcome == convmodel.OutcomeRunning {
				out[i].Outcome = convmodel.OutcomeDone
			}
			return out, true
		}
	}
	t := out[last]
	if t.Outcome != convmodel.OutcomeRunning || lastWords(t) != "" {
		return out, true // an ended turn, or a newer turn already speaking in its own words: not this event's
	}
	if !force {
		return out, false
	}
	t.Items = append(append([]convmodel.Item(nil), t.Items...), convmodel.Item{Type: convmodel.ItemAgentText,
		AgentText: &convmodel.AgentText{ID: "hook-" + t.ID, Markdown: want}})
	t.Outcome = convmodel.OutcomeDone
	out[last] = t
	return out, true
}

// lastWords is the turn's last non-blank assistant text, trimmed.
func lastWords(t convmodel.Turn) string {
	var last string
	for _, it := range t.Items {
		if it.AgentText != nil {
			if m := strings.TrimSpace(it.AgentText.Markdown); m != "" {
				last = m
			}
		}
	}
	return last
}

// summarisable: a turn with no assistant text and no tool step is not summarised (spec §4.3).
func summarisable(t convmodel.Turn) bool {
	for _, it := range t.Items {
		if it.Step != nil || (it.AgentText != nil && strings.TrimSpace(it.AgentText.Markdown) != "") {
			return true
		}
	}
	return false
}

func turnEndedAt(t convmodel.Turn, fallback int64) int64 {
	switch {
	case t.EndedAt != nil:
		return *t.EndedAt
	case t.StartedAt > 0:
		return t.StartedAt
	}
	return fallback
}

// fallbackPick is the turn when its transcript cannot be read: the hook's own text, keyed by a hash of it and a
// two-minute time bucket so a retried hook dedupes and two same-text turns minutes apart do not (plan D1).
func (e *Engine) fallbackPick(ev agent.TurnEndEvent) []pick {
	text := strings.TrimSpace(ev.Text)
	if text == "" {
		return nil // nothing to summarise: the transcript is unreadable and the hook said nothing
	}
	sum := sha256.Sum256([]byte(text))
	id := "t:" + hex.EncodeToString(sum[:])[:16] + ":" + strconv.FormatInt(ev.At/fallbackBucket, 10)
	turn := convmodel.Turn{ID: id, Outcome: convmodel.OutcomeDone, Items: []convmodel.Item{
		{Type: convmodel.ItemAgentText, AgentText: &convmodel.AgentText{ID: id, Markdown: text}},
	}}
	return []pick{{turnID: id, at: ev.At, seq: ev.Seq, turn: turn}}
}
