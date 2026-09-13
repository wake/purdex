package peers

import (
	"errors"
	"testing"
)

// --- helpers -------------------------------------------------------------

func ccRecord(name, code, peerName string) PeerRecord {
	return PeerRecord{
		SessionName: name,
		SessionCode: code,
		Agent:       &AgentInfo{Type: "cc", PeerName: peerName},
	}
}

// --- Resolve: each tier resolves -----------------------------------------

func TestResolve_NameTier(t *testing.T) {
	records := []PeerRecord{
		{SessionName: "alpha", SessionCode: "a1"},
		{SessionName: "beta", SessionCode: "b1"},
	}
	got, err := Resolve(records, "beta")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.SessionName != "beta" {
		t.Fatalf("got %+v, want beta", got)
	}
}

func TestResolve_CodeTier(t *testing.T) {
	records := []PeerRecord{
		{SessionName: "alpha", SessionCode: "a1"},
		{SessionName: "beta", SessionCode: "b1"},
	}
	got, err := Resolve(records, "b1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.SessionCode != "b1" {
		t.Fatalf("got %+v, want code b1", got)
	}
}

func TestResolve_CCTier_TmuxHostedSession(t *testing.T) {
	records := []PeerRecord{
		{SessionName: "alpha", SessionCode: "a1"},
		ccRecord("beta", "b1", "worker-1"),
	}
	got, err := Resolve(records, "cc:worker-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.SessionName != "beta" {
		t.Fatalf("got %+v, want beta (tmux-hosted cc session)", got)
	}
}

// --- Resolve: tier precedence and ambiguity -------------------------------

func TestResolve_NameTierBeatsCodeTier(t *testing.T) {
	// One record's Name equals another record's Code: "shared".
	records := []PeerRecord{
		{SessionName: "shared", SessionCode: "x1"},
		{SessionName: "other", SessionCode: "shared"},
	}
	got, err := Resolve(records, "shared")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.SessionName != "shared" || got.SessionCode != "x1" {
		t.Fatalf("got %+v, want the name-tier match (SessionName=shared, SessionCode=x1)", got)
	}
}

func TestResolve_AmbiguousAtNameTierNeverFallsToCodeTier(t *testing.T) {
	// Two records share Name "dup"; a third record's Code equals "dup" too,
	// but since the name tier already has >=1 match, the code tier (and
	// that third record) must never be consulted.
	records := []PeerRecord{
		{SessionName: "dup", SessionCode: "c1"},
		{SessionName: "dup", SessionCode: "c2"},
		{SessionName: "third", SessionCode: "dup"},
	}
	_, err := Resolve(records, "dup")
	var ambErr *AmbiguousError
	if !errors.As(err, &ambErr) {
		t.Fatalf("err = %v, want *AmbiguousError", err)
	}
	if ambErr.Session != "dup" {
		t.Fatalf("ambErr.Session = %q, want dup", ambErr.Session)
	}
	if len(ambErr.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2 (the two name-tier matches only)", len(ambErr.Candidates))
	}
	for _, c := range ambErr.Candidates {
		if c.SessionName != "dup" {
			t.Fatalf("candidate %+v is not a name-tier match", c)
		}
	}
}

func TestResolve_NameTierBeatsCCTier(t *testing.T) {
	// A record's SessionName is literally "cc:foo"; another record's
	// Agent.PeerName is "foo". The name tier must win over the cc: tier.
	nameRecord := PeerRecord{SessionName: "cc:foo", SessionCode: "n1"}
	records := []PeerRecord{
		nameRecord,
		ccRecord("other", "o1", "foo"),
	}
	got, err := Resolve(records, "cc:foo")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.SessionCode != "n1" {
		t.Fatalf("got %+v, want the name-tier match (SessionCode=n1)", got)
	}
}

func TestResolve_NameTierAmbiguousNeverFallsToCCTier(t *testing.T) {
	// Two records share SessionName "cc:foo"; a third has Agent.PeerName
	// "foo". Ambiguity at the name tier must win; the cc: tier (and that
	// third record) must never be consulted.
	records := []PeerRecord{
		{SessionName: "cc:foo", SessionCode: "n1"},
		{SessionName: "cc:foo", SessionCode: "n2"},
		ccRecord("other", "o1", "foo"),
	}
	_, err := Resolve(records, "cc:foo")
	var ambErr *AmbiguousError
	if !errors.As(err, &ambErr) {
		t.Fatalf("err = %v, want *AmbiguousError", err)
	}
	if len(ambErr.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2 (the two name-tier matches only)", len(ambErr.Candidates))
	}
	for _, c := range ambErr.Candidates {
		if c.SessionName != "cc:foo" {
			t.Fatalf("candidate %+v is not a name-tier match", c)
		}
	}
}

func TestResolve_CCTier_SkipsProxyRows(t *testing.T) {
	records := []PeerRecord{
		{
			Agent: &AgentInfo{Type: "proxy", PeerName: "helper"},
		},
		ccRecord("realsession", "r1", "helper"),
	}
	got, err := Resolve(records, "cc:helper")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.SessionName != "realsession" {
		t.Fatalf("got %+v, want realsession (proxy row skipped)", got)
	}
}

func TestResolve_CCTier_AmbiguousWhenTwoRecordsShareName(t *testing.T) {
	records := []PeerRecord{
		ccRecord("one", "o1", "dup-peer"),
		ccRecord("two", "t1", "dup-peer"),
	}
	_, err := Resolve(records, "cc:dup-peer")
	var ambErr *AmbiguousError
	if !errors.As(err, &ambErr) {
		t.Fatalf("err = %v, want *AmbiguousError", err)
	}
	if len(ambErr.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2", len(ambErr.Candidates))
	}
}

// --- Resolve: not found ----------------------------------------------------

func TestResolve_UnknownSessionReturnsErrNotFound(t *testing.T) {
	records := []PeerRecord{
		{SessionName: "alpha", SessionCode: "a1"},
	}
	_, err := Resolve(records, "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolve_EmptySessionReturnsErrNotFound(t *testing.T) {
	records := []PeerRecord{
		{SessionName: "alpha", SessionCode: "a1"},
	}
	_, err := Resolve(records, "")
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
