package ccnorm

import "github.com/wake/purdex/internal/convmodel"

// assistantRow: stage (a) only needs the text blocks.
func (n *Normalizer) assistantRow(l *rawLine, off int64) {
	msg, _ := parseObject(l.Message)
	blocks, ok := contentBlocks(msg.get("content"))
	if !ok || len(n.turns) == 0 {
		return
	}
	ti := len(n.turns) - 1
	for _, b := range blocks {
		if b.typ == "text" && b.text != "" {
			n.upsert(n.turns[ti].t.ID, convmodel.Item{Type: convmodel.ItemAgentText, AgentText: &convmodel.AgentText{
				ID: l.uuid, At: l.at, Markdown: b.text,
			}}, off)
		}
	}
	n.attribute(ti, l.at)
}
