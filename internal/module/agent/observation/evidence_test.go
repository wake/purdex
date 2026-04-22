package observation_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/module/agent/observation"
)

func TestEvidenceRef_StringValue_JSON(t *testing.T) {
	orig := observation.EvidenceRef{Key: "pid", Value: "12345"}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got observation.EvidenceRef
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Key != "pid" {
		t.Errorf("Key = %q; want \"pid\"", got.Key)
	}
	if got.Value != "12345" {
		t.Errorf("Value = %v; want \"12345\"", got.Value)
	}
}

func TestEvidenceRef_Int64Value_JSON(t *testing.T) {
	// NOTE: JSON numbers decode to float64 when unmarshaled into `any`.
	// This is stdlib behavior, not a bug. We only assert Key roundtrips and
	// Value is non-nil.
	orig := observation.EvidenceRef{Key: "pid", Value: int64(12345)}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got observation.EvidenceRef
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Key != "pid" {
		t.Errorf("Key = %q; want \"pid\"", got.Key)
	}
	if got.Value == nil {
		t.Error("Value is nil; want non-nil")
	}
	// JSON number → float64 is expected stdlib behavior.
	// Callers that need int64 must type-assert after Unmarshal.
}

func TestEvidenceRef_ActorKeyValue_JSON(t *testing.T) {
	ak := observation.ActorKey{SessionID: "s1", Generation: 2, ActorID: "primary:main"}
	orig := observation.EvidenceRef{Key: "parent_actor_key", Value: ak}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Inspect raw JSON bytes for ActorKey field names.
	if !bytes.Contains(data, []byte(`"session_id":"s1"`)) {
		t.Errorf("marshaled JSON missing session_id field; got: %s", data)
	}
	if !bytes.Contains(data, []byte(`"generation":2`)) {
		t.Errorf("marshaled JSON missing generation field; got: %s", data)
	}
	if !bytes.Contains(data, []byte(`"actor_id":"primary:main"`)) {
		t.Errorf("marshaled JSON missing actor_id field; got: %s", data)
	}
	// Note: we do NOT re-unmarshal into EvidenceRef because the `any` field
	// loses type information (ActorKey becomes map[string]interface{}).
}

func TestEvidenceRef_ZeroValue(t *testing.T) {
	ref := observation.EvidenceRef{}

	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("MarshalJSON on zero EvidenceRef panicked or errored: %v", err)
	}

	if !bytes.Contains(data, []byte(`"key":""`)) {
		t.Errorf("expected key:\"\" in JSON; got: %s", data)
	}
	if !bytes.Contains(data, []byte(`"value":null`)) {
		t.Errorf("expected value:null in JSON; got: %s", data)
	}
}
