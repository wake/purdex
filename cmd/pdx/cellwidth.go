package main

import (
	"io"
	"strings"
	"unicode"
)

// runeWidth is how many terminal columns a rune takes: 0 for combining marks
// and zero-width characters, 2 for East Asian wide characters and emoji, else 1.
func runeWidth(r rune) int {
	switch {
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || (r >= 0x200B && r <= 0x200F) || r == 0xFE0F:
		return 0
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F,
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

// cellWidth is the display width of s in terminal columns.
func cellWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// cutWidth cuts s to at most max display columns, ending in "…" when it cut.
func cutWidth(s string, max int) string {
	if cellWidth(s) <= max {
		return s
	}
	w := 0
	var b strings.Builder
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > max-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

// alignRows writes rows as a table whose columns are padded by display width
// (text/tabwriter counts runes, which misaligns CJK), gap spaces apart; the
// last column is not padded.
func alignRows(w io.Writer, rows [][]string, gap int) {
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
		io.WriteString(w, b.String())
	}
}
