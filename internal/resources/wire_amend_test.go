package resources

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLeaseRequest_ModeCommandCwdRoundTrip(t *testing.T) {
	in := LeaseRequest{ClientID: "c", Kind: "build", HolderPID: 7, Mode: CallQueue, Command: "go build ./...", Cwd: "/w"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out LeaseRequest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != CallQueue || out.Command != "go build ./..." || out.Cwd != "/w" {
		t.Fatalf("round trip lost a field: %+v", out)
	}
}

func TestLeaseResponse_NoticeAndPositionOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(LeaseResponse{State: StateHeld})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"notice", "position_id", "queued_reason"} {
		if _, ok := m[k]; ok {
			t.Fatalf("%s should be omitted when empty: %s", k, b)
		}
	}
	b, _ = json.Marshal(LeaseResponse{State: StateQueued, PositionID: "pos_ab12cd34", QueuedReason: QueuedOwnRunning, Notice: "x"})
	for _, want := range []string{`"position_id":"pos_ab12cd34"`, `"queued_reason":"own_running"`, `"notice":"x"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
}
