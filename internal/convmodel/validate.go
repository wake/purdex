package convmodel

import (
	"bytes"
	"encoding/json"
	"errors"
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
	if !it.Type.known() {
		return fmt.Errorf("unknown item type %q", it.Type)
	}
	// Exactly the variant Type names must be set; check before any deref.
	n, own := 0, false
	for _, v := range []struct {
		set bool
		typ ItemType
	}{
		{it.User != nil, ItemUser},
		{it.AgentText != nil, ItemAgentText},
		{it.Thinking != nil, ItemThinking},
		{it.Step != nil, ItemStep},
		{it.System != nil, ItemSystem},
	} {
		if v.set {
			n++
			own = own || v.typ == it.Type
		}
	}
	if n != 1 || !own {
		return fmt.Errorf("item type %q must hold exactly its own variant, has %d variants (own set: %v)", it.Type, n, own)
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
		if it.Thinking.Truncated && len(it.Thinking.Text) < MaxText-3 {
			err = fmt.Errorf("thinking flagged truncated but only %d bytes", len(it.Thinking.Text))
		}
	case ItemSystem:
		id = it.System.ID
		if !slices.Contains([]SystemKind{SystemInterrupted, SystemCompacted, SystemHandoff, SystemModelChanged, SystemResumed, SystemCommandOutput, SystemNotice}, it.System.Kind) {
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
	var in map[string]any
	if json.Unmarshal(s.Input, &in) != nil || in == nil {
		return fmt.Errorf("step input is not a JSON object: %.40q", s.Input)
	}
	// input_truncated says the stored input is not the complete tool input,
	// for any reason (a cut string, the total cap, the depth cap, a dropped
	// member), so the output alone cannot prove it. What can be proven is the
	// other direction: an input that is not flagged is within the caps.
	if !s.InputTruncated {
		if err := inputOverCap(s.Input); err != nil {
			return fmt.Errorf("step input not flagged truncated but %w", err)
		}
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
	if err := validateStepExtras(s); err != nil {
		return err
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

// inputTooDeep reports whether raw nests containers deeper than MaxInputDepth.
// It walks tokens with a counter, not recursion, and stops at the first
// container past the limit, so hostile nesting costs at most MaxInput bytes.
func inputTooDeep(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				if depth++; depth > MaxInputDepth {
					return true
				}
			default:
				depth--
			}
		}
	}
}

// inputOverCap reports a step input that exceeds a cap: the whole input over
// MaxInput bytes, containers nested deeper than MaxInputDepth, or any string
// value over MaxInputString bytes.
func inputOverCap(raw json.RawMessage) error {
	if len(raw) > MaxInput {
		return fmt.Errorf("is %d bytes, over the %d cap", len(raw), MaxInput)
	}
	if inputTooDeep(raw) {
		return fmt.Errorf("nests deeper than %d levels", MaxInputDepth)
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var walk func(any) int
	walk = func(v any) int {
		switch x := v.(type) {
		case string:
			if len(x) > MaxInputString {
				return len(x)
			}
		case []any:
			for _, e := range x {
				if n := walk(e); n > 0 {
					return n
				}
			}
		case map[string]any:
			for _, e := range x {
				if n := walk(e); n > 0 {
					return n
				}
			}
		}
		return 0
	}
	if n := walk(v); n > 0 {
		return fmt.Errorf("holds a string of %d bytes, over the %d cap", n, MaxInputString)
	}
	return nil
}

// validateStepExtras checks the additive members of a step (U3-0).
func validateStepExtras(s *Step) error {
	if q := s.Question; q != nil {
		if len(q.Questions) == 0 || len(q.Questions) > MaxQuestions {
			return fmt.Errorf("question has %d questions, want 1 to %d", len(q.Questions), MaxQuestions)
		}
		for i, it := range q.Questions {
			if it.Question == "" {
				return fmt.Errorf("question %d has no text", i)
			}
			if len(it.Options) > MaxQuestionOptions {
				return fmt.Errorf("question %d has %d options, over the %d cap", i, len(it.Options), MaxQuestionOptions)
			}
		}
		if q.Answers != nil {
			if len(q.Answers) != len(q.Questions) {
				return fmt.Errorf("question has %d answers for %d questions", len(q.Answers), len(q.Questions))
			}
			for i, a := range q.Answers {
				if len(a) == 0 {
					return fmt.Errorf("answer %d is empty", i)
				}
			}
		}
	}
	if r := s.Read; r != nil && (r.Offset < 0 || r.Limit < 0 || (r.Offset == 0 && r.Limit == 0)) {
		return fmt.Errorf("read range offset %d limit %d: negative, or neither given", r.Offset, r.Limit)
	}
	if sc := s.Search; sc != nil && sc.Where == "" {
		return errors.New("search scope is empty")
	}
	return nil
}
