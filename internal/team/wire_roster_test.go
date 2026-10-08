package team

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The roster's JSON (PL-1f′): snake_case, teams:[] and members:[] never
// null (the App replaces its store from them), a member is its session's
// fields flattened plus state / origin / joined_at, and the optional text
// fields are left out when empty while live is always there.
func TestWireRoster_JSONShapes(t *testing.T) {
	if RosterEventType != "team.roster" {
		t.Errorf("RosterEventType = %q, want team.roster", RosterEventType)
	}
	for _, c := range []struct {
		name string
		v    any
		want string
	}{
		{"empty roster", Roster{}, `{"teams":[]}`},
		{"empty event", RosterEventValue{Op: "snapshot"}, `{"op":"snapshot","teams":[]}`},
		{"team without members", TeamRoster{ID: "t", HostID: "h", CreatedAt: 5, Lead: RosterSession{SessionID: "s", Ref: "_abc123", Address: "a/_abc123"}},
			`{"id":"t","host_id":"h","team_name":"","team_label":"","created_at":5,"lead":{"session_id":"s","ref":"_abc123","address":"a/_abc123","live":false},"members":[]}`},
	} {
		raw, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != c.want {
			t.Errorf("%s: %s, want %s", c.name, raw, c.want)
		}
	}

	// model / effort / context (PL-1f′3): left out when empty / nil, written
	// in the member context's own shape when there.
	used := 41.5
	withCtx := RosterSession{SessionID: "s", Ref: "_abc123", Address: "a/_abc123", Live: true,
		Model: "sonnet", Effort: "high", Context: &MemberContext{UsedPercentage: &used, Window: 200000, ModelID: "claude-sonnet-5-5", Effort: "high", At: 9}}
	raw, err := json.Marshal(withCtx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"session_id":"s","ref":"_abc123","address":"a/_abc123","live":true,"model":"sonnet","effort":"high","context":{"used_percentage":41.5,"window":200000,"model_id":"claude-sonnet-5-5","effort":"high","at":9}}`; string(raw) != want {
		t.Errorf("session with model/effort/context: %s, want %s", raw, want)
	}
	raw, err = json.Marshal(RosterSession{SessionID: "s", Ref: "_abc123", Address: "a/_abc123"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"model"`, `"effort"`, `"context"`} {
		if strings.Contains(string(raw), k) {
			t.Errorf("session without them still writes %s: %s", k, raw)
		}
	}

	full := Roster{Teams: []TeamRoster{{
		ID: "t", HostID: "h", TeamName: "build", CreatedAt: 5,
		Lead: RosterSession{SessionID: "s0", Ref: "_lead01", Address: "a/lead", Title: "lead", Name: "n0", TmuxSession: "main", Live: true},
		Members: []RosterMember{{
			RosterSession: RosterSession{SessionID: "s1", Ref: "_mem001", Address: "a/_mem001", TmuxSession: "tm-0123456789", Live: true},
			State:         MemberActive, Origin: MemberOriginSpawned, JoinedAt: 7,
		}},
	}}}
	raw, err = json.Marshal(RosterEventValue{Op: "changed", Teams: full.Teams})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"op": "changed", "teams": []any{map[string]any{
		"id": "t", "host_id": "h", "team_name": "build", "team_label": "", "created_at": 5.0,
		"lead": map[string]any{"session_id": "s0", "ref": "_lead01", "address": "a/lead", "title": "lead", "name": "n0", "tmux_session": "main", "live": true},
		"members": []any{map[string]any{
			"session_id": "s1", "ref": "_mem001", "address": "a/_mem001", "tmux_session": "tm-0123456789", "live": true,
			"state": "active", "origin": "spawned", "joined_at": 7.0}},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("event = %s\nwant %v", raw, want)
	}
}
