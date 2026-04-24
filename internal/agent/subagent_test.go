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
