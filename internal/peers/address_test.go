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
		got, err := Resolve(recs, in, false)
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
	got, _ := Resolve(recs, "mt4", false)
	if got.Agent.PID != 1 {
		t.Errorf("bare mt4 resolved to pid %d, want the label holder 1", got.Agent.PID)
	}
	got, _ = Resolve(recs, "tmux:mt4", false)
	if got.Agent.PID != 2 {
		t.Errorf("tmux:mt4 resolved to pid %d, want 2", got.Agent.PID)
	}
	if _, err := Resolve(recs, "tmux:", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("tmux: empty ⇒ %v", err)
	}
}

func TestResolve_TmuxFallback_OnlyWhenComplete(t *testing.T) {
	recs := []PeerRecord{{SessionName: "shell"}} // no cc agent, no label
	if got, err := Resolve(recs, "shell", false); err != nil || got.SessionName != "shell" {
		t.Fatalf("complete: %+v %v", got, err)
	}
	if _, err := Resolve(recs, "shell", true); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("partial: got %v, want ErrResolveNotReady", err)
	}
	if _, err := Resolve(recs, "shell:x", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("suffix on a tmux fallback: got %v, want ErrNotFound", err)
	}
}

func TestResolve_CCShortCircuit(t *testing.T) {
	recs := []PeerRecord{labelRecord("cc", "user", "", 1)} // cannot exist, but the resolver must not care
	for _, partial := range []bool{false, true} {
		_, err := Resolve(recs, "cc:foo", partial)
		if !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrLegacyCC) {
			t.Errorf("partial=%v: %v", partial, err)
		}
	}
}

func TestResolve_Ambiguous_SameLabel(t *testing.T) {
	recs := []PeerRecord{labelRecord("_abc123", "default", "mt0", 1), labelRecord("_abc123", "default", "", 2)}
	_, err := Resolve(recs, "_abc123", false)
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
	got, err := Resolve(recs, "x1", false)
	if err != nil || got.Agent != nil {
		t.Fatalf("got %+v %v, want the tmux row via tier 2", got, err)
	}
}

// --- Resolve: not found ----------------------------------------------------

func TestResolve_UnknownSessionReturnsErrNotFound(t *testing.T) {
	records := []PeerRecord{
		{SessionName: "alpha", SessionCode: "a1"},
	}
	_, err := Resolve(records, "nonexistent", false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolve_EmptySessionReturnsErrNotFound(t *testing.T) {
	records := []PeerRecord{
		{SessionName: "alpha", SessionCode: "a1"},
	}
	_, err := Resolve(records, "", false)
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
