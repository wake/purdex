package wire

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxAliasBytes bounds a host alias.
const MaxAliasBytes = 128

// ValidAlias is the one rule for a host alias: not empty, not "." or "..", at most MaxAliasBytes, valid UTF-8, no control
// character. Nothing else is restricted (an alias travels as one escaped query value or path segment), so every alias wwd's
// config accepts is one the Purdex daemon's pool routes can name, and the other way round. "." and ".." are the exception:
// as a path segment a browser or the ServeMux takes them for a dot segment and the request never reaches its handler (#3615).
func ValidAlias(s string) bool {
	return s != "" && s != "." && s != ".." && len(s) <= MaxAliasBytes && utf8.ValidString(s) &&
		!strings.ContainsFunc(s, unicode.IsControl) // C0, DEL and C1 (U+0080-U+009F)
}
