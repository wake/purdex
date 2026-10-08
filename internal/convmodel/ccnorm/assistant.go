package ccnorm

import (
	"fmt"

	"github.com/wake/purdex/internal/convmodel"
)

// assistantRow handles one assistant row. Claude Code writes one content
// block per row (M-U1-7), so each row is handled on its own and nothing is
// buffered by message.id; an older multi-block row yields `<uuid>#<n>` ids
// for its second and later items.
func (n *Normalizer) assistantRow(l *rawLine, off int64) {
	msg, _ := parseObject(l.Message)
	blocks, ok := contentBlocks(msg.get("content"))
	if !ok {
		n.skip("content")
		return
	}
	model := msg.str("model")
	if l.apiError {
		n.apiError(l, off, joinText(blocks))
		return
	}
	if model == "<synthetic>" {
		// Claude Code's own rows ("No response requested.", …) are never
		// the agent's words and never the model in use.
		n.skip("synthetic")
		return
	}
	// The model in use is the last main-thread assistant row's, whatever its
	// blocks are (a tool_use row counts).
	n.model = model
	n.effort = l.str(l.PerTurnEffort)
	if n.effort == "" {
		n.effort = l.str(l.Effort)
	}

	var items []convmodel.Item
	next := func() string {
		if len(items) == 0 {
			return l.uuid
		}
		return fmt.Sprintf("%s#%d", l.uuid, len(items))
	}
	for _, b := range blocks {
		switch b.typ {
		case "text":
			if b.text == "" {
				continue
			}
			text, cut := capText(b.text, convmodel.MaxText)
			items = append(items, convmodel.Item{Type: convmodel.ItemAgentText, AgentText: &convmodel.AgentText{
				ID: next(), At: l.at, Markdown: text, Truncated: cut,
			}})
		case "thinking":
			dur, _ := jsonInt(l.ThinkingDurationMS)
			if b.text == "" && dur == 0 {
				continue // nothing to show: no text, no duration
			}
			text, cut := capText(b.text, convmodel.MaxText)
			items = append(items, convmodel.Item{Type: convmodel.ItemThinking, Thinking: &convmodel.Thinking{
				ID: next(), At: l.at, Text: text, Truncated: cut, DurationMS: dur,
			}})
		case "tool_use":
			n.skip("step:deferred") // steps arrive in U1-4c
		default:
			n.skip("block:" + b.typ)
		}
	}
	// Turn bookkeeping belongs to the row, not to the items it produced: a
	// tool_use row (its step arrives in U1-4c) is a main-thread assistant
	// row too, and decides the API-error state and the model in use. A row
	// that shows nothing does not open a turn of its own (the model in use
	// above still counts).
	if len(items) == 0 && len(n.turns) == 0 {
		return
	}
	ti := n.ensureTurn(l.uuid, l.at, off)
	tr := n.turns[ti]
	tr.hasModel = true
	tr.apiErr = nil // the turn's last assistant row is a reply, not an error
	n.modelChanged(ti, model, l.at, off)
	for _, it := range items {
		n.upsert(tr.t.ID, it, off)
	}
	n.attribute(ti, l.at)
}

// apiError records the synthetic assistant row of an API error as the turn's
// error (spec §3 M-U1-7: model "<synthetic>", isApiErrorMessage, error ∈
// rate_limit | server_error | …). Its text is the error message, never an
// agent_text. A later real reply in the same turn clears it: only the turn's
// last assistant row decides.
func (n *Normalizer) apiError(l *rawLine, off int64, text string) {
	msg, _ := capText(text, convmodel.MaxText)
	ti := n.ensureTurn(l.uuid, l.at, off)
	n.turns[ti].apiErr = &convmodel.TurnError{Kind: l.str(l.Error), Message: msg}
	n.attribute(ti, l.at)
}

// modelChanged adds the derived `model_changed` item (id `<turn id>#model`)
// at the turn's first reply when the main-thread model differs from the one
// before the turn.
func (n *Normalizer) modelChanged(ti int, model string, at, off int64) {
	tr := n.turns[ti]
	if tr.sawModel {
		return
	}
	tr.sawModel = true
	if tr.prevModel == "" || tr.prevModel == model {
		return
	}
	n.upsert(tr.t.ID, convmodel.Item{Type: convmodel.ItemSystem, System: &convmodel.System{
		ID: tr.t.ID + "#model", At: at, Kind: convmodel.SystemModelChanged,
		Detail: marshalNoEscape(struct {
			Model string `json:"model"`
		}{model}),
	}}, off)
}

// handoff adds the derived `handoff` item (id `<turn id>#handoff`) to a turn
// whose opening row changed the entrypoint between the terminal (cli) and an
// execution (sdk-cli) compared with the rows before it.
func (n *Normalizer) handoff(ti int, entry string, at, off int64) {
	if n.entryBefore == "" || entry == "" || entry == n.entryBefore {
		return
	}
	to := map[string]string{"cli": "terminal", "sdk-cli": "execution"}[entry]
	if to == "" || (n.entryBefore != "cli" && n.entryBefore != "sdk-cli") {
		return
	}
	tr := n.turns[ti]
	n.upsert(tr.t.ID, convmodel.Item{Type: convmodel.ItemSystem, System: &convmodel.System{
		ID: tr.t.ID + "#handoff", At: at, Kind: convmodel.SystemHandoff,
		Detail: marshalNoEscape(struct {
			To string `json:"to"`
		}{to}),
	}}, off)
}
