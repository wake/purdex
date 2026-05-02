package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSubagentRef_JSONRoundTripFull(t *testing.T) {
	ref := SubagentRef{
		ID:              "a",
		Type:            "cc",
		StartedAt:       123,
		SourcePID:       1000,
		SourceStartTime: "t0",
		IsProxy:         true,
	}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"source_pid"`) {
		t.Errorf("missing source_pid key: %s", s)
	}
	if !strings.Contains(s, `"source_start_time"`) {
		t.Errorf("missing source_start_time key: %s", s)
	}
	if !strings.Contains(s, `"is_proxy":true`) {
		t.Errorf("missing is_proxy:true: %s", s)
	}

	var got SubagentRef
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != ref {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, ref)
	}
}

func TestSubagentRef_OmitsIsProxyWhenFalse(t *testing.T) {
	ref := SubagentRef{
		ID:        "a",
		Type:      "cc",
		StartedAt: 1,
		IsProxy:   false,
	}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "is_proxy") {
		t.Errorf("is_proxy should be omitted when false, got: %s", s)
	}
}

// TestSubagentRef_JSONRoundTrip_SourceTurnID is the L2 P1-T2 contract test:
// SourceTurnID round-trips through JSON when set and is omitted from the wire
// representation when empty (omitempty), keeping the field backward-compatible
// with subagents_json blobs persisted before the L2 expansion.
func TestSubagentRef_JSONRoundTrip_SourceTurnID(t *testing.T) {
	// Case (a): SourceTurnID set → key and value present, round-trips.
	ref := SubagentRef{
		ID:              "proxy:codex:42:t0",
		Type:            "codex",
		StartedAt:       42,
		SourcePID:       42,
		SourceStartTime: "t0",
		IsProxy:         true,
		SourceTurnID:    "t_a",
	}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"source_turn_id":"t_a"`) {
		t.Errorf("missing source_turn_id key/value: %s", s)
	}
	var got SubagentRef
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != ref {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, ref)
	}

	// Case (b): SourceTurnID empty → omitted from wire format.
	ref2 := SubagentRef{
		ID:              "proxy:codex:42:t0",
		Type:            "codex",
		StartedAt:       42,
		SourcePID:       42,
		SourceStartTime: "t0",
		IsProxy:         true,
	}
	data2, err := json.Marshal(ref2)
	if err != nil {
		t.Fatalf("Marshal empty: %v", err)
	}
	if strings.Contains(string(data2), "source_turn_id") {
		t.Errorf("source_turn_id should be omitted when empty, got: %s", string(data2))
	}
	var got2 SubagentRef
	if err := json.Unmarshal(data2, &got2); err != nil {
		t.Fatalf("Unmarshal empty: %v", err)
	}
	if got2.SourceTurnID != "" {
		t.Errorf("expected zero-value SourceTurnID after round-trip, got %q", got2.SourceTurnID)
	}
}

func TestSubagentRef_NativeZeroSourceFieldsValid(t *testing.T) {
	ref := SubagentRef{
		ID:        "a",
		Type:      "cc",
		StartedAt: 1,
	}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"source_pid":0`) {
		t.Errorf("expected source_pid:0 present: %s", s)
	}
	if !strings.Contains(s, `"source_start_time":""`) {
		t.Errorf("expected source_start_time:\"\" present: %s", s)
	}

	var got SubagentRef
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != ref {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, ref)
	}
}
