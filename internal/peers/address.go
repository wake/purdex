package peers

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned by Resolve when no record matches session at any
// tier.
var ErrNotFound = errors.New("peer not found")

// AmbiguousError is returned by Resolve when a tier matches more than one
// record; Candidates holds only the matches from the tier that decided.
type AmbiguousError struct {
	Session    string
	Candidates []PeerRecord
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("peer address %q is ambiguous (%d candidates)", e.Session, len(e.Candidates))
}

// Resolve implements spec §4.1 for ONE host's records; host has already
// been matched by the caller. Tiers, in order: tmux session name; session
// code; "cc:<peer_name>". The first tier with >=1 match decides: exactly
// one match => that record; several => *AmbiguousError (never falls
// through to a lower tier). No tier matches => ErrNotFound.
func Resolve(records []PeerRecord, session string) (PeerRecord, error) {
	if session == "" {
		return PeerRecord{}, ErrNotFound
	}

	if peerName, ok := strings.CutPrefix(session, "cc:"); ok {
		return resolveTier(records, session, func(r PeerRecord) bool {
			return r.Agent != nil && r.Agent.PeerName == peerName && r.Agent.Type != "proxy"
		})
	}

	if rec, err := resolveTier(records, session, func(r PeerRecord) bool {
		return r.SessionName == session
	}); !errors.Is(err, ErrNotFound) {
		return rec, err
	}

	return resolveTier(records, session, func(r PeerRecord) bool {
		return r.SessionCode == session
	})
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
