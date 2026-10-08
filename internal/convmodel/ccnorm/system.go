package ccnorm

// systemRow: stage (a) only needs turn_duration.
func (n *Normalizer) systemRow(l *rawLine, off int64) {
	if l.subtype != "turn_duration" || len(n.turns) == 0 {
		return
	}
	ti := len(n.turns) - 1
	tr := n.turns[ti]
	tr.duration, tr.durationAt = true, l.at
	n.attribute(ti, l.at)
}
