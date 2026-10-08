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
	if msg.str("model") == "<synthetic>" {
		// Claude Code's own rows ("No response requested.", API errors) are
		// never the agent's words.
		n.skip("synthetic")
		return
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
	if len(items) == 0 {
		return
	}
	ti := n.ensureTurn(l.uuid, l.at, off)
	tr := n.turns[ti]
	tr.hasModel = true
	for _, it := range items {
		n.upsert(tr.t.ID, it, off)
	}
	n.attribute(ti, l.at)
}
