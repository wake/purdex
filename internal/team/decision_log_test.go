package team

import (
	"encoding/json"
	"testing"
)

// The wire literals of the unattended panel's log (name pool v2 spec §8, plan §1.1), pinned one by one: the App reads these
// keys, and the `on` of a toggle must survive false.
func TestUnattendedLogEntry_WireLiterals(t *testing.T) {
	on, off := true, false
	for _, c := range []struct {
		name string
		e    UnattendedLogEntry
		want string
	}{
		{"a decision", UnattendedLogEntry{At: 5, Type: LogTypeDecision, Kind: KindSelfRelay, Origin: &Origin{SessionID: "s1", Ref: "_abc123"},
			DecidedBy: &Client{Kind: "app", Label: "Purdex.app @ air26"}, State: StateApproved},
			`{"at":5,"type":"decision","kind":"self_relay","origin":{"session_id":"s1","ref":"_abc123","name":"","pid":0,"proc_start":"","cwd":"","tmux":""},"decided_by":{"kind":"app","label":"Purdex.app @ air26"},"state":"approved"}`},
		{"a timeout has no decider", UnattendedLogEntry{At: 6, Type: LogTypeDecision, Kind: KindLead, Origin: &Origin{SessionID: "s2"}, State: StateTimeout},
			`{"at":6,"type":"decision","kind":"lead","origin":{"session_id":"s2","ref":"","name":"","pid":0,"proc_start":"","cwd":"","tmux":""},"state":"timeout"}`},
		{"a toggle on", UnattendedLogEntry{At: 7, Type: LogTypeToggle, On: &on, By: &Client{Kind: "app", Label: "x"}},
			`{"at":7,"type":"toggle","on":true,"by":{"kind":"app","label":"x"}}`},
		{"a toggle off keeps on:false", UnattendedLogEntry{At: 8, Type: LogTypeToggle, On: &off, By: &Client{Kind: "app", Label: "x"}},
			`{"at":8,"type":"toggle","on":false,"by":{"kind":"app","label":"x"}}`},
	} {
		got, err := json.Marshal(c.e)
		if err != nil || string(got) != c.want {
			t.Errorf("%s:\n got %s\nwant %s (%v)", c.name, got, c.want, err)
		}
	}
}

// An empty log is written as [], never null: the App replaces its list from the answer.
func TestUnattendedView_LogIsNeverNull(t *testing.T) {
	b, err := json.Marshal(UnattendedView{})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if string(m["log"]) != "[]" {
		t.Errorf("log = %s, want []", m["log"])
	}
}

func TestDecisionLogEntry(t *testing.T) {
	origin := Origin{SessionID: "s1", Ref: "_abc123", PID: 7, Cwd: "/w"}
	app := &Client{Kind: "app", Label: "Purdex.app @ air26", Addr: "100.64.0.4:5000"}
	t.Run("an approval by a person: at its decided_at, the address is not shown", func(t *testing.T) {
		e, ok := DecisionLogEntry(Approval{ID: "a", Kind: KindMemberRelay, Origin: origin, State: StateApproved, DeadlineAt: 100, DecidedAt: 500, DecidedBy: app})
		if !ok || e.At != 500 || e.State != StateApproved || e.Type != LogTypeDecision || e.Kind != KindMemberRelay {
			t.Fatalf("entry = %+v ok=%v", e, ok)
		}
		if e.DecidedBy == nil || e.DecidedBy.Addr != "" || e.DecidedBy.Label != app.Label || e.Origin == nil || e.Origin.SessionID != "s1" {
			t.Errorf("decided_by / origin = %+v / %+v", e.DecidedBy, e.Origin)
		}
	})
	t.Run("a timeout: at its deadline, not at the sweep that closed it", func(t *testing.T) {
		e, ok := DecisionLogEntry(Approval{ID: "a", Kind: KindLead, Origin: origin, State: StateTimeout, DeadlineAt: 100, DecidedAt: 9000})
		if !ok || e.At != 100 || e.State != StateTimeout || e.DecidedBy != nil {
			t.Fatalf("entry = %+v ok=%v", e, ok)
		}
	})
	t.Run("a denial", func(t *testing.T) {
		e, ok := DecisionLogEntry(Approval{ID: "a", Kind: KindAdopt, Origin: origin, State: StateDenied, DecidedAt: 300, DecidedBy: app})
		if !ok || e.At != 300 || e.State != StateDenied {
			t.Fatalf("entry = %+v ok=%v", e, ok)
		}
	})
	t.Run("a lead or adopt request the daemon refused while unattended is a denial by it", func(t *testing.T) {
		for _, k := range []Kind{KindLead, KindAdopt} {
			e, ok := DecisionLogEntry(Approval{ID: "a", Kind: k, Origin: origin, State: StateCancelled, CloseReason: ErrUnattendedRefused, DecidedAt: 400})
			if !ok || e.At != 400 || e.State != StateDenied || e.DecidedBy == nil || e.DecidedBy.Kind != ClientKindUnattended {
				t.Errorf("%s: entry = %+v ok=%v", k, e, ok)
			}
		}
	})
	t.Run("what is not a decision is left out", func(t *testing.T) {
		for name, a := range map[string]Approval{
			"open":                                {Kind: KindLead, State: StateOpen},
			"withdrawn":                           {Kind: KindLead, State: StateCancelled, DecidedAt: 1},
			"cancelled for another reason":        {Kind: KindAdopt, State: StateCancelled, CloseReason: "target_gone", DecidedAt: 1},
			"abandoned":                           {Kind: KindSelfRelay, State: StateAbandoned, DecidedAt: 1},
			"a chat prompt":                       {Kind: KindHookAsk, State: StateApproved, DecidedAt: 1},
			"a permission prompt":                 {Kind: KindHookPermission, State: StateDenied, DecidedAt: 1},
			"refused, but not a lead or an adopt": {Kind: KindSelfRelay, State: StateCancelled, CloseReason: ErrUnattendedRefused, DecidedAt: 1},
		} {
			if e, ok := DecisionLogEntry(a); ok {
				t.Errorf("%s: %+v is in the log", name, e)
			}
		}
	})
}
