package peers

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned by Resolve when no record matches session at any
// tier.
var ErrNotFound = errors.New("peer not found")

// ErrResolveNotReady is returned by Resolve when the snapshot cannot
// vouch for the label tier's answer: the label tier found no match and
// the snapshot is Partial (the inventory may be missing the very label
// that would have matched, so a bare tmux-name fallback — tier 2 — would
// risk landing on the wrong session), or it found exactly one match while
// the snapshot's registry is incomplete (the unreadable file may hide a
// second live process of that same conversation, which would have made
// the label ambiguous). The caller retries, or addresses the session
// explicitly as "tmux:<name>" to bypass the label tier altogether.
var ErrResolveNotReady = errors.New("inventory partial: the label may be missing")

// ResolveSnapshot is what the caller knows about the completeness of the
// records it hands Resolve — both flags come straight from the target
// host's inventory envelope (spec §3.3).
type ResolveSnapshot struct {
	// Partial is the envelope's partial flag: some owner lookup did not
	// run, the label store could not be read, or the registry is
	// incomplete. A tier-1 miss on a Partial snapshot is ErrResolveNotReady
	// rather than a tier-2 fallback.
	Partial bool
	// RegistryIncomplete is true when the envelope named at least one
	// alive-but-undecodable registry file (len(UnknownRegistryFiles) > 0).
	// It is the one Partial cause that can hide a whole live PROCESS, so
	// even a single tier-1 hit is ErrResolveNotReady under it: the hidden
	// process may belong to the same conversation and make the label
	// ambiguous. A caller setting RegistryIncomplete should set Partial
	// too; Resolve does not require it.
	RegistryIncomplete bool
}

// ErrLegacyCC is returned (wrapped under ErrNotFound) by Resolve for the
// v1 "cc:<name>" address form, which Peer Address v2 retires.
var ErrLegacyCC = errors.New("cc: addresses were removed; run pdx peers --all to see the new addresses")

// AmbiguousError is returned by Resolve when a tier matches more than one
// record; Candidates holds only the matches from the tier that decided.
type AmbiguousError struct {
	Session    string
	Candidates []PeerRecord
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("peer address %q is ambiguous (%d candidates)", e.Session, len(e.Candidates))
}

// Resolve implements Peer Address v2 spec §3.2 for ONE host's records;
// host has already been matched by the caller. Forms and tiers, in order:
//
//   - "cc:<anything>": the retired v1 form, always ErrNotFound wrapping
//     ErrLegacyCC, regardless of partial.
//   - "tmux:<name>": explicit tmux form, bypassing the label tier
//     entirely; matches PeerRecord.SessionName == name. "tmux:" with an
//     empty name is ErrNotFound.
//   - "<label>[:<suffix>]": tier 1, the label, matched over every row
//     that carries a LIVE cc entry — session rows and entry rows alike,
//     but only those whose Agent is a real registry entry (Type "cc",
//     PID != 0). Proxy rows, rows with Label == "" and owner-fallback
//     rows (inbox_dead / ambiguous: Agent.PID == 0, no entry behind them)
//     are excluded — spec §3.3 makes a row whose holder is not live
//     inert, neither resolving nor blocking, and that is how the rule
//     reaches resolution. A typed suffix is ignored (display-only).
//     Several matches => *AmbiguousError regardless of the snapshot.
//     Exactly one match and snap.RegistryIncomplete => ErrResolveNotReady
//     (an unreadable registry file may hide a second process of that
//     conversation). No match and snap.Partial => ErrResolveNotReady (the
//     inventory may be missing the label that would have matched). No
//     match, not Partial and the head contains no ':' => tier 2: the bare
//     string as a tmux session name.
//
// The first tier (or form) with >=1 match decides: exactly one match =>
// that record (subject to the RegistryIncomplete rule above); several =>
// *AmbiguousError (never falls through to a lower tier). No tier matches
// => ErrNotFound.
func Resolve(records []PeerRecord, session string, snap ResolveSnapshot) (PeerRecord, error) {
	if session == "" {
		return PeerRecord{}, ErrNotFound
	}
	head, rest := SplitSession(session)
	switch head {
	case LabelReservedCC:
		return PeerRecord{}, fmt.Errorf("%w: %w", ErrNotFound, ErrLegacyCC)
	case LabelReservedTmux:
		if rest == "" {
			return PeerRecord{}, ErrNotFound
		}
		return resolveTier(records, session, func(r PeerRecord) bool { return r.SessionName == rest })
	}
	// Tier 1: label, over every row backed by a live entry; the typed
	// suffix is ignored.
	rec, err := resolveTier(records, session, func(r PeerRecord) bool {
		return r.Label != "" && r.Label == head && hasLiveEntry(r)
	})
	if err == nil && snap.RegistryIncomplete {
		// One hit, but a registry file for an alive pid could not be
		// decoded: that file may be a second live process of this very
		// conversation, which would have made the label ambiguous. Do
		// not pick one process on incomplete evidence.
		return PeerRecord{}, ErrResolveNotReady
	}
	if !errors.Is(err, ErrNotFound) {
		return rec, err
	}
	if snap.Partial {
		return PeerRecord{}, ErrResolveNotReady
	}
	// Tier 2: bare tmux session name, complete inventory only.
	if rest != "" {
		return PeerRecord{}, ErrNotFound
	}
	return resolveTier(records, session, func(r PeerRecord) bool { return r.SessionName == head })
}

// hasLiveEntry reports whether r's agent is a real, live Claude Code
// registry entry: Type "cc" with a pid. Build's owner-fallback rows
// (ownerFallbackAgent, used for inbox_dead / ambiguous session rows) have
// Type "cc" but PID 0 — the conversation is known only from owner
// resolution, no live entry stands behind the row — and proxy rows have
// another Type; neither may decide the label tier.
func hasLiveEntry(r PeerRecord) bool {
	return r.Agent != nil && r.Agent.Type == "cc" && r.Agent.PID != 0
}

// resolveTier finds all records matching predicate and applies the
// exactly-one/several/none decision for a single tier.
func resolveTier(records []PeerRecord, session string, match func(PeerRecord) bool) (PeerRecord, error) {
	var candidates []PeerRecord
	for _, r := range records {
		if match(r) {
			candidates = append(candidates, r)
		}
	}
	switch len(candidates) {
	case 0:
		return PeerRecord{}, ErrNotFound
	case 1:
		return candidates[0], nil
	default:
		return PeerRecord{}, &AmbiguousError{Session: session, Candidates: candidates}
	}
}

// SplitAddress splits "<host>/<session>" into host and session. ok is
// false when there is no '/', either side is empty, or session contains
// another '/'.
func SplitAddress(addr string) (host, session string, ok bool) {
	idx := strings.Index(addr, "/")
	if idx < 0 {
		return "", "", false
	}
	host = addr[:idx]
	session = addr[idx+1:]
	if host == "" || session == "" {
		return "", "", false
	}
	if strings.Contains(session, "/") {
		return "", "", false
	}
	return host, session, true
}

// HostMatches reports whether want equals alias or hostID,
// case-insensitively.
func HostMatches(want, alias, hostID string) bool {
	return strings.EqualFold(want, alias) || strings.EqualFold(want, hostID)
}
