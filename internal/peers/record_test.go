package peers

import (
	"encoding/json"
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
	// Address / Host construction (rule 1).
	if got[2].Address != "mini-lab/zeta" {
		t.Errorf("Address = %q, want mini-lab/zeta", got[2].Address)
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
// session (rule 6, no pane fallback), and that entry gets its own cc: row
// only if its TmuxSessionName() is not a listed session.
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
	if outsideRow.Address != "mini-lab/cc:stray" {
		t.Errorf("outsideRow.Address = %q, want mini-lab/cc:stray", outsideRow.Address)
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
	if r.Address != "mini-lab/cc:outside-1" {
		t.Errorf("Address = %q, want mini-lab/cc:outside-1", r.Address)
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
// produces a cc: row: the entry appears exactly once, as the session row.
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
		t.Fatalf("len = %d, want 1 (session row only, no duplicate cc: row)", len(got))
	}
	sessionRow := got[0]
	if sessionRow.SessionCode != "s1" || !sessionRow.Deliverable || sessionRow.Agent == nil || sessionRow.Agent.PID != 5 {
		t.Fatalf("sessionRow = %+v, want deliverable session row via entry PID 5", sessionRow)
	}

	// (c) Resolve(records, "cc:purdex-5") returns the session row itself
	// (no AmbiguousError), since the entry it was built from is not
	// duplicated into a separate outside row.
	resolved, err := Resolve(got, "cc:purdex-5")
	if err != nil {
		t.Fatalf("Resolve: unexpected err: %v", err)
	}
	if resolved.SessionCode != "s1" {
		t.Fatalf("Resolve() = %+v, want the session row (SessionCode=s1)", resolved)
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
	wantAddrs := map[string]bool{"mini-lab/cc:one": true, "mini-lab/cc:two": true}
	if len(outsideAddrs) != 2 || !wantAddrs[outsideAddrs[0]] || !wantAddrs[outsideAddrs[1]] {
		t.Fatalf("outsideAddrs = %v, want both mini-lab/cc:one and mini-lab/cc:two", outsideAddrs)
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
		for _, key := range []string{"session_code", "session_name", "tmux_instance", "reason"} {
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

	mt1 := byAddress["mini-lab/mt1"]
	if !mt1.Deliverable || mt1.Reason != "" || mt1.Agent == nil {
		t.Fatalf("mt1 = %+v, want deliverable cc row", mt1)
	}
	if mt1.Agent.PID != 76973 || mt1.Agent.Version != "2.1.270" || mt1.Agent.Status != "busy" {
		t.Errorf("mt1.Agent = %+v", mt1.Agent)
	}
	if mt1.TmuxInstance != "6901:1789205013" {
		t.Errorf("mt1.TmuxInstance = %q", mt1.TmuxInstance)
	}

	aigora3 := byAddress["mini-lab/aigora3"]
	if aigora3.Agent != nil || aigora3.Reason != "no_agent" || aigora3.Deliverable {
		t.Errorf("aigora3 = %+v, want no_agent shell row", aigora3)
	}

	codexy := byAddress["mini-lab/codexy"]
	if codexy.Agent == nil || codexy.Agent.Type != "codex" || codexy.Agent.Status != "busy" {
		t.Errorf("codexy = %+v, want codex agent status busy", codexy)
	}
	if codexy.Deliverable || codexy.Reason != "not_cc" {
		t.Errorf("codexy deliverable/reason = %v/%q, want false/not_cc", codexy.Deliverable, codexy.Reason)
	}

	outside := byAddress["mini-lab/cc:scratch-1"]
	if outside.Agent == nil || !outside.Deliverable || outside.Reason != "" {
		t.Errorf("outside = %+v, want deliverable outside-tmux row", outside)
	}
	if outside.SessionCode != "" || outside.SessionName != "" || outside.TmuxInstance != "" {
		t.Errorf("outside session fields not empty: %+v", outside)
	}
}
