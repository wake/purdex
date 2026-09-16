package peers

import (
	"errors"
	"testing"
)

// --- helpers -------------------------------------------------------------

// Canonical ids as they come out of CanonicalID: "_" + 8 base36 digits.
// Tier 1 compares them as plain strings, so literals are the real thing.
const (
	canonA = "_3k9f2mq4"
	canonB = "_zq81ab00"
	canonC = "_0000zzzz"
)

// liveRow is the row shape tier 1 decides on: a live cc entry whose
// Canonical is its address head (spec §4.5). The label rides along and is
// deliberately never what tier 1 matches — that is D3.
func liveRow(canonical, label, sessionName string, pid int) PeerRecord {
	source := ""
	if label != "" {
		source = "user"
	}
	return PeerRecord{SessionName: sessionName, Canonical: canonical, Label: label, LabelSource: source,
		Agent: &AgentInfo{Type: "cc", PID: pid}, Deliverable: true}
}

// inboxDeadRow is the owner-fallback session row Build emits when a cc
// owner's conversation has no live registry entry (ownerFallbackAgent: PID
// 0, no inbox). It carries the conversation's canonical id and its
// persisted label, but its holder is not live, so per spec §3.3 it is
// inert — it must neither resolve nor block.
func inboxDeadRow(canonical, label, sessionName string) PeerRecord {
	return PeerRecord{
		RowKind: "session", SessionName: sessionName, Canonical: canonical, Label: label, LabelSource: "user",
		Agent:  &AgentInfo{Type: "cc", SessionID: "dead-sid"},
		Reason: "inbox_dead",
	}
}

// --- Resolve: tiers --------------------------------------------------------

// TestResolve_CanonicalTier pins the v3 tier 1: the canonical id resolves,
// with or without a typed suffix (the suffix is display-only and ignored).
func TestResolve_CanonicalTier(t *testing.T) {
	recs := []PeerRecord{liveRow(canonA, "purdex-dev", "mt0", 1), liveRow(canonB, "", "", 2)}
	for _, c := range []struct {
		in  string
		pid int
	}{
		{canonA, 1}, {canonA + ":whatever-suffix", 1},
		{canonB, 2}, {canonB + ":x", 2},
	} {
		got, err := Resolve(recs, c.in, ResolveSnapshot{})
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got.Agent.PID != c.pid {
			t.Errorf("%q resolved to pid %d, want %d", c.in, got.Agent.PID, c.pid)
		}
	}
}

// TestResolve_LabelDoesNotResolve is the test that pins D3: a label is a
// display name and nothing else. Sending to one must fall through tier 1
// (which now matches Canonical) AND tier 2 (which matches a tmux session
// name) and land on ErrNotFound — not on the row that happens to carry
// that label, and not on an ambiguity verdict either.
//
// Before T5 this same input delivered a message, which is exactly the
// behaviour v3 exists to remove.
func TestResolve_LabelDoesNotResolve(t *testing.T) {
	recs := []PeerRecord{liveRow(canonA, "purdex-tester", "mt0", 1)}
	for _, in := range []string{"purdex-tester", "purdex-tester:mt0-claude"} {
		_, err := Resolve(recs, in, ResolveSnapshot{})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("%q: got %v, want ErrNotFound — a label must never route", in, err)
		}
		var amb *AmbiguousError
		if errors.As(err, &amb) {
			t.Fatalf("%q: got %v, want a plain miss, not an ambiguity verdict", in, err)
		}
	}
	// D5 says two conversations may hold one label. That is harmless
	// precisely because the shared string is not an address: the send
	// still misses, rather than becoming ambiguous.
	recs = append(recs, liveRow(canonB, "purdex-tester", "mt1", 2))
	if _, err := Resolve(recs, "purdex-tester", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate label: got %v, want ErrNotFound", err)
	}
	// And each holder is still reachable at its own canonical.
	for _, c := range []struct {
		in  string
		pid int
	}{{canonA, 1}, {canonB, 2}} {
		got, err := Resolve(recs, c.in, ResolveSnapshot{})
		if err != nil || got.Agent.PID != c.pid {
			t.Fatalf("%q: got %+v %v, want pid %d", c.in, got, err, c.pid)
		}
	}
}

// TestResolve_LabelNoLongerShadowsTmuxName is the same D3 rule seen from
// the other side. Under v2 a label "mt4" shadowed a tmux session named
// "mt4" at tier 1; under v3 tier 1 does not see the label at all, so the
// bare name falls through to tier 2 and lands on the tmux session — the
// place it names. The explicit "tmux:" form agrees, as it always did.
func TestResolve_LabelNoLongerShadowsTmuxName(t *testing.T) {
	recs := []PeerRecord{liveRow(canonA, "mt4", "mt0", 1), liveRow(canonB, "", "mt4", 2)}
	for _, in := range []string{"mt4", "tmux:mt4"} {
		got, err := Resolve(recs, in, ResolveSnapshot{})
		if err != nil || got.Agent.PID != 2 {
			t.Errorf("%q: got %+v %v, want the tmux session mt4 (pid 2)", in, got, err)
		}
	}
	if _, err := Resolve(recs, "tmux:", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("tmux: empty ⇒ %v", err)
	}
}

// TestResolve_CanonicalWinsOverTmuxName pins tier order: tier 1 decides
// before the bare-name fallback is ever consulted.
func TestResolve_CanonicalWinsOverTmuxName(t *testing.T) {
	recs := []PeerRecord{liveRow(canonA, "", "mt0", 1), liveRow(canonB, "", canonA, 2)}
	got, err := Resolve(recs, canonA, ResolveSnapshot{})
	if err != nil || got.Agent.PID != 1 {
		t.Fatalf("got %+v %v, want the canonical holder (pid 1)", got, err)
	}
	got, err = Resolve(recs, "tmux:"+canonA, ResolveSnapshot{})
	if err != nil || got.Agent.PID != 2 {
		t.Fatalf("tmux form: got %+v %v, want the tmux row (pid 2)", got, err)
	}
}

func TestResolve_TmuxFallback_OnlyWhenComplete(t *testing.T) {
	recs := []PeerRecord{{SessionName: "shell"}} // no cc agent, no canonical
	if got, err := Resolve(recs, "shell", ResolveSnapshot{}); err != nil || got.SessionName != "shell" {
		t.Fatalf("complete: %+v %v", got, err)
	}
	if _, err := Resolve(recs, "shell", ResolveSnapshot{Partial: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("partial: got %v, want ErrResolveNotReady", err)
	}
	if _, err := Resolve(recs, "shell:x", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("suffix on a tmux fallback: got %v, want ErrNotFound", err)
	}
}

func TestResolve_CCShortCircuit(t *testing.T) {
	recs := []PeerRecord{liveRow(canonA, "cc", "", 1)} // cannot exist, but the resolver must not care
	for _, snap := range []ResolveSnapshot{{}, {Partial: true}, {Partial: true, RegistryIncomplete: true}} {
		_, err := Resolve(recs, "cc:foo", snap)
		if !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrLegacyCC) {
			t.Errorf("%+v: %v", snap, err)
		}
	}
}

// TestResolve_Ambiguous_SameCanonical pins the backstop spec §4.1 keeps:
// two live rows sharing a canonical — one conversation with two processes,
// or §3.2's forged twin — make the resolver refuse rather than pick one.
func TestResolve_Ambiguous_SameCanonical(t *testing.T) {
	recs := []PeerRecord{liveRow(canonA, "", "mt0", 1), liveRow(canonA, "", "", 2)}
	_, err := Resolve(recs, canonA, ResolveSnapshot{})
	var amb *AmbiguousError
	if !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("got %v", err)
	}
}

// TestResolve_ProxyRowsExcluded pins that a proxy row cannot decide tier 1
// even when it carries the head, so a same-named tmux session is still
// reachable through tier 2.
func TestResolve_ProxyRowsExcluded(t *testing.T) {
	recs := []PeerRecord{
		{Canonical: canonC, Agent: &AgentInfo{Type: "proxy"}},
		{SessionName: canonC}, // tier 2 would match
	}
	got, err := Resolve(recs, canonC, ResolveSnapshot{})
	if err != nil || got.Agent != nil {
		t.Fatalf("got %+v %v, want the tmux row via tier 2", got, err)
	}
}

// TestResolve_EmptyHeadMatchesNothing pins the guard v2 spelled as
// `r.Label != ""`: ":suffix" splits to an empty head, and a row whose
// Canonical is empty must not answer to it. Locally that row cannot
// exist, but these records may come from a REMOTE host — a v2 daemon that
// has never heard of `canonical` sends live rows with it empty, and
// without the guard "b/:x" would reach one of them.
func TestResolve_EmptyHeadMatchesNothing(t *testing.T) {
	v2Row := PeerRecord{SessionName: "mt0", Label: "purdex-tester",
		Agent: &AgentInfo{Type: "cc", PID: 1}, Deliverable: true} // no Canonical
	recs := []PeerRecord{v2Row, liveRow(canonA, "", "mt1", 2)}
	if _, err := Resolve(recs, ":suffix", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// --- Resolve: registry completeness (X1) -----------------------------------

// TestResolve_SingleCanonicalHit_RegistryIncompleteIsNotReady pins the X1
// rule: a conversation with two live processes is ambiguous at tier 1, so
// when one of those processes' registry files is temporarily unreadable (an
// alive pid whose file is in unknown_registry_files) the single remaining
// row must NOT be delivered to — the hidden file may be the second process
// of that very conversation. Exactly one tier-1 match while the registry
// is incomplete is ErrResolveNotReady; with a complete registry the same
// single match resolves, whatever Partial says.
func TestResolve_SingleCanonicalHit_RegistryIncompleteIsNotReady(t *testing.T) {
	recs := []PeerRecord{liveRow(canonA, "", "", 1), liveRow(canonB, "other", "mt0", 2)}
	for _, in := range []string{canonA, canonA + ":suffix"} {
		if _, err := Resolve(recs, in, ResolveSnapshot{Partial: true, RegistryIncomplete: true}); !errors.Is(err, ErrResolveNotReady) {
			t.Fatalf("%q registry incomplete: got %v, want ErrResolveNotReady", in, err)
		}
	}
	for _, snap := range []ResolveSnapshot{{}, {Partial: true}} {
		got, err := Resolve(recs, canonA, snap)
		if err != nil || got.Agent.PID != 1 {
			t.Fatalf("%+v: got %+v %v, want the single canonical hit (pid 1)", snap, got, err)
		}
	}
}

// TestResolve_CanonicalMissUnderPartialIsNotReady pins spec §5.1's
// accepted conservatism: a tier-1 miss on a Partial snapshot is still
// ErrResolveNotReady, even though a canonical id depends on neither the
// label store nor owner resolution and so cannot have missed because of
// them. Retrying costs a round trip; a false "not found" costs a message.
func TestResolve_CanonicalMissUnderPartialIsNotReady(t *testing.T) {
	recs := []PeerRecord{liveRow(canonA, "", "mt0", 1)}
	if _, err := Resolve(recs, canonB, ResolveSnapshot{Partial: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("got %v, want ErrResolveNotReady", err)
	}
	if _, err := Resolve(recs, canonB, ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("complete inventory: got %v, want ErrNotFound", err)
	}
}

// TestResolve_Ambiguous_WinsOverRegistryIncomplete pins ordering: more than
// one tier-1 match is AmbiguousError even when the registry is incomplete
// (the caller learns the candidates, not a retry hint).
func TestResolve_Ambiguous_WinsOverRegistryIncomplete(t *testing.T) {
	recs := []PeerRecord{liveRow(canonA, "", "mt0", 1), liveRow(canonA, "", "", 2)}
	_, err := Resolve(recs, canonA, ResolveSnapshot{Partial: true, RegistryIncomplete: true})
	var amb *AmbiguousError
	if !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("got %v, want AmbiguousError with 2 candidates", err)
	}
}

// TestResolve_TmuxForm_IgnoresSnapshotFlags pins that the explicit
// "tmux:<name>" form bypasses every completeness rule: it resolves with
// both flags set exactly as with none.
func TestResolve_TmuxForm_IgnoresSnapshotFlags(t *testing.T) {
	recs := []PeerRecord{liveRow(canonA, "mt4", "mt0", 1), liveRow(canonB, "", "mt4", 2)}
	got, err := Resolve(recs, "tmux:mt4", ResolveSnapshot{Partial: true, RegistryIncomplete: true})
	if err != nil || got.Agent.PID != 2 {
		t.Fatalf("got %+v %v, want the tmux row (pid 2)", got, err)
	}
}

// --- Resolve: inert rows (X2) ----------------------------------------------

// TestResolve_DeadHolderDoesNotResolve pins X2: an inbox_dead fallback row
// carries the conversation's canonical id but no live entry, so tier 1
// must not see it. Alone it is a miss; beside a LIVE row of the same
// conversation it neither resolves nor makes the pair ambiguous.
func TestResolve_DeadHolderDoesNotResolve(t *testing.T) {
	recs := []PeerRecord{inboxDeadRow(canonA, "foo", "dead-session")}
	if _, err := Resolve(recs, canonA, ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound — a dead holder is inert", err)
	}
	recs = append(recs, liveRow(canonA, "foo", "", 7))
	got, err := Resolve(recs, canonA, ResolveSnapshot{})
	if err != nil || got.Agent == nil || got.Agent.PID != 7 {
		t.Fatalf("got %+v %v, want the live entry row (pid 7)", got, err)
	}
}

// TestResolve_DeadHolder_PartialStillNotReady pins that removing the dead
// row from tier 1 does not weaken the partial rule: a tier-1 miss on a
// partial inventory is still ErrResolveNotReady.
func TestResolve_DeadHolder_PartialStillNotReady(t *testing.T) {
	recs := []PeerRecord{inboxDeadRow(canonA, "foo", "dead-session"), {SessionName: "foo"}}
	if _, err := Resolve(recs, canonA, ResolveSnapshot{Partial: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("got %v, want ErrResolveNotReady", err)
	}
}

// --- Resolve: not found ----------------------------------------------------

func TestResolve_UnknownSessionReturnsErrNotFound(t *testing.T) {
	records := []PeerRecord{
		{SessionName: "alpha", SessionCode: "a1"},
	}
	_, err := Resolve(records, "nonexistent", ResolveSnapshot{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolve_EmptySessionReturnsErrNotFound(t *testing.T) {
	records := []PeerRecord{
		{SessionName: "alpha", SessionCode: "a1"},
	}
	_, err := Resolve(records, "", ResolveSnapshot{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- AmbiguousError.Error() --------------------------------------------

func TestAmbiguousError_ErrorMessageMentionsSession(t *testing.T) {
	err := &AmbiguousError{
		Session:    "dup",
		Candidates: []PeerRecord{{SessionName: "dup", SessionCode: "c1"}, {SessionName: "dup", SessionCode: "c2"}},
	}
	if msg := err.Error(); msg == "" {
		t.Fatal("Error() returned empty string")
	}
}

// --- SplitAddress ----------------------------------------------------------

func TestSplitAddress(t *testing.T) {
	cases := []struct {
		addr       string
		host, sess string
		ok         bool
	}{
		{"a/b", "a", "b", true},
		{"a/", "", "", false},
		{"/b", "", "", false},
		{"ab", "", "", false},
		{"a/b/c", "", "", false},
	}
	for _, c := range cases {
		host, sess, ok := SplitAddress(c.addr)
		if ok != c.ok {
			t.Errorf("SplitAddress(%q) ok = %v, want %v", c.addr, ok, c.ok)
			continue
		}
		if ok && (host != c.host || sess != c.sess) {
			t.Errorf("SplitAddress(%q) = (%q, %q), want (%q, %q)", c.addr, host, sess, c.host, c.sess)
		}
	}
}

// --- HostMatches -------------------------------------------------------

func TestHostMatches(t *testing.T) {
	cases := []struct {
		want, alias, hostID string
		expect              bool
	}{
		{"Mini-Lab", "mini-lab", "mini-lab:278cbm", true},
		{"MINI-LAB:278CBM", "mini-lab", "mini-lab:278cbm", true},
		{"mini", "mini-lab", "mini-lab:278cbm", false},
	}
	for _, c := range cases {
		got := HostMatches(c.want, c.alias, c.hostID)
		if got != c.expect {
			t.Errorf("HostMatches(%q, %q, %q) = %v, want %v", c.want, c.alias, c.hostID, got, c.expect)
		}
	}
}
