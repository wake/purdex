package peers

import (
	"errors"
	"testing"
)

// relayedRow is a live row that took over from older refs (newest first).
func relayedRow(ref, name string, pid int, previous ...string) PeerRecord {
	r := liveRow(ref, name, "", "", pid)
	r.PreviousRefs = previous
	return r
}

// Spec §8.4 / §15 "Relay": Resolve finds an old ref in exactly one row's
// previous_refs, and a live ref wins over previous_refs.
func TestResolve_PreviousRefsTier(t *testing.T) {
	recs := []PeerRecord{
		relayedRow(refB, "purdex-b0", 2, refA, refC), // relayed twice: A then C are its past
		liveRow("_other1", "someone", "", "", 3),
	}
	for _, in := range []string{refA, refA[1:], refC, "purdex-b0 [" + refA[1:] + "]"} {
		rec, err := Resolve(recs, in, ResolveSnapshot{})
		if err != nil || rec.Agent.PID != 2 {
			t.Fatalf("Resolve(%q): pid=%d err=%v; the row that relayed from it must answer", in, pidOf(rec), err)
		}
	}
	// A LIVE row carrying refA wins over the row that merely relayed from it.
	withLive := append([]PeerRecord{liveRow(refA, "fresh", "", "", 9)}, recs...)
	rec, err := Resolve(withLive, refA, ResolveSnapshot{})
	if err != nil || rec.Agent.PID != 9 {
		t.Fatalf("live ref must win: pid=%d err=%v", pidOf(rec), err)
	}
	// Two rows listing the same old ref: ambiguous, never a guess.
	two := append([]PeerRecord{relayedRow("_dup001", "dup", 7, refA)}, recs...)
	var amb *AmbiguousError
	if _, err := Resolve(two, refA, ResolveSnapshot{}); !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("two rows with the same previous ref: err=%v", err)
	}
	// The combined form still checks the name: an old ref with the wrong name is a mismatch.
	if _, err := Resolve(recs, "wrong-name ["+refA[1:]+"]", ResolveSnapshot{}); !errors.Is(err, ErrNameMismatch) {
		t.Fatalf("old ref + wrong name: err=%v, want ErrNameMismatch", err)
	}
	// Mutation gate (spec §15): a row that carries no previous_refs leaves the old ref at peer_not_found.
	bare := []PeerRecord{liveRow(refB, "purdex-b0", "", "", 2)}
	if _, err := Resolve(bare, refA, ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("without previous_refs the old ref must be not found: err=%v", err)
	}
	// An inert row (owner fallback, no live entry) never answers through its lineage either.
	dead := inboxDeadRow(refB, "", "mt0")
	dead.PreviousRefs = []string{refA}
	if _, err := Resolve([]PeerRecord{dead}, refA, ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a dead holder's lineage must not resolve: err=%v", err)
	}
	// Partial snapshot: a lineage miss is not-ready, like a bare ref miss.
	if _, err := Resolve(bare, refA, ResolveSnapshot{Partial: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("lineage miss under Partial: err=%v", err)
	}
}

func pidOf(r PeerRecord) int {
	if r.Agent == nil {
		return 0
	}
	return r.Agent.PID
}

// Build copies the lineage onto the row that carries the session id, and a
// row without one has no previous_refs key on the wire.
func TestBuild_AttachesPreviousRefsBySessionID(t *testing.T) {
	in := BuildInput{
		HostID: "h", Alias: "mlab",
		Sessions: []SessionSummary{{Code: "c1", Name: "mt1"}, {Code: "c2", Name: "mt2"}},
		Owners: map[string]Owner{
			"c1": {AgentType: "cc", SessionID: "sid-new"},
			"c2": {AgentType: "cc", SessionID: "sid-plain"},
		},
		Entries: []Entry{
			{PID: 1, SessionID: "sid-new", Name: "purdex-b0", Inbox: "/tmp/1.sock"},
			{PID: 2, SessionID: "sid-plain", Name: "other", Inbox: "/tmp/2.sock"},
		},
		PreviousRefs: map[string][]string{"sid-new": {refA, refC}},
	}
	recs := Build(in)
	var got, plain PeerRecord
	for _, r := range recs {
		switch r.SessionCode {
		case "c1":
			got = r
		case "c2":
			plain = r
		}
	}
	if len(got.PreviousRefs) != 2 || got.PreviousRefs[0] != refA {
		t.Fatalf("c1 previous_refs = %v", got.PreviousRefs)
	}
	if plain.PreviousRefs != nil {
		t.Fatalf("c2 previous_refs = %v, want none", plain.PreviousRefs)
	}
	in.PreviousRefs["sid-new"][0] = "_mutate"
	if got.PreviousRefs[0] != refA {
		t.Fatal("Build must copy the slice, not alias the caller's map")
	}
}
