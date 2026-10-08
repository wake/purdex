package convmodel

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Validate checks the invariants of what the daemon produces: known enum
// values, non-empty ids unique per namespace (turn ids among turns, item ids
// among all items including step children — a turn and its opening user item
// share a uuid by design), contiguous turn indexes, ordered times, a running
// turn only last, and consistent denial / output / truncation fields.
//
// It is for the normalizer's tests and debug builds; decoding a document from
// a newer producer never goes through it.
func (c *Conversation) Validate() error {
	turnIDs := map[string]bool{}
	itemIDs := map[string]bool{}
	for i := range c.Turns {
		t := &c.Turns[i]
		switch {
		case t.ID == "":
			return fmt.Errorf("turn %d: empty turn id", i)
		case turnIDs[t.ID]:
			return fmt.Errorf("turn %d: duplicate turn id %q", i, t.ID)
		case t.Index != i:
			return fmt.Errorf("turn %q: index %d, want %d", t.ID, t.Index, i)
		case !slices.Contains([]Outcome{OutcomeDone, OutcomeInterrupted, OutcomeFailed, OutcomeRunning}, t.Outcome):
			return fmt.Errorf("turn %q: unknown outcome %q", t.ID, t.Outcome)
		case t.EndedAt != nil && *t.EndedAt < t.StartedAt:
			return fmt.Errorf("turn %q: ended_at %d before started_at %d", t.ID, *t.EndedAt, t.StartedAt)
		case t.Outcome == OutcomeRunning && i != len(c.Turns)-1:
			return fmt.Errorf("turn %q: running turn is not the last", t.ID)
		}
		turnIDs[t.ID] = true
		for _, it := range t.Items {
			if err := validateItem(it, itemIDs); err != nil {
				return fmt.Errorf("turn %q: %w", t.ID, err)
			}
		}
	}
	return nil
}

func validateItem(it Item, ids map[string]bool) error {
	n := 0
	for _, set := range []bool{it.User != nil, it.AgentText != nil, it.Thinking != nil, it.Step != nil, it.System != nil} {
		if set {
			n++
		}
	}
	if !it.Type.known() {
		return fmt.Errorf("unknown item type %q", it.Type)
	}
	if n != 1 {
		return fmt.Errorf("item type %q must hold exactly its own variant, has %d variants", it.Type, n)
	}

	var id string
	var err error
	switch it.Type {
	case ItemUser:
		u := it.User
		id = u.ID
		if !slices.Contains([]Source{SourceUser, SourceQueued, SourcePeer, SourceSlash, SourceBash, SourceTask, SourceScheduled}, u.Source) {
			err = fmt.Errorf("unknown source %q", u.Source)
		} else if u.Truncated && len(u.Text) < MaxText-3 {
			err = fmt.Errorf("user text flagged truncated but only %d bytes", len(u.Text))
		}
	case ItemAgentText:
		a := it.AgentText
		id = a.ID
		if a.Truncated && len(a.Markdown) < MaxText-3 {
			err = fmt.Errorf("agent_text flagged truncated but only %d bytes", len(a.Markdown))
		}
	case ItemThinking:
		id = it.Thinking.ID
	case ItemSystem:
		id = it.System.ID
		if !slices.Contains([]SystemKind{SystemInterrupted, SystemCompacted, SystemHandoff, SystemModelChanged, SystemResumed, SystemCommandOutput}, it.System.Kind) {
			err = fmt.Errorf("unknown system kind %q", it.System.Kind)
		}
	case ItemStep:
		id = it.Step.ID
		err = validateStep(it.Step)
	}
	switch {
	case id == "":
		return fmt.Errorf("empty item id (type %q)", it.Type)
	case ids[id]:
		return fmt.Errorf("duplicate item id %q", id)
	}
	ids[id] = true
	if err != nil {
		return fmt.Errorf("item %q: %w", id, err)
	}
	if it.Step != nil {
		for _, child := range it.Step.Children {
			if err := validateItem(child, ids); err != nil {
				return fmt.Errorf("item %q: child: %w", id, err)
			}
		}
	}
	return nil
}

func validateStep(s *Step) error {
	if !slices.Contains([]StepKind{StepEdit, StepExecute, StepRead, StepSearch, StepFetch, StepTask, StepOther}, s.Kind) {
		return fmt.Errorf("unknown step kind %q", s.Kind)
	}
	if !slices.Contains([]StepStatus{StepRunning, StepDone, StepFailed, StepDenied}, s.Status) {
		return fmt.Errorf("unknown step status %q", s.Status)
	}
	if (s.Status == StepDenied) != (s.Denial != "") {
		return fmt.Errorf("denial %q with status %q: a denial goes with denied, and denied needs a denial", s.Denial, s.Status)
	}
	if s.InputTruncated && !inputAtCap(s.Input) {
		return fmt.Errorf("step input flagged truncated but no string or total reaches its cap")
	}
	if o := s.Output; o != nil {
		cut := len(o.Text) < o.TotalBytes
		switch {
		case o.Keep != "" && o.Keep != KeepHead && o.Keep != KeepTail:
			return fmt.Errorf("unknown keep %q", o.Keep)
		case o.TotalBytes < len(o.Text):
			return fmt.Errorf("output total_bytes %d below the kept text (%d bytes)", o.TotalBytes, len(o.Text))
		case o.Truncated != cut:
			return fmt.Errorf("output truncated=%v but text is %d of %d bytes", o.Truncated, len(o.Text), o.TotalBytes)
		case (o.Keep != "") != o.Truncated:
			return fmt.Errorf("output keep %q must be set exactly when truncated (%v)", o.Keep, o.Truncated)
		}
	}
	if d := s.Diff; d != nil && d.Truncated {
		lines := 0
		for _, h := range d.Hunks {
			lines += len(h.Lines)
		}
		if lines < MaxDiffLines {
			return fmt.Errorf("diff flagged truncated but only %d hunk lines", lines)
		}
	}
	return nil
}

// inputAtCap reports whether a step input looks cut: it is at the whole-input
// cap, or one of its string values is at the per-string cap (a cut on a UTF-8
// boundary lands up to 3 bytes under).
func inputAtCap(raw json.RawMessage) bool {
	if len(raw) >= MaxInput-3 {
		return true
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case string:
			return len(x) >= MaxInputString-3
		case []any:
			return slices.ContainsFunc(x, walk)
		case map[string]any:
			for _, e := range x {
				if walk(e) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}
