package team

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNormaliseTeamName(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want string
		bad  bool
	}{
		{"empty", "", "", false},
		{"spaces only", "   ", "", false},
		{"tabs and newlines only", " \t\n ", "", false},
		{"trimmed", "  build  ", "build", false},
		{"inner space kept", "build the thing", "build the thing", false},
		{"64 ascii bytes", strings.Repeat("a", 64), strings.Repeat("a", 64), false},
		{"65 ascii bytes", strings.Repeat("a", 65), "", true},
		{"21 CJK characters (63 bytes)", strings.Repeat("驗", 21), strings.Repeat("驗", 21), false},
		{"22 CJK characters (66 bytes)", strings.Repeat("驗", 22), "", true},
		{"bell", "a\x07b", "", true},
		{"inner newline", "a\nb", "", true},
		{"inner tab", "a\tb", "", true},
		{"escape", "a\x1b[2Jb", "", true},
		{"invalid utf-8", "a\xffb", "", true},
	} {
		got, err := NormaliseTeamName(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%s: %q accepted as %q", c.name, c.in, got)
			} else if !errors.Is(err, ErrTeamNameInvalid) {
				t.Errorf("%s: error %v does not wrap ErrTeamNameInvalid", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		} else if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// D-N6: the always-present / optional split of the team name on every wire
// type that carries it.
func TestTeamNameWireShapes(t *testing.T) {
	empty, named := "", "x"
	for _, c := range []struct {
		name string
		v    any
		has  string // a substring that must be present
		not  string // a key that must be absent ("" = no check)
	}{
		{"payload without a name still writes it", LeadPayload{Reason: "r", Roots: []string{"/w"}}, `"team_name":""`, ""},
		{"payload with a name", LeadPayload{TeamName: "build"}, `"team_name":"build"`, ""},
		{"grant nil name writes no key", Grant{MaxMembers: 2}, `"max_members":2`, `team_name`},
		{"grant empty name writes it", Grant{TeamName: &empty}, `"team_name":""`, ""},
		{"grant name", Grant{TeamName: &named}, `"team_name":"x"`, ""},
		{"request without a name writes no key", CreateApprovalRequest{ID: "i"}, `"id":"i"`, `team_name`},
		{"request with a name", CreateApprovalRequest{TeamName: "n"}, `"team_name":"n"`, ""},
		{"team always writes it", Team{ID: "t"}, `"team_name":""`, ""},
		{"roster always writes it", TeamRoster{ID: "t"}, `"team_name":""`, ""},
		{"roster with a name", TeamRoster{ID: "t", TeamName: "build"}, `"team_name":"build"`, ""},
	} {
		raw, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), c.has) {
			t.Errorf("%s: %s lacks %s", c.name, raw, c.has)
		}
		if c.not != "" && strings.Contains(string(raw), c.not) {
			t.Errorf("%s: %s must not contain %s", c.name, raw, c.not)
		}
	}

	// A grant decoded from a body without the key keeps a nil pointer; with
	// "" it is a non-nil empty string (D-N3).
	var g Grant
	if err := json.Unmarshal([]byte(`{"max_members":2}`), &g); err != nil || g.TeamName != nil {
		t.Errorf("absent key: err=%v TeamName=%v", err, g.TeamName)
	}
	if err := json.Unmarshal([]byte(`{"team_name":""}`), &g); err != nil || g.TeamName == nil || *g.TeamName != "" {
		t.Errorf("empty key: err=%v TeamName=%v", err, g.TeamName)
	}
}
