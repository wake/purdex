// internal/peers/ref.go
package peers

import (
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

// Label rules (Peer Address v2 spec §3.1).
const (
	LabelSourceUser = "user"

	// LabelReservedCC / LabelReservedTmux are refused as labels and
	// short-circuited by Resolve so "cc:<x>" and "tmux:<x>" never parse as
	// label+suffix.
	LabelReservedCC   = "cc"
	LabelReservedTmux = "tmux"

	sanitizeMax  = 32
	base36Digits = "0123456789abcdefghijklmnopqrstuvwxyz"

	// canonicalN is 6, not 8: the ref is no longer the only way to reach a
	// conversation (v4 spec §5.1). The name covers a ref collision exactly as
	// the ref covers a name collision, so the width carries a tiebreaker's
	// budget rather than the whole address's. 36^6 ≈ 2.18e9 puts the birthday
	// probability for 100 live conversations at ≈2.3e-6.
	canonicalN     = 6
	canonicalSpace = 36 * 36 * 36 * 36 * 36 * 36 // 36^6
)

var (
	ErrLabelInvalid  = errors.New("label invalid")
	ErrLabelReserved = errors.New("label reserved")

	userLabelPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)
	refPattern        = regexp.MustCompile(`^_[0-9a-z]{6}$`)
	suffixWirePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,65}$`)

	routableNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
	refShapedPattern    = regexp.MustCompile(`^[0-9a-z]{6}$`)
)

// ValidateUserLabel applies the user label rule and the reserved words.
func ValidateUserLabel(s string) error {
	if s == LabelReservedCC || s == LabelReservedTmux {
		return fmt.Errorf("%w: %q", ErrLabelReserved, s)
	}
	if !userLabelPattern.MatchString(s) {
		return fmt.Errorf("%w: must match ^[a-z0-9][a-z0-9-]{1,31}$", ErrLabelInvalid)
	}
	return nil
}

// IsRef reports whether s has the ref form, "_" followed by exactly 6 base36
// digits. The leading '_' is half of what keeps the ref namespace disjoint
// from the name namespace; RoutableName (Task A2) is the other half.
func IsRef(s string) bool { return refPattern.MatchString(s) }

// RefID derives a conversation's ref from its Claude Code sessionId:
// "_" + base36(FNV-1a-64(sessionId) mod 36^6), 6 digits, zero-padded. Pure
// and deterministic across resumes and daemon restarts.
func RefID(sessionID string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionID))
	n := h.Sum64() % canonicalSpace
	out := make([]byte, canonicalN)
	for i := canonicalN - 1; i >= 0; i-- {
		out[i] = base36Digits[n%36]
		n /= 36
	}
	return "_" + string(out)
}

// RoutableName reports whether a Claude Code registry name may be used as an
// address head (v4 spec §5.2).
//
// The registry name is an unvalidated JSON string: registry.go assigns it raw
// and ccuds.RewriteRegistryName can write anything into it. Two independent
// hazards follow, and this function closes both:
//
//   - the PATTERN keeps '/', ':', ' ', '[', ']' and a leading '_' out of an
//     address head, so "<host>/<name>" always parses and can never be read as
//     a ref;
//   - the REF-SHAPED exclusion is what makes Resolve's bare-ref tier safe. The
//     table prints "[q34psn]", so an operator copying bracket text types
//     "q34psn"; without this clause a conversation named "q34psn" would
//     silently shadow another's ref, and anything able to write a registry
//     file could arrange exactly that.
//
// A failing name is still displayed. It simply never becomes an address: its
// row is reachable by ref only and carries Reason "name_unroutable".
func RoutableName(s string) bool {
	return routableNamePattern.MatchString(s) && !refShapedPattern.MatchString(s)
}

// Deprecated: use RefID / IsRef.
//
// These exist for exactly one task. Renaming the FUNCTION here and the FIELD
// in Task A3 as one change would mean a single unreviewable commit spanning
// 45 files; splitting them means this task cannot also delete the old names.
// Task A3 removes both lines along with the Canonical field.
func CanonicalID(sessionID string) string { return RefID(sessionID) }
func IsCanonicalID(s string) bool         { return IsRef(s) }

// Sanitize is the suffix component sanitizer: keeps [A-Za-z0-9_.-],
// replaces every other byte with '_', truncates to 32 bytes; "" ⇒ "_".
func Sanitize(s string) string {
	if s == "" {
		return "_"
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(b) < sanitizeMax; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '.', c == '-':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	return string(b)
}

// Suffix is the display suffix: san(tmux)-san(cc) inside tmux, san(cc)
// outside (tmuxSessionName == "").
func Suffix(tmuxSessionName, ccName string) string {
	if tmuxSessionName == "" {
		return Sanitize(ccName)
	}
	return Sanitize(tmuxSessionName) + "-" + Sanitize(ccName)
}

// ValidSuffix is the wire grammar a receiver checks on from.address's
// suffix part.
func ValidSuffix(s string) bool { return suffixWirePattern.MatchString(s) }

// SplitSession splits the <session> half of an address at its first ':'
// into head and rest; rest is "" when there is no ':'.
func SplitSession(session string) (head, rest string) {
	if i := strings.IndexByte(session, ':'); i >= 0 {
		return session[:i], session[i+1:]
	}
	return session, ""
}
