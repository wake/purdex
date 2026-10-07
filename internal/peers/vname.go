package peers

import (
	"regexp"
	"strings"
)

// vnameMaxLen is RoutableName's length cap; vnameSuffixLen is "-" plus the
// two ref digits the virtual name carries (peer mailbox spec §3.1).
const (
	vnameMaxLen    = 64
	vnameSuffixLen = 3
)

var vnameBasePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// VirtualName is a conversation's pdx-assigned name (Peer Address v5, spec
// §3.1): "<base>-<ref[1:3]>", e.g. base "purdex-54" and ref "_k3m9qz" give
// "purdex-54-k3".
//
// The order is the spec's and it matters: RoutableName caps the length, so it
// cannot run on a raw base that is merely too long. The base's characters are
// checked first, then the base is cut from the end so the whole name fits in
// 64 (a trailing '-' left by the cut is trimmed), and only then is the whole
// name checked. ok is false — and no name is assigned — when the base fails
// the shape check, when ref is not a ref, or when the result is unroutable.
//
// A virtual name always contains '-', so it can never be the 6 base36 digits
// RoutableName refuses: a valid base is enough.
func VirtualName(base, ref string) (string, bool) {
	if !IsRef(ref) || !vnameBasePattern.MatchString(base) {
		return "", false
	}
	if max := vnameMaxLen - vnameSuffixLen; len(base) > max {
		base = base[:max]
	}
	base = strings.TrimRight(base, "-")
	if base == "" {
		return "", false
	}
	name := base + "-" + ref[1:3]
	if !RoutableName(name) {
		return "", false
	}
	return name, true
}

// NormalizeBase turns free text (an execution's cwd basename) into a
// candidate base: lowercased, every rune outside [a-z0-9-] replaced by '-',
// runs of '-' collapsed, '-' trimmed from both ends. "" when nothing is left.
func NormalizeBase(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
