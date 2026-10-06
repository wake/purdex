package team

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestEventValue_SnapshotEmitsEmptyArray(t *testing.T) {
	got, err := json.Marshal(EventValue{Op: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"op":"snapshot","approvals":[]}` {
		t.Fatalf("snapshot = %s", got)
	}
	got, err = json.Marshal(EventValue{Op: "closed", Approval: &Approval{ID: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if _, has := m["approvals"]; has {
		t.Fatalf("closed must not carry approvals: %s", got)
	}
	if string(m["op"]) != `"closed"` || !json.Valid(m["approval"]) {
		t.Fatalf("closed = %s", got)
	}
}

func TestApproval_JSONKeysAndRoundTrip(t *testing.T) {
	payload, _ := json.Marshal(LeadPayload{Reason: "split the work", MaxMembers: 3, Roots: []string{"/w"}})
	in := Approval{
		ID: "11111111-1111-4111-8111-111111111111", Kind: KindLead, HostID: "h:1",
		Origin:  Origin{SessionID: "sid-1", Ref: "_abc123", Name: "n10", PID: 10, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: "/w", Tmux: "mt0:@1.%1", Title: "lead-team", Address: "mlab/n10"},
		Payload: payload, State: StateApproved, CreatedAt: 1000, DeadlineAt: 541000, LeaseUntil: 31000,
		DecidedBy: &Client{Kind: "app", Label: "Purdex.app @ air26", Addr: "100.64.0.4:5"}, DecidedAt: 2000,
		Grant: &Grant{MaxMembers: 2, Roots: []string{"/w"}},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(keys))
	for k := range keys {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"created_at", "deadline_at", "decided_at", "decided_by", "grant", "host_id", "id", "kind", "lease_until", "origin", "payload", "state"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	if !strings.Contains(string(keys["origin"]), `"title":"lead-team"`) || !strings.Contains(string(keys["origin"]), `"address":"mlab/n10"`) {
		t.Fatalf("origin must carry title and address: %s", keys["origin"])
	}
	if bare, _ := json.Marshal(Origin{SessionID: "x"}); strings.Contains(string(bare), `"title"`) || strings.Contains(string(bare), `"address"`) {
		t.Fatalf("empty title/address must be omitted: %s", bare)
	}
	var out Approval
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	// Payload is RawMessage: compare it as JSON, the rest structurally.
	var pIn, pOut LeadPayload
	_ = json.Unmarshal(in.Payload, &pIn)
	_ = json.Unmarshal(out.Payload, &pOut)
	in.Payload, out.Payload = nil, nil
	if !reflect.DeepEqual(in, out) || !reflect.DeepEqual(pIn, pOut) {
		t.Fatalf("round trip changed the value:\n in=%+v\nout=%+v", in, out)
	}
	open, _ := json.Marshal(Approval{State: StateOpen})
	keys = nil // Unmarshal into a non-nil map keeps old keys
	if err := json.Unmarshal(open, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"decided_by", "decided_at", "grant"} {
		if _, has := keys[k]; has {
			t.Fatalf("open approval must omit %s: %s", k, open)
		}
	}
}

// The restart confirm (spec §9.5) reads both counts; a zero must still be
// on the wire, so neither field is omitempty.
func TestInflightResponse_JSONKeys(t *testing.T) {
	got, err := json.Marshal(InflightResponse{ApprovalsOpen: 2})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"approvals_open":2,"relays_active":0}` {
		t.Fatalf("inflight = %s, want both keys with relays_active 0", got)
	}
}
