package team

import (
	"encoding/json"
	"testing"
)

// The shapes of the pause wire (unattended quota pause spec §3.1): snake_case, sessions:[] never null, the roster's
// `paused` is always there and null for a session that is not paused.
func TestWirePause_JSONShapes(t *testing.T) {
	if PausedRoute != "/api/team/paused" || PauseCauseQuota != "quota" || PauseCauseLead != "lead" ||
		PauseRoleSolo != "solo" || PauseRoleLead != "lead" || PauseRoleMember != "member" {
		t.Fatal("a pause wire constant changed")
	}
	for _, c := range []struct {
		name string
		v    any
		want string
	}{
		{"no sessions", PausedView{}, `{"sessions":[]}`},
		{"one session", PausedView{Sessions: []PausedSession{{SessionID: "s", Ref: "_abc123", Title: "t", Role: PauseRoleMember,
			Causes: []string{PauseCauseQuota, PauseCauseLead}, Since: 9, LeadRef: "_def456", Bounced: 2}}},
			`{"sessions":[{"session_id":"s","ref":"_abc123","title":"t","role":"member","causes":["quota","lead"],"since":9,"lead_ref":"_def456","bounced":2}]}`},
		{"roster pause", RosterPause{Causes: []string{PauseCauseLead}, Since: 4}, `{"causes":["lead"],"since":4}`},
		{"a paused roster session", RosterSession{SessionID: "s", Ref: "_abc123", Address: "a/_abc123", Paused: &RosterPause{Causes: []string{PauseCauseQuota}, Since: 4}},
			`{"session_id":"s","ref":"_abc123","address":"a/_abc123","live":false,"relay_quota":{"self_left":0,"member_pool_left":0,"rev":0},"paused":{"causes":["quota"],"since":4}}`},
		{"an unpaused roster session", RosterSession{SessionID: "s", Ref: "_abc123", Address: "a/_abc123"},
			`{"session_id":"s","ref":"_abc123","address":"a/_abc123","live":false,"relay_quota":{"self_left":0,"member_pool_left":0,"rev":0},"paused":null}`},
	} {
		raw, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != c.want {
			t.Errorf("%s: %s, want %s", c.name, raw, c.want)
		}
	}
}
