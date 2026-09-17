// internal/peers/ref.go
package peers

import (
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Label rules (Peer Address v2 spec §3.1).
const (
	LabelSourceUser = "user"

	// LabelReservedCC / LabelReservedTmux are refused as labels and
	// short-circuited by Resolve so "cc:<x>" and "tmux:<x>" never parse as
	// label+suffix.
	LabelReservedCC   = "cc"
	LabelReservedTmux = "tmux"

	base36Digits = "0123456789abcdefghijklmnopqrstuvwxyz"

	// canonicalN is 6, not 8: the ref is no longer the only way to reach a
	// conversation (v4 spec §5.1). The name covers a ref collision exactly as
	// the ref covers a name collision, so the width carries a tiebreaker's
	// budget rather than the whole address's. 36^6 ≈ 2.18e9 puts the birthday
	// probability for 100 live conversations at ≈2.3e-6.
	canonicalN     = 6
	canonicalSpace = 36 * 36 * 36 * 36 * 36 * 36 // 36^6

	titleMaxBytes = 64
)

var (
	ErrLabelInvalid  = errors.New("label invalid")
	ErrLabelReserved = errors.New("label reserved")

	// ErrTitleInvalid is what every ValidateTitle failure wraps.
	ErrTitleInvalid = errors.New("title invalid")

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

// ValidateTitle applies the title rule (v4 spec §6): 1..64 bytes of printable
// UTF-8.
//
// There are no reserved words. A title reaches nothing — Resolve never
// consults one — so there is nothing for "cc" or "tmux" to shadow, and
// refusing them would make the field harder to use than it is dangerous.
//
// Control characters are refused because a title is printed into a terminal
// table and into peer warnings; an ANSI escape in one is a way to rewrite
// somebody else's screen.
func ValidateTitle(s string) error {
	if s == "" {
		return fmt.Errorf("%w: empty", ErrTitleInvalid)
	}
	if len(s) > titleMaxBytes {
		return fmt.Errorf("%w: %d bytes, max %d", ErrTitleInvalid, len(s), titleMaxBytes)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: not valid UTF-8", ErrTitleInvalid)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return fmt.Errorf("%w: contains a non-printable character", ErrTitleInvalid)
		}
	}
	return nil
}

// NormalizeTitle is the form two titles are compared in when deciding whether
// to warn: case-folded, runs of whitespace collapsed, ends trimmed.
//
// "Purdex Tester" and "purdex  tester" name the same thing to a reader, so
// they must collide for the warning too — otherwise the warning misses exactly
// the near-duplicates it exists for.
//
// It stays consistent with ValidateTitle because unicode.IsPrint reports false
// for every rune strings.Fields would split on except the plain ASCII space —
// \t, \n and \r, but also NBSP and U+3000 (verified). So a stored title can
// only ever contain the one kind of whitespace this collapses.
func NormalizeTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
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
