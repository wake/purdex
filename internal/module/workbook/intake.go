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
func (e *Engine) OnTurnEnd(ev agent.TurnEndEvent) { e.catchUp(ev, 0) }

func (e *Engine) catchUp(ev agent.TurnEndEvent, attempt int) {
	e.life.RLock()
	defer e.life.RUnlock()
	if e.stopped || ev.SessionID == "" {
		return
	}
	conv, err := e.convKey(ev.SessionID)
	if err != nil {
		e.d.Logf("[workbook] conversation of a session: %v", err)
		return
	}
	picks, retry := e.pickTurns(ev, attempt)
	if retry {
		e.d.After(busyDelay, func() { e.catchUp(ev, attempt+1) })
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

// pickTurns decides which ended turns of the session to record (plan D1 / D2). retry is true when the transcript cache
// was busy and the event should be handled again in a moment.
func (e *Engine) pickTurns(ev agent.TurnEndEvent, attempt int) (picks []pick, retry bool) {
	if e.d.Turns == nil {
		return e.fallbackPick(ev), false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	turns, err := e.d.Turns.LastTurns(ctx, "claude", ev.SessionID, catchUpWindow)
	switch {
	case errors.Is(err, convfeed.ErrBusy) && attempt < busyRetries:
		return nil, true
	case err != nil:
		return e.fallbackPick(ev), false
	}
	cursor, _, has, err := e.d.Store.NewestTurn(ev.SessionID)
	if err != nil {
		e.d.Logf("[workbook] read the newest recorded turn: %v", err)
		return nil, false
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
	return picks, false
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
