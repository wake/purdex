package ccnorm

import (
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

// openStep settles the status of a new step against its turn.
func (n *Normalizer) openStep(tr *turnRec, s *convmodel.Step) {
	s.Status, s.Denial = pendingStatus(tr.t.Outcome)
}
