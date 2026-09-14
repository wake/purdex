package peers

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned by Resolve when no record matches session at any
// tier.
var ErrNotFound = errors.New("peer not found")

// ErrResolveNotReady is returned by Resolve when the label tier found no
// match and the caller passed partial=true: the inventory that produced
// records may be missing the very label that would have matched, so a
// bare tmux-name fallback (tier 2) would risk landing on the wrong
// session. The caller retries, or addresses the session explicitly as
// "tmux:<name>" to bypass the label tier altogether.
var ErrResolveNotReady = errors.New("inventory partial: the label may be missing")

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
//     (proxy rows and rows with Label == "" excluded; a typed suffix is
//     ignored — it is display-only). If tier 1 finds no match and
//     partial is true, ErrResolveNotReady (the inventory may be missing
//     the label that would have matched). If partial is false and the
//     head contains no ':', tier 2: the bare string as a tmux session
//     name.
//
// The first tier (or form) with >=1 match decides: exactly one match =>
// that record; several => *AmbiguousError (never falls through to a
// lower tier). No tier matches => ErrNotFound.
func Resolve(records []PeerRecord, session string, partial bool) (PeerRecord, error) {
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
	// Tier 1: label, over every row; the typed suffix is ignored.
	rec, err := resolveTier(records, session, func(r PeerRecord) bool {
		return r.Label != "" && r.Label == head && (r.Agent == nil || r.Agent.Type != "proxy")
	})
	if !errors.Is(err, ErrNotFound) {
		return rec, err
	}
	if partial {
		return PeerRecord{}, ErrResolveNotReady
	}
	// Tier 2: bare tmux session name, complete inventory only.
	if rest != "" {
		return PeerRecord{}, ErrNotFound
	}
	return resolveTier(records, session, func(r PeerRecord) bool { return r.SessionName == head })
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
