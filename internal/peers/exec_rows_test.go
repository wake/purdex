package peers

import (
	"errors"
	"testing"

	"github.com/wake/purdex/internal/peers/execpeers"
)

// Execution rows (peer mailbox spec §4.1).

const (
	exSidA = "aaaaaaaa-0000-4000-8000-000000000001"
	exSidB = "bbbbbbbb-0000-4000-8000-000000000002"
)

// rowsOf returns the rows carrying sid.
func rowsOf(recs []PeerRecord, sid string) []PeerRecord {
	var out []PeerRecord
	for _, r := range recs {
		if r.Agent != nil && r.Agent.SessionID == sid {
			out = append(out, r)
		}
	}
	return out
}

func oneRowOf(t *testing.T, recs []PeerRecord, sid string) PeerRecord {
	t.Helper()
	got := rowsOf(recs, sid)
	if len(got) != 1 {
		t.Fatalf("rows for %s = %d (%+v), want exactly 1", sid, len(got), got)
	}
	return got[0]
}

// A sleeping (idle) execution and a running one each give exactly one row,
// addressed by the conversation's virtual name like any other conversation.
func TestBuild_ExecutionRows_IdleAndRunning(t *testing.T) {
	in := BuildInput{
		HostID: "mlab:abc", Alias: "mlab", MailboxEnabled: true,
		Executions: []execpeers.Row{
			{ExecutionID: "E1", SessionID: exSidA, Cwd: "/work/a", State: "idle", Title: "alpha"},
			{ExecutionID: "E2", SessionID: exSidB, Cwd: "/work/b", State: "running", PID: 222},
		},
		VirtualNames: map[string]string{exSidA: "work-a-" + RefID(exSidA)[1:3]},
		PreviousRefs: map[string][]string{exSidA: {"_old001"}},
	}
	got := Build(in)
	if len(got) != 2 {
		t.Fatalf("rows = %d (%+v), want 2", len(got), got)
	}
	a := oneRowOf(t, got, exSidA)
	if a.RowKind != RowKindExecution || a.ExecutionID != "E1" || a.ExecState != "idle" {
		t.Errorf("row A kind/id/state = %q/%q/%q", a.RowKind, a.ExecutionID, a.ExecState)
	}
	wantName := in.VirtualNames[exSidA]
	if a.Ref != RefID(exSidA) || a.Name != wantName || a.Address != "mlab/"+wantName {
		t.Errorf("row A ref/name/address = %q/%q/%q", a.Ref, a.Name, a.Address)
	}
	if a.Agent.Type != "cc" || a.Agent.PID != 0 || a.Cwd != "/work/a" || a.Title != "alpha" || a.HostID != "mlab:abc" {
		t.Errorf("row A = %+v agent %+v", a, *a.Agent)
	}
	if len(a.PreviousRefs) != 1 || a.PreviousRefs[0] != "_old001" {
		t.Errorf("row A previous_refs = %v", a.PreviousRefs)
	}
	b := oneRowOf(t, got, exSidB)
	if b.Agent.PID != 222 || b.ExecState != "running" || b.Address != "mlab/"+RefID(exSidB) {
		t.Errorf("row B = %+v agent %+v", b, *b.Agent)
	}
	// P4a: listed and addressable, not deliverable until the mailbox last hop
	// is wired.
	if a.Deliverable || a.Reason != ReasonMailboxNotWired {
		t.Errorf("deliverable/reason = %v/%q, want false/%q", a.Deliverable, a.Reason, ReasonMailboxNotWired)
	}
}

// The mailbox switch only decides deliverability: the rows are listed alike.
func TestBuild_ExecutionRows_MailboxOff(t *testing.T) {
	got := Build(BuildInput{Alias: "mlab", Executions: []execpeers.Row{{ExecutionID: "E1", SessionID: exSidA, State: "idle"}}})
	r := oneRowOf(t, got, exSidA)
	if r.RowKind != RowKindExecution || r.Deliverable || r.Reason != ReasonMailboxDisabled {
		t.Errorf("row = kind %q deliverable %v reason %q", r.RowKind, r.Deliverable, r.Reason)
	}
}

// The live process of a running execution is a registry entry outside any
// tmux session: it folds into the execution row, which takes its live
// identity, and is not listed on its own.
func TestBuild_ExecutionFoldsStandaloneEntry(t *testing.T) {
	entry := Entry{
		PID: 222, SessionID: exSidA, Name: "work-a-9f", Cwd: "/work/a", Inbox: "/tmp/222.sock",
		ProcStart: "Sun Sep 13 15:22:36 2026", Version: "2.1.270", Status: "busy",
	}
	got := Build(BuildInput{
		Alias: "mlab", Entries: []Entry{entry},
		Executions: []execpeers.Row{{ExecutionID: "E1", SessionID: exSidA, Cwd: "/work/a", State: "running"}},
	})
	if len(got) != 1 {
		t.Fatalf("rows = %d (%+v), want 1", len(got), got)
	}
	r := got[0]
	want := AgentInfo{Type: "cc", SessionID: exSidA, PeerName: "work-a-9f", PID: 222,
		ProcStart: "Sun Sep 13 15:22:36 2026", Inbox: "/tmp/222.sock", Status: "busy", Version: "2.1.270"}
	if r.RowKind != RowKindExecution || *r.Agent != want {
		t.Errorf("row = kind %q agent %+v, want execution %+v", r.RowKind, *r.Agent, want)
	}
}

// A tmux session row carrying the execution's session id is the terminal
// taking the conversation back: it wins, and the execution is not listed —
// whether the session row consumed the live entry or has none behind it.
func TestBuild_TmuxSessionRowSuppressesExecution(t *testing.T) {
	entry := Entry{PID: 300, SessionID: exSidA, Name: "work-a-9f", Tmux: "mt1:@1.%1", Inbox: "/tmp/300.sock"}
	for name, entries := range map[string][]Entry{"entry consumed": {entry}, "inbox_dead": nil} {
		t.Run(name, func(t *testing.T) {
			got := Build(BuildInput{
				Alias:      "mlab",
				Sessions:   []SessionSummary{{Code: "s1", Name: "mt1"}},
				Owners:     map[string]Owner{"s1": {AgentType: "cc", SessionID: exSidA, TmuxPaneID: "%1"}},
				Entries:    entries,
				Executions: []execpeers.Row{{ExecutionID: "E1", SessionID: exSidA, State: "idle"}},
			})
			if r := oneRowOf(t, got, exSidA); r.RowKind != "session" {
				t.Errorf("surviving row kind = %q, want session", r.RowKind)
			}
		})
	}
}

// An execution row is addressable by name, by ref and by both, while it
// sleeps with no process behind it.
func TestResolve_ExecutionRowWhileAsleep(t *testing.T) {
	name := "work-a-" + RefID(exSidA)[1:3]
	recs := Build(BuildInput{
		Alias:        "mlab",
		Executions:   []execpeers.Row{{ExecutionID: "E1", SessionID: exSidA, State: "idle"}},
		VirtualNames: map[string]string{exSidA: name},
	})
	for _, addr := range []string{name, RefID(exSidA), RefID(exSidA)[1:], name + " [" + RefID(exSidA)[1:] + "]"} {
		got, err := Resolve(recs, addr, ResolveSnapshot{})
		if err != nil || got.ExecutionID != "E1" {
			t.Errorf("Resolve(%q) = %q, %v; want E1", addr, got.ExecutionID, err)
		}
	}
}

// With the execution list unreadable, a miss is not a verdict: the address
// may be an execution's, so it is not-ready, never not-found — and never a
// fall-through to the bare tmux-name tier. A hit still resolves.
func TestResolve_ExecutionsUnavailable(t *testing.T) {
	recs := Build(BuildInput{
		Alias:    "mlab",
		Sessions: []SessionSummary{{Code: "s1", Name: "work-a"}},
		Owners:   map[string]Owner{},
	})
	recs = append(recs, Build(BuildInput{Alias: "mlab",
		Executions: []execpeers.Row{{ExecutionID: "E2", SessionID: exSidB, State: "idle"}}})...)
	unavailable := ResolveSnapshot{ExecutionsUnavailable: true}
	for _, addr := range []string{"nobody-x1", "_zzzzzz", "work-a", "nobody-x1 [zzzzzz]"} {
		if _, err := Resolve(recs, addr, unavailable); !errors.Is(err, ErrResolveNotReady) {
			t.Errorf("Resolve(%q) err = %v, want ErrResolveNotReady", addr, err)
		}
	}
	if got, err := Resolve(recs, RefID(exSidB), unavailable); err != nil || got.ExecutionID != "E2" {
		t.Errorf("hit under the flag = %q, %v", got.ExecutionID, err)
	}
	// Without the flag the same misses are what they always were.
	if _, err := Resolve(recs, "nobody-x1", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("miss without the flag = %v, want ErrNotFound", err)
	}
	if got, err := Resolve(recs, "work-a", ResolveSnapshot{}); err != nil || got.SessionCode != "s1" {
		t.Errorf("tier 4 without the flag = %q, %v", got.SessionCode, err)
	}
}
