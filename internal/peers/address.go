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
	// run, the title store could not be read, the registry is incomplete,
	// or the budget ran out before the tmux-generation re-check. A tier-1 miss on a Partial snapshot is ErrResolveNotReady
	// rather than a tier-2 fallback.
	Partial bool
	// RegistryIncomplete is true when the envelope named at least one
	// alive-but-undecodable registry file (len(UnknownRegistryFiles) > 0).
	// It is the one Partial cause that can hide a whole live PROCESS, so
	// even a single tier-1 hit is ErrResolveNotReady under it: the hidden
	// process may belong to the same conversation and make the name
	// ambiguous. A caller setting RegistryIncomplete should set Partial
	// too; Resolve does not require it.
	RegistryIncomplete bool
}

// ErrLegacyCC is returned (wrapped under ErrNotFound) by Resolve for the
// v1 "cc:<name>" address form, which Peer Address v2 retires.
var ErrLegacyCC = errors.New("cc: addresses were removed; run pdx peers --all to see the new addresses")

// ErrRemoteTooOld is returned (wrapped under ErrNotFound, like ErrLegacyCC)
// by Resolve when tier 1 misses over a batch of rows that came from a
// daemon predating Peer Address v4 — see hasStaleVersionRows for the signal.
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
var ErrRemoteTooOld = errors.New("the peer host's daemon predates Peer Address v4 and reports no refs; upgrade and restart pdx there, or address a session as tmux:<name>")

// ErrNameMismatch is returned for the combined form "<name> [<ref>]" when the
// ref resolves to a row whose name is not the one typed.
//
// It refuses rather than delivering-with-a-warning because the name in a
// combined address is the reader's only check on the ref beside it.
// "trusted-name [attackerRef]" is precisely the string worth getting pasted,
// and a warning arrives after the message has gone. Legitimate drift has an
// explicit escape hatch: "<host>/_<ref>" asks for the ref outright.
var ErrNameMismatch = errors.New("the typed name does not match the ref's current name")

// AmbiguousError is returned by Resolve when a tier matches more than one
// record; Candidates holds only the matches from the tier that decided.
type AmbiguousError struct {
	Session    string
	Candidates []PeerRecord
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("peer address %q is ambiguous (%d candidates)", e.Session, len(e.Candidates))
}

// Resolve implements Peer Address v4 spec §5.4 for ONE host's records; host
// has already been matched by the caller. The forms, in the order they are
// decided:
//
//   - "cc:<anything>": the retired v1 form, always ErrNotFound wrapping
//     ErrLegacyCC, regardless of the snapshot.
//   - "tmux:<name>": the explicit tmux form, bypassing every tier and every
//     completeness rule; matches PeerRecord.SessionName == name. "tmux:" with
//     an empty name is ErrNotFound.
//   - the stale-version gate (see below), which decides nothing but can
//     refuse everything under it.
//   - "<name> [<ref>]": the combined form the peers table prints. The typed
//     name must be routable and both halves must match the SAME row; failing
//     that the ref is asked on its own, only to say which half went wrong. A
//     mismatch is ErrNameMismatch, never a delivery.
//   - tier 1, "<name>": Agent.PeerName == head, over rows carrying a LIVE cc
//     entry whose name is routable.
//   - tiers 2 and 3, "_<ref>" and the same ref without its underscore:
//     PeerRecord.Ref == head over those same live rows.
//   - tier 4, "<name>" read as a bare tmux session name: SessionName == head,
//     over a complete inventory only.
//
// Every tier decides only for a session carrying no ':' at all. v4 has no
// suffix form, so "<head>:<anything>" is not an address; matching it on its
// head alone would keep every retired v3 address routable.
//
// Tiers 1 to 3 see only rows whose Agent is a real registry entry (Type "cc",
// PID != 0). Proxy rows and owner-fallback rows (inbox_dead / ambiguous:
// Agent.PID == 0, no entry behind them) are excluded — spec §3.3 makes a row
// whose holder is not live inert, neither resolving nor blocking, and this is
// how that rule reaches resolution.
//
// The first form or tier with >=1 match decides: exactly one match => that
// record (subject to the RegistryIncomplete rule below); several =>
// *AmbiguousError, which never falls through to a lower tier. Nothing matches
// => ErrNotFound.
//
// Tiers 1 and 2/3 cannot both match one row, and that disjointness is CREATED
// rather than observed: RoutableName forbids a ref-shaped name (spec §5.2), so
// no ordering rule between them is needed. Tier 1 is restricted to routable
// names for the same reason read backwards — an unroutable name could never
// have produced the address being typed, so it must not win a tier.
//
// The stale-version gate sits ABOVE every tier rather than in front of the
// fallback, which is where v3 put it. In v3 what lay below the gate was a
// tmux-name guess; in v4 the tier immediately below it would SUCCEED. A row
// from a daemon that predates v4 still carries a usable registry name in
// agent.peer_name, so a bare name would resolve against a row whose ref the
// sender could never have verified. One stale row condemns the batch, because
// Resolve is called per host and every row in it came from the same daemon.
// "tmux:<name>" is decided above the gate: it names a place outright, an old
// daemon reports SessionName exactly as a current one does, and it is the
// escape hatch the refusal points the caller at.
//
// PeerRecord.Title is matched by NOTHING here, and that is D3: a title is a
// self-declared display name, read to CHOOSE a peer, never used to reach one.
// Two conversations may hold one title (D5) precisely because no routing
// decision rests on it. A bare title therefore misses every tier and comes
// back ErrNotFound.
//
// Three conservatisms are deliberate, so that none reads as an oversight:
//
//   - A ref miss under snap.Partial is ErrResolveNotReady even though a ref is
//     derived from the sessionId alone — it depends on neither the title store
//     nor owner resolution, so a partial inventory can never be the reason it
//     missed. It is retried anyway because that is the safe direction: a retry
//     costs one round trip, a false "not found" costs a message. Splitting
//     Partial by cause is out of scope (spec §5.4).
//   - The combined form carries those same rules: RegistryIncomplete refuses
//     its single hit exactly as it refuses a bare ref's, and a miss falls
//     through resolveRefHead, which applies Partial. It cannot quietly
//     acquire a weaker rule set than the address it contains.
//   - "tmux:<name>" and tier 4 match SessionName without comparing
//     TmuxInstance, so with two tmux servers holding same-named sessions they
//     resolve the one the daemon can see, which may not be the one the
//     operator meant. Pre-existing, out of scope (spec §5.4).
func Resolve(records []PeerRecord, session string, snap ResolveSnapshot) (PeerRecord, error) {
	if session == "" {
		return PeerRecord{}, ErrNotFound
	}
	head, rest := SplitSession(session)
	// The two explicit forms are the PREFIXED ones, and the colon is what
	// makes them explicit — matching on the head alone would swallow a bare
	// "cc" or "tmux".
	//
	// That is reachable, not theoretical: RoutableName accepts both (it only
	// requires two or more characters of [a-z0-9-]), so a conversation whose
	// registry name is exactly "cc" gets the perfectly ordinary address
	// "<host>/cc" — which the head-only switch then answered with "cc:
	// addresses were removed". An address this daemon mints and prints must
	// be one it can also resolve.
	if strings.ContainsRune(session, ':') {
		switch head {
		case LabelReservedCC:
			return PeerRecord{}, fmt.Errorf("%w: %w", ErrNotFound, ErrLegacyCC)
		case LabelReservedTmux:
			if rest == "" {
				return PeerRecord{}, ErrNotFound
			}
			return resolveTier(records, session, func(r PeerRecord) bool { return r.SessionName == rest })
		}
	}
	// The stale-version gate, ABOVE every tier — not in front of the
	// fallback, where v3 had it. A pre-v4 row still carries a usable registry
	// name, so tier 1 would SUCCEED against it and deliver to a conversation
	// whose ref the sender could never have verified. Refuse by name: "retry,
	// the inventory is partial" is advice that can never come true against an
	// old daemon, and a wrong diagnosis costs the operator the time they spend
	// following it.
	if hasStaleVersionRows(records) {
		return PeerRecord{}, fmt.Errorf("%w: %w", ErrNotFound, ErrRemoteTooOld)
	}

	// The combined form "<name> [<ref>]", which the peers table prints and an
	// operator pastes verbatim. The ref decides where this goes; the name is
	// the reader's check on it, so a mismatch refuses rather than delivers.
	if typedName, typedRef, ok := splitCombined(session); ok {
		// The typed name must clear the same grammar tier 1 applies. Without
		// this the combined form is a way around RoutableName: §5.2 says an
		// unroutable registry name is displayed but never becomes an address,
		// and a bare-name send honours that — but comparing the typed name
		// against the row's without asking either to be routable made
		// "<unroutable name> [<its ref>]" deliver.
		if !RoutableName(typedName) {
			return PeerRecord{}, fmt.Errorf("%w: %q cannot be an address head", ErrNotFound, typedName)
		}
		ref := typedRef
		if !strings.HasPrefix(ref, "_") {
			ref = "_" + ref
		}
		if !IsRef(ref) {
			return PeerRecord{}, ErrNotFound
		}

		// Match the ref and the name TOGETHER rather than resolving the ref
		// and then checking the name. Two live rows may share a ref (spec
		// §3.1's P3 residual, which promises they "remain reachable by
		// name"), and the typed name is what tells them apart — resolving the
		// ref alone called the pasted table address ambiguous when it named
		// exactly one row.
		//
		// The name is still a check ON the ref, not a second way in past it:
		// a name carried by no row with this ref is refused below, never
		// delivered.
		rec, err := resolveTier(records, session, func(r PeerRecord) bool {
			return hasLiveEntry(r) && RoutableName(r.Agent.PeerName) &&
				r.Ref == ref && r.Agent.PeerName == typedName
		})
		switch {
		case err == nil:
			if snap.RegistryIncomplete {
				// The same rule the bare ref gets: an undecodable registry
				// file may be a second live process of this conversation,
				// which would have made even this pair ambiguous.
				return PeerRecord{}, ErrResolveNotReady
			}
			return rec, nil
		case !errors.Is(err, ErrNotFound):
			// Same name AND same ref on two rows: genuinely unresolvable.
			return PeerRecord{}, err
		}

		// Nothing matched both halves. Ask the ref on its own, because the two
		// ways this can happen need different actions from the reader: a
		// drifted name means re-read the address, an absent ref means the
		// conversation is gone. resolveRefHead is also what applies the
		// snapshot conservatisms to a miss.
		byRef, refErr := resolveRefHead(records, ref, snap)
		var amb *AmbiguousError
		switch {
		case refErr == nil:
			return PeerRecord{}, fmt.Errorf("%w: typed %q, but %s is now %q",
				ErrNameMismatch, typedName, ref, byRef.Agent.PeerName)
		case errors.As(refErr, &amb):
			// The ref is ambiguous and none of its rows carries the typed
			// name — a mismatch, not an ambiguity: no candidate was ever in
			// the running.
			return PeerRecord{}, fmt.Errorf("%w: typed %q, but %s names no such conversation",
				ErrNameMismatch, typedName, ref)
		default:
			// ErrNotFound or ErrResolveNotReady, both already the right answer.
			return PeerRecord{}, refErr
		}
	}

	// Tiers 1 to 4 all require a session carrying no ':' at all: v4 deleted
	// the "<head>:<suffix>" form outright (spec §5.3 removes the field it
	// printed), so a string with a ':' in it is not an address and must not
	// be matched on its head alone. v3's tiers 2/3 said so; tier 1 did not,
	// which left every retired "<name>:<suffix>" in a scrollback still
	// delivering to <name> — and the suffix was the very part that said WHICH
	// conversation, so the half still honoured was the half that can drift.
	//
	// The test is the ':' itself rather than SplitSession's rest, because
	// "<head>:" splits to an empty rest and would otherwise slip through the
	// gate the other suffixed forms are stopped by. Only "cc:" and
	// "tmux:<name>", decided above, ever carry a ':' legitimately.
	if !strings.Contains(session, ":") {
		// Tier 1: the registry name, over live rows whose name is routable.
		// The RoutableName guard is not decoration — an unroutable name must
		// not win a tier, because it could never have produced the address
		// being typed.
		rec, err := resolveTier(records, session, func(r PeerRecord) bool {
			return hasLiveEntry(r) && RoutableName(r.Agent.PeerName) && r.Agent.PeerName == head
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

		// Tiers 2/3: the ref, with or without its underscore. No ordering
		// rule is needed against tier 1: RoutableName forbids a ref-shaped
		// name, so no row can match both.
		rec, err = resolveRefHead(records, head, snap)
		if !errors.Is(err, ErrNotFound) {
			return rec, err
		}
	}

	if snap.Partial {
		return PeerRecord{}, ErrResolveNotReady
	}
	// Tier 4: bare tmux session name, complete inventory only.
	if strings.Contains(session, ":") {
		return PeerRecord{}, ErrNotFound
	}
	return resolveTier(records, session, func(r PeerRecord) bool { return r.SessionName == head })
}

// splitCombined splits "<name> [<ref>]" into its parts. ok is false for
// anything else, including a nested or doubled bracket group — those are not
// a form this accepts, and treating them as one would let a crafted string
// choose which half gets checked.
func splitCombined(s string) (name, ref string, ok bool) {
	if !strings.HasSuffix(s, "]") {
		return "", "", false
	}
	i := strings.IndexByte(s, '[')
	if i <= 0 {
		return "", "", false
	}
	inner := s[i+1 : len(s)-1]
	if strings.ContainsAny(inner, "[]") {
		return "", "", false
	}
	name = strings.TrimSpace(s[:i])
	if name == "" || inner == "" {
		return "", "", false
	}
	return name, inner, true
}

// resolveRefHead resolves a ref with or without its leading underscore,
// applying every conservatism the bare-ref tier applies.
//
// The combined form calls it too, but only after its own name+ref match has
// missed — there to diagnose which half was wrong, and to apply Partial to the
// miss. It does not decide that form, because a combined address names one row
// out of the several a shared ref may reach.
func resolveRefHead(records []PeerRecord, ref string, snap ResolveSnapshot) (PeerRecord, error) {
	if !strings.HasPrefix(ref, "_") {
		ref = "_" + ref
	}
	if !IsRef(ref) {
		return PeerRecord{}, ErrNotFound
	}
	rec, err := resolveTier(records, ref, func(r PeerRecord) bool {
		return hasLiveEntry(r) && r.Ref == ref
	})
	if err == nil && snap.RegistryIncomplete {
		return PeerRecord{}, ErrResolveNotReady
	}
	if errors.Is(err, ErrNotFound) && snap.Partial {
		return PeerRecord{}, ErrResolveNotReady
	}
	return rec, err
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

// hasStaleVersionRows reports whether records came from a daemon predating
// Peer Address v4 — the condition behind ErrRemoteTooOld.
//
// v4 gets the signal for free: the JSON key moved from "canonical" to "ref"
// (spec §5.3), so a v3 daemon's rows decode with Ref == "" whatever they
// actually sent. No version string is needed, and none is trusted: the rows
// say it themselves.
//
// The hasLiveEntry conjunction is load-bearing, not caution. A CURRENT daemon
// also emits rows with an empty Ref — owner-fallback rows (inbox_dead /
// ambiguous, PID 0), proxy rows and agent:null rows — and counting those would
// declare every v4 host obsolete.
//
// Refusing matters more here than it did in v3, where what lay below was a
// tmux-name guess. A v3 row still carries a usable registry name in
// agent.peer_name, so v4's name tier would SUCCEED against it, delivering to a
// row whose ref the sender could never have verified.
func hasStaleVersionRows(records []PeerRecord) bool {
	for _, r := range records {
		if hasLiveEntry(r) && r.Ref == "" {
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
