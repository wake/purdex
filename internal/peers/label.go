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
	LabelSourceUser    = "user"
	LabelSourceDefault = "default"

	// LabelReservedCC / LabelReservedTmux are refused as labels and
	// short-circuited by Resolve so "cc:<x>" and "tmux:<x>" never parse as
	// label+suffix.
	LabelReservedCC   = "cc"
	LabelReservedTmux = "tmux"

	sanitizeMax   = 32
	defaultLabelN = 6
	labelSpace    = 36 * 36 * 36 * 36 * 36 * 36 // 36^6
	base36Digits  = "0123456789abcdefghijklmnopqrstuvwxyz"
)

var (
	ErrLabelInvalid  = errors.New("label invalid")
	ErrLabelReserved = errors.New("label reserved")

	userLabelPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)
	defaultLabelPattern = regexp.MustCompile(`^_[0-9a-z]{6}$`)
	suffixWirePattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,65}$`)
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

// IsDefaultLabel reports whether s has the "_xxxxxx" default form.
func IsDefaultLabel(s string) bool { return defaultLabelPattern.MatchString(s) }

// DefaultLabel derives the unnamed label of a conversation from its Claude
// Code sessionId: "_" + base36(FNV-1a-64(sessionId) mod 36^6), 6 digits,
// zero-padded. Deterministic across resumes and daemon restarts.
func DefaultLabel(sessionID string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionID))
	n := h.Sum64() % labelSpace
	out := make([]byte, defaultLabelN)
	for i := defaultLabelN - 1; i >= 0; i-- {
		out[i] = base36Digits[n%36]
		n /= 36
	}
	return "_" + string(out)
}

// SanitizeLabel derives a user-label-shaped string from a tmux session
// name (spec §3.1). Substitution is byte-wise and happens before the
// 32-byte truncation, so the cut can never split a multi-byte rune. ok is
// false — and label "" — when the name cannot yield a valid, unreserved
// label. The result is lossy: two different tmux names can produce the
// same label, which spec §3.3 rule 2 resolves like any other collision.
func SanitizeLabel(name string) (string, bool) {
	b := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			c = '-'
		}
		// Collapsing as the bytes are produced also drops the leading
		// run, so only a trailing '-' is left to trim.
		if c == '-' && (len(b) == 0 || b[len(b)-1] == '-') {
			continue
		}
		b = append(b, c)
	}
	if len(b) > sanitizeMax {
		b = b[:sanitizeMax]
	}
	label := strings.TrimRight(string(b), "-")
	// Checked against the rule directly rather than by calling
	// ValidateUserLabel, so the tests' regexp assertion stays independent
	// of the construction.
	if len(label) < 2 || label == LabelReservedCC || label == LabelReservedTmux {
		return "", false
	}
	return label, true
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
