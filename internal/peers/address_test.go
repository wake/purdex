package peers

import (
	"errors"
	"strings"
	"testing"
)

// --- helpers -------------------------------------------------------------

// Refs as v4 mints them: "_" + 6 base36 digits. The ref tiers compare them as
// plain strings, so literals are the real thing.
const (
	refA = "_3k9f2m"
	refB = "_zq81ab"
	refC = "_0000zz"
)

// liveRow is the row shape the name and ref tiers decide on: a live cc entry
// carrying both its registry name and its ref. The label rides along and is
// deliberately never what any tier matches — that is D3, unchanged in v4.
func liveRow(ref, name, label, sessionName string, pid int) PeerRecord {
	source := ""
	if label != "" {
		source = "user"
	}
	return PeerRecord{
		SessionName: sessionName, Ref: ref, Title: label, TitleSource: source,
		Agent:       &AgentInfo{Type: "cc", PID: pid, PeerName: name},
		Deliverable: true,
	}
}

// inboxDeadRow is the owner-fallback session row Build emits when a cc
// owner's conversation has no live registry entry (ownerFallbackAgent: PID
// 0, no inbox). It carries the conversation's ref and its persisted
// label, but its holder is not live, so per spec §3.3 it is
// inert — it must neither resolve nor block.
func inboxDeadRow(ref, label, sessionName string) PeerRecord {
	return PeerRecord{
		RowKind: "session", SessionName: sessionName, Ref: ref, Title: label, TitleSource: "user",
		Agent:  &AgentInfo{Type: "cc", SessionID: "dead-sid"},
		Reason: "inbox_dead",
	}
}

// --- Resolve: tiers --------------------------------------------------------

// TestResolve_RefTier pins tiers 2/3, the v3 canonical tier at its v4 width:
// the ref resolves, with or without its leading underscore.
//
// The ":<suffix>" variants this test carried in v3 are gone with the field
// itself (v4 spec §5.3) — an address is "<name> [<ref>]" or a bare ref now, so
// the suffixed forms are asserted to MISS rather than quietly resolve. That is
// a narrowing of what routes, not of a conservatism.
func TestResolve_RefTier(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "", "purdex-dev", "mt0", 1), liveRow(refB, "", "", "", 2)}
	for _, c := range []struct {
		in  string
		pid int
	}{
		{refA, 1}, {strings.TrimPrefix(refA, "_"), 1},
		{refB, 2}, {strings.TrimPrefix(refB, "_"), 2},
	} {
		got, err := Resolve(recs, c.in, ResolveSnapshot{})
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got.Agent.PID != c.pid {
			t.Errorf("%q resolved to pid %d, want %d", c.in, got.Agent.PID, c.pid)
		}
	}
	for _, in := range []string{refA + ":whatever-suffix", refB + ":x", refA + ":"} {
		if _, err := Resolve(recs, in, ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: got %v, want ErrNotFound — v4 has no suffix form", in, err)
		}
	}
}

// TestResolve_LabelDoesNotResolve is the test that pins D3: a label is a
// display name and nothing else. Sending to one must fall through EVERY tier
// — the registry-name tier, the ref tiers and the bare tmux-name fallback —
// and land on ErrNotFound: not on the row that happens to carry that label,
// and not on an ambiguity verdict either.
//
// v4 sharpens it. The rows now carry a routable registry NAME that does route,
// so a miss here can no longer be an artefact of nothing being matchable; the
// label is skipped while the name beside it would have been taken.
//
// Before T5 this same input delivered a message, which is exactly the
// behaviour v3 exists to remove.
func TestResolve_LabelDoesNotResolve(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-b0", "purdex-tester", "mt0", 1)}
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
	recs = append(recs, liveRow(refB, "nexen-f2", "purdex-tester", "mt1", 2))
	if _, err := Resolve(recs, "purdex-tester", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate label: got %v, want ErrNotFound", err)
	}
	// And each holder is still reachable at its own ref.
	for _, c := range []struct {
		in  string
		pid int
	}{{refA, 1}, {refB, 2}} {
		got, err := Resolve(recs, c.in, ResolveSnapshot{})
		if err != nil || got.Agent.PID != c.pid {
			t.Fatalf("%q: got %+v %v, want pid %d", c.in, got, err, c.pid)
		}
	}
}

// TestResolve_LabelNoLongerShadowsTmuxName is the same D3 rule seen from
// the other side. Under v2 a label "mt4" shadowed a tmux session named
// "mt4" at tier 1; from v3 on no tier sees the label at all, so the bare
// name falls through to the tmux fallback and lands on the tmux session —
// the place it names. The explicit "tmux:" form agrees, as it always did.
func TestResolve_LabelNoLongerShadowsTmuxName(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-b0", "mt4", "mt0", 1), liveRow(refB, "", "", "mt4", 2)}
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

// TestResolve_RefWinsOverTmuxName pins tier order: the ref tier decides
// before the bare-name fallback is ever consulted.
func TestResolve_RefWinsOverTmuxName(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "", "", "mt0", 1), liveRow(refB, "", "", refA, 2)}
	got, err := Resolve(recs, refA, ResolveSnapshot{})
	if err != nil || got.Agent.PID != 1 {
		t.Fatalf("got %+v %v, want the ref holder (pid 1)", got, err)
	}
	got, err = Resolve(recs, "tmux:"+refA, ResolveSnapshot{})
	if err != nil || got.Agent.PID != 2 {
		t.Fatalf("tmux form: got %+v %v, want the tmux row (pid 2)", got, err)
	}
}

func TestResolve_TmuxFallback_OnlyWhenComplete(t *testing.T) {
	recs := []PeerRecord{{SessionName: "shell"}} // no cc agent, no ref
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
	recs := []PeerRecord{liveRow(refA, "cc", "", "", 1)} // cannot exist, but the resolver must not care
	for _, snap := range []ResolveSnapshot{{}, {Partial: true}, {Partial: true, RegistryIncomplete: true}} {
		_, err := Resolve(recs, "cc:foo", snap)
		if !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrLegacyCC) {
			t.Errorf("%+v: %v", snap, err)
		}
	}
}

// TestResolve_Ambiguous_SameRef pins the backstop spec §4.1 keeps: two live
// rows sharing a ref — one conversation with two processes, or §3.2's forged
// twin — make the resolver refuse rather than pick one.
func TestResolve_Ambiguous_SameRef(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "", "", "mt0", 1), liveRow(refA, "", "", "", 2)}
	_, err := Resolve(recs, refA, ResolveSnapshot{})
	var amb *AmbiguousError
	if !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("got %v", err)
	}
}

// TestResolve_ProxyRowsExcluded pins that a proxy row cannot decide the ref
// tier even when it carries the head, so a same-named tmux session is still
// reachable through the bare-name fallback.
func TestResolve_ProxyRowsExcluded(t *testing.T) {
	recs := []PeerRecord{
		{Ref: refC, Agent: &AgentInfo{Type: "proxy"}},
		{SessionName: refC}, // the tmux fallback would match
	}
	got, err := Resolve(recs, refC, ResolveSnapshot{})
	if err != nil || got.Agent != nil {
		t.Fatalf("got %+v %v, want the tmux row via the fallback", got, err)
	}
}

// TestResolve_EmptyHeadMatchesNothing pins that ":suffix" — which splits to
// an EMPTY head — reaches nothing.
//
// v2 spelled the guard as `r.Label != ""` and v3 as `head != ""`. v4 needs
// neither: RoutableName("") is false, so the name tier cannot take an empty
// head, and IsRef("_") is false, so the ref tiers cannot either. The guard is
// now a property of the two grammars rather than a check, which is exactly the
// sort of thing that stops being tested and then stops being true. A row whose
// registry name is empty — every non-cc row, and any cc row whose name a
// daemon did not send — is the one that would answer if it regressed.
func TestResolve_EmptyHeadMatchesNothing(t *testing.T) {
	namelessRow := liveRow(refB, "", "purdex-tester", "mt0", 1)
	recs := []PeerRecord{namelessRow, liveRow(refA, "purdex-b0", "", "mt1", 2)}
	if _, err := Resolve(recs, ":suffix", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// --- Resolve: registry completeness (X1) -----------------------------------

// TestResolve_SingleHit_RegistryIncompleteIsNotReady pins the X1 rule: a
// conversation with two live processes is ambiguous, so when one of those
// processes' registry files is temporarily unreadable (an alive pid whose file
// is in unknown_registry_files) the single remaining row must NOT be delivered
// to — the hidden file may be the second process of that very conversation.
// Exactly one match while the registry is incomplete is ErrResolveNotReady;
// with a complete registry the same single match resolves, whatever Partial
// says.
//
// v4 asserts it on BOTH deciding tiers. Spec §5.4 states the rule of "a single
// hit", not of one tier, and the name tier is new enough that leaving it
// untested is how it would come to lack the rule.
func TestResolve_SingleHit_RegistryIncompleteIsNotReady(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-b0", "", "", 1), liveRow(refB, "nexen-f2", "other", "mt0", 2)}
	for _, in := range []string{refA, "purdex-b0"} {
		if _, err := Resolve(recs, in, ResolveSnapshot{Partial: true, RegistryIncomplete: true}); !errors.Is(err, ErrResolveNotReady) {
			t.Fatalf("%q registry incomplete: got %v, want ErrResolveNotReady", in, err)
		}
		for _, snap := range []ResolveSnapshot{{}, {Partial: true}} {
			got, err := Resolve(recs, in, snap)
			if err != nil || got.Agent.PID != 1 {
				t.Fatalf("%q %+v: got %+v %v, want the single hit (pid 1)", in, snap, got, err)
			}
		}
	}
}

// TestResolve_RefMissUnderPartialIsNotReady pins spec §5.4's accepted
// conservatism: a ref miss on a Partial snapshot is still ErrResolveNotReady,
// even though a ref depends on neither the label store nor owner resolution
// and so cannot have missed because of them. Retrying costs a round trip; a
// false "not found" costs a message.
func TestResolve_RefMissUnderPartialIsNotReady(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-b0", "", "mt0", 1)}
	if _, err := Resolve(recs, refB, ResolveSnapshot{Partial: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("got %v, want ErrResolveNotReady", err)
	}
	if _, err := Resolve(recs, refB, ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("complete inventory: got %v, want ErrNotFound", err)
	}
}

// TestResolve_Ambiguous_WinsOverRegistryIncomplete pins ordering: more than
// one match in the deciding tier is AmbiguousError even when the registry is
// incomplete (the caller learns the candidates, not a retry hint).
func TestResolve_Ambiguous_WinsOverRegistryIncomplete(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "", "", "mt0", 1), liveRow(refA, "", "", "", 2)}
	_, err := Resolve(recs, refA, ResolveSnapshot{Partial: true, RegistryIncomplete: true})
	var amb *AmbiguousError
	if !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("got %v, want AmbiguousError with 2 candidates", err)
	}
}

// TestResolve_TmuxForm_IgnoresSnapshotFlags pins that the explicit
// "tmux:<name>" form bypasses every completeness rule: it resolves with
// both flags set exactly as with none.
func TestResolve_TmuxForm_IgnoresSnapshotFlags(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-b0", "mt4", "mt0", 1), liveRow(refB, "", "", "mt4", 2)}
	got, err := Resolve(recs, "tmux:mt4", ResolveSnapshot{Partial: true, RegistryIncomplete: true})
	if err != nil || got.Agent.PID != 2 {
		t.Fatalf("got %+v %v, want the tmux row (pid 2)", got, err)
	}
}

// --- Resolve: inert rows (X2) ----------------------------------------------

// TestResolve_DeadHolderDoesNotResolve pins X2: an inbox_dead fallback row
// carries the conversation's ref but no live entry, so no tier must see it.
// Alone it is a miss; beside a LIVE row of the same conversation it neither
// resolves nor makes the pair ambiguous.
func TestResolve_DeadHolderDoesNotResolve(t *testing.T) {
	recs := []PeerRecord{inboxDeadRow(refA, "foo", "dead-session")}
	if _, err := Resolve(recs, refA, ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound — a dead holder is inert", err)
	}
	recs = append(recs, liveRow(refA, "", "foo", "", 7))
	got, err := Resolve(recs, refA, ResolveSnapshot{})
	if err != nil || got.Agent == nil || got.Agent.PID != 7 {
		t.Fatalf("got %+v %v, want the live entry row (pid 7)", got, err)
	}
}

// TestResolve_DeadHolder_PartialStillNotReady pins that removing the dead
// row from the ref tier does not weaken the partial rule: a ref miss on a
// partial inventory is still ErrResolveNotReady.
func TestResolve_DeadHolder_PartialStillNotReady(t *testing.T) {
	recs := []PeerRecord{inboxDeadRow(refA, "foo", "dead-session"), {SessionName: "foo"}}
	if _, err := Resolve(recs, refA, ResolveSnapshot{Partial: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("got %v, want ErrResolveNotReady", err)
	}
}

// --- Resolve: v4 name tier, ref tiers and the combined form -----------------

func TestResolve_BareName(t *testing.T) {
	recs := []PeerRecord{
		liveRow("_q34psn", "purdex-b0", "", "aigora2", 1),
		liveRow("_df25d0", "nexen-f2", "", "nexen", 2),
	}
	got, err := Resolve(recs, "purdex-b0", ResolveSnapshot{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Ref != "_q34psn" {
		t.Errorf("resolved %q, want the purdex-b0 row", got.Ref)
	}
}

func TestResolve_RefWithAndWithoutUnderscore(t *testing.T) {
	recs := []PeerRecord{liveRow("_q34psn", "purdex-b0", "", "aigora2", 1)}
	for _, form := range []string{"_q34psn", "q34psn"} {
		got, err := Resolve(recs, form, ResolveSnapshot{})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", form, err)
		}
		if got.Ref != "_q34psn" {
			t.Errorf("Resolve(%q) landed on %q", form, got.Ref)
		}
	}
}

// RoutableName defended end to end: a six-digit registry name must not win
// the bare-ref form.
func TestResolve_RefShapedNameCannotShadow(t *testing.T) {
	recs := []PeerRecord{
		liveRow("_aaaaaa", "q34psn", "", "evil", 1),
		liveRow("_q34psn", "purdex-b0", "", "aigora2", 2),
	}
	got, err := Resolve(recs, "q34psn", ResolveSnapshot{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Ref != "_q34psn" {
		t.Errorf("bare ref resolved to %q; a ref-shaped NAME shadowed it", got.Ref)
	}
}

func TestResolve_CombinedFormMatches(t *testing.T) {
	recs := []PeerRecord{liveRow("_q34psn", "purdex-b0", "", "aigora2", 1)}
	got, err := Resolve(recs, "purdex-b0 [q34psn]", ResolveSnapshot{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Ref != "_q34psn" {
		t.Errorf("resolved %q", got.Ref)
	}
}

// The security property: the name in a combined address is a check digit.
func TestResolve_CombinedFormMismatchRefuses(t *testing.T) {
	recs := []PeerRecord{liveRow("_q34psn", "attacker", "", "evil", 1)}
	_, err := Resolve(recs, "trusted-name [q34psn]", ResolveSnapshot{})
	if !errors.Is(err, ErrNameMismatch) {
		t.Fatalf("Resolve err = %v, want ErrNameMismatch", err)
	}
	for _, want := range []string{"trusted-name", "attacker", "q34psn"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// The combined form must carry the SAME conservatisms as the bare ref tier.
func TestResolve_CombinedFormHonoursSnapshot(t *testing.T) {
	recs := []PeerRecord{liveRow("_q34psn", "purdex-b0", "", "aigora2", 1)}
	if _, err := Resolve(recs, "purdex-b0 [q34psn]", ResolveSnapshot{Partial: true, RegistryIncomplete: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Errorf("err = %v, want ErrResolveNotReady", err)
	}
	empty := []PeerRecord{}
	if _, err := Resolve(empty, "purdex-b0 [q34psn]", ResolveSnapshot{Partial: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Errorf("miss under Partial: err = %v, want ErrResolveNotReady", err)
	}
}

func TestResolve_AmbiguousName(t *testing.T) {
	recs := []PeerRecord{
		liveRow("_aaaaaa", "purdex-b0", "", "a", 1),
		liveRow("_bbbbbb", "purdex-b0", "", "b", 2),
	}
	var amb *AmbiguousError
	if _, err := Resolve(recs, "purdex-b0", ResolveSnapshot{}); !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("err = %v, want AmbiguousError with 2 candidates", err)
	}
}

// §3.1's residual, asserted: a shared ref costs the ref form, not the name.
func TestResolve_AmbiguousRefStillReachableByName(t *testing.T) {
	recs := []PeerRecord{
		liveRow("_q34psn", "purdex-b0", "", "a", 1),
		liveRow("_q34psn", "nexen-f2", "", "b", 2),
	}
	var amb *AmbiguousError
	if _, err := Resolve(recs, "_q34psn", ResolveSnapshot{}); !errors.As(err, &amb) {
		t.Fatalf("ref err = %v, want AmbiguousError", err)
	}
	if _, err := Resolve(recs, "purdex-b0", ResolveSnapshot{}); err != nil {
		t.Errorf("name Resolve: %v, want the name to still work", err)
	}
}

func TestResolve_UnroutableNameNeverWinsTier1(t *testing.T) {
	recs := []PeerRecord{liveRow("_aaaaaa", "has/slash", "", "a", 1)}
	if _, err := Resolve(recs, "has/slash", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSplitCombined_Malformed(t *testing.T) {
	for _, s := range []string{
		"purdex-b0", "[q34psn]", "purdex-b0 [", "purdex-b0]", " [q34psn]",
		"purdex-b0 [a][b]", "purdex-b0 []",
	} {
		if _, _, ok := splitCombined(s); ok {
			t.Errorf("splitCombined(%q) reported ok; want not-combined", s)
		}
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

// --- Resolve: rows fetched from a pre-v3 daemon -----------------------------

// v2LiveRow is a live cc row exactly as a daemon that predates v3 puts it
// on the wire: a label, a tmux session name, and NO canonical — that
// daemon has never heard of the field, so it decodes as "".
func v2LiveRow(label, sessionName string, pid int) PeerRecord {
	return PeerRecord{RowKind: "session", SessionName: sessionName, Title: label, TitleSource: "user",
		Agent: &AgentInfo{Type: "cc", PID: pid}, Deliverable: true}
}

// TestResolve_V2Rows_HeadRefusedRatherThanGuessed pins the mixed-version
// rule. A v2 daemon still prints a label as the address head, so that is
// what an operator on the v3 side types. Tier 1 cannot match it (the rows
// carry no canonical), and the old behaviour was to drop into tier 2 and
// try the string as a tmux session name — a guess, on a batch the resolver
// can see is too old to answer properly. It must say so instead, in both
// the bare and the "<head>:<suffix>" forms.
func TestResolve_V2Rows_HeadRefusedRatherThanGuessed(t *testing.T) {
	recs := []PeerRecord{v2LiveRow("purdex-tester", "mt0", 1), v2LiveRow("purdex-dev", "mt1", 2)}
	for _, in := range []string{"purdex-tester", "purdex-tester:mt0-purdex-b0", refA} {
		_, err := Resolve(recs, in, ResolveSnapshot{})
		if !errors.Is(err, ErrRemoteTooOld) {
			t.Errorf("%q: got %v, want ErrRemoteTooOld", in, err)
		}
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: got %v, want it to stay an ErrNotFound for existing callers", in, err)
		}
	}
}

// TestResolve_V2Rows_NeverLandOnASameNamedTmuxSession is the misroute this
// exists to stop: one row calls itself "purdex-tester", a DIFFERENT row's
// tmux session is named "purdex-tester". Under the tier-2 fallback the
// message went to the second row — a successful delivery to the wrong
// conversation, which is worse than any refusal.
func TestResolve_V2Rows_NeverLandOnASameNamedTmuxSession(t *testing.T) {
	recs := []PeerRecord{v2LiveRow("purdex-tester", "mt0", 1), v2LiveRow("", "purdex-tester", 2)}
	got, err := Resolve(recs, "purdex-tester", ResolveSnapshot{})
	if !errors.Is(err, ErrRemoteTooOld) {
		t.Fatalf("got %+v %v, want ErrRemoteTooOld", got, err)
	}
	if got.Agent != nil {
		t.Fatalf("resolved to pid %d — a v2 label head must never route", got.Agent.PID)
	}
}

// TestResolve_StaleBatch_NameTierRefusedNotResolved pins WHERE the
// stale-version gate sits, which is the one thing v4 moved about it.
//
// v3 ran the gate after a tier-1 miss, and that was safe because the only
// thing under it was a tmux-name guess. v4's tier 1 matches agent.peer_name —
// a field an old daemon sends perfectly well — so a stale row would SUCCEED
// there. The message would land on a conversation whose ref the sender could
// never have verified, which is a delivery to the wrong place, not a miss.
//
// The row below is therefore the shape that only this rule catches: a live cc
// entry with a usable, routable registry name and no ref. If the gate is ever
// moved back under tier 1, this test is what goes red.
func TestResolve_StaleBatch_NameTierRefusedNotResolved(t *testing.T) {
	stale := PeerRecord{
		RowKind: "session", SessionName: "aigora2",
		Agent:       &AgentInfo{Type: "cc", PID: 42, PeerName: "purdex-b0"},
		Deliverable: true,
	} // no Ref: a pre-v4 daemon's JSON has no such key
	got, err := Resolve([]PeerRecord{stale}, "purdex-b0", ResolveSnapshot{})
	if !errors.Is(err, ErrRemoteTooOld) {
		t.Fatalf("got %+v %v, want ErrRemoteTooOld — the name tier resolved a batch it must refuse", got, err)
	}
	if got.Agent != nil {
		t.Fatalf("resolved to pid %d; a stale row must never route", got.Agent.PID)
	}
	// And the escape hatch the refusal names is decided above the gate.
	if got, err := Resolve([]PeerRecord{stale}, "tmux:aigora2", ResolveSnapshot{}); err != nil || got.SessionName != "aigora2" {
		t.Fatalf("tmux form: got %+v %v, want it to resolve", got, err)
	}
}

// TestResolve_V2Rows_ExplicitTmuxFormStillResolves pins what the refusal
// must NOT swallow. "tmux:<name>" addresses a place, the operator said so
// in as many words, and a v2 daemon sends SessionName just as a v3 one
// does — so that form keeps working against an old peer and is the escape
// hatch the refusal points at.
func TestResolve_V2Rows_ExplicitTmuxFormStillResolves(t *testing.T) {
	recs := []PeerRecord{v2LiveRow("purdex-tester", "mt0", 1), v2LiveRow("", "purdex-tester", 2)}
	got, err := Resolve(recs, "tmux:purdex-tester", ResolveSnapshot{})
	if err != nil || got.Agent == nil || got.Agent.PID != 2 {
		t.Fatalf("got %+v %v, want the row whose tmux session is purdex-tester (pid 2)", got, err)
	}
	if got, err := Resolve(recs, "tmux:mt0", ResolveSnapshot{}); err != nil || got.Agent.PID != 1 {
		t.Fatalf("tmux:mt0: got %+v %v, want pid 1", got, err)
	}
}

// TestResolve_CurrentRows_TmuxFallbackUnaffected pins the other side of the
// guard: the version signal is a LIVE cc row with no ref, so a normal current
// batch — a ref on every live row, rows with no agent carrying none by design
// — keeps every tier exactly as it was.
func TestResolve_CurrentRows_TmuxFallbackUnaffected(t *testing.T) {
	recs := []PeerRecord{
		liveRow(refA, "purdex-b0", "purdex-tester", "mt0", 1),
		{RowKind: "session", SessionName: "shell"},    // agent: null, ref "" by design
		{Ref: refC, Agent: &AgentInfo{Type: "proxy"}}, // proxy row, not a live cc entry
		inboxDeadRow(refB, "purdex-dev", "mt1"),       // owner fallback, pid 0
	}
	if got, err := Resolve(recs, refA, ResolveSnapshot{}); err != nil || got.Agent.PID != 1 {
		t.Fatalf("ref: got %+v %v", got, err)
	}
	if got, err := Resolve(recs, "purdex-b0", ResolveSnapshot{}); err != nil || got.Agent.PID != 1 {
		t.Fatalf("name: got %+v %v", got, err)
	}
	if got, err := Resolve(recs, "shell", ResolveSnapshot{}); err != nil || got.SessionName != "shell" {
		t.Fatalf("tmux fallback: got %+v %v, want the shell row", got, err)
	}
	_, err := Resolve(recs, "purdex-tester", ResolveSnapshot{})
	if !errors.Is(err, ErrNotFound) || errors.Is(err, ErrRemoteTooOld) {
		t.Fatalf("a label on a current batch: got %v, want a plain ErrNotFound", err)
	}
}

// --- the stale-version signal itself ------------------------------------

// TestStaleVersionRows_SignalIsTheConjunction calls the predicate by name,
// because the name is half of what this rule delivers: "pre-v3" stopped being
// true the moment v4 moved the signal to the ref field, and a predicate whose
// name misstates its own cause is read wrong by the next person to touch the
// gate above tier 1.
func TestStaleVersionRows_SignalIsTheConjunction(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  PeerRecord
		want bool
	}{
		{"live cc row without a ref", PeerRecord{Agent: &AgentInfo{Type: "cc", PID: 42, PeerName: "purdex-b0"}}, true},
		{"live cc row with a ref", liveRow(refA, "purdex-b0", "", "mt0", 42), false},
		{"owner fallback", PeerRecord{Agent: &AgentInfo{Type: "cc", PID: 0}, Reason: "inbox_dead"}, false},
		{"proxy", PeerRecord{Agent: &AgentInfo{Type: "proxy", PID: 7}, Reason: "proxy"}, false},
		{"agent null", PeerRecord{Agent: nil, Reason: "no_agent", SessionName: "aigora3"}, false},
	} {
		if got := hasStaleVersionRows([]PeerRecord{tc.rec}); got != tc.want {
			t.Errorf("hasStaleVersionRows(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
	// One stale row condemns the batch: Resolve is called per host, so every
	// row in it came from the same daemon.
	mixed := []PeerRecord{
		liveRow(refA, "purdex-b0", "", "mt0", 1),
		{Agent: &AgentInfo{Type: "cc", PID: 2, PeerName: "purdex-dev"}},
	}
	if !hasStaleVersionRows(mixed) {
		t.Error("hasStaleVersionRows(mixed batch) = false, want true")
	}
}

// TestStaleVersion_V3BatchRefusesEveryForm drives a v3-shaped batch through
// Resolve: a v3 daemon sent "canonical", a v4 decoder reads "ref", so Ref is
// empty while the row is otherwise a live, usable cc row carrying a name that
// tier 1 would happily match.
func TestStaleVersion_V3BatchRefusesEveryForm(t *testing.T) {
	recs := []PeerRecord{{
		SessionName: "aigora2",
		Agent:       &AgentInfo{Type: "cc", PID: 42, PeerName: "purdex-b0"},
	}}
	for _, form := range []string{"purdex-b0", "_q34psn", "q34psn", "purdex-b0 [q34psn]"} {
		if _, err := Resolve(recs, form, ResolveSnapshot{}); !errors.Is(err, ErrRemoteTooOld) {
			t.Errorf("Resolve(%q) err = %v, want ErrRemoteTooOld", form, err)
		}
	}
	// The escape hatch the refusal names must still work.
	if _, err := Resolve(recs, "tmux:aigora2", ResolveSnapshot{}); err != nil {
		t.Errorf("tmux form: %v, want it to resolve", err)
	}
}

// TestStaleVersion_V4RowsNotMisjudged pins the other half of the conjunction.
// Each row below legitimately carries an empty Ref on a CURRENT daemon, so
// testing Ref alone would declare every v4 host obsolete.
func TestStaleVersion_V4RowsNotMisjudged(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  PeerRecord
	}{
		{"owner fallback", PeerRecord{Agent: &AgentInfo{Type: "cc", PID: 0}, Reason: "inbox_dead"}},
		{"proxy", PeerRecord{Agent: &AgentInfo{Type: "proxy", PID: 7}, Reason: "proxy"}},
		{"agent null", PeerRecord{Agent: nil, Reason: "no_agent", SessionName: "aigora3"}},
	} {
		_, err := Resolve([]PeerRecord{tc.rec}, "nobody", ResolveSnapshot{})
		if errors.Is(err, ErrRemoteTooOld) {
			t.Errorf("%s: judged stale; want a plain miss", tc.name)
		}
	}
}

// TestResolve_NameTierRejectsSuffixForm pins the half of §5.4's grammar that
// tier 1 was missing: v4 deleted the "<head>:<suffix>" form outright, and the
// ref tiers refuse it (they run only when rest == ""), but the name tier
// matched on head alone — so "<name>:anything" delivered to <name>, and every
// retired v3 address in someone's scrollback stayed live.
//
// It is a real hole rather than a cosmetic one: the suffix v3 printed was the
// tmux-and-conversation identity, so a stale suffixed address names a place
// that may have moved while the bare name in front of it did not.
func TestResolve_NameTierRejectsSuffixForm(t *testing.T) {
	recs := []PeerRecord{liveRow(refA, "purdex-b0", "", "aigora2", 1)}
	// The bare name is live, so a miss below cannot be an artefact of the
	// row being unmatchable.
	if _, err := Resolve(recs, "purdex-b0", ResolveSnapshot{}); err != nil {
		t.Fatalf("bare name: %v, want it to resolve", err)
	}
	for _, in := range []string{"purdex-b0:old-suffix", "purdex-b0:", "purdex-b0:aigora2-claude"} {
		if _, err := Resolve(recs, in, ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: got %v, want ErrNotFound — v4 has no suffix form", in, err)
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
