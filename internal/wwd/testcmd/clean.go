package testcmd

import (
	"fmt"
	"strings"
	"unicode"
)

// Clean makes text that came from a job or another host safe to print on a terminal (spec §11: content from other hosts is
// not trusted): control characters — an escape sequence that clears the screen or rewrites a line, a carriage return that
// overwrites the status above it — and the invisible characters that reorder text are shown as \xNN or \uNNNN. A newline and
// a tab are kept. Everything else, non-ASCII text included, passes unchanged.
func Clean(s string) string {
	if !needsClean(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.IsControl(r) || isFormat(r):
			if r < 0x100 {
				fmt.Fprintf(&b, "\\x%02x", r)
			} else {
				fmt.Fprintf(&b, "\\u%04x", r)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CleanLine is Clean for one table cell or one line: a newline or tab becomes a space.
func CleanLine(s string) string {
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(Clean(s))
}

func needsClean(s string) bool {
	for _, r := range s {
		if (r != '\n' && r != '\t' && unicode.IsControl(r)) || isFormat(r) {
			return true
		}
	}
	return false
}

// isFormat: zero-width and direction-changing characters (bidi overrides and isolates, zero-width spaces, the byte order mark).
func isFormat(r rune) bool { return unicode.Is(unicode.Cf, r) }
