package agent

import (
	"encoding/json"
	"reflect"
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
	if !reflect.DeepEqual(got, ref) {
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
	if !reflect.DeepEqual(got, ref) {
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

// TestSubagentRef_DelegatingFields_OmitemptyJSON is the Phase 1 P1-T1 contract
// test for the cc-native delegation flag (spec §3.1 / §3.5). Both
// Delegating and DelegatingToolUseIDs ride the omitempty wire pattern so
// existing subagents_json blobs deserialize cleanly (no DB migration). When
// either is set, the keys must appear in the marshaled output.
func TestSubagentRef_DelegatingFields_OmitemptyJSON(t *testing.T) {
	// Case (a): zero-value Delegating fields → keys omitted.
	zero := SubagentRef{
		ID:        "a",
		Type:      "cc",
		StartedAt: 1,
	}
	data, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("Marshal zero: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "delegating") {
		t.Errorf("delegating-related keys must be omitted on zero value, got: %s", s)
	}

	// Case (b): set Delegating=true + populated tool_use_ids → keys present.
	marked := SubagentRef{
		ID:                   "a",
		Type:                 "cc",
		StartedAt:            1,
		Delegating:           true,
		DelegatingToolUseIDs: []string{"toolUseA"},
	}
	data2, err := json.Marshal(marked)
	if err != nil {
		t.Fatalf("Marshal marked: %v", err)
	}
	s2 := string(data2)
	if !strings.Contains(s2, `"delegating":true`) {
		t.Errorf("missing delegating:true: %s", s2)
	}
	if !strings.Contains(s2, `"delegating_tool_use_ids":["toolUseA"]`) {
		t.Errorf("missing delegating_tool_use_ids array: %s", s2)
	}

	// Round-trip preserves the slice.
	var got SubagentRef
	if err := json.Unmarshal(data2, &got); err != nil {
		t.Fatalf("Unmarshal marked: %v", err)
	}
	if !got.Delegating {
		t.Errorf("expected Delegating=true after round-trip, got false")
	}
	if len(got.DelegatingToolUseIDs) != 1 || got.DelegatingToolUseIDs[0] != "toolUseA" {
		t.Errorf("DelegatingToolUseIDs round-trip mismatch: %+v", got.DelegatingToolUseIDs)
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
	if !reflect.DeepEqual(got, ref) {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, ref)
	}
}
