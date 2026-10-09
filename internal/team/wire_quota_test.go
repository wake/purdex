package team

import (
	"encoding/json"
	"testing"
)

// The relay quota's wire shapes (#2062). Mutation gate: rename a json key → red.
func TestWireQuota_JSONShapes(t *testing.T) {
	two := 2
	for _, c := range []struct {
		name string
		v    any
		want string
	}{
		{"numbers", RelayQuota{SelfLeft: 3, MemberPoolLeft: 1}, `{"self_left":3,"member_pool_left":1,"rev":0}`},
		{"put, one field", RelayQuotaPutRequest{SessionID: "s", SelfLeft: &two, Client: Client{Kind: "app", Label: "x"}},
			`{"session_id":"s","self_left":2,"client":{"kind":"app","label":"x"}}`},
		{"view", RelayQuotaView{SessionID: "s", RootSessionID: "r", RelayQuota: RelayQuota{SelfLeft: 1}, UpdatedAt: 5, UpdatedBy: "x"},
			`{"session_id":"s","root_session_id":"r","self_left":1,"member_pool_left":0,"rev":0,"updated_at":5,"updated_by":"x"}`},
		{"view, provisional root", RelayQuotaView{SessionID: "s", RootSessionID: "s", PendingLineage: true},
			`{"session_id":"s","root_session_id":"s","self_left":0,"member_pool_left":0,"rev":0,"pending_lineage":true,"updated_at":0}`},
		{"numbers with a version", RelayQuota{SelfLeft: 1, Rev: 7}, `{"self_left":1,"member_pool_left":0,"rev":7}`},
		{"unattended view with none live: [] not null", UnattendedView{Quotas: []SessionQuota{}}, `{"on":false,"since":0,"changed_at":0,"approved":[],"truncated":false,"quotas":[],"held":null}`},
		{"event", RelayQuotaEventValue{Op: "changed", RootSessionID: "r", RelayQuota: RelayQuota{MemberPoolLeft: 4}},
			`{"op":"changed","root_session_id":"r","self_left":0,"member_pool_left":4,"rev":0}`},
		{"session row", SessionQuota{SessionID: "s", RootSessionID: "r", Address: "a/_abc123", IsLead: true, RelayQuota: RelayQuota{SelfLeft: 2}},
			`{"session_id":"s","root_session_id":"r","address":"a/_abc123","is_lead":true,"self_left":2,"member_pool_left":0,"rev":0}`},
		{"unattended view whose quotas could not be read: null, never absent", UnattendedView{}, `{"on":false,"since":0,"changed_at":0,"approved":[],"truncated":false,"quotas":null,"held":null}`},
	} {
		raw, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != c.want {
			t.Errorf("%s: %s, want %s", c.name, raw, c.want)
		}
	}
	if RelayQuotaRoute != "/api/team/relay-quota" || RelayQuotaEventType != "team.relay_quota" || MaxRelayQuota != 99 {
		t.Error("route, event type or maximum changed")
	}
}
