package peers

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
)

// --- helpers -----------------------------------------------------------

func mustMarshalMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return m
}

// --- Rule 1: one record per session, sorted, Address/Host -------------

func TestBuild_SortedBySessionNameThenCode(t *testing.T) {
	in := BuildInput{
		HostID: "mini-lab:278cbm",
		Alias:  "mini-lab",
		Sessions: []SessionSummary{
			{Code: "b2", Name: "zeta", Cwd: "/z", TmuxInstance: "1:1"},
			{Code: "a1", Name: "alpha", Cwd: "/a", TmuxInstance: "2:2"},
			{Code: "c1", Name: "alpha", Cwd: "/a2", TmuxInstance: "3:3"}, // tie on Name, sort by Code
		},
		Unresolved: map[string]bool{"b2": true, "a1": true, "c1": true},
	}
	got := Build(in)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantOrder := []string{"a1", "c1", "b2"} // alpha/a1, alpha/c1, zeta/b2
	for i, code := range wantOrder {
		if got[i].SessionCode != code {
			t.Errorf("record[%d].SessionCode = %q, want %q", i, got[i].SessionCode, code)
		}
	}
	// Address / Host construction (rule 1): a session row with no cc agent
	// uses the tmux: form (spec §3.4).
	if got[2].Address != "mini-lab/tmux:zeta" {
		t.Errorf("Address = %q, want mini-lab/tmux:zeta", got[2].Address)
	}
	if got[2].Host != "mini-lab" {
		t.Errorf("Host = %q, want mini-lab", got[2].Host)
	}
	if got[2].HostID != "mini-lab:278cbm" {
		t.Errorf("HostID = %q, want mini-lab:278cbm", got[2].HostID)
	}
}

// --- Rule 2: Unresolved / no owner -------------------------------------

func TestBuild_UnresolvedSession(t *testing.T) {
	in := BuildInput{
		Alias:      "mini-lab",
		Sessions:   []SessionSummary{{Code: "s1", Name: "s1name"}},
		Unresolved: map[string]bool{"s1": true},
	}
	got := Build(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Agent != nil {
		t.Errorf("Agent = %+v, want nil", got[0].Agent)
	}
	if got[0].Reason != "" {
		t.Errorf("Reason = %q, want empty", got[0].Reason)
	}
	if got[0].Deliverable {
		t.Errorf("Deliverable = true, want false")
	}
}

func TestBuild_NoOwner_NoAgent(t *testing.T) {
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "aigora3"}},
		Owners:   map[string]Owner{},
	}
	got := Build(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Agent != nil {
		t.Errorf("Agent = %+v, want nil", got[0].Agent)
	}
	if got[0].Reason != "no_agent" {
		t.Errorf("Reason = %q, want no_agent", got[0].Reason)
	}
	if got[0].Deliverable {
		t.Errorf("Deliverable = true, want false")
	}
}

// --- Rule 3: owner type not cc -----------------------------------------

func TestBuild_OwnerNotCC(t *testing.T) {
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "codexy"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "codex", SessionID: "codex-sess-1", Status: "busy"},
		},
	}
	got := Build(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	r := got[0]
	if r.Agent == nil {
		t.Fatalf("Agent = nil, want non-nil")
	}
	want := AgentInfo{Type: "codex", SessionID: "codex-sess-1", Status: "busy", Version: ""}
	if *r.Agent != want {
		t.Errorf("Agent = %+v, want %+v", *r.Agent, want)
	}
	if r.Deliverable {
		t.Errorf("Deliverable = true, want false")
	}
	if r.Reason != "not_cc" {
		t.Errorf("Reason = %q, want not_cc", r.Reason)
	}
}

// --- Rule 4: owner type cc, candidate resolution ------------------------

func TestBuild_CC_ZeroCandidates_InboxDead(t *testing.T) {
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: "sess-x", Status: "idle"},
		},
		Entries: []Entry{}, // no live entry for sess-x
	}
	got := Build(in)
	r := got[0]
	if r.Agent == nil {
		t.Fatalf("Agent = nil, want non-nil")
	}
	want := AgentInfo{Type: "cc", SessionID: "sess-x", Status: "idle", Version: ""}
	if *r.Agent != want {
		t.Errorf("Agent = %+v, want %+v", *r.Agent, want)
	}
	if r.Deliverable {
		t.Errorf("Deliverable = true, want false")
	}
	if r.Reason != "inbox_dead" {
		t.Errorf("Reason = %q, want inbox_dead", r.Reason)
	}
}

// TestBuild_CC_IsProxyEntry_ExcludedFromCandidates pins D9: an entry
// classified as a proxy via Entry.IsProxy (not via the legacy ProxyPIDs
// map) must be excluded from session candidates exactly like a ProxyPIDs
// entry — e.IsProxy || in.ProxyPIDs[e.PID] in the candidates filter.
func TestBuild_CC_IsProxyEntry_ExcludedFromCandidates(t *testing.T) {
	entry := Entry{PID: 100, SessionID: "sess-x", Name: "helper", IsProxy: true}
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: "sess-x", Status: "idle"},
		},
		Entries:   []Entry{entry},
		ProxyPIDs: map[int]bool{}, // deliberately NOT set here — IsProxy alone must suffice
	}
	got := Build(in)
	r := got[0]
	if r.Deliverable {
		t.Errorf("Deliverable = true, want false (IsProxy entry excluded from candidates)")
	}
	if r.Reason != "inbox_dead" {
		t.Errorf("Reason = %q, want inbox_dead (zero non-proxy candidates)", r.Reason)
	}
}

func TestBuild_CC_OneCandidate_Deliverable(t *testing.T) {
	entry := Entry{
		PID: 100, SessionID: "sess-x", Name: "purdex-1", NameSource: "derived",
		Cwd: "/w", Tmux: "mt1:@1.%1", Inbox: "/tmp/1.sock",
		ProcStart: "Sun Sep 13 15:22:36 2026", Version: "2.1.270", Status: "busy",
	}
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1", Cwd: "/w", TmuxInstance: "t1"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%1", Status: "idle"},
		},
		Entries: []Entry{entry},
	}
	got := Build(in)
	r := got[0]
	wantAgent := AgentInfo{
		Type: "cc", SessionID: "sess-x", PeerName: "purdex-1", PID: 100,
		ProcStart: "Sun Sep 13 15:22:36 2026", Inbox: "/tmp/1.sock",
		Status: "busy", Version: "2.1.270",
	}
	if r.Agent == nil || *r.Agent != wantAgent {
		t.Errorf("Agent = %+v, want %+v", r.Agent, wantAgent)
	}
	if !r.Deliverable {
		t.Errorf("Deliverable = false, want true")
	}
	if r.Reason != "" {
		t.Errorf("Reason = %q, want empty", r.Reason)
	}
}

// Resumed session: two live entries, same SessionID, panes %10 and %11,
// owner pane %10 => %10 chosen, deliverable.
func TestBuild_CC_TwoCandidates_PaneDisambiguates(t *testing.T) {
	e10 := Entry{PID: 10, SessionID: "sess-x", Name: "old", Tmux: "mt1:@1.%10"}
	e11 := Entry{PID: 11, SessionID: "sess-x", Name: "new", Tmux: "mt1:@1.%11"}
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%10", Status: "idle"},
		},
		Entries: []Entry{e10, e11},
	}
	got := Build(in)
	r := got[0]
	if r.Agent == nil || r.Agent.PID != 10 {
		t.Fatalf("Agent = %+v, want PID 10", r.Agent)
	}
	if !r.Deliverable {
		t.Errorf("Deliverable = false, want true")
	}
	if r.Reason != "" {
		t.Errorf("Reason = %q, want empty", r.Reason)
	}
}

// Two live entries same SessionID, both pane %10 => ambiguous.
func TestBuild_CC_TwoCandidates_SamePane_Ambiguous(t *testing.T) {
	e1 := Entry{PID: 10, SessionID: "sess-x", Tmux: "mt1:@1.%10"}
	e2 := Entry{PID: 11, SessionID: "sess-x", Tmux: "mt2:@1.%10"}
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1"}, {Code: "s2", Name: "mt2"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%10", Status: "idle"},
		},
		Entries: []Entry{e1, e2},
	}
	got := Build(in)
	var r PeerRecord
	for _, rec := range got {
		if rec.SessionCode == "s1" {
			r = rec
		}
	}
	if r.Reason != "ambiguous" {
		t.Errorf("Reason = %q, want ambiguous", r.Reason)
	}
	if r.Deliverable {
		t.Errorf("Deliverable = true, want false")
	}
	want := AgentInfo{Type: "cc", SessionID: "sess-x", Status: "idle", Version: ""}
	if r.Agent == nil || *r.Agent != want {
		t.Errorf("Agent = %+v, want %+v", r.Agent, want)
	}
}

// No pane match => ambiguous.
func TestBuild_CC_TwoCandidates_NoPaneMatch_Ambiguous(t *testing.T) {
	e1 := Entry{PID: 10, SessionID: "sess-x", Tmux: "mt1:@1.%20"}
	e2 := Entry{PID: 11, SessionID: "sess-x", Tmux: "mt1:@1.%21"}
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%10", Status: "idle"},
		},
		Entries: []Entry{e1, e2},
	}
	got := Build(in)
	r := got[0]
	if r.Reason != "ambiguous" {
		t.Errorf("Reason = %q, want ambiguous", r.Reason)
	}
	if r.Deliverable {
		t.Errorf("Deliverable = true, want false")
	}
}

// Entry with matching pane but different SessionID => inbox_dead for the
// session (rule 6, no pane fallback), and that entry gets its own entry
// row only if its TmuxSessionName() is not a listed session.
func TestBuild_CC_PaneMatchWrongSessionID_InboxDeadAndOutsideRow(t *testing.T) {
	entry := Entry{PID: 99, SessionID: "other-sess", Name: "stray", Tmux: "elsewhere:@1.%10"}
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%10", Status: "idle"},
		},
		Entries: []Entry{entry},
	}
	got := Build(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (session row + outside row)", len(got))
	}
	var sessionRow, outsideRow *PeerRecord
	for i := range got {
		if got[i].SessionCode == "s1" {
			sessionRow = &got[i]
		} else {
			outsideRow = &got[i]
		}
	}
	if sessionRow == nil || sessionRow.Reason != "inbox_dead" {
		t.Fatalf("sessionRow = %+v, want reason inbox_dead", sessionRow)
	}
	if outsideRow == nil {
		t.Fatalf("outsideRow missing")
	}
	// v2: the entry's own tmux session ("elsewhere") is not a listed
	// session either way, but the row is now an entry row (RowKind) with
	// a label+suffix address, not the retired "cc:<name>" form.
	// Expectation updated: "other-sess" is the only conversation in the
	// population and its entries agree on tmux "elsewhere", so its default
	// is now that name rather than the v2 hash (spec §3.3).
	wantAddr := "mini-lab/elsewhere:elsewhere-stray"
	if outsideRow.RowKind != "entry" || outsideRow.Address != wantAddr {
		t.Errorf("outsideRow rowkind/address = %q/%q, want entry/%q", outsideRow.RowKind, outsideRow.Address, wantAddr)
	}
	if !outsideRow.Deliverable || outsideRow.Reason != "" {
		t.Errorf("outsideRow deliverable/reason = %v/%q, want true/empty", outsideRow.Deliverable, outsideRow.Reason)
	}
}

// --- Rule 5: outside-tmux rows + proxy -----------------------------------

func TestBuild_OutsideTmuxRow(t *testing.T) {
	entry := Entry{
		PID: 200, SessionID: "sess-y", Name: "outside-1", Cwd: "/out",
		Tmux: "", Inbox: "/tmp/200.sock", ProcStart: "Mon Sep 14 09:00:00 2026",
		Version: "2.1.270", Status: "idle",
	}
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1"}},
		Entries:  []Entry{entry},
	}
	got := Build(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	r := got[1]
	if r.SessionCode != "" || r.SessionName != "" || r.TmuxInstance != "" {
		t.Errorf("outside row session fields not empty: %+v", r)
	}
	wantAddr := "mini-lab/" + DefaultLabel("sess-y") + ":outside-1"
	if r.Address != wantAddr {
		t.Errorf("Address = %q, want %q", r.Address, wantAddr)
	}
	if r.Cwd != "/out" {
		t.Errorf("Cwd = %q, want /out", r.Cwd)
	}
	if !r.Deliverable || r.Reason != "" {
		t.Errorf("deliverable/reason = %v/%q, want true/empty", r.Deliverable, r.Reason)
	}
	wantAgent := AgentInfo{
		Type: "cc", SessionID: "sess-y", PeerName: "outside-1", PID: 200,
		ProcStart: "Mon Sep 14 09:00:00 2026", Inbox: "/tmp/200.sock",
		Status: "idle", Version: "2.1.270",
	}
	if r.Agent == nil || *r.Agent != wantAgent {
		t.Errorf("Agent = %+v, want %+v", r.Agent, wantAgent)
	}
}

func TestBuild_OutsideTmuxRow_Proxy(t *testing.T) {
	entry := Entry{PID: 300, SessionID: "sess-z", Name: "helper-1", Tmux: ""}
	in := BuildInput{
		Alias:     "mini-lab",
		Entries:   []Entry{entry},
		ProxyPIDs: map[int]bool{300: true},
	}
	got := Build(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	r := got[0]
	if r.Agent == nil || r.Agent.Type != "proxy" {
		t.Fatalf("Agent.Type = %v, want proxy", r.Agent)
	}
	if r.Agent.Status != "proxy" {
		t.Errorf("Agent.Status = %q, want proxy (spec §4.2)", r.Agent.Status)
	}
	if r.Deliverable {
		t.Errorf("Deliverable = true, want false")
	}
	if r.Reason != "proxy" {
		t.Errorf("Reason = %q, want proxy", r.Reason)
	}
}

// TestBuild_OutsideTmuxRow_IsProxy pins D9: an entry classified as a proxy
// via Entry.IsProxy (not the legacy ProxyPIDs map) produces the same
// non-deliverable "proxy" outside row as a ProxyPIDs entry, and is excluded
// from session candidates via the same check.
func TestBuild_OutsideTmuxRow_IsProxy(t *testing.T) {
	entry := Entry{PID: 300, SessionID: "sess-z", Name: "helper-1", Tmux: "", IsProxy: true}
	in := BuildInput{
		Alias:   "mini-lab",
		Entries: []Entry{entry},
	}
	got := Build(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	r := got[0]
	if r.Agent == nil || r.Agent.Type != "proxy" {
		t.Fatalf("Agent.Type = %v, want proxy", r.Agent)
	}
	if r.Agent.Status != "proxy" {
		t.Errorf("Agent.Status = %q, want proxy", r.Agent.Status)
	}
	if r.Deliverable {
		t.Errorf("Deliverable = true, want false")
	}
	if r.Reason != "proxy" {
		t.Errorf("Reason = %q, want proxy", r.Reason)
	}
}

// Outside rows sorted after tmux rows, by PeerName (ties by PID).
func TestBuild_OutsideTmuxRows_SortedByPeerNameThenPID(t *testing.T) {
	eZeta := Entry{PID: 2, SessionID: "s2", Name: "zeta", Tmux: ""}
	eAlpha1 := Entry{PID: 20, SessionID: "s3", Name: "alpha", Tmux: ""}
	eAlpha2 := Entry{PID: 10, SessionID: "s4", Name: "alpha", Tmux: ""}
	in := BuildInput{
		Alias:   "mini-lab",
		Entries: []Entry{eZeta, eAlpha1, eAlpha2},
	}
	got := Build(in)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Agent.PID != 10 || got[1].Agent.PID != 20 || got[2].Agent.PID != 2 {
		t.Errorf("PIDs = [%d,%d,%d], want [10,20,2]", got[0].Agent.PID, got[1].Agent.PID, got[2].Agent.PID)
	}
}

// Entry with Tmux:"" that rule 4 consumed (the single candidate) never also
// produces an entry row: the entry appears exactly once, as the session row.
func TestBuild_EntryConsumedByRule4_NeverAlsoAppearsAsOutsideRow(t *testing.T) {
	entry := Entry{PID: 5, SessionID: "sess-x", Name: "purdex-5", Tmux: ""}
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "", Status: "idle"},
		},
		Entries: []Entry{entry},
	}
	got := Build(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (session row only, no duplicate entry row)", len(got))
	}
	sessionRow := got[0]
	if sessionRow.SessionCode != "s1" || !sessionRow.Deliverable || sessionRow.Agent == nil || sessionRow.Agent.PID != 5 {
		t.Fatalf("sessionRow = %+v, want deliverable session row via entry PID 5", sessionRow)
	}

	// (c) The entry it was built from is not duplicated into a separate
	// outside row: len(got) == 1 above already proves it (Resolve's
	// "cc:<name>" tier is retired in Task 6, so there is no lookup left
	// to re-demonstrate it through).
}

// TestBuild_TwoSessionsSameOwnerSessionID_EntryConsumedOnce pins Item 4: two
// tmux sessions whose resolved owners both carry the SAME SessionID (a pane
// that moved mid-request, so two session rows would otherwise independently
// pick the one live entry as "the" single candidate) must not both select
// it. The first session in list order wins it (deliverable, full agent
// info); the second must fall back to the owner-only agent with
// Deliverable=false, Reason="ambiguous" rather than also claiming the entry
// as deliverable. The entry appears exactly once in the whole output, and
// the winning row is the first (session s1) — found directly by RowKind
// and PID here, since Resolve's "cc:<name>" tier is retired in Task 6.
func TestBuild_TwoSessionsSameOwnerSessionID_EntryConsumedOnce(t *testing.T) {
	entry := Entry{
		PID: 100, SessionID: "sess-x", Name: "purdex-1", NameSource: "derived",
		Cwd: "/w", Tmux: "mt1:@1.%1", Inbox: "/tmp/1.sock",
		ProcStart: "Sun Sep 13 15:22:36 2026", Version: "2.1.270", Status: "busy",
	}
	in := BuildInput{
		Alias: "mini-lab",
		Sessions: []SessionSummary{
			{Code: "s1", Name: "mt1"},
			{Code: "s2", Name: "mt2"},
		},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%1", Status: "idle"},
			"s2": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%1", Status: "idle"},
		},
		Entries: []Entry{entry},
	}
	got := Build(in)

	var s1Rec, s2Rec *PeerRecord
	for i := range got {
		switch got[i].SessionCode {
		case "s1":
			s1Rec = &got[i]
		case "s2":
			s2Rec = &got[i]
		}
	}
	if s1Rec == nil || s2Rec == nil {
		t.Fatalf("got = %+v, want both s1 and s2 session rows", got)
	}

	if !s1Rec.Deliverable {
		t.Errorf("s1 Deliverable = false, want true")
	}
	if s1Rec.Agent == nil || s1Rec.Agent.PeerName != "purdex-1" || s1Rec.Agent.PID != 100 {
		t.Errorf("s1 Agent = %+v, want the live entry (PeerName purdex-1, PID 100)", s1Rec.Agent)
	}

	if s2Rec.Deliverable {
		t.Errorf("s2 Deliverable = true, want false")
	}
	if s2Rec.Reason != "ambiguous" {
		t.Errorf("s2 Reason = %q, want ambiguous", s2Rec.Reason)
	}
	wantS2Agent := AgentInfo{Type: "cc", SessionID: "sess-x", Status: "idle", Version: ""}
	if s2Rec.Agent == nil || *s2Rec.Agent != wantS2Agent {
		t.Errorf("s2 Agent = %+v, want owner-only fallback %+v", s2Rec.Agent, wantS2Agent)
	}

	count := 0
	for _, r := range got {
		if r.Agent != nil && r.Agent.PID == 100 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("entry PID 100 appears %d times across records, want exactly 1", count)
	}

	if s1Rec.RowKind != "session" || !s1Rec.Deliverable || s1Rec.Agent == nil || s1Rec.Agent.PID != 100 {
		t.Fatalf("s1 = %+v, want the winning deliverable session row (PID 100)", s1Rec)
	}
	// Both rows carry the same default label (Item 4's ambiguity is now
	// visible in the label too): Resolve (Task 6) reports the pair
	// ambiguous by label+suffix, not by this row's SessionCode.
	// Expectation updated: sess-x's one live entry is in tmux "mt1" and no
	// other conversation competes for that name, so the shared default is
	// "mt1" rather than the v2 hash. The point of the assertion — that BOTH
	// rows read the same label, the s2 ambiguous fallback included — is
	// unchanged, and is exactly spec §3.2's one-conversation-one-label rule.
	if s1Rec.Label != "mt1" || s2Rec.Label != "mt1" {
		t.Errorf("labels = %q/%q, want both %q", s1Rec.Label, s2Rec.Label, "mt1")
	}
}

// Entries that were candidates but NOT chosen by rule 4 (ambiguous case) are
// not consumed and still get outside rows when their tmux name is unlisted.
func TestBuild_AmbiguousCandidates_StillGetOutsideRows(t *testing.T) {
	e1 := Entry{PID: 10, SessionID: "sess-x", Name: "one", Tmux: "elsewhere:@1.%10"}
	e2 := Entry{PID: 11, SessionID: "sess-x", Name: "two", Tmux: "elsewhere:@1.%10"}
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%10", Status: "idle"},
		},
		Entries: []Entry{e1, e2},
	}
	got := Build(in)
	// session row (ambiguous) + two outside rows (neither entry consumed).
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (session row + 2 outside rows)", len(got))
	}
	var sessionRow *PeerRecord
	var outsideAddrs []string
	for i := range got {
		if got[i].SessionCode == "s1" {
			sessionRow = &got[i]
		} else {
			outsideAddrs = append(outsideAddrs, got[i].Address)
		}
	}
	if sessionRow == nil || sessionRow.Reason != "ambiguous" {
		t.Fatalf("sessionRow = %+v, want reason ambiguous", sessionRow)
	}
	// Expectation updated: both entries belong to sess-x and agree on tmux
	// "elsewhere" (rule 1 is satisfied — two processes of ONE conversation
	// are not competitors), and nothing else is in the population, so the
	// shared default is "elsewhere" rather than the v2 hash.
	label := "elsewhere"
	wantAddrs := map[string]bool{
		"mini-lab/" + label + ":elsewhere-one": true,
		"mini-lab/" + label + ":elsewhere-two": true,
	}
	if len(outsideAddrs) != 2 || !wantAddrs[outsideAddrs[0]] || !wantAddrs[outsideAddrs[1]] {
		t.Fatalf("outsideAddrs = %v, want both entry-row addresses in %v", outsideAddrs, wantAddrs)
	}
}

// --- Empty input ---------------------------------------------------------

func TestBuild_Empty(t *testing.T) {
	got := Build(BuildInput{})
	if got == nil {
		t.Fatalf("got nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("Marshal = %s, want []", b)
	}
}

// --- JSON key assertions --------------------------------------------------

func TestBuild_JSON_NotCCRow_VersionEmptyPresent(t *testing.T) {
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "codexy"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "codex", SessionID: "codex-sess-1", Status: "busy"},
		},
	}
	got := Build(in)
	m := mustMarshalMap(t, got[0])
	agent, ok := m["agent"].(map[string]any)
	if !ok {
		t.Fatalf("agent not an object: %+v", m["agent"])
	}
	v, present := agent["version"]
	if !present {
		t.Fatalf("agent.version key missing: %+v", agent)
	}
	if v != "" {
		t.Errorf("agent.version = %v, want empty string", v)
	}
}

func TestBuild_JSON_ShellRow_AgentNullReasonNoAgent(t *testing.T) {
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "aigora3"}},
	}
	got := Build(in)
	m := mustMarshalMap(t, got[0])
	if m["agent"] != nil {
		t.Errorf("agent = %v, want null", m["agent"])
	}
	if m["reason"] != "no_agent" {
		t.Errorf("reason = %v, want no_agent", m["reason"])
	}
}

func TestBuild_JSON_UnresolvedRow_AgentNullReasonEmpty(t *testing.T) {
	in := BuildInput{
		Alias:      "mini-lab",
		Sessions:   []SessionSummary{{Code: "s1", Name: "mt1"}},
		Unresolved: map[string]bool{"s1": true},
	}
	got := Build(in)
	m := mustMarshalMap(t, got[0])
	if m["agent"] != nil {
		t.Errorf("agent = %v, want null", m["agent"])
	}
	reason, present := m["reason"]
	if !present {
		t.Fatalf("reason key missing")
	}
	if reason != "" {
		t.Errorf("reason = %v, want empty string", reason)
	}
}

func TestBuild_JSON_EveryRecordHasCoreKeys(t *testing.T) {
	in := BuildInput{
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1"}},
		Entries:  []Entry{{PID: 1, SessionID: "sx", Name: "outside", Tmux: ""}},
	}
	got := Build(in)
	for _, rec := range got {
		m := mustMarshalMap(t, rec)
		for _, key := range []string{"session_code", "session_name", "tmux_instance", "reason", "row_kind", "label", "label_source", "label_rev", "suffix"} {
			if _, present := m[key]; !present {
				t.Errorf("record %+v missing key %q", rec, key)
			}
		}
	}
}

// --- Golden reproduction of the spec's mlab state -------------------------

func TestBuild_GoldenMlabReproduction(t *testing.T) {
	mt1Entry := Entry{
		PID:        76973,
		SessionID:  "fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c",
		Name:       "purdex-47",
		NameSource: "derived",
		Cwd:        "/Users/wake/Workspace/wake/purdex",
		Tmux:       "mt1:@10.%10",
		Inbox:      "/tmp/cc-socks/76973.sock",
		ProcStart:  "Sun Sep 13 15:22:36 2026",
		Version:    "2.1.270",
		Status:     "busy",
	}
	outsideEntry := Entry{
		PID:       76980,
		SessionID: "b1e5c9a0-0000-0000-0000-000000000000",
		Name:      "scratch-1",
		Cwd:       "/Users/wake/Workspace/wake/purdex",
		Tmux:      "",
		Inbox:     "/tmp/cc-socks/76980.sock",
		ProcStart: "Sun Sep 13 16:00:00 2026",
		Version:   "2.1.270",
		Status:    "idle",
	}

	in := BuildInput{
		HostID: "mini-lab:278cbm",
		Alias:  "mini-lab",
		Sessions: []SessionSummary{
			{Code: "02ybs5", Name: "mt1", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxInstance: "6901:1789205013"},
			{Code: "ai3xyz", Name: "aigora3", Cwd: "/Users/wake/Workspace/wake/aigora3", TmuxInstance: "6901:1789205020"},
			{Code: "cdxabc", Name: "codexy", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxInstance: "6901:1789205030"},
		},
		Owners: map[string]Owner{
			"02ybs5": {AgentType: "cc", SessionID: mt1Entry.SessionID, TmuxPaneID: "%10", Status: "busy"},
			"cdxabc": {AgentType: "codex", SessionID: "codex-sess-1", Status: "busy"},
			// "ai3xyz" (aigora3, a plain shell) has no owner entry.
		},
		Entries: []Entry{mt1Entry, outsideEntry},
	}

	got := Build(in)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4 (mt1, aigora3, codexy, outside)", len(got))
	}

	byAddress := map[string]PeerRecord{}
	for _, r := range got {
		byAddress[r.Address] = r
	}

	// Expectation updated: mt1Entry is the only live entry of its
	// conversation and sits in tmux "mt1", uncontested, so the golden
	// address is now the readable one this change exists to produce.
	// outsideEntry below is deliberately left on its hash: its Tmux field
	// is "", so it is not in tmux and has no place to be named after.
	mt1 := byAddress["mini-lab/mt1:mt1-purdex-47"]
	if !mt1.Deliverable || mt1.Reason != "" || mt1.Agent == nil {
		t.Fatalf("mt1 = %+v, want deliverable cc row", mt1)
	}
	if mt1.Agent.PID != 76973 || mt1.Agent.Version != "2.1.270" || mt1.Agent.Status != "busy" {
		t.Errorf("mt1.Agent = %+v", mt1.Agent)
	}
	if mt1.TmuxInstance != "6901:1789205013" {
		t.Errorf("mt1.TmuxInstance = %q", mt1.TmuxInstance)
	}

	aigora3 := byAddress["mini-lab/tmux:aigora3"]
	if aigora3.Agent != nil || aigora3.Reason != "no_agent" || aigora3.Deliverable {
		t.Errorf("aigora3 = %+v, want no_agent shell row", aigora3)
	}

	codexy := byAddress["mini-lab/tmux:codexy"]
	if codexy.Agent == nil || codexy.Agent.Type != "codex" || codexy.Agent.Status != "busy" {
		t.Errorf("codexy = %+v, want codex agent status busy", codexy)
	}
	if codexy.Deliverable || codexy.Reason != "not_cc" {
		t.Errorf("codexy deliverable/reason = %v/%q, want false/not_cc", codexy.Deliverable, codexy.Reason)
	}

	outside := byAddress["mini-lab/"+DefaultLabel(outsideEntry.SessionID)+":scratch-1"]
	if outside.Agent == nil || !outside.Deliverable || outside.Reason != "" {
		t.Errorf("outside = %+v, want deliverable outside-tmux row", outside)
	}
	if outside.SessionCode != "" || outside.SessionName != "" || outside.TmuxInstance != "" {
		t.Errorf("outside session fields not empty: %+v", outside)
	}
}

// --- Labels, suffix, entry rows (Peer Address v2, Task 4) -----------------

// TestBuild_LabelsAndAddresses pins the address rules of spec §3.4: a cc
// row (session or entry) reads "<alias>/<label>:<suffix>" with the suffix
// derived from the row's own registry tmux field; a session row with no cc
// agent reads "<alias>/tmux:<name>"; a user label (with its Rev) wins over
// the default label.
func TestBuild_LabelsAndAddresses(t *testing.T) {
	in := BuildInput{
		HostID: "h:1", Alias: "mini-lab",
		Sessions: []SessionSummary{{Code: "c1", Name: "mt0", Cwd: "/w"}, {Code: "c2", Name: "shell"}},
		Owners: map[string]Owner{
			"c1": {AgentType: "cc", SessionID: "sid-1", TmuxPaneID: "%1"},
		},
		Entries: []Entry{
			{PID: 10, SessionID: "sid-1", Name: "purdex-49", Tmux: "mt0:@1.%1", Inbox: "/s/10"},
			{PID: 20, SessionID: "sid-2", Name: "purdex-3f", Tmux: "", Inbox: "/s/20"}, // Desktop
		},
		Labels: map[string]LabelInfo{"sid-1": {Label: "purdex-dev", Rev: 7}},
	}
	recs := Build(in)
	byAddr := map[string]PeerRecord{}
	for _, r := range recs {
		byAddr[r.Address] = r
	}
	dev, ok := byAddr["mini-lab/purdex-dev:mt0-purdex-49"]
	if !ok {
		t.Fatalf("no dev row; addresses: %v", keys(byAddr))
	}
	if dev.RowKind != "session" || dev.Label != "purdex-dev" || dev.LabelSource != LabelSourceUser || dev.LabelRev != 7 || dev.Suffix != "mt0-purdex-49" {
		t.Errorf("dev row = %+v", dev)
	}
	want := "mini-lab/" + DefaultLabel("sid-2") + ":purdex-3f"
	desk, ok := byAddr[want]
	if !ok {
		t.Fatalf("no desktop row %q; addresses: %v", want, keys(byAddr))
	}
	if desk.RowKind != "entry" || desk.LabelSource != LabelSourceDefault || desk.LabelRev != 0 || !desk.Deliverable {
		t.Errorf("desktop row = %+v", desk)
	}
	shell, ok := byAddr["mini-lab/tmux:shell"]
	if !ok || shell.Label != "" || shell.Suffix != "" || shell.LabelSource != "" {
		t.Errorf("shell row = %+v (ok=%v)", shell, ok)
	}
}

// TestBuild_EntryRow_NonOwnerEntryInsideListedSession pins the v2 delta
// over rule 5: two live processes of DIFFERENT conversations in one tmux
// session — the owner is consumed by the session row; the other gets an
// entry row even though its tmux field names a listed session.
func TestBuild_EntryRow_NonOwnerEntryInsideListedSession(t *testing.T) {
	in := BuildInput{
		Alias:    "a",
		Sessions: []SessionSummary{{Code: "c1", Name: "mt0"}},
		Owners:   map[string]Owner{"c1": {AgentType: "cc", SessionID: "sid-1", TmuxPaneID: "%1"}},
		Entries: []Entry{
			{PID: 10, SessionID: "sid-1", Name: "n1", Tmux: "mt0:@1.%1", Inbox: "/s/10"},
			{PID: 11, SessionID: "sid-9", Name: "n9", Tmux: "mt0:@1.%2", Inbox: "/s/11"},
		},
	}
	recs := Build(in)
	if len(recs) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(recs), recs)
	}
	if recs[1].RowKind != "entry" || recs[1].Agent.PID != 11 || recs[1].SessionName != "" || !recs[1].Deliverable {
		t.Errorf("entry row = %+v", recs[1])
	}
	// The suffix comes from the entry's own tmux field, so an entry row
	// inside tmux reads like its session row would.
	if recs[1].Suffix != "mt0-n9" || recs[1].Address != "a/"+DefaultLabel("sid-9")+":mt0-n9" {
		t.Errorf("entry row suffix/address = %q %q", recs[1].Suffix, recs[1].Address)
	}
}

// TestEntryRecord_MatchesBuild pins that EntryRecord is the exact function
// Build uses for an entry row — Task 7 relies on this for whoami/claim/
// release to render the identical address the listing shows.
func TestEntryRecord_MatchesBuild(t *testing.T) {
	e := Entry{PID: 11, SessionID: "sid-9", Name: "n9", Tmux: "mt0:@1.%2", Inbox: "/s/11", Cwd: "/w"}
	info := LabelInfo{Label: "purdex-tester", Rev: 3}
	labels := map[string]LabelInfo{"sid-9": info}
	// The direct call must be fed the SAME population Build resolves over,
	// which is what a self route has to do too (spec §3.4). Deriving it here
	// the way Build does is the point: a hand-written map would let the test
	// pass while proving nothing about the two paths agreeing.
	defaults := ResolveDefaultLabels([]Entry{e}, nil, labels)
	one := EntryRecord("a", "h:1", e, false, info, defaults)
	all := Build(BuildInput{HostID: "h:1", Alias: "a", Entries: []Entry{e}, Labels: labels})
	if len(all) != 1 || !reflect.DeepEqual(all[0], one) {
		t.Errorf("EntryRecord ≠ Build row:\n%+v\n%+v", one, all)
	}
	p := EntryRecord("a", "h:1", e, true, info, defaults)
	if p.Agent.Type != "proxy" || p.Deliverable || p.Reason != "proxy" || p.Address != "a/cc:n9" || p.Label != "" {
		t.Errorf("proxy entry record = %+v", p)
	}
}

// TestBuild_SameConversationTwoProcesses_TwoRowsSameLabel pins that same
// sessionId twice, no pane tiebreak, yields three rows sharing one label:
// the ambiguous session row (fallback agent) AND both entries get entry
// rows; Resolve (Task 6) reports them ambiguous with exactly the two
// ENTRY rows as candidates — the owner-fallback session row carries no
// live entry (PID 0) and is inert at tier 1 (spec §3.3, X2).
func TestBuild_SameConversationTwoProcesses_TwoRowsSameLabel(t *testing.T) {
	in := BuildInput{
		Alias:    "a",
		Sessions: []SessionSummary{{Code: "c1", Name: "mt0"}},
		Owners:   map[string]Owner{"c1": {AgentType: "cc", SessionID: "sid-1", TmuxPaneID: "%9"}},
		Entries: []Entry{
			{PID: 10, SessionID: "sid-1", Name: "n1", Tmux: "mt0:@1.%1", Inbox: "/s/10"},
			{PID: 11, SessionID: "sid-1", Name: "n1", Tmux: "mt0:@1.%2", Inbox: "/s/11"},
		},
	}
	recs := Build(in)
	if len(recs) != 3 {
		t.Fatalf("got %d rows, want 3", len(recs))
	}
	// Expectation updated: both processes are sid-1's and both report tmux
	// "mt0", so rule 1 holds and nothing competes — the three rows share
	// "mt0" instead of sharing the hash. Sharing ONE label across the
	// ambiguous session row and both entry rows is what this test pins, and
	// that is unchanged.
	for _, r := range recs {
		if r.Label != "mt0" {
			t.Errorf("row %s label = %q", r.Address, r.Label)
		}
	}
	if recs[0].Reason != "ambiguous" || recs[0].Deliverable {
		t.Errorf("session row = %+v", recs[0])
	}

	_, err := Resolve(recs, "mt0", ResolveSnapshot{})
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("Resolve = %v, want AmbiguousError", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("candidates = %d, want exactly the 2 entry rows: %+v", len(amb.Candidates), amb.Candidates)
	}
	for _, c := range amb.Candidates {
		if c.RowKind != "entry" || c.Agent == nil || c.Agent.PID == 0 {
			t.Errorf("candidate %+v is not a live entry row", c)
		}
	}
}

func keys(m map[string]PeerRecord) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- PeerRecord.WireAddress() -------------------------------------------

// TestPeerRecord_WireAddress pins WireAddress's shape: Label + ":" +
// Suffix for a row that carries a cc agent, "" for a row with none (Label
// == "" is the no-agent signal — spec §3.4).
func TestPeerRecord_WireAddress(t *testing.T) {
	labelled := PeerRecord{Label: "purdex-tester", Suffix: "purdex-3f"}
	if got := labelled.WireAddress(); got != "purdex-tester:purdex-3f" {
		t.Errorf("WireAddress() = %q, want purdex-tester:purdex-3f", got)
	}
	noAgent := PeerRecord{Label: "", Suffix: ""}
	if got := noAgent.WireAddress(); got != "" {
		t.Errorf("WireAddress() = %q, want \"\"", got)
	}
}

// --- Defaults resolved from the tmux session name (spec §3.3/§3.4) -------

// TestBuild_SessionRow_TmuxDerivedDefault pins the happy path: the one live
// entry of an unnamed conversation in tmux "purdex1" makes the whole row
// readable — label, label_source and address together.
func TestBuild_SessionRow_TmuxDerivedDefault(t *testing.T) {
	entry := Entry{PID: 100, SessionID: "sess-x", Name: "purdex-69", Tmux: "purdex1:@1.%1", Inbox: "/s/100"}
	in := BuildInput{
		HostID:   "h:1",
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "purdex1"}},
		Owners:   map[string]Owner{"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%1"}},
		Entries:  []Entry{entry},
	}
	got := Build(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (session row only)", len(got))
	}
	r := got[0]
	if r.Label != "purdex1" || r.LabelSource != LabelSourceDefault {
		t.Errorf("label/source = %q/%q, want purdex1/%s", r.Label, r.LabelSource, LabelSourceDefault)
	}
	if want := "mini-lab/purdex1:purdex1-purdex-69"; r.Address != want {
		t.Errorf("Address = %q, want %q", r.Address, want)
	}
}

// TestBuild_DefaultNeverShadowsADifferentRealTmuxSession is the round-2
// attack case (spec §11 finding 1, §4 row 7, §6.2 "shadowing"): tmux
// session "foo.bar" holds the one live agent, and a SEPARATE, real tmux
// session "foo-bar" — the string a sanitizer would have produced — has no
// agent at all. A caller typing "foo-bar" means the real "foo-bar", and
// must reach it at tier 2; if the agent in "foo.bar" were allowed to
// derive "foo-bar", tier 1 would answer first and silently deliver into a
// different tmux session. "foo.bar" does not qualify (§3.1), so nothing
// shadows the real session.
func TestBuild_DefaultNeverShadowsADifferentRealTmuxSession(t *testing.T) {
	agent := Entry{PID: 21, SessionID: "sid-dotted", Name: "n1", Tmux: "foo.bar:@1.%1", Inbox: "/s/21"}
	recs := Build(BuildInput{
		HostID: "h:1",
		Alias:  "mini-lab",
		Sessions: []SessionSummary{
			{Code: "c1", Name: "foo.bar"},
			{Code: "c2", Name: "foo-bar"}, // real, agentless, and NOT the agent's place
		},
		Owners:  map[string]Owner{"c1": {AgentType: "cc", SessionID: "sid-dotted", TmuxPaneID: "%1"}},
		Entries: []Entry{agent},
	})

	for _, r := range recs {
		if r.Label == "foo-bar" {
			t.Fatalf("row %+v claims the name of a different real tmux session", r)
		}
		if r.Agent != nil && r.Agent.SessionID == "sid-dotted" && r.Label != DefaultLabel("sid-dotted") {
			t.Errorf("agent row label = %q, want the v2 hash %q", r.Label, DefaultLabel("sid-dotted"))
		}
	}

	rec, err := Resolve(recs, "foo-bar", ResolveSnapshot{})
	if err != nil {
		t.Fatalf("Resolve(%q) = %v, want the real foo-bar session row", "foo-bar", err)
	}
	if rec.SessionName != "foo-bar" || rec.RowKind != "session" {
		t.Fatalf("Resolve(%q) = %+v, want the real \"foo-bar\" session row", "foo-bar", rec)
	}
	if rec.Agent != nil && rec.Agent.SessionID == "sid-dotted" {
		t.Fatalf("Resolve(%q) landed on the agent living in tmux \"foo.bar\"", "foo-bar")
	}
}

// TestBuild_EntryRow_TmuxDerivedDefault pins that an entry row no session
// consumed derives its default from its OWN registry tmux field, exactly as
// a session row does.
func TestBuild_EntryRow_TmuxDerivedDefault(t *testing.T) {
	entry := Entry{PID: 11, SessionID: "sess-b", Name: "barbox-0b", Tmux: "bb2:@1.%2", Inbox: "/s/11"}
	got := Build(BuildInput{HostID: "h:1", Alias: "air", Entries: []Entry{entry}})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (entry row only)", len(got))
	}
	r := got[0]
	if r.RowKind != "entry" || r.Label != "bb2" || r.LabelSource != LabelSourceDefault {
		t.Errorf("row = %+v, want entry row labelled bb2 by default", r)
	}
	if want := "air/bb2:bb2-barbox-0b"; r.Address != want {
		t.Errorf("Address = %q, want %q", r.Address, want)
	}
}

// TestBuild_InboxDeadSessionRow_KeepsHashDefault pins the first half of
// spec §3.2: an inbox_dead row's owner has NO live entry, so it is not in
// the population and keeps the v2 hash — even though its tmux session is
// named "purdex1". A different, live conversation in that same tmux session
// IS in the population and gets the place address.
func TestBuild_InboxDeadSessionRow_KeepsHashDefault(t *testing.T) {
	live := Entry{PID: 11, SessionID: "sess-live", Name: "n9", Tmux: "purdex1:@1.%2", Inbox: "/s/11"}
	in := BuildInput{
		HostID:   "h:1",
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "purdex1"}},
		Owners:   map[string]Owner{"s1": {AgentType: "cc", SessionID: "sess-dead", TmuxPaneID: "%1"}},
		Entries:  []Entry{live},
	}
	got := Build(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (inbox_dead session row + live entry row)", len(got))
	}
	var sessionRow, entryRow PeerRecord
	for _, r := range got {
		if r.RowKind == "session" {
			sessionRow = r
		} else {
			entryRow = r
		}
	}
	if sessionRow.Reason != "inbox_dead" {
		t.Fatalf("session row = %+v, want reason inbox_dead", sessionRow)
	}
	if sessionRow.Label != DefaultLabel("sess-dead") || sessionRow.LabelSource != LabelSourceDefault {
		t.Errorf("session row label/source = %q/%q, want %q/%s", sessionRow.Label, sessionRow.LabelSource, DefaultLabel("sess-dead"), LabelSourceDefault)
	}
	if entryRow.Label != "purdex1" {
		t.Errorf("entry row label = %q, want purdex1", entryRow.Label)
	}
	if want := "mini-lab/purdex1:purdex1-n9"; entryRow.Address != want {
		t.Errorf("entry row address = %q, want %q", entryRow.Address, want)
	}
}

// TestBuild_AmbiguousSessionRow_RendersPopulationDefault pins the second
// half of spec §3.2, which differs from inbox_dead and differs correctly:
// an ambiguous row's owner DOES have live entries — that is why it is
// ambiguous — so it is in the population and must render exactly what its
// own conversation's entry rows render. One conversation, one label.
func TestBuild_AmbiguousSessionRow_RendersPopulationDefault(t *testing.T) {
	in := BuildInput{
		Alias:    "a",
		Sessions: []SessionSummary{{Code: "c1", Name: "purdex1"}},
		Owners:   map[string]Owner{"c1": {AgentType: "cc", SessionID: "sid-1", TmuxPaneID: "%9"}},
		Entries: []Entry{
			{PID: 10, SessionID: "sid-1", Name: "n1", Tmux: "purdex1:@1.%1", Inbox: "/s/10"},
			{PID: 11, SessionID: "sid-1", Name: "n2", Tmux: "purdex1:@1.%2", Inbox: "/s/11"},
		},
	}
	recs := Build(in)
	if len(recs) != 3 {
		t.Fatalf("got %d rows, want 3 (ambiguous session row + 2 entry rows)", len(recs))
	}
	if recs[0].Reason != "ambiguous" || recs[0].Deliverable {
		t.Fatalf("session row = %+v, want non-deliverable ambiguous row", recs[0])
	}
	for _, r := range recs {
		if r.Label != "purdex1" || r.LabelSource != LabelSourceDefault {
			t.Errorf("row %s label/source = %q/%q, want purdex1/%s", r.Address, r.Label, r.LabelSource, LabelSourceDefault)
		}
	}
	// Ambiguity is unchanged by the label's shape: it was an AmbiguousError
	// over the two entry rows with the hash, and it still is.
	_, err := Resolve(recs, "purdex1", ResolveSnapshot{})
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("Resolve = %v, want AmbiguousError", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("candidates = %d, want exactly the 2 entry rows: %+v", len(amb.Candidates), amb.Candidates)
	}
	for _, c := range amb.Candidates {
		if c.RowKind != "entry" || c.Agent == nil || c.Agent.PID == 0 {
			t.Errorf("candidate %+v is not a live entry row", c)
		}
	}
}

// TestBuild_AmbiguousSessionRow_CompetitorForcesHash is the other half of
// the same shape: add a SECOND conversation to tmux "purdex1" and rule 2
// makes the place address name nobody, so every row — the ambiguous session
// row included — falls back to its own hash.
func TestBuild_AmbiguousSessionRow_CompetitorForcesHash(t *testing.T) {
	in := BuildInput{
		Alias:    "a",
		Sessions: []SessionSummary{{Code: "c1", Name: "purdex1"}},
		Owners:   map[string]Owner{"c1": {AgentType: "cc", SessionID: "sid-1", TmuxPaneID: "%9"}},
		Entries: []Entry{
			{PID: 10, SessionID: "sid-1", Name: "n1", Tmux: "purdex1:@1.%1", Inbox: "/s/10"},
			{PID: 11, SessionID: "sid-1", Name: "n2", Tmux: "purdex1:@1.%2", Inbox: "/s/11"},
			{PID: 12, SessionID: "sid-2", Name: "n3", Tmux: "purdex1:@1.%3", Inbox: "/s/12"},
		},
	}
	recs := Build(in)
	if len(recs) != 4 {
		t.Fatalf("got %d rows, want 4 (ambiguous session row + 3 entry rows)", len(recs))
	}
	for _, r := range recs {
		want := DefaultLabel("sid-1")
		if r.Agent != nil && r.Agent.SessionID == "sid-2" {
			want = DefaultLabel("sid-2")
		}
		if r.Label != want {
			t.Errorf("row %s label = %q, want %q", r.Address, r.Label, want)
		}
	}
}

// TestBuild_UserLabelBeatsTmuxDerivedDefault: a claimed label still wins,
// with label_source "user" and its rev — the default is not even rendered.
func TestBuild_UserLabelBeatsTmuxDerivedDefault(t *testing.T) {
	entry := Entry{PID: 100, SessionID: "sess-x", Name: "purdex-69", Tmux: "purdex1:@1.%1", Inbox: "/s/100"}
	in := BuildInput{
		HostID:   "h:1",
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "purdex1"}},
		Owners:   map[string]Owner{"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%1"}},
		Entries:  []Entry{entry},
		Labels:   map[string]LabelInfo{"sess-x": {Label: "purdex-tester", Rev: 4}},
	}
	r := Build(in)[0]
	if r.Label != "purdex-tester" || r.LabelSource != LabelSourceUser || r.LabelRev != 4 {
		t.Errorf("label/source/rev = %q/%q/%d, want purdex-tester/%s/4", r.Label, r.LabelSource, r.LabelRev, LabelSourceUser)
	}
	if want := "mini-lab/purdex-tester:purdex1-purdex-69"; r.Address != want {
		t.Errorf("Address = %q, want %q", r.Address, want)
	}
}

// TestBuild_ProxyEntryInTmux_KeepsCCFormNoLabel: a proxy row never gets a
// label at all, so a tmux name it happens to sit in changes nothing.
func TestBuild_ProxyEntryInTmux_KeepsCCFormNoLabel(t *testing.T) {
	entry := Entry{PID: 300, SessionID: "sess-z", Name: "helper-1", Tmux: "purdex1:@1.%1", IsProxy: true}
	r := Build(BuildInput{Alias: "mini-lab", Entries: []Entry{entry}})[0]
	if r.Address != "mini-lab/cc:helper-1" || r.Label != "" || r.LabelSource != "" || r.Suffix != "" {
		t.Errorf("proxy row = %+v, want mini-lab/cc:helper-1 with no label", r)
	}
	if r.Deliverable || r.Reason != "proxy" {
		t.Errorf("proxy row deliverable/reason = %v/%q, want false/proxy", r.Deliverable, r.Reason)
	}
}

// TestBuild_TwoConversationsOneTmuxSession_BothHash is rule 2 end to end
// through Build: two conversations in one place means the place address
// names neither, and both revert to exactly the behaviour that ships today.
func TestBuild_TwoConversationsOneTmuxSession_BothHash(t *testing.T) {
	in := BuildInput{
		Alias: "mini-lab",
		Entries: []Entry{
			{PID: 10, SessionID: "sid-1", Name: "n1", Tmux: "purdex1:@1.%1", Inbox: "/s/10"},
			{PID: 11, SessionID: "sid-2", Name: "n2", Tmux: "purdex1:@1.%2", Inbox: "/s/11"},
		},
	}
	recs := Build(in)
	if len(recs) != 2 {
		t.Fatalf("got %d rows, want 2", len(recs))
	}
	byAddr := map[string]PeerRecord{}
	for _, r := range recs {
		byAddr[r.Address] = r
	}
	for _, want := range []string{
		"mini-lab/" + DefaultLabel("sid-1") + ":purdex1-n1",
		"mini-lab/" + DefaultLabel("sid-2") + ":purdex1-n2",
	} {
		if _, ok := byAddr[want]; !ok {
			t.Errorf("missing %q; addresses: %v", want, keys(byAddr))
		}
	}
}
