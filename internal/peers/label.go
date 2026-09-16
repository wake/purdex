// internal/peers/label.go
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

	// canonicalN is 8, not 6, because the canonical id is the ONLY thing
	// a conversation is reachable by (spec §4.1): there is no allocator
	// to fall back on, so the width has to carry the collision budget on
	// its own. 36^8 ≈ 2.82e12 puts the birthday probability for 100 live
	// conversations at ≈1.8e-9, against ≈2.3e-6 at width 6.
	canonicalN     = 8
	canonicalSpace = 36 * 36 * 36 * 36 * 36 * 36 * 36 * 36 // 36^8
)

var (
	ErrLabelInvalid  = errors.New("label invalid")
	ErrLabelReserved = errors.New("label reserved")

	userLabelPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)
	canonicalPattern  = regexp.MustCompile(`^_[0-9a-z]{8}$`)
	suffixWirePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,65}$`)
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

// IsCanonicalID reports whether s has the canonical form, "_" followed by
// exactly 8 base36 digits. The leading '_' is what keeps the address
// namespace disjoint from the label namespace: a user label must match
// ^[a-z0-9][a-z0-9-]{1,31}$ and so can never begin with '_'.
func IsCanonicalID(s string) bool { return canonicalPattern.MatchString(s) }

// CanonicalID derives a conversation's address from its Claude Code
// sessionId: "_" + base36(FNV-1a-64(sessionId) mod 36^8), 8 digits,
// zero-padded. Pure and deterministic across resumes and daemon
// restarts, because the sessionId is the one identity nobody issues,
// requests, or competes for (spec §3.1) — which is exactly why an
// address derived from it cannot be allocated twice.
func CanonicalID(sessionID string) string {
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
