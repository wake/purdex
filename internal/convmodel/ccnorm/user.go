package ccnorm

import "github.com/wake/purdex/internal/convmodel"

// userRow: stage (a) only needs a prompt row to open a turn.
func (n *Normalizer) userRow(l *rawLine, off int64) {
	msg, _ := parseObject(l.Message)
	blocks, ok := contentBlocks(msg.get("content"))
	if !ok {
		return
	}
	ti, ok := n.openTurn(l.uuid, l.at, off)
	if !ok {
		return
	}
	n.upsert(l.uuid, convmodel.Item{Type: convmodel.ItemUser, User: &convmodel.UserMessage{
		ID: l.uuid, At: l.at, Text: joinText(blocks), Source: convmodel.SourceUser,
	}}, off)
	n.attribute(ti, l.at)
}

func (n *Normalizer) attachmentRow(l *rawLine, off int64) {}
