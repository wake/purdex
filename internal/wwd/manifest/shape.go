package manifest

import "strings"

// ShapeKind says what a word of a task's command is for the entry (the hook, which recognises a command by its shape).
type ShapeKind int

const (
	// ShapeLiteral is a word the caller types as it is.
	ShapeLiteral ShapeKind = iota
	// ShapeArgs is the lone {files} or {packages}: where the caller's arguments go.
	ShapeArgs
	// ShapeOutput is a word that carries an output file of the worker's ({report}, {junit}); a caller does not type it. Text is the
	// literal text before the placeholder ("--outputFile=", or "" for a word that is the placeholder alone).
	ShapeOutput
)

// ShapeWord is one word of a task's command.
type ShapeWord struct {
	Kind ShapeKind
	Text string
}

// Shape is the task's command as words, in order. It is a copy.
func (t *Task) Shape() []ShapeWord {
	out := make([]ShapeWord, 0, len(t.argv))
	for _, w := range t.argv {
		if _, ok := w.multi(); ok {
			out = append(out, ShapeWord{Kind: ShapeArgs})
			continue
		}
		var b strings.Builder
		output := false
		for _, p := range w {
			if p.ph != "" {
				output = true
				break
			}
			b.WriteString(p.lit)
		}
		if output {
			out = append(out, ShapeWord{Kind: ShapeOutput, Text: b.String()})
			continue
		}
		out = append(out, ShapeWord{Kind: ShapeLiteral, Text: b.String()})
	}
	return out
}
