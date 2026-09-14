package peers

import (
	"errors"
	"testing"
)

// --- helpers -------------------------------------------------------------

func labelRecord(label, source, sessionName string, pid int) PeerRecord {
	return PeerRecord{SessionName: sessionName, Label: label, LabelSource: source,
		Agent: &AgentInfo{Type: "cc", PID: pid}, Deliverable: true}
}

// --- Resolve: tiers --------------------------------------------------------

func TestResolve_LabelTier(t *testing.T) {
	recs := []PeerRecord{labelRecord("purdex-dev", "user", "mt0", 1), labelRecord("_abc123", "default", "", 2)}
	for _, in := range []string{"purdex-dev", "purdex-dev:whatever-suffix", "_abc123", "_abc123:x"} {
		got, err := Resolve(recs, in, ResolveSnapshot{})
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if (in[0] == '_' && got.Agent.PID != 2) || (in[0] != '_' && got.Agent.PID != 1) {
			t.Errorf("%q resolved to pid %d", in, got.Agent.PID)
		}
	}
}

func TestResolve_LabelShadowsTmuxName_TmuxFormBypasses(t *testing.T) {
	recs := []PeerRecord{labelRecord("mt4", "user", "mt0", 1), labelRecord("_zzzzzz", "default", "mt4", 2)}
	got, _ := Resolve(recs, "mt4", ResolveSnapshot{})
	if got.Agent.PID != 1 {
		t.Errorf("bare mt4 resolved to pid %d, want the label holder 1", got.Agent.PID)
	}
	got, _ = Resolve(recs, "tmux:mt4", ResolveSnapshot{})
	if got.Agent.PID != 2 {
		t.Errorf("tmux:mt4 resolved to pid %d, want 2", got.Agent.PID)
	}
	if _, err := Resolve(recs, "tmux:", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("tmux: empty ⇒ %v", err)
	}
}

func TestResolve_TmuxFallback_OnlyWhenComplete(t *testing.T) {
	recs := []PeerRecord{{SessionName: "shell"}} // no cc agent, no label
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
	recs := []PeerRecord{labelRecord("cc", "user", "", 1)} // cannot exist, but the resolver must not care
	for _, snap := range []ResolveSnapshot{{}, {Partial: true}, {Partial: true, RegistryIncomplete: true}} {
		_, err := Resolve(recs, "cc:foo", snap)
		if !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrLegacyCC) {
			t.Errorf("%+v: %v", snap, err)
		}
	}
}

func TestResolve_Ambiguous_SameLabel(t *testing.T) {
	recs := []PeerRecord{labelRecord("_abc123", "default", "mt0", 1), labelRecord("_abc123", "default", "", 2)}
	_, err := Resolve(recs, "_abc123", ResolveSnapshot{})
	var amb *AmbiguousError
	if !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("got %v", err)
	}
}

func TestResolve_ProxyAndUnlabelledExcluded(t *testing.T) {
	recs := []PeerRecord{
		{Label: "x1", Agent: &AgentInfo{Type: "proxy"}},
		{SessionName: "x1"}, // no label; tier 2 would match
	}
	got, err := Resolve(recs, "x1", ResolveSnapshot{})
	if err != nil || got.Agent != nil {
		t.Fatalf("got %+v %v, want the tmux row via tier 2", got, err)
	}
}

// --- Resolve: registry completeness (X1) -----------------------------------

// TestResolve_SingleLabelHit_RegistryIncompleteIsNotReady pins the X1 rule:
// a conversation with two live processes is ambiguous at tier 1, so when
// one of those processes' registry files is temporarily unreadable (an
// alive pid whose file is in unknown_registry_files) the single remaining
// row must NOT be delivered to — the hidden file may be the second process
// of that very conversation. Exactly one tier-1 match while the registry
// is incomplete is ErrResolveNotReady; with a complete registry the same
// single match resolves, whatever Partial says.
func TestResolve_SingleLabelHit_RegistryIncompleteIsNotReady(t *testing.T) {
	recs := []PeerRecord{labelRecord("_abc123", "default", "", 1), labelRecord("other", "user", "mt0", 2)}
	for _, in := range []string{"_abc123", "_abc123:suffix"} {
		if _, err := Resolve(recs, in, ResolveSnapshot{Partial: true, RegistryIncomplete: true}); !errors.Is(err, ErrResolveNotReady) {
			t.Fatalf("%q registry incomplete: got %v, want ErrResolveNotReady", in, err)
		}
	}
	for _, snap := range []ResolveSnapshot{{}, {Partial: true}} {
		got, err := Resolve(recs, "_abc123", snap)
		if err != nil || got.Agent.PID != 1 {
			t.Fatalf("%+v: got %+v %v, want the single label hit (pid 1)", snap, got, err)
		}
	}
}

// TestResolve_Ambiguous_WinsOverRegistryIncomplete pins ordering: more than
// one tier-1 match is AmbiguousError even when the registry is incomplete
// (the caller learns the candidates, not a retry hint).
func TestResolve_Ambiguous_WinsOverRegistryIncomplete(t *testing.T) {
	recs := []PeerRecord{labelRecord("_abc123", "default", "mt0", 1), labelRecord("_abc123", "default", "", 2)}
	_, err := Resolve(recs, "_abc123", ResolveSnapshot{Partial: true, RegistryIncomplete: true})
	var amb *AmbiguousError
	if !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("got %v, want AmbiguousError with 2 candidates", err)
	}
}

// TestResolve_TmuxForm_IgnoresSnapshotFlags pins that the explicit
// "tmux:<name>" form bypasses every completeness rule: it resolves with
// both flags set exactly as with none.
func TestResolve_TmuxForm_IgnoresSnapshotFlags(t *testing.T) {
	recs := []PeerRecord{labelRecord("mt4", "user", "mt0", 1), labelRecord("_zzzzzz", "default", "mt4", 2)}
	got, err := Resolve(recs, "tmux:mt4", ResolveSnapshot{Partial: true, RegistryIncomplete: true})
	if err != nil || got.Agent.PID != 2 {
		t.Fatalf("got %+v %v, want the tmux row (pid 2)", got, err)
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
