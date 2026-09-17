package peers

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
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
	// The entry's own tmux session ("elsewhere") is not a listed session
	// either way; the row is an entry row (RowKind) addressed by its
	// registry name, not the retired "cc:<name>" form. Where it sits does
	// not enter the address at all (v4 §5.2).
	wantAddr := "mini-lab/stray"
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
	wantAddr := "mini-lab/outside-1"
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
	// Both rows carry the same ref, the s2 ambiguous fallback included:
	// they are two rows of ONE conversation, and the ref is the part of an
	// address that says so (v4 §5.3). The s2 fallback has no live entry
	// behind it, so it has no name to be addressed by and falls back to the
	// ref form -- which is the same ref s1 carries.
	wantHead := RefID("sess-x")
	if s1Rec.Ref != wantHead || s2Rec.Ref != wantHead {
		t.Errorf("refs = %q/%q, want both %q", s1Rec.Ref, s2Rec.Ref, wantHead)
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
	// Both entries belong to sess-x, so both entry rows carry that one
	// conversation's ref; their registry names are what tell them apart,
	// and under v4 that is exactly what each is addressed by.
	head := RefID("sess-x")
	for _, r := range got {
		if r.RowKind == "entry" && r.Ref != head {
			t.Errorf("entry row %q ref = %q, want %q", r.Address, r.Ref, head)
		}
	}
	wantAddrs := map[string]bool{
		"mini-lab/one": true,
		"mini-lab/two": true,
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
		for _, key := range []string{"session_code", "session_name", "tmux_instance", "reason", "row_kind", "title", "title_source", "title_rev", "ref"} {
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

	// Both live rows are addressed by their own registry name, in tmux or
	// out of it: v4's address does not encode where a conversation sits.
	mt1 := byAddress["mini-lab/purdex-47"]
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

	outside := byAddress["mini-lab/scratch-1"]
	if outside.Agent == nil || !outside.Deliverable || outside.Reason != "" {
		t.Errorf("outside = %+v, want deliverable outside-tmux row", outside)
	}
	if outside.SessionCode != "" || outside.SessionName != "" || outside.TmuxInstance != "" {
		t.Errorf("outside session fields not empty: %+v", outside)
	}
}

// --- Labels, addresses, entry rows (Peer Address v2, Task 4) --------------

// TestBuild_LabelsAndAddresses pins the address rules of v4 §5.2: a cc row
// (session or entry) reads "<alias>/<registry name>"; a session row with no
// cc agent reads "<alias>/tmux:<name>"; a user label is reported with its Rev
// and title_source "user" without touching the address.
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
		Titles: map[string]TitleInfo{"sid-1": {Title: "purdex-dev", Rev: 7}},
	}
	recs := Build(in)
	byAddr := map[string]PeerRecord{}
	for _, r := range recs {
		byAddr[r.Address] = r
	}
	dev, ok := byAddr["mini-lab/purdex-49"]
	if !ok {
		t.Fatalf("no dev row; addresses: %v", keys(byAddr))
	}
	if dev.RowKind != "session" || dev.Title != "purdex-dev" || dev.TitleSource != TitleSourceUser || dev.TitleRev != 7 || dev.Ref != RefID("sid-1") {
		t.Errorf("dev row = %+v", dev)
	}
	want := "mini-lab/purdex-3f"
	desk, ok := byAddr[want]
	if !ok {
		t.Fatalf("no desktop row %q; addresses: %v", want, keys(byAddr))
	}
	if desk.RowKind != "entry" || desk.Title != "" || desk.TitleSource != "" || desk.TitleRev != 0 || !desk.Deliverable {
		t.Errorf("desktop row = %+v", desk)
	}
	shell, ok := byAddr["mini-lab/tmux:shell"]
	if !ok || shell.Ref != "" || shell.Title != "" || shell.TitleSource != "" {
		t.Errorf("shell row = %+v (ok=%v)", shell, ok)
	}
}

// TestBuild_SessionRowTracksLiveTmuxRename is the regression test for spec
// §2's P1. v3 observed it on the display suffix; v4 deletes that field, so the
// assertion moves to the two places the frozen registry name could still leak
// into a row: SessionName, and the address itself.
//
// The scenario is the measurement recorded in §2 P1. A conversation started
// in a tmux session called "aigora2", so Claude Code froze
// `tmux = "aigora2:@5.%5"` into its registry file. The user then renamed the
// tmux session to "aigora2zz". The registry file is NOT rewritten with the
// new name — it keeps saying "aigora2" until the agent exits — so
// Entry.TmuxSessionName() is stale by construction here.
//
// The daemon's own tmux inventory, on the other hand, is live: the
// SessionSummary being rendered already says "aigora2zz". A session row must
// use that, because it IS the row for that very session.
//
// Both winner-selection paths are covered, because both used to pass the
// frozen value: the single-candidate path and the pane-tiebreak path.
func TestBuild_SessionRowTracksLiveTmuxRename(t *testing.T) {
	const (
		frozenName = "aigora2"   // what the registry file still says
		liveName   = "aigora2zz" // what tmux actually calls the session now
	)

	tests := []struct {
		name    string
		entries []Entry
	}{
		{
			name: "single candidate",
			entries: []Entry{
				{PID: 10, SessionID: "sid-1", Name: "purdex-49", Tmux: frozenName + ":@5.%5", Inbox: "/s/10"},
			},
		},
		{
			// Two live entries of the same conversation; the owner's pane
			// picks the winner. Same frozen tmux field, same requirement.
			name: "pane tiebreak winner",
			entries: []Entry{
				{PID: 10, SessionID: "sid-1", Name: "purdex-49", Tmux: frozenName + ":@5.%5", Inbox: "/s/10"},
				{PID: 11, SessionID: "sid-1", Name: "purdex-4a", Tmux: frozenName + ":@5.%6", Inbox: "/s/11"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recs := Build(BuildInput{
				HostID: "h:1", Alias: "mini-lab",
				Sessions: []SessionSummary{{Code: "c1", Name: liveName}},
				Owners:   map[string]Owner{"c1": {AgentType: "cc", SessionID: "sid-1", TmuxPaneID: "%5"}},
				Entries:  tc.entries,
			})

			var row PeerRecord
			var found bool
			for _, r := range recs {
				if r.RowKind == "session" {
					row, found = r, true
				}
			}
			if !found {
				t.Fatalf("no session row in %+v", recs)
			}
			if !row.Deliverable || row.Agent == nil || row.Agent.PID != 10 {
				t.Fatalf("expected the pid-10 entry to win: %+v", row)
			}

			if row.SessionName != liveName {
				t.Errorf("session name = %q, want %q (the live tmux name, not the frozen registry one)", row.SessionName, liveName)
			}
			if want := "mini-lab/purdex-49"; row.Address != want {
				t.Errorf("address = %q, want %q", row.Address, want)
			}
			if strings.Contains(row.Address, frozenName) || row.SessionName == frozenName {
				t.Errorf("row still carries the frozen registry name %q: address %q, session %q", frozenName, row.Address, row.SessionName)
			}
		})
	}
}

// TestBuild_EntryRowAddressIgnoresRegistryTmuxName is the v4 form of the test
// that pinned the other half of v3 §5.4. v3's entry row rendered the frozen
// registry tmux name in its suffix, because it had no session row behind it to
// supply a live one. v4 deletes the suffix, and with it the only place that
// frozen value was ever shown: an entry row is addressed by its registry NAME,
// and where the registry thinks it sits reaches nothing.
func TestBuild_EntryRowAddressIgnoresRegistryTmuxName(t *testing.T) {
	recs := Build(BuildInput{
		HostID: "h:1", Alias: "mini-lab",
		Sessions: []SessionSummary{{Code: "c1", Name: "aigora2zz"}},
		Owners:   map[string]Owner{"c1": {AgentType: "cc", SessionID: "sid-1", TmuxPaneID: "%5"}},
		Entries: []Entry{
			{PID: 10, SessionID: "sid-1", Name: "purdex-49", Tmux: "aigora2:@5.%5", Inbox: "/s/10"},
			// A different conversation, whose registry names a tmux
			// session the inventory does not list at all.
			{PID: 20, SessionID: "sid-9", Name: "n9", Tmux: "gone-box:@1.%1", Inbox: "/s/20"},
		},
	})

	var row PeerRecord
	var found bool
	for _, r := range recs {
		if r.RowKind == "entry" {
			row, found = r, true
		}
	}
	if !found {
		t.Fatalf("no entry row in %+v", recs)
	}
	if want := "mini-lab/n9"; row.Address != want {
		t.Errorf("entry row address = %q, want %q", row.Address, want)
	}
	if strings.Contains(row.Address, "gone-box") {
		t.Errorf("entry row address %q still carries the frozen registry tmux name", row.Address)
	}
	if row.Ref != RefID("sid-9") {
		t.Errorf("entry row ref = %q, want %q", row.Ref, RefID("sid-9"))
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
	// An entry row is addressed by its registry name, in tmux or out of it.
	if recs[1].Address != "a/n9" || recs[1].Ref != RefID("sid-9") {
		t.Errorf("entry row address/ref = %q %q", recs[1].Address, recs[1].Ref)
	}
}

// TestEntryRecord_MatchesBuild pins that EntryRecord is the exact function
// Build uses for an entry row — Task 7 relies on this for whoami/claim/
// release to render the identical address the listing shows.
func TestEntryRecord_MatchesBuild(t *testing.T) {
	e := Entry{PID: 11, SessionID: "sid-9", Name: "n9", Tmux: "mt0:@1.%2", Inbox: "/s/11", Cwd: "/w"}
	info := TitleInfo{Title: "purdex-tester", Rev: 3}
	titles := map[string]TitleInfo{"sid-9": info}
	// Under v3 the direct call needs no population argument at all: both
	// paths derive the head from the entry's own sessionId, so agreeing is
	// structural rather than a matter of being handed the same map.
	one := EntryRecord("a", "h:1", e, false, info)
	all := Build(BuildInput{HostID: "h:1", Alias: "a", Entries: []Entry{e}, Titles: titles})
	if len(all) != 1 || !reflect.DeepEqual(all[0], one) {
		t.Errorf("EntryRecord ≠ Build row:\n%+v\n%+v", one, all)
	}
	p := EntryRecord("a", "h:1", e, true, info)
	if p.Agent.Type != "proxy" || p.Deliverable || p.Reason != "proxy" || p.Address != "a/cc:n9" || p.Title != "" {
		t.Errorf("proxy entry record = %+v", p)
	}
}

// TestBuild_SameConversationTwoProcesses_ThreeRowsOneCanonical pins that
// the same sessionId twice, with no pane tiebreak, yields three rows
// sharing ONE canonical id: the ambiguous session row (fallback agent) and
// both entry rows. One conversation, one address — the property that used
// to be stated as "one label" and is now stated where it belongs.
//
// The resolution half then reads that shared id back through Resolve: the
// two live ENTRY rows are the ambiguous candidates, while the PID-0
// session row is inert at tier 1. Build and Resolve agree on what the
// address is, which is the whole point of deriving it from the sessionId.
func TestBuild_SameConversationTwoProcesses_ThreeRowsOneCanonical(t *testing.T) {
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
	want := RefID("sid-1")
	for _, r := range recs {
		if r.Ref != want {
			t.Errorf("row %s canonical = %q, want %q", r.Address, r.Ref, want)
		}
		if r.Title != "" || r.TitleSource != "" {
			t.Errorf("row %s label/source = %q/%q, want \"\"/\"\" — nothing named it", r.Address, r.Title, r.TitleSource)
		}
	}
	if recs[0].Reason != "ambiguous" || recs[0].Deliverable {
		t.Errorf("session row = %+v", recs[0])
	}
	live := 0
	for _, r := range recs {
		if r.RowKind == "entry" && r.Agent != nil && r.Agent.PID != 0 {
			live++
		}
	}
	if live != 2 {
		t.Errorf("live entry rows = %d, want 2", live)
	}

	// The resolution half: sending to that one address is refused with
	// both live processes named, never delivered to one of them. The
	// fallback session row carries the same canonical but no live entry,
	// so it is not among the candidates.
	_, err := Resolve(recs, want, ResolveSnapshot{})
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("Resolve(%q) = %v, want *AmbiguousError", want, err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("candidates = %d, want the 2 live entry rows", len(amb.Candidates))
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

// TestPeerRecord_WireAddress pins WireAddress's shape under v4: the bare Ref
// for a row that carries a cc agent, "" for a row with none (Ref == "" is the
// no-agent signal).
//
// The label is deliberately set and deliberately ignored: send.go's
// wireFromRecord puts this string in the outbound from.address, so a
// label-based WireAddress would have a sender announce itself at an address
// nothing routes on.
func TestPeerRecord_WireAddress(t *testing.T) {
	labelled := PeerRecord{Ref: "_3k9f2m", Title: "purdex-tester"}
	if got := labelled.WireAddress(); got != "_3k9f2m" {
		t.Errorf("WireAddress() = %q, want _3k9f2m", got)
	}
	noAgent := PeerRecord{Ref: "", Title: ""}
	if got := noAgent.WireAddress(); got != "" {
		t.Errorf("WireAddress() = %q, want \"\"", got)
	}
}

// TestBuild_UserLabelDoesNotMoveTheAddress is the same fixture that used
// to assert a claimed label CHANGED the row's address. Under D3 it asserts
// the opposite, which is the whole point of v3: the label is reported with
// title_source "user" and its rev, and the address is exactly what it was
// before anyone named anything.
func TestBuild_UserLabelDoesNotMoveTheAddress(t *testing.T) {
	entry := Entry{PID: 100, SessionID: "sess-x", Name: "purdex-69", Tmux: "purdex1:@1.%1", Inbox: "/s/100"}
	in := BuildInput{
		HostID:   "h:1",
		Alias:    "mini-lab",
		Sessions: []SessionSummary{{Code: "s1", Name: "purdex1"}},
		Owners:   map[string]Owner{"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%1"}},
		Entries:  []Entry{entry},
		Titles:   map[string]TitleInfo{"sess-x": {Title: "purdex-tester", Rev: 4}},
	}
	r := Build(in)[0]
	if r.Title != "purdex-tester" || r.TitleSource != TitleSourceUser || r.TitleRev != 4 {
		t.Errorf("label/source/rev = %q/%q/%d, want purdex-tester/%s/4", r.Title, r.TitleSource, r.TitleRev, TitleSourceUser)
	}
	if want := "mini-lab/purdex-69"; r.Address != want {
		t.Errorf("Address = %q, want %q — a label names a conversation, it does not move it (D3)", r.Address, want)
	}
	// And it is byte for byte the address the same conversation had with no
	// label at all.
	in.Titles = nil
	if unnamed := Build(in)[0].Address; unnamed != r.Address {
		t.Errorf("address without a label = %q, with one = %q; want identical", unnamed, r.Address)
	}
}

// TestBuild_ProxyEntryInTmux_KeepsCCFormNoLabel: a proxy row never gets a
// label at all, so a tmux name it happens to sit in changes nothing.
func TestBuild_ProxyEntryInTmux_KeepsCCFormNoLabel(t *testing.T) {
	entry := Entry{PID: 300, SessionID: "sess-z", Name: "helper-1", Tmux: "purdex1:@1.%1", IsProxy: true}
	r := Build(BuildInput{Alias: "mini-lab", Entries: []Entry{entry}})[0]
	if r.Address != "mini-lab/cc:helper-1" || r.Ref != "" || r.Title != "" || r.TitleSource != "" {
		t.Errorf("proxy row = %+v, want mini-lab/cc:helper-1 with no label", r)
	}
	if r.Deliverable || r.Reason != "proxy" {
		t.Errorf("proxy row deliverable/reason = %v/%q, want false/proxy", r.Deliverable, r.Reason)
	}
}

// TestBuild_TwoConversationsOneTmuxSession_DistinctCanonicals is the case
// that motivated v3 (spec §2 P2): two conversations sharing one tmux
// session. Under v2 they contended for the session's name and both lost
// it. Under v3 there is nothing to contend for — each is addressed by its
// own sessionId — so both stay reachable and neither is affected by the
// other's presence.
func TestBuild_TwoConversationsOneTmuxSession_DistinctCanonicals(t *testing.T) {
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
		"mini-lab/n1",
		"mini-lab/n2",
	} {
		if _, ok := byAddr[want]; !ok {
			t.Errorf("missing %q; addresses: %v", want, keys(byAddr))
		}
	}
}

// --- Peer Address v3/v4: the fixture every invariant test shares ---------

// v3Fixture is one BuildInput covering every row kind the invariant table
// distinguishes: a deliverable session row with a user label, a deliverable
// session row with none, an entry row outside tmux, a proxy entry row, an
// agentless session row, and an owner-fallback (inbox_dead) session row.
func v3Fixture() BuildInput {
	return BuildInput{
		HostID: "h:1", Alias: "mini-lab",
		Sessions: []SessionSummary{
			{Code: "c1", Name: "mt0", Cwd: "/w"},
			{Code: "c2", Name: "mt9"},
			{Code: "c3", Name: "shell"},
			{Code: "c4", Name: "deadplace"},
		},
		Owners: map[string]Owner{
			"c1": {AgentType: "cc", SessionID: "sid-labelled", TmuxPaneID: "%1"},
			"c2": {AgentType: "cc", SessionID: "sid-plain", TmuxPaneID: "%2"},
			"c4": {AgentType: "cc", SessionID: "sid-dead", TmuxPaneID: "%4"},
		},
		Entries: []Entry{
			{PID: 10, SessionID: "sid-labelled", Name: "purdex-49", Tmux: "mt0:@1.%1", Inbox: "/s/10"},
			{PID: 11, SessionID: "sid-plain", Name: "purdex-4a", Tmux: "mt9:@1.%2", Inbox: "/s/11"},
			{PID: 20, SessionID: "sid-outside", Name: "purdex-3f", Tmux: "", Inbox: "/s/20"},
			{PID: 30, SessionID: "sid-proxy", Name: "helper-1", Tmux: "", IsProxy: true},
		},
		Titles: map[string]TitleInfo{"sid-labelled": {Title: "purdex-tester", Rev: 7}},
	}
}

// bySessionID indexes built rows by the agent session id they carry, and
// separately returns the rows with no agent at all.
func bySessionID(recs []PeerRecord) (map[string]PeerRecord, []PeerRecord) {
	byID := map[string]PeerRecord{}
	var agentless []PeerRecord
	for _, r := range recs {
		if r.Agent == nil {
			agentless = append(agentless, r)
			continue
		}
		byID[r.Agent.SessionID] = r
	}
	return byID, agentless
}

// TestBuild_V4FieldInvariants asserts v4 §5.2/§5.3's table on EVERY row whose
// agent is a live cc entry: the ref is non-empty and derived from the
// sessionId, the address is the registry name when that name is routable and
// the ref otherwise, and title_source is "user" exactly when a label is set.
// One loop over the whole fixture, because that is a property of every such
// row rather than of a chosen one.
func TestBuild_V4FieldInvariants(t *testing.T) {
	recs := Build(v3Fixture())
	live := 0
	for _, r := range recs {
		if !hasLiveEntry(r) {
			continue
		}
		live++
		if r.Ref == "" {
			t.Errorf("row %s: ref = \"\", want the sessionId-derived id", r.Address)
			continue
		}
		if want := RefID(r.Agent.SessionID); r.Ref != want {
			t.Errorf("row %s: ref = %q, want %q", r.Address, r.Ref, want)
		}
		want := "mini-lab/" + r.Ref
		if RoutableName(r.Agent.PeerName) {
			want = "mini-lab/" + r.Agent.PeerName
		}
		if r.Address != want {
			t.Errorf("row: address = %q, want %q", r.Address, want)
		}
		wantSource := ""
		if r.Title != "" {
			wantSource = TitleSourceUser
		}
		if r.TitleSource != wantSource {
			t.Errorf("row %s: label %q has title_source %q, want %q", r.Address, r.Title, r.TitleSource, wantSource)
		}
	}
	if live != 3 {
		t.Fatalf("fixture produced %d live cc rows, want 3", live)
	}
}

// TestBuild_NoLabel_EmptyLabelAndNameAddress pins D4: a conversation that has
// never claimed a label carries no label at all — not a tmux-derived one, not
// a hash — and is still addressable.
func TestBuild_NoLabel_EmptyLabelAndNameAddress(t *testing.T) {
	byID, _ := bySessionID(Build(v3Fixture()))
	plain := byID["sid-plain"]
	if plain.Title != "" || plain.TitleSource != "" {
		t.Errorf("unlabelled row label/source = %q/%q, want \"\"/\"\"", plain.Title, plain.TitleSource)
	}
	want := "mini-lab/purdex-4a"
	if plain.Address != want {
		t.Errorf("unlabelled row address = %q, want %q", plain.Address, want)
	}
}

// TestBuild_UserLabel_AddressStaysPut is D3's pin, and the single most
// important assertion in this change: setting a label names the conversation,
// it does not move it. The label is reported, title_source says "user", and
// the address is STILL the registry name.
func TestBuild_UserLabel_AddressStaysPut(t *testing.T) {
	byID, _ := bySessionID(Build(v3Fixture()))
	labelled := byID["sid-labelled"]
	if labelled.Title != "purdex-tester" || labelled.TitleSource != TitleSourceUser {
		t.Errorf("labelled row label/source = %q/%q, want purdex-tester/%s", labelled.Title, labelled.TitleSource, TitleSourceUser)
	}
	want := "mini-lab/purdex-49"
	if labelled.Address != want {
		t.Errorf("labelled row address = %q, want %q — a label is not an address (D3)", labelled.Address, want)
	}
	if got := RefID("sid-labelled"); labelled.Ref != got {
		t.Errorf("labelled row ref = %q, want %q", labelled.Ref, got)
	}
}

// TestBuild_LabelRev_PassesThrough pins spec §4.4: TitleRev is still the
// label's revision, carried straight through from the store. It stops
// implying an address change; it does not stop existing.
func TestBuild_LabelRev_PassesThrough(t *testing.T) {
	byID, _ := bySessionID(Build(v3Fixture()))
	if got := byID["sid-labelled"].TitleRev; got != 7 {
		t.Errorf("labelled row title_rev = %d, want 7", got)
	}
	if got := byID["sid-plain"].TitleRev; got != 0 {
		t.Errorf("unlabelled row title_rev = %d, want 0", got)
	}
}

// TestBuild_AgentNullRow_NoRef pins the first of the three row kinds the
// invariant table deliberately does NOT cover: a session row with no agent
// keeps the tmux: address and has no ref, which is what tells an SPA reading
// title_source == "" apart from an unlabelled live conversation.
func TestBuild_AgentNullRow_NoRef(t *testing.T) {
	_, agentless := bySessionID(Build(v3Fixture()))
	if len(agentless) != 1 {
		t.Fatalf("got %d agentless rows, want 1", len(agentless))
	}
	shell := agentless[0]
	if shell.Ref != "" {
		t.Errorf("agentless row ref = %q, want \"\"", shell.Ref)
	}
	if shell.Address != "mini-lab/tmux:shell" {
		t.Errorf("agentless row address = %q, want mini-lab/tmux:shell", shell.Address)
	}
}

// TestBuild_ProxyAndOwnerFallbackRows_Unchanged pins the other two row kinds
// the invariant table excludes: a proxy row keeps the retired, deliberately
// unresolvable cc: form with no ref, and an owner-fallback (inbox_dead) row
// still renders an address — the ref form, since applyIdentity writes it and
// no live entry stands behind the row to supply a name.
//
// Its Reason stays "inbox_dead": applyIdentity writes an address, never a
// Reason, so the caller's verdict is the only one on the row.
func TestBuild_ProxyAndOwnerFallbackRows_Unchanged(t *testing.T) {
	byID, _ := bySessionID(Build(v3Fixture()))

	proxy := byID["sid-proxy"]
	if proxy.Address != "mini-lab/cc:helper-1" || proxy.Reason != "proxy" || proxy.Deliverable {
		t.Errorf("proxy row = %+v, want the unresolvable cc: form", proxy)
	}
	if proxy.Ref != "" || proxy.Title != "" || proxy.TitleSource != "" {
		t.Errorf("proxy row ref/label/source = %q/%q/%q, want all empty", proxy.Ref, proxy.Title, proxy.TitleSource)
	}

	dead := byID["sid-dead"]
	if dead.Reason != "inbox_dead" || dead.Deliverable {
		t.Fatalf("owner-fallback row = %+v, want a non-deliverable inbox_dead row", dead)
	}
	if want := "mini-lab/" + RefID("sid-dead"); dead.Address != want {
		t.Errorf("owner-fallback row address = %q, want %q", dead.Address, want)
	}
	if dead.Ref != RefID("sid-dead") {
		t.Errorf("owner-fallback row ref = %q, want %q", dead.Ref, RefID("sid-dead"))
	}
}

// TestBuild_JSON_RefKeyAlwaysPresent pins that `ref` is on the wire for every
// row, empty string included — a consumer must be able to read it without
// checking whether the key exists (spec §6.1).
func TestBuild_JSON_RefKeyAlwaysPresent(t *testing.T) {
	for _, rec := range Build(v3Fixture()) {
		m := mustMarshalMap(t, rec)
		if _, present := m["ref"]; !present {
			t.Errorf("record %+v missing key \"ref\"", rec)
		}
	}
}

// --- Peer Address v4: Ref, the name address, and the ref fallback ------

// ccBuildInput is the minimal BuildInput for one deliverable cc row. It
// mirrors TestBuild_CC_OneCandidate_Deliverable's shape.
func ccBuildInput(name, sessionID string) BuildInput {
	return BuildInput{
		Alias:    "mlab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1", Cwd: "/w", TmuxInstance: "t1"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: sessionID, TmuxPaneID: "%1", Status: "idle"},
		},
		Entries: []Entry{{
			PID: 100, SessionID: sessionID, Name: name, NameSource: "derived",
			Cwd: "/w", Tmux: "mt1:@1.%1", Inbox: "/tmp/1.sock", Status: "idle",
		}},
	}
}

func TestApplyIdentity_NameAddress(t *testing.T) {
	got := Build(ccBuildInput("purdex-b0", "sess-x"))[0]
	if want := "mlab/purdex-b0"; got.Address != want {
		t.Errorf("Address = %q, want %q", got.Address, want)
	}
	if want := RefID("sess-x"); got.Ref != want {
		t.Errorf("Ref = %q, want %q", got.Ref, want)
	}
}

// A name that cannot be an address must not produce a broken one — and must
// not cost the row its deliverability either. The ref form IS the row's
// address; there is nothing degraded about it, so nothing is reported.
func TestApplyIdentity_UnroutableNameFallsBackToRef(t *testing.T) {
	for _, bad := range []string{"has/slash", "q34psn", "_underscore"} {
		got := Build(ccBuildInput(bad, "sess-y"))[0]
		if want := "mlab/" + RefID("sess-y"); got.Address != want {
			t.Errorf("name %q: Address = %q, want %q", bad, got.Address, want)
		}
		if !got.Deliverable {
			t.Errorf("name %q: Deliverable = false; an unroutable name does not stop delivery", bad)
		}
		if got.Reason != "" {
			t.Errorf("name %q: Reason = %q, want \"\" — Reason says why a row cannot be DELIVERED to", bad, got.Reason)
		}
	}
}

// TestBuild_DeliverableRowsCarryNoReason pins the invariant every reader of
// Reason depends on: Deliverable == true implies Reason == "".
//
// cmd/pdx's deliverableField renders "yes" whenever Deliverable is true and
// only otherwise falls back to Reason, and the SPA's PEER_REASONS enumerates
// the non-deliverable causes — so a Reason set on a deliverable row is a
// string nothing can ever show. That is how "name_unroutable" got written into
// a field whose documented domain is why a row cannot be delivered to, and sat
// there invisible: an unroutable name is an ADDRESSING fact, and the address
// already says it by taking the ref form.
func TestBuild_DeliverableRowsCarryNoReason(t *testing.T) {
	batches := map[string][]PeerRecord{"fixture": Build(v3Fixture())}
	for _, name := range []string{"purdex-b0", "has/slash", "q34psn", "_underscore", "trusted:ops", ""} {
		batches["name="+name] = Build(ccBuildInput(name, "sess-z"))
	}
	for what, recs := range batches {
		for _, rec := range recs {
			if rec.Deliverable && rec.Reason != "" {
				t.Errorf("%s: deliverable row %+v carries Reason %q, which nothing renders", what, rec, rec.Reason)
			}
		}
	}
	// The implication is not vacuous: the awkward rows above are deliverable.
	for _, name := range []string{"has/slash", "trusted:ops"} {
		if got := batches["name="+name][0]; !got.Deliverable {
			t.Errorf("name %q: row is not deliverable, so the invariant above proves nothing", name)
		}
	}
}

func TestPeerRecord_JSONKeys(t *testing.T) {
	m := mustMarshalMap(t, PeerRecord{Ref: "_abc123"})
	if _, ok := m["ref"]; !ok {
		t.Error(`marshalled record has no "ref" key`)
	}
	for _, gone := range []string{"canonical", "suffix"} {
		if _, ok := m[gone]; ok {
			t.Errorf("marshalled record still has %q key", gone)
		}
	}
}

// V8: an agentless tmux row is untouched.
func TestApplyIdentity_TmuxRowUnchanged(t *testing.T) {
	in := BuildInput{
		Alias:    "mlab",
		Sessions: []SessionSummary{{Code: "s1", Name: "aigora3", Cwd: "~", TmuxInstance: "t1"}},
	}
	got := Build(in)[0]
	if want := "mlab/tmux:aigora3"; got.Address != want {
		t.Errorf("Address = %q, want %q", got.Address, want)
	}
	if got.Ref != "" {
		t.Errorf("Ref = %q, want empty on an agentless row", got.Ref)
	}
}

func TestBuild_TmuxName_SessionRowUsesLiveName(t *testing.T) {
	in := ccBuildInput("purdex-b0", "sess-x")
	got := Build(in)[0]
	if got.TmuxName != "mt1" {
		t.Errorf("TmuxName = %q, want the live session name %q", got.TmuxName, "mt1")
	}
	if got.RowKind != "session" {
		t.Fatalf("RowKind = %q, want session", got.RowKind)
	}
}

// An entry row has no session behind it, so its tmux name comes from the
// registry file, where it was frozen at startup.
func TestBuild_TmuxName_EntryRowUsesFrozenRegistryName(t *testing.T) {
	in := BuildInput{
		Alias: "mlab",
		Entries: []Entry{{
			PID: 100, SessionID: "sess-z", Name: "purdex-b0",
			Tmux: "aigora2:@5.%5", Inbox: "/tmp/1.sock",
		}},
	}
	got := Build(in)[0]
	if got.RowKind != "entry" {
		t.Fatalf("RowKind = %q, want entry", got.RowKind)
	}
	if got.TmuxName != "aigora2" {
		t.Errorf("TmuxName = %q, want the frozen registry name %q", got.TmuxName, "aigora2")
	}
	// SessionName stays empty: tier 4 and "tmux:<name>" must not be able to
	// reach a row through a value that may already be wrong.
	if got.SessionName != "" {
		t.Errorf("SessionName = %q, want empty on an entry row", got.SessionName)
	}
}

func TestBuild_TmuxName_EmptyOutsideTmux(t *testing.T) {
	in := BuildInput{
		Alias:   "mlab",
		Entries: []Entry{{PID: 100, SessionID: "sess-z", Name: "purdex-b0", Inbox: "/tmp/1.sock"}},
	}
	if got := Build(in)[0]; got.TmuxName != "" {
		t.Errorf("TmuxName = %q, want empty for an agent outside tmux", got.TmuxName)
	}
}
