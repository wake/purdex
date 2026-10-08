package main

import (
	"io"
	"strings"

	"github.com/wake/purdex/internal/textwidth"
)

// The width code lives in internal/textwidth, where the daemon shares it.
var (
	cellWidth = textwidth.CellWidth
	cutWidth  = textwidth.CutWidth
)

// alignRows writes rows as a table whose columns are padded by display width
// (text/tabwriter counts runes, which misaligns CJK), gap spaces apart; the
// last column is not padded. It returns the first write error.
func alignRows(w io.Writer, rows [][]string, gap int) error {
	var widths []int
	for _, row := range rows {
		for i, c := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], cellWidth(c))
		}
	}
	for _, row := range rows {
		var b strings.Builder
		for i, c := range row {
			b.WriteString(c)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-cellWidth(c)+gap))
			}
		}
		b.WriteByte('\n')
		if _, err := io.WriteString(w, b.String()); err != nil {
			return err
		}
	}
	return nil
}
