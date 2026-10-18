package wire

import (
	"strings"
	"unicode/utf8"
)

// MaxAliasBytes bounds a host alias.
const MaxAliasBytes = 128

// ValidAlias is the one rule for a host alias: not empty, at most MaxAliasBytes, valid UTF-8, no control character.
// Nothing else is restricted (an alias travels as one escaped query value or path segment), so every alias wwd's config
// accepts is one the Purdex daemon's pool routes can name, and the other way round.
func ValidAlias(s string) bool {
	return s != "" && len(s) <= MaxAliasBytes && utf8.ValidString(s) &&
		!strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}
