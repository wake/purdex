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
// vouch for tier 1's answer: tier 1 found no match and the snapshot is
// Partial (the inventory may be missing the very row that would have
// matched, so a bare tmux-name fallback — tier 2 — would risk landing on
// the wrong session), or it found exactly one match while the snapshot's
// registry is incomplete (the unreadable file may hide a second live
// process of that same conversation, which would have made the address
// ambiguous). The caller retries, or addresses the session explicitly as
// "tmux:<name>" to bypass tier 1 altogether.
var ErrResolveNotReady = errors.New("inventory partial: the peer may be missing")

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

// ErrRemoteTooOld is returned (wrapped under ErrNotFound, like ErrLegacyCC)
// by Resolve when tier 1 misses over a batch of rows that came from a
// daemon predating Peer Address v3 — see hasPreV3Rows for the signal.
//
// It exists because the upgrade is not atomic: one host runs v3 while the
// other still runs v2, and the v2 host keeps PRINTING a label as its
// address head. An operator reads that head off the old daemon and types
// it here, tier 1 cannot match it (a v2 row carries no canonical), and
// without this the resolver dropped into tier 2 and tried the very same
// string as a tmux session NAME. Where the remote happened to have a tmux
// session of that name, the message was delivered — to a different
// conversation than the one the operator read the head from.
//
// Refusing is the whole posture of v3: an address is resolved, never
// guessed at. A version mismatch is a fact the batch itself reveals, so it
// is named rather than silently absorbed into a fallback.
var ErrRemoteTooOld = errors.New("the peer host's daemon predates Peer Address v3 and reports no canonical ids; upgrade and restart pdx there, or address a session as tmux:<name>")

// AmbiguousError is returned by Resolve when a tier matches more than one
// record; Candidates holds only the matches from the tier that decided.
type AmbiguousError struct {
	Session    string
	Candidates []PeerRecord
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("peer address %q is ambiguous (%d candidates)", e.Session, len(e.Candidates))
}

// Resolve implements Peer Address v3 spec §5.1 for ONE host's records;
// host has already been matched by the caller. Forms and tiers, in order:
//
//   - "cc:<anything>": the retired v1 form, always ErrNotFound wrapping
//     ErrLegacyCC, regardless of partial.
//   - "tmux:<name>": explicit tmux form, bypassing tier 1 entirely;
//     matches PeerRecord.SessionName == name. "tmux:" with an empty name
//     is ErrNotFound.
//   - "<canonical>[:<suffix>]": tier 1, the canonical id — the
//     sessionId-derived head PeerRecord.Canonical carries — matched over
//     every row that carries a LIVE cc entry, session rows and entry rows
//     alike, but only those whose Agent is a real registry entry (Type
//     "cc", PID != 0). Proxy rows and owner-fallback rows (inbox_dead /
//     ambiguous: Agent.PID == 0, no entry behind them) are excluded —
//     spec §3.3 makes a row whose holder is not live inert, neither
//     resolving nor blocking, and that is how the rule reaches
//     resolution. A typed suffix is ignored (display-only). Several
//     matches => *AmbiguousError regardless of the snapshot. Exactly one
//     match and snap.RegistryIncomplete => ErrResolveNotReady (an
//     unreadable registry file may hide a second process of that
//     conversation). No match and snap.Partial => ErrResolveNotReady. No
//     match, not Partial and the head contains no ':' => tier 2: the bare
//     string as a tmux session name.
//
// The first tier (or form) with >=1 match decides: exactly one match =>
// that record (subject to the RegistryIncomplete rule above); several =>
// *AmbiguousError (never falls through to a lower tier). No tier matches
// => ErrNotFound.
//
// One exception overrides tier 2 and the Partial rule alike: when the
// batch itself shows it came from a pre-v3 daemon (hasPreV3Rows), a
// tier-1 miss is ErrNotFound wrapping ErrRemoteTooOld and nothing else is
// tried. The explicit "tmux:<name>" form is decided above this and is
// unaffected — it names a place outright, a v2 daemon reports SessionName
// exactly as a v3 one does, and it is the escape hatch the refusal points
// the caller at.
//
// PeerRecord.Label is matched by NOTHING here, and that is D3: a label is
// a self-declared display name, read to CHOOSE a peer, never used to
// reach one. Two conversations may hold one label (D5) precisely because
// no routing decision rests on it. A bare label therefore misses tier 1,
// fails tier 2, and comes back ErrNotFound.
//
// Two conservatisms are deliberate, so that neither reads as an oversight:
//
//   - A tier-1 miss under snap.Partial is ErrResolveNotReady even though
//     a canonical id is derived from the sessionId alone — it depends on
//     neither the label store nor owner resolution, so a partial
//     inventory can never be the reason it missed. It is retried anyway
//     because that is the safe direction: a retry costs one round trip, a
//     false "not found" costs a message. Splitting Partial by cause is
//     out of scope (spec §5.1).
//   - "tmux:<name>" and tier 2 match SessionName without comparing
//     TmuxInstance, so with two tmux servers holding same-named sessions
//     they resolve the one the daemon can see, which may not be the one
//     the operator meant. Pre-existing, out of scope (spec §5.1).
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
	// Tier 1: the canonical id, over every row backed by a live entry;
	// the typed suffix is ignored. head != "" carries over the guard v2
	// spelled as `r.Label != ""`: ":suffix" splits to an empty head, and
	// these records may come from a REMOTE host — a v2 daemon that has
	// never heard of `canonical` sends live rows with it empty, and
	// without the guard every one of them would match that head.
	rec, err := resolveTier(records, session, func(r PeerRecord) bool {
		return hasLiveEntry(r) && head != "" && r.Canonical == head
	})
	if err == nil && snap.RegistryIncomplete {
		// One hit, but a registry file for an alive pid could not be
		// decoded: that file may be a second live process of this very
		// conversation, which would have made the address ambiguous. Do
		// not pick one process on incomplete evidence.
		return PeerRecord{}, ErrResolveNotReady
	}
	if !errors.Is(err, ErrNotFound) {
		return rec, err
	}
	// Tier 1 missed. Before anything is guessed at, ask whether this batch
	// could have answered at all: rows from a pre-v3 daemon carry no
	// canonical, so a miss says nothing about the head and everything
	// about the peer's version. Refuse by name. This sits ahead of the
	// Partial check on purpose — "retry, the inventory is partial" is
	// advice that can never come true against a v2 daemon, and a wrong
	// diagnosis costs the operator the time they spend following it.
	if hasPreV3Rows(records) {
		return PeerRecord{}, fmt.Errorf("%w: %w", ErrNotFound, ErrRemoteTooOld)
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
// another Type; neither may decide tier 1.
func hasLiveEntry(r PeerRecord) bool {
	return r.Agent != nil && r.Agent.Type == "cc" && r.Agent.PID != 0
}

// hasPreV3Rows reports whether records were produced by a daemon that
// predates Peer Address v3 — the condition behind ErrRemoteTooOld.
//
// The signal is spec §4.5's invariant read backwards. A v3 daemon gives
// every row with a live cc entry a non-empty Canonical, so one such row
// WITHOUT it can only have come from a daemon that does not know the
// field, whose JSON therefore decodes it as "". No version string is
// needed, and none is trusted: the rows say it themselves.
//
// Only live cc rows count. A row with agent: null, a proxy row and an
// owner-fallback row all carry an empty Canonical by design on a v3
// daemon too, so counting them would declare every v3 host obsolete.
func hasPreV3Rows(records []PeerRecord) bool {
	for _, r := range records {
		if hasLiveEntry(r) && r.Canonical == "" {
			return true
		}
	}
	return false
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
