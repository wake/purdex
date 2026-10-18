package peers

import (
	"errors"
	"testing"
)

// Name pool v2 (spec §6.3, plan NP-3, codex #4): a renamed session keeps its old name as an alias. Resolve gets the aliases as
// data (ResolveSnapshot.Aliases: full alias name -> ref) and reaches the session by the existing ref path.

func aliases(m map[string]string) ResolveSnapshot { return ResolveSnapshot{Aliases: m} }

func TestResolve_AnAliasReachesTheRenamedSession(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-hiro", "", "", 1), liveRow(refB, "purdex-wei", "", "", 2)}
	rec, err := Resolve(recs, "purdex-adam", aliases(map[string]string{"purdex-adam": refA}))
	if err != nil || rec.Agent.PID != 1 {
		t.Fatalf("pid=%d err=%v; the alias must reach the row that owns its ref", pidOf(rec), err)
	}
	// without the alias the old name is gone
	if _, err := Resolve(recs, "purdex-adam", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no alias: %v, want ErrNotFound", err)
	}
}

// Tier 1 always wins: a live session that holds the name beats an alias of it.
func TestResolve_ALiveHolderOfTheNameBeatsItsAlias(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-hiro", "", "", 1), liveRow(refB, "purdex-adam", "", "", 2)}
	rec, err := Resolve(recs, "purdex-adam", aliases(map[string]string{"purdex-adam": refA}))
	if err != nil || rec.Agent.PID != 2 {
		t.Fatalf("pid=%d err=%v; the live holder of the name must win", pidOf(rec), err)
	}
}

// An alias of a relayed session reaches the successor, by the lineage tier of the ref path.
func TestResolve_AnAliasOfARelayedSessionReachesTheSuccessor(t *testing.T) {
	recs := []PeerRecord{relayedRow(refB, "purdex-hiro", 2, refA)}
	rec, err := Resolve(recs, "purdex-adam", aliases(map[string]string{"purdex-adam": refA}))
	if err != nil || rec.Agent.PID != 2 {
		t.Fatalf("pid=%d err=%v; the successor that relayed from the alias's ref must answer", pidOf(rec), err)
	}
}

// An alias whose session is gone falls through like any unknown name.
func TestResolve_AnAliasWithNoLiveSessionIsNotFound(t *testing.T) {
	recs := []PeerRecord{liveRow(refB, "purdex-wei", "", "", 2)}
	if _, err := Resolve(recs, "purdex-adam", aliases(map[string]string{"purdex-adam": refA})); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// The ref path's conservatisms apply to an alias hit: an undecodable registry file may hide a second process.
func TestResolve_AnAliasHitKeepsTheRefPathConservatisms(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-hiro", "", "", 1)}
	snap := ResolveSnapshot{Aliases: map[string]string{"purdex-adam": refA}, RegistryIncomplete: true}
	if _, err := Resolve(recs, "purdex-adam", snap); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("got %v, want ErrResolveNotReady", err)
	}
	snap = ResolveSnapshot{Aliases: map[string]string{"purdex-adam": refB}, Partial: true}
	if _, err := Resolve(recs, "purdex-adam", snap); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("a miss on a partial inventory: got %v, want ErrResolveNotReady", err)
	}
}

// Two rows sharing the alias's ref are an ambiguity, not a guess.
func TestResolve_AnAliasWhoseRefTwoRowsShareIsAmbiguous(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-hiro", "", "", 1), liveRow(refA, "purdex-wei", "", "", 2)}
	var amb *AmbiguousError
	if _, err := Resolve(recs, "purdex-adam", aliases(map[string]string{"purdex-adam": refA})); !errors.As(err, &amb) {
		t.Fatalf("got %v, want an AmbiguousError", err)
	}
}

// The bracket check accepts a name that is the session's current name or one of its aliases (spec §6.3).
func TestResolve_TheBracketCheckAcceptsAnAlias(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-hiro", "", "", 1), liveRow(refB, "purdex-wei", "", "", 2)}
	snap := aliases(map[string]string{"purdex-adam": refA})
	for _, in := range []string{"purdex-adam [" + refA[1:] + "]", "purdex-adam [" + refA + "]", "purdex-hiro [" + refA[1:] + "]"} {
		rec, err := Resolve(recs, in, snap)
		if err != nil || rec.Agent.PID != 1 {
			t.Fatalf("Resolve(%q): pid=%d err=%v", in, pidOf(rec), err)
		}
	}
	// an alias of A beside B's ref is a mismatch, never a delivery to B
	if _, err := Resolve(recs, "purdex-adam ["+refB[1:]+"]", snap); !errors.Is(err, ErrNameMismatch) {
		t.Fatalf("alias beside another session's ref: got %v, want ErrNameMismatch", err)
	}
	// and without the alias row the old name stays a mismatch
	if _, err := Resolve(recs, "purdex-adam ["+refA[1:]+"]", ResolveSnapshot{}); !errors.Is(err, ErrNameMismatch) {
		t.Fatalf("no alias: got %v, want ErrNameMismatch", err)
	}
}

// The combined form through the lineage: the alias belongs to the old ref, which the successor relayed from.
func TestResolve_TheBracketCheckAcceptsAnAliasThroughTheLineage(t *testing.T) {
	recs := []PeerRecord{relayedRow(refB, "purdex-hiro", 2, refA)}
	rec, err := Resolve(recs, "purdex-adam ["+refA[1:]+"]", aliases(map[string]string{"purdex-adam": refA}))
	if err != nil || rec.Agent.PID != 2 {
		t.Fatalf("pid=%d err=%v", pidOf(rec), err)
	}
}

// A relayed successor is another session than the alias's: the owner check does not apply to the lineage match.
func TestResolve_TheBracketAliasThroughTheLineageIgnoresTheAliasOwner(t *testing.T) {
	succ := relayedRow(refB, "purdex-hiro", 2, refA)
	succ.Agent.SessionID = "successor-sid"
	snap := ResolveSnapshot{Aliases: map[string]string{"purdex-adam": refA}, AliasSessions: map[string]string{"purdex-adam": "old-sid"}}
	rec, err := Resolve([]PeerRecord{succ}, "purdex-adam ["+refA[1:]+"]", snap)
	if err != nil || rec.Agent.PID != 2 {
		t.Fatalf("pid=%d err=%v; the successor must answer although it is not the alias's own session", pidOf(rec), err)
	}
}

// An alias that is not routable can never have produced an address: it must not resolve.
func TestResolve_AnUnroutableAliasNeverResolves(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-hiro", "", "", 1)}
	if _, err := Resolve(recs, "x", aliases(map[string]string{"x": refA})); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// A ref-shaped alias would shadow tiers 2/3: the alias lookup must not apply to it.
func TestResolve_AnAliasNeverShadowsARef(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-hiro", "", "", 1), liveRow(refB, "purdex-wei", "", "", 2)}
	rec, err := Resolve(recs, refB, aliases(map[string]string{refB: refA}))
	if err != nil || rec.Agent.PID != 2 {
		t.Fatalf("pid=%d err=%v; a ref resolves as a ref", pidOf(rec), err)
	}
}

// Two live rows share a ref (a 36^6 collision): an alias that belonged to one session selects that row in the combined form,
// instead of making both pass or the pair ambiguous (codex attack on #3435).
func TestResolve_TheBracketAliasPicksTheRowOfItsOwnSession(t *testing.T) {
	a := liveRow(refA, "purdex-hiro", "", "", 1)
	a.Agent.SessionID = "aaaa-sid"
	b := liveRow(refA, "purdex-wei", "", "", 2)
	b.Agent.SessionID = "bbbb-sid"
	snap := ResolveSnapshot{Aliases: map[string]string{"purdex-adam": refA}, AliasSessions: map[string]string{"purdex-adam": "BBBB-sid"}}
	rec, err := Resolve([]PeerRecord{a, b}, "purdex-adam ["+refA[1:]+"]", snap)
	if err != nil || rec.Agent.PID != 2 {
		t.Fatalf("pid=%d err=%v; the row of the alias's own session must answer", pidOf(rec), err)
	}
	// without the session mapping the pair stays ambiguous: fail closed, never a guess
	var amb *AmbiguousError
	snap.AliasSessions = nil
	if _, err := Resolve([]PeerRecord{a, b}, "purdex-adam ["+refA[1:]+"]", snap); !errors.As(err, &amb) {
		t.Fatalf("no session mapping: got %v, want an AmbiguousError", err)
	}
}

// A known alias is authoritative: when its session is gone, the answer is "not found", not a bare tmux session of the same
// text (codex critic on #3435).
func TestResolve_AKnownAliasWithNoLiveSessionNeverFallsThroughToATmuxName(t *testing.T) {
	other := liveRow(refB, "purdex-wei", "", "purdex-adam", 2) // a tmux session that happens to be called like the alias
	if _, err := Resolve([]PeerRecord{other}, "purdex-adam", aliases(map[string]string{"purdex-adam": refA})); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	// with no alias row the same text is still a bare tmux session name (tier 4), as before
	rec, err := Resolve([]PeerRecord{other}, "purdex-adam", ResolveSnapshot{})
	if err != nil || rec.Agent.PID != 2 {
		t.Fatalf("no alias: pid=%d err=%v; tier 4 is unchanged", pidOf(rec), err)
	}
}
