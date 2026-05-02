package agent

import (
	"encoding/json"
	"testing"
)

// TestParseCodexTurnID exercises the L2 P1-T3 fail-soft helper. The helper is
// strictly read-only: any deserialization error, missing field, non-string
// type, or empty-string value yields "" so the caller (applyFrameEvent) can
// treat all failure modes uniformly via the spec §3.3.D parse-failure dispatch.
func TestParseCodexTurnID(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"valid_string", `{"turn_id":"t_a"}`, "t_a"},
		{"missing_field", `{}`, ""},
		{"malformed_json", `{turn_id":"t_a"`, ""},
		{"non_string_int", `{"turn_id":42}`, ""},
		{"non_string_null", `{"turn_id":null}`, ""},
		{"empty_string", `{"turn_id":""}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCodexTurnID(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Errorf("parseCodexTurnID(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
