package team

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// The names an older or newer peer matches on (unattended spec D-U23-2,
// D-U23-5; plan PU-1a): the capability the App gates on, the host event
// type, the decider of an auto-approval, the list's page sizes and the U25
// cap of an unattended lead grant.
func TestWireUnattended_LiteralsArePinned(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{CapabilityUnattended, "relay.unattended.v1"},
		{UnattendedEventType, "team.unattended"},
		{ClientKindUnattended, "unattended"},
		{UnattendedLabel, "無人值守模式"},
	} {
		if c.got != c.want {
			t.Errorf("literal = %q, want %q", c.got, c.want)
		}
	}
	if UnattendedPageDefault != 50 || UnattendedPageMax != 200 {
		t.Errorf("page sizes = %d / %d, want 50 / 200", UnattendedPageDefault, UnattendedPageMax)
	}
	if UnattendedLeadMaxMembers != 3 {
		t.Errorf("UnattendedLeadMaxMembers = %d, want 3 (D-U24-7)", UnattendedLeadMaxMembers)
	}
	if got, want := UnattendedClient(), (Client{Kind: "unattended", Label: "無人值守模式"}); got != want {
		t.Errorf("UnattendedClient() = %+v, want %+v (no addr: the daemon decided)", got, want)
	}
	raw, _ := json.Marshal(UnattendedClient())
	if string(raw) != `{"kind":"unattended","label":"無人值守模式"}` {
		t.Errorf("decided_by = %s", raw)
	}
}

func jsonKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// UnattendedView flattens the state; approved is [] when empty, never null
// (the SPA replaces its list from it); truncated is always there; the
// cursor, the PUT's counts, list_failed and changed_by are left out when
// zero / false / nil.
func TestWireUnattended_JSONShapes(t *testing.T) {
	zero, err := json.Marshal(UnattendedView{})
	if err != nil {
		t.Fatal(err)
	}
	if string(zero) != `{"on":false,"since":0,"changed_at":0,"approved":[],"truncated":false,"quotas":null,"held":null}` {
		t.Fatalf("zero view = %s", zero)
	}
	if empty, _ := json.Marshal(UnattendedView{Approved: []Approval{}}); string(empty) != string(zero) {
		t.Fatalf("an empty slice and nil must read alike: %s", empty)
	}

	by := &Client{Kind: "app", Label: "Purdex.app @ air26", Addr: "100.64.0.4:51234"}
	full := UnattendedView{
		UnattendedState: UnattendedState{On: true, Since: 1000, ChangedAt: 1000, ChangedBy: by},
		Approved:        []Approval{{ID: "a", Kind: KindLead, State: StateApproved, DecidedAt: 1500, DecidedBy: &Client{Kind: ClientKindUnattended, Label: UnattendedLabel}}},
		Truncated:       true, NextBefore: 1500, Swept: 2, Pending: 1, ListFailed: true,
	}
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"approved", "changed_at", "changed_by", "held", "list_failed", "next_before", "on", "pending", "quotas", "since", "swept", "truncated"}
	if got := jsonKeys(t, raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v (state flattened)", got, want)
	}
	var back UnattendedView
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	back.Approved[0].Payload, full.Approved[0].Payload = nil, nil
	if !reflect.DeepEqual(back, full) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, full)
	}

	ev, err := json.Marshal(UnattendedEventValue{Op: "changed", State: UnattendedState{On: true, Since: 5, ChangedAt: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if string(ev) != `{"op":"changed","state":{"on":true,"since":5,"changed_at":5}}` {
		t.Fatalf("event value = %s", ev)
	}

	var put UnattendedPutRequest
	if err := json.Unmarshal([]byte(`{"on":false,"client":{"kind":"app","label":"x"}}`), &put); err != nil || put.On == nil || *put.On || put.Client.Label != "x" {
		t.Fatalf("put = %+v (%v): an explicit false must be seen", put, err)
	}
	put = UnattendedPutRequest{}
	if err := json.Unmarshal([]byte(`{"client":{"kind":"app","label":"x"}}`), &put); err != nil || put.On != nil {
		t.Fatalf("put without on = %+v (%v): on must read as missing", put, err)
	}
}

// Only the kinds the user put under the switch (U23, adopt U24 PL-1c); never the hook
// kinds, which stay with the person (U23 "不在範圍內").
func TestAutoApprovable_LeadSelfRelayAndAdoptOnly(t *testing.T) {
	for k, want := range map[Kind]bool{
		KindLead: true, KindSelfRelay: true, KindAdopt: true, KindMemberRelay: true,
		KindHookAsk: false, KindHookPermission: false, "adopt ": false, "": false, "LEAD": false,
	} {
		if got := AutoApprovable(k); got != want {
			t.Errorf("AutoApprovable(%q) = %v, want %v", k, got, want)
		}
	}
}
