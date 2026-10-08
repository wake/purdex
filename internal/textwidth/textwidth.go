package textwidth

import (
	"strings"
	"unicode"
)

// RuneWidth is how many terminal columns a rune takes (the product-defined
// weight of spec team-label D-L1: a count per code point, no promise about a
// terminal): 0 for combining marks
// and zero-width characters, 2 for East Asian wide characters and emoji, else 1.
func RuneWidth(r rune) int {
	switch {
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || (r >= 0x200B && r <= 0x200F) || r == 0xFE0F || (r >= 0x1F3FB && r <= 0x1F3FF):
		return 0
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F,
		r >= 0x1F680 && r <= 0x1F6FF,
		r >= 0x1F900 && r <= 0x1FAFF,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

// CellWidth is the display width of s in terminal columns.
func CellWidth(s string) int {
	n := 0
	for _, r := range s {
		n += RuneWidth(r)
	}
	return n
}

// CutWidth cuts s to at most max display columns, ending in "…" when it cut.
func CutWidth(s string, max int) string {
	if CellWidth(s) <= max {
		return s
	}
	w := 0
	var b strings.Builder
	for _, r := range s {
		rw := RuneWidth(r)
		if w+rw > max-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}
