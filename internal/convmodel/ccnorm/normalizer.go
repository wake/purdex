package ccnorm

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/convmodel"
)

// Options configures a Normalizer.
type Options struct {
	SessionID string // fills key.session_id
}

// Change reports one turn or item that a feed (or SetLive) created or changed.
type Change struct {
	TurnID, ItemID string // ItemID "" = the turn itself (opened, outcome / ended_at changed)
	Offset         int64  // byte offset of the row that caused it; -1 for a SetLive change
}

// Position is where in the file a turn or item was created and last changed:
// the byte offsets of the rows. Updated >= Created.
type Position struct{ Created, Updated int64 }

// Stats counts what the normalizer saw and left out.
type Stats struct {
	Lines, BadJSON, Replayed int
	Skipped                  map[string]int // by reason: "type:<t>", "attachment:<t>", "origin:<k>", …
}

// Bounds on what one row, or the counters, can cost.
const (
	maxBlocksPerRow = 64      // content blocks processed per row; the rest are skipped
	maxSkipKeys     = 64      // distinct Stats.Skipped reasons; later new ones count as "other"
	maxLineBytes    = 8 << 20 // a longer line is not parsed (the transcript API's per-line cap)
)

// ErrGap is returned by Feed for a line beyond the next expected offset.
var ErrGap = errors.New("ccnorm: offset beyond the next expected line")

// Normalizer builds a convmodel.Conversation from one transcript file, fed
// line by line from offset 0.
//
// Not safe for concurrent use: the caller serializes feeds per transcript.
type Normalizer struct {
	opts  Options
	next  int64 // the offset the next line must have
	live  bool
	stats Stats

	// The id store: one map per namespace. Both the transcript path and,
	// in U1-5, the mod path write through upsert.
	turns  []*turnRec
	turnAt map[string]int     // turn id → index in turns
	itemAt map[string]itemLoc // item id → where it lives

	resulted map[string]struct{} // ids of the steps that have had their result

	pend    []Change               // changes of the current feed, in order
	pendSet map[[2]string]struct{} // (turn id, item id) of pend, for O(1) dedupe
	touched int                    // turn index the current row belonged to, -1 for none

	dynKeys map[string]struct{} // the dynamic Skipped reasons kept so far

	// Facts carried across rows.
	customTitle, aiTitle string
	model, effort        string // last main-thread assistant row (usage)
	entry                string // entrypoint (cli / sdk-cli) of the last row that had one
	entryBefore          string // the same, as it was before the current row
}

type itemLoc struct{ turn, item int }

// New returns a live normalizer.
func New(o Options) *Normalizer {
	return &Normalizer{
		opts:     o,
		live:     true,
		stats:    Stats{Skipped: map[string]int{}},
		turnAt:   map[string]int{},
		itemAt:   map[string]itemLoc{},
		resulted: map[string]struct{}{},
		pendSet:  map[[2]string]struct{}{},
		dynKeys:  map[string]struct{}{},
		touched:  -1,
	}
}

// Feed adds one complete line (no '\n') whose first byte is at offset.
//
// Lines are contiguous from offset 0: a line below the next expected offset
// is a replay and is ignored; a line beyond it is a gap and is refused with
// ErrGap, changing nothing. Next is offset + len(line) + 1 after a line is
// taken. Feed never panics, whatever the bytes.
func (n *Normalizer) Feed(offset int64, line []byte) ([]Change, error) {
	switch {
	case offset < n.next:
		n.stats.Replayed++
		return nil, nil
	case offset > n.next:
		return nil, fmt.Errorf("%w: line at %d, next expected %d", ErrGap, offset, n.next)
	}
	n.next = offset + int64(len(line)) + 1
	n.stats.Lines++
	if len(line) > maxLineBytes {
		// Too big to decode (a huge inline image): the offset has moved on,
		// the content is dropped and counted.
		n.skip("line:oversize")
		return nil, nil
	}
	n.row(offset, line)
	return n.flush(), nil
}

// Next is the offset the next line must have.
func (n *Normalizer) Next() int64 { return n.next }

// Stats returns a copy of the counters.
func (n *Normalizer) Stats() Stats {
	s := n.stats
	s.Skipped = make(map[string]int, len(n.stats.Skipped))
	for k, v := range n.stats.Skipped {
		s.Skipped[k] = v
	}
	return s
}

// Position returns the position of a turn (itemID "") or of one of its items.
func (n *Normalizer) Position(turnID, itemID string) (Position, bool) {
	ti, ok := n.turnAt[turnID]
	if !ok {
		return Position{}, false
	}
	tr := n.turns[ti]
	if itemID == "" {
		return Position{tr.created, tr.updated}, true
	}
	loc, ok := n.itemAt[itemID]
	if !ok || loc.turn != ti {
		return Position{}, false
	}
	return tr.pos[loc.item], true
}

func (n *Normalizer) skip(reason string) { n.stats.Skipped[reason]++ }

// skipDyn counts a reason built from a transcript value (an unknown type,
// subtype, block type, origin kind, …). At most maxSkipKeys distinct such
// reasons are kept; later new ones are counted under "other", so the map
// stays small however varied the input is.
func (n *Normalizer) skipDyn(reason string) {
	if _, known := n.dynKeys[reason]; !known {
		if len(n.dynKeys) >= maxSkipKeys {
			reason = "other"
		} else {
			n.dynKeys[reason] = struct{}{}
		}
	}
	n.stats.Skipped[reason]++
}

// row applies one decoded line.
func (n *Normalizer) row(off int64, line []byte) {
	l, ok := decodeLine(line)
	if !ok {
		n.stats.BadJSON++
		return
	}
	n.touched = -1
	switch l.typ {
	case "custom-title":
		if s := strings.TrimSpace(l.str(l.CustomTitle)); s != "" {
			n.customTitle = s
		}
		return
	case "ai-title":
		if s := strings.TrimSpace(l.str(l.AITitle)); s != "" {
			n.aiTitle = s
		}
		return
	}
	if l.sidechain {
		n.skip("sidechain")
		return
	}
	switch l.typ {
	case "user", "assistant", "system", "attachment":
		if l.uuid == "" {
			n.skip("no_uuid")
			return
		}
	default:
		n.skipDyn("type:" + l.typ)
		return
	}
	n.entryBefore = n.entry
	switch l.typ {
	case "user":
		n.userRow(&l, off)
	case "assistant":
		n.assistantRow(&l, off)
	case "system":
		n.systemRow(&l, off)
	case "attachment":
		n.attachmentRow(&l, off)
	}
	if e := l.str(l.Entrypoint); e == "cli" || e == "sdk-cli" {
		n.entry = e
	}
	if n.touched >= 0 {
		n.refresh(n.touched, off)
	}
}

// add records a change, once per (turn, item) within a feed.
func (n *Normalizer) add(c Change) {
	k := [2]string{c.TurnID, c.ItemID}
	if _, dup := n.pendSet[k]; dup {
		return
	}
	n.pendSet[k] = struct{}{}
	n.pend = append(n.pend, c)
}

// flush returns the changes of the current feed and resets them.
func (n *Normalizer) flush() []Change {
	out := n.pend
	n.pend = nil
	clear(n.pendSet)
	return out
}

// upsert is the single write path for items. An item whose id is already in
// the store is replaced in place (it keeps its place and its Created
// position, and its Updated position moves); a new id is appended to the
// turn. It reports whether the turn exists. U1-5's mod entry point calls it
// too, so a mod item and its later transcript row are one item.
func (n *Normalizer) upsert(turnID string, it convmodel.Item, off int64) bool {
	ti, ok := n.turnAt[turnID]
	id := idOf(it)
	if !ok || id == "" {
		return false
	}
	if loc, ok := n.itemAt[id]; ok {
		tr := n.turns[loc.turn]
		it.Offset = tr.t.Items[loc.item].Offset
		tr.t.Items[loc.item] = it
		tr.pos[loc.item].Updated = off
		tr.updated = off
		n.add(Change{tr.t.ID, id, off})
		return true
	}
	tr := n.turns[ti]
	it.Offset = off
	tr.t.Items = append(tr.t.Items, it)
	tr.pos = append(tr.pos, Position{off, off})
	n.itemAt[id] = itemLoc{ti, len(tr.t.Items) - 1}
	tr.updated = off
	n.add(Change{turnID, id, off})
	return true
}

// idOf is the id of an item's variant ("" for an unknown type).
func idOf(it convmodel.Item) string {
	switch {
	case it.User != nil:
		return it.User.ID
	case it.AgentText != nil:
		return it.AgentText.ID
	case it.Thinking != nil:
		return it.Thinking.ID
	case it.Step != nil:
		return it.Step.ID
	case it.System != nil:
		return it.System.ID
	}
	return ""
}

// Conversation returns a deep copy of the model: turns, items, title and
// usage. The last open turn stays running while the normalizer is live
// (see SetLive); asking never changes any state.
func (n *Normalizer) Conversation() convmodel.Conversation {
	caps := convmodel.TranscriptCapabilities()
	c := convmodel.Conversation{
		Key:          convmodel.Key{Provider: "claude", SessionID: n.opts.SessionID},
		Provider:     "claude",
		Title:        n.title(),
		Capabilities: &caps,
		Turns:        make([]convmodel.Turn, len(n.turns)),
	}
	if n.model != "" || n.effort != "" {
		c.Usage = &convmodel.Usage{Model: n.model, Effort: n.effort}
	}
	for i, tr := range n.turns {
		c.Turns[i] = cloneTurn(tr.t)
	}
	return c
}

// title is the last custom-title, else the last ai-title, else empty.
func (n *Normalizer) title() string {
	if n.customTitle != "" {
		return n.customTitle
	}
	return n.aiTitle
}
