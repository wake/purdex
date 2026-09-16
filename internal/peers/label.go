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

// qualifiesAsDefaultLabel reports whether a tmux session name may be used
// as a default label (spec §3.1): only when it ALREADY is a valid user
// label. There is deliberately no sanitizing — no folding, no
// substitution, no truncation — because a transformed name is a different
// string from the session it came from, and that other string may be the
// real name of another tmux session on the same host. Refusing to mint a
// label that differs from the name it stands for is what makes tier 1 and
// tier 2 always mean the same place.
func qualifiesAsDefaultLabel(tmuxName string) bool {
	return ValidateUserLabel(tmuxName) == nil
}

// DefaultLabels maps a live conversation's sessionId to its resolved
// default label. Only sessions whose tmux-derived candidate survived
// spec §3.3 are present, so a nil map behaves exactly as Peer Address v2
// did: every lookup falls back to the hash.
type DefaultLabels map[string]string

// For is the only way to read a default label, so a caller can never
// accidentally render a place address the resolver rejected.
func (d DefaultLabels) For(sessionID string) string {
	if label := d[sessionID]; label != "" {
		return label
	}
	return DefaultLabel(sessionID)
}

// sidSet is a set of conversation ids (sessionIds).
type sidSet map[string]bool

// otherThan reports whether the set holds any conversation but sid — the
// one question rules 2 and 3 both ask, since a conversation never
// competes with itself for its own place.
func (s sidSet) otherThan(sid string) bool {
	for other := range s {
		if other != sid {
			return true
		}
	}
	return false
}

// ResolveDefaultLabels applies spec §3.3 over one population: the live,
// non-proxy registry entries of this host. A conversation keeps the v2
// hash — it is absent from the result — unless all of its entries sit in
// one tmux session whose name already is a valid user label, no other
// live conversation has a process in that same tmux session, and no other
// live conversation holds that name as a user label. labels may cover
// sessions outside the population (dead rows); those are inert and
// ignored.
func ResolveDefaultLabels(entries []Entry, proxyPIDs map[int]bool, labels map[string]LabelInfo) DefaultLabels {
	population := livePopulation(entries, proxyPIDs)
	candidateBySID := candidatesOf(population)
	occupantsByLabel := occupantsOf(population)
	userLabelHolders := userLabelHoldersOf(population, labels)

	resolved := make(DefaultLabels, len(candidateBySID))
	for sid, candidate := range candidateBySID {
		// Rule 2 (the place is shared) and rule 3 (someone else owns the
		// name) are the same question asked of two different sets.
		if occupantsByLabel[candidate].otherThan(sid) {
			continue
		}
		if userLabelHolders[candidate].otherThan(sid) {
			continue
		}
		resolved[sid] = candidate
	}
	return resolved
}

// livePopulation is spec §3.2's population: every registry entry that is
// not one of this daemon's proxy helpers. Filtering once, up front, is
// what lets every rule below read "population" and mean the same thing.
func livePopulation(entries []Entry, proxyPIDs map[int]bool) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.IsProxy || proxyPIDs[e.PID] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// candidatesOf is spec §3.3's candidate[sid]: per CONVERSATION, the one
// qualifying tmux session name all of its live entries report. A
// conversation with live processes in two different tmux sessions has no
// single place, so it gets no place address and is absent here. The
// candidate is the session's own name byte for byte (§3.1), so rule 1's
// "same tmux session" test is a plain string comparison.
func candidatesOf(population []Entry) map[string]string {
	candidate := make(map[string]string, len(population))
	// Disqualification is sticky and separate from the map, so a later
	// entry can never revive a conversation an earlier one ruled out.
	disqualified := make(map[string]bool, len(population))
	for _, e := range population {
		if disqualified[e.SessionID] {
			continue
		}
		name := e.TmuxSessionName()
		seenName, seen := candidate[e.SessionID]
		if !qualifiesAsDefaultLabel(name) || (seen && seenName != name) {
			disqualified[e.SessionID] = true
			delete(candidate, e.SessionID)
			continue
		}
		candidate[e.SessionID] = name
	}
	return candidate
}

// occupantsOf is spec §3.3's occupants[name]: per ENTRY, every
// conversation with at least one live process in the qualifying tmux
// session name.
//
// Occupancy is about the place; candidacy is about the conversation. A
// conversation disqualified as a candidate — because it spans two tmux
// sessions — is still an occupant of both of them, and counting only
// surviving candidates as competitors let it drop out of the race for a
// place it was still sitting in, handing that place to the other agent
// sharing it (spec §11.3).
func occupantsOf(population []Entry) map[string]sidSet {
	occupants := make(map[string]sidSet, len(population))
	for _, e := range population {
		name := e.TmuxSessionName()
		if !qualifiesAsDefaultLabel(name) {
			// A name no candidate can ever equal competes with nothing.
			continue
		}
		if occupants[name] == nil {
			occupants[name] = sidSet{}
		}
		occupants[name][e.SessionID] = true
	}
	return occupants
}

// userLabelHoldersOf indexes the user labels held by conversations IN the
// population, by label (spec §3.3 rule 3). Label rows whose session is not
// live are inert and never appear, so a dead holder blocks nothing.
func userLabelHoldersOf(population []Entry, labels map[string]LabelInfo) map[string]sidSet {
	holders := make(map[string]sidSet, len(population))
	for _, e := range population {
		label := labels[e.SessionID].Label
		if label == "" {
			continue
		}
		if holders[label] == nil {
			holders[label] = sidSet{}
		}
		holders[label][e.SessionID] = true
	}
	return holders
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
