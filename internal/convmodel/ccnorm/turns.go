package ccnorm

import (
	"slices"

	"github.com/wake/purdex/internal/convmodel"
)

// turnRec is a turn plus what the normalizer needs to settle its outcome.
type turnRec struct {
	t                convmodel.Turn
	created, updated int64      // row offsets
	pos              []Position // parallel to t.Items

	duration    bool  // a turn_duration row was seen
	durationAt  int64 // its time
	interrupted bool  // an interrupt marker was seen
	markerAt    int64
	apiErr      *convmodel.TurnError // the turn's last assistant row is an API error
	hasModel    bool                 // an assistant row (a model reply) is in the turn
	modelFree   bool                 // opened by a command that never reaches the model
	lastAt      int64                // time of the last row that belonged to the turn
	prevModel   string               // the model before this turn, for model_changed
	sawModel    bool                 // the turn's first model reply has been compared
	pending     []string             // ids of steps that have had no result (pruned lazily)
}

// openTurn starts a turn whose id is the opening row's uuid. It reports false
// (and opens nothing) when a turn with that id exists. Opening a turn is when
// the previous last turn stops being the last one, so it is settled and
// reported first.
func (n *Normalizer) openTurn(id string, at, off int64) (int, bool) {
	if _, dup := n.turnAt[id]; dup {
		n.skip("duplicate_turn")
		return -1, false
	}
	if last := len(n.turns) - 1; last >= 0 {
		// The new turn is not in the list yet, but the previous one is
		// already "not last" for the outcome rules.
		n.settle(last, false, off)
	}
	tr := &turnRec{created: off, updated: off, lastAt: at, prevModel: n.model}
	tr.t = convmodel.Turn{
		ID: id, Index: len(n.turns), StartedAt: at, Outcome: convmodel.OutcomeRunning,
		Items: []convmodel.Item{}, Offset: off,
	}
	n.turns = append(n.turns, tr)
	n.turnAt[id] = tr.t.Index
	n.add(Change{id, "", off})
	n.refresh(tr.t.Index, off)
	return tr.t.Index, true
}

// ensureTurn is the current (last) turn; when the file has none yet — it
// started mid-conversation — it opens one with no user item whose id is the
// uuid of the row that needs it.
func (n *Normalizer) ensureTurn(id string, at, off int64) int {
	if len(n.turns) > 0 {
		return len(n.turns) - 1
	}
	ti, _ := n.openTurn(id, at, off)
	return ti
}

// attribute records that the current row belongs to turn ti.
func (n *Normalizer) attribute(ti int, at int64) {
	n.turns[ti].lastAt = max(n.turns[ti].lastAt, at)
	n.touched = ti
}

// refresh settles turn ti as it is now (last or not).
func (n *Normalizer) refresh(ti int, off int64) {
	n.settle(ti, ti == len(n.turns)-1, off)
}

// settle recomputes a turn's outcome, error and ended_at and reports a change
// when any of them moved. last says whether the turn is the conversation's
// last turn. off < 0 marks a SetLive change, which is not a row.
func (n *Normalizer) settle(ti int, last bool, off int64) {
	tr := n.turns[ti]
	outcome := n.outcomeOf(tr, last)
	wasRunning := tr.t.Outcome == convmodel.OutcomeRunning
	var ended *int64
	if outcome != convmodel.OutcomeRunning {
		e := tr.lastAt
		switch {
		case tr.duration:
			e = tr.durationAt
		case tr.interrupted:
			e = tr.markerAt
		}
		e = max(e, tr.t.StartedAt)
		ended = &e
	}
	var terr *convmodel.TurnError
	if outcome == convmodel.OutcomeFailed {
		e := *tr.apiErr
		terr = &e
	}
	if tr.t.Outcome == outcome && sameInt(tr.t.EndedAt, ended) && sameErr(tr.t.Error, terr) {
		return
	}
	tr.t.Outcome, tr.t.EndedAt, tr.t.Error = outcome, ended, terr
	if off >= 0 {
		tr.updated = off
	}
	if wasRunning != (outcome == convmodel.OutcomeRunning) {
		n.repend(tr, off) // a step with no result follows its turn
	}
	n.add(Change{tr.t.ID, "", off})
}

// outcomeOf is the outcome rule of spec §8.1: failed by an API error as the
// last assistant row; else interrupted by a marker, or by being a closed
// turn that never reached turn_duration (a killed process); else done by
// turn_duration, by being closed, or by the session not being live; else
// running. A turn opened by a local command has no model work to wait for.
func (n *Normalizer) outcomeOf(tr *turnRec, last bool) convmodel.Outcome {
	switch {
	case tr.apiErr != nil:
		return convmodel.OutcomeFailed
	case tr.interrupted:
		return convmodel.OutcomeInterrupted
	case tr.modelFree && !tr.hasModel:
		return convmodel.OutcomeDone
	case !last && !tr.duration:
		return convmodel.OutcomeInterrupted
	case tr.duration || !last || !n.live:
		return convmodel.OutcomeDone
	}
	return convmodel.OutcomeRunning
}

// SetLive says whether the session is still live. The default is live.
//
// SetLive(false) is for a caller that has fed the transcript to its end and
// knows the session is over: it closes the last open turn as done (a turn
// that already ended is untouched). SetLive(true) reopens a last turn that
// has no end marker. Both return the turns they changed, with Offset -1.
// Liveness is never inferred from the input.
//
// The steps of the turn that have no result follow it: closing makes them
// denied with denial "interrupted", reopening makes them running again (see
// repend). Their changes carry Offset -1 too.
func (n *Normalizer) SetLive(live bool) []Change {
	n.live = live
	if len(n.turns) > 0 {
		n.refresh(len(n.turns)-1, -1)
	}
	return n.flush()
}

func sameInt(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameErr(a, b *convmodel.TurnError) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// cloneTurn deep-copies a turn.
func cloneTurn(t convmodel.Turn) convmodel.Turn {
	c := t
	if t.EndedAt != nil {
		e := *t.EndedAt
		c.EndedAt = &e
	}
	if t.Error != nil {
		e := *t.Error
		c.Error = &e
	}
	c.Items = cloneItems(t.Items)
	return c
}

func cloneItems(items []convmodel.Item) []convmodel.Item {
	if items == nil {
		return nil
	}
	out := make([]convmodel.Item, len(items))
	for i, it := range items {
		out[i] = cloneItem(it)
	}
	return out
}

func cloneItem(it convmodel.Item) convmodel.Item {
	c := it
	if it.User != nil {
		u := *it.User
		if u.From != nil {
			f := *u.From
			u.From = &f
		}
		u.Images = slices.Clone(u.Images)
		c.User = &u
	}
	if it.AgentText != nil {
		a := *it.AgentText
		c.AgentText = &a
	}
	if it.Thinking != nil {
		th := *it.Thinking
		c.Thinking = &th
	}
	if it.System != nil {
		s := *it.System
		s.Detail = slices.Clone(s.Detail)
		c.System = &s
	}
	if it.Step != nil {
		c.Step = cloneStep(it.Step)
	}
	return c
}

func cloneStep(s *convmodel.Step) *convmodel.Step {
	c := *s
	if s.DurationMS != nil {
		d := *s.DurationMS
		c.DurationMS = &d
	}
	c.Input = slices.Clone(s.Input)
	if s.Output != nil {
		o := *s.Output
		o.Images = slices.Clone(o.Images)
		c.Output = &o
	}
	if s.Diff != nil {
		d := *s.Diff
		d.Hunks = make([]convmodel.Hunk, len(s.Diff.Hunks))
		for i, h := range s.Diff.Hunks {
			h.Lines = slices.Clone(h.Lines)
			d.Hunks[i] = h
		}
		if s.Diff.Hunks == nil {
			d.Hunks = nil
		}
		c.Diff = &d
	}
	if s.Command != nil {
		cm := *s.Command
		if cm.ExitCode != nil {
			e := *cm.ExitCode
			cm.ExitCode = &e
		}
		c.Command = &cm
	}
	if s.Subagent != nil {
		sa := *s.Subagent
		c.Subagent = &sa
	}
	c.Children = cloneItems(s.Children)
	return &c
}
