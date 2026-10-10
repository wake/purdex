package ccnorm

import (
	"encoding/json"
	"strings"

	"github.com/wake/purdex/internal/convmodel"
)

// Steps: one `step` item per tool_use block, paired with its tool_result by
// tool_use_id wherever the result appears (spec §8.1 "Steps", lead rulings
// D4 / D6 / D9). The status of a step with no result follows its turn: it is
// running while the turn is, else denied with denial "interrupted" (the iOS
// "stale" rule; Nexen's toolevents calls that an error, we diverge on
// purpose).

// newStep builds the step of a tool_use block, before it has a result. ok is
// false (and the reason is counted) for a block with no id or an id already
// taken.
func (n *Normalizer) newStep(b block, taken []convmodel.Item, at int64) (convmodel.Item, bool) {
	id := b.obj.str("id")
	if id == "" {
		n.skip("step:no_id")
		return convmodel.Item{}, false
	}
	if _, dup := n.itemAt[id]; dup {
		n.skip("step:duplicate")
		return convmodel.Item{}, false
	}
	for _, it := range taken {
		if it.Step != nil && it.Step.ID == id {
			n.skip("step:duplicate")
			return convmodel.Item{}, false
		}
	}
	tool := b.obj.str("name")
	in, _ := parseObject(b.obj.get("input"))
	input, cut := capInput(b.obj.get("input"))
	s := &convmodel.Step{
		ID: id, At: at, Kind: kindOf(tool), Tool: tool,
		Summary: summaryOf(tool, in), StartedAt: at,
		Input: input, InputTruncated: cut,
	}
	switch s.Kind {
	case convmodel.StepEdit:
		s.Diff = inputDiff(tool, in) // replaced by the exact patch when the result brings one
	case convmodel.StepExecute:
		s.Command = commandOf(in)
	case convmodel.StepRead:
		s.Read = readRangeOf(in)
	case convmodel.StepSearch:
		s.Search = searchScopeOf(tool, in)
	}
	s.Question = questionOf(in) // by the shape of the input, whatever the tool is called
	return convmodel.Item{Type: convmodel.ItemStep, Step: s}, true
}

// pendingStatus is the status of a step with no result in a turn with this
// outcome.
func pendingStatus(o convmodel.Outcome) (convmodel.StepStatus, string) {
	if o == convmodel.OutcomeRunning {
		return convmodel.StepRunning, ""
	}
	return convmodel.StepDenied, "interrupted"
}

// openStep settles the status of a new step against its turn and remembers
// that it is waiting for a result.
func (n *Normalizer) openStep(tr *turnRec, s *convmodel.Step) {
	s.Status, s.Denial = pendingStatus(tr.t.Outcome)
	tr.pending = append(tr.pending, s.ID)
}

// repend re-derives the status of the turn's steps that have no result after
// the turn went from running to closed or back (a turn_duration, a marker,
// the next turn, SetLive). off < 0 marks a SetLive change, which is not a row
// and moves no position.
func (n *Normalizer) repend(tr *turnRec, off int64) {
	status, denial := pendingStatus(tr.t.Outcome)
	keep := tr.pending[:0]
	for _, id := range tr.pending {
		if _, has := n.resulted[id]; has {
			continue // answered since; drop it from the list
		}
		keep = append(keep, id)
		loc := n.itemAt[id]
		s := tr.t.Items[loc.item].Step
		if s.Status == status && s.Denial == denial {
			continue
		}
		s.Status, s.Denial = status, denial
		if off >= 0 {
			tr.pos[loc.item].Updated = off
		}
		n.add(Change{tr.t.ID, id, off})
	}
	tr.pending = keep
}

// result is one tool_result block with what the row around it says.
type result struct {
	blocks []block // the content blocks
	text   string  // the content's text blocks joined by newlines
	isErr  bool
	denial string          // the row's toolDenialKind, "" when absent
	tur    json.RawMessage // the row's toolUseResult
	at     int64
}

// maxDenial bounds a pass-through toolDenialKind value.
const maxDenial = 64

// toolResultRow applies the tool_result blocks of one user row to the steps
// they answer. The row never opens a turn; for the turn's bookkeeping it
// belongs to the last turn, where it physically stands, even when the step
// it answers is in an older one (a background tool's late result).
//
// toolDenialKind and toolUseResult are members of the row, not of a block:
// they are read only when the row holds exactly one result, since otherwise
// there is no saying which result they are about. A several-result row that
// carries toolDenialKind is counted as Skipped["multi_result_denial"].
func (n *Normalizer) toolResultRow(l *rawLine, blocks []block, off int64) {
	var results []block
	for _, b := range blocks {
		if b.typ == "tool_result" {
			results = append(results, b)
		}
	}
	if len(results) > 1 && l.str(l.ToolDenialKind) != "" {
		// not seen in any real transcript; if it ever happens, say so
		// instead of guessing which result the field is about
		n.skip("multi_result_denial")
	}
	for _, b := range results {
		r := result{at: l.at, isErr: jsonTrue(b.obj.get("is_error"))}
		// Not capBlocks: the totals of an output describe the whole result.
		// What is stored stays bounded all the same (text 16 KiB, images
		// maxOutputImages), and the content cannot outgrow the line cap.
		r.blocks, _ = contentBlocks(b.obj.get("content"))
		r.text = joinText(r.blocks)
		if len(results) == 1 {
			r.denial, _ = capText(l.str(l.ToolDenialKind), maxDenial)
			r.tur = l.ToolUseResult
		}
		n.applyResult(b.obj.str("tool_use_id"), r, off)
	}
	if len(n.turns) > 0 {
		n.attribute(len(n.turns)-1, l.at)
	}
}

// applyResult pairs a result with the step of that id, wherever that step
// lives, and replaces the item in place (so a late result moves the older
// turn's Updated position and reports a change at the result row's offset).
func (n *Normalizer) applyResult(id string, r result, off int64) {
	loc, ok := n.itemAt[id]
	if !ok || n.turns[loc.turn].t.Items[loc.item].Step == nil {
		n.skip("step:orphan_result")
		return
	}
	tr := n.turns[loc.turn]
	s := *tr.t.Items[loc.item].Step
	s.Status, s.Denial = resultStatus(r)
	s.Output = n.outputOf(r.blocks, s.Kind)
	tur, _ := parseObject(r.tur) // a string or absent toolUseResult is no object
	switch s.Kind {
	case convmodel.StepEdit:
		path := ""
		if s.Diff != nil {
			path = s.Diff.Path
		}
		if d := patchDiff(path, tur); d != nil {
			s.Diff = d
		}
		if s.Status == convmodel.StepDone {
			s.Diff = createdDiff(s.Diff, path, tur)
		}
	case convmodel.StepExecute:
		if s.Command != nil {
			s.Command = commandWithResult(s.Command, r.text, tur)
		}
	case convmodel.StepTask:
		s.Subagent = n.subagentOf(&s, tur)
	}
	if s.Question != nil && s.Status == convmodel.StepDone {
		s.Question = withAnswers(s.Question, tur)
	}
	if r.at > 0 && s.StartedAt > 0 {
		d := max(r.at-s.StartedAt, 0)
		s.DurationMS = &d
	}
	n.resulted[id] = struct{}{}
	n.upsert(tr.t.ID, convmodel.Item{Type: convmodel.ItemStep, Step: &s}, off)
}

// refusals are the texts older Claude Code versions put in an error result
// when the person said no; newer rows carry toolDenialKind instead.
var refusals = [...]string{"doesn't want to proceed", "[Request interrupted by user", "was rejected", "dismissed the question"}

// resultStatus is the status of a step that has a result, first match wins
// (spec §8.1): toolDenialKind present → denied with that value, with or
// without is_error; an error whose text is a refusal → denied
// "user-rejected"; any other error → failed; else done.
func resultStatus(r result) (convmodel.StepStatus, string) {
	switch {
	case r.denial != "":
		return convmodel.StepDenied, r.denial
	case r.isErr:
		for _, w := range refusals {
			if strings.Contains(r.text, w) {
				return convmodel.StepDenied, "user-rejected"
			}
		}
		return convmodel.StepFailed, ""
	}
	return convmodel.StepDone, ""
}
