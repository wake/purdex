package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParseCodexTurnID exercises the L2 P1-T3 fail-soft helper. The helper is
// strictly read-only: any deserialization error, missing field, non-string
// type, empty-string value, or over-cap length yields "" so the caller
// (applyFrameEvent) can treat all failure modes uniformly via the spec
// §3.3.D parse-failure dispatch.
func TestParseCodexTurnID(t *testing.T) {
	atCap := strings.Repeat("a", maxCodexTurnIDLen)
	overCap := strings.Repeat("a", maxCodexTurnIDLen+1)

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
		// Round-2 A4: turn_id length cap. At-cap (512 bytes) accepted,
		// over-cap rejected as parse-failure.
		{"at_cap_accepted", `{"turn_id":"` + atCap + `"}`, atCap},
		{"over_cap_rejected", `{"turn_id":"` + overCap + `"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCodexTurnID(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Errorf("parseCodexTurnID(len=%d) = %q (len=%d), want %q (len=%d)", len(tc.raw), got, len(got), tc.want, len(tc.want))
			}
		})
	}
}
