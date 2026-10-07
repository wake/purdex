package team

import (
	"encoding/json"
	"testing"
)

// The JSON names are the contract across Go, CLI, SPA and the mod (plan
// preamble "Wire additions"); this pins them and the omitempty of the
// optional fields.
func TestRelayOp_JSONNames(t *testing.T) {
	pct := 72.4
	op := RelayOp{ID: "op", Kind: RelayKindSelf, HostID: "h", SessionID: "s", Ref: "_abc123", RequestID: "r",
		State: RelayAwaitingApproval, HandoffPath: "/d/relay/op.md", UsedPercentage: &pct, CreatedAt: 1, UpdatedAt: 2}
	b, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"op","kind":"self","host_id":"h","session_id":"s","ref":"_abc123","request_id":"r","state":"awaiting_approval","handoff_path":"/d/relay/op.md","used_percentage":72.4,"created_at":1,"updated_at":2}`
	if string(b) != want {
		t.Fatalf("RelayOp JSON =\n%s\nwant\n%s", b, want)
	}
	var back RelayOp
	if err := json.Unmarshal(b, &back); err != nil || back.State != RelayAwaitingApproval || *back.UsedPercentage != 72.4 {
		t.Fatalf("round trip: %+v err=%v", back, err)
	}
}

func TestRelayState_Terminal(t *testing.T) {
	for s, want := range map[RelayState]bool{
		RelayAwaitingApproval: false, RelayRequested: false, RelayClaimed: false, RelayWriting: false,
		RelayWritten: false, RelayCleared: false, RelayDone: true, RelayFailed: true, RelayCancelled: true,
	} {
		if s.Terminal() != want {
			t.Errorf("%s.Terminal() = %v, want %v", s, s.Terminal(), want)
		}
	}
}

func TestAPIError_CarriesOp(t *testing.T) {
	b, _ := json.Marshal(APIError{Error: ErrRelayOpen, Op: &RelayOp{ID: "op", State: RelayClaimed}})
	var e APIError
	if err := json.Unmarshal(b, &e); err != nil || e.Op == nil || e.Op.ID != "op" {
		t.Fatalf("APIError op: %s err=%v", b, err)
	}
	if b, _ := json.Marshal(APIError{Error: ErrBadRequest}); string(b) != `{"error":"bad_request"}` {
		t.Fatalf("op must be omitted when nil: %s", b)
	}
	if SelfRelayDeadlineS != 600 || RelayThresholdPct != 70 || RelayMinGrowth != 20000 || RelayDir != "relay" {
		t.Fatal("limits moved")
	}
}
