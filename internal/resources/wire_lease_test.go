package resources

import (
	"encoding/json"
	"testing"
	"time"
)

// The lease views carry exactly the names of the App contract.
func TestSnapshot_LeaseFieldsShape(t *testing.T) {
	s := fixedSnapshot()
	s.Leases = []LeaseView{{ID: "l1", Kind: "test-full", Weight: 35, Charge: 17.5, Use: 12.5, SessionID: "s-1", AgeS: 40, Overrun: true}}
	s.Waiters = []WaiterView{{ID: "w1", Kind: "build", Weight: 35, Position: 1, WaitedS: 3, DeadlineInS: 297}}
	s.Recent = []RecentView{{ID: "r1", Weight: 20, EndReason: EndReleased, WaitedMS: 1500,
		EndedAt: time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC)}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Leases  []map[string]any `json:"leases"`
		Waiters []map[string]any `json:"waiters"`
		Recent  []map[string]any `json:"recent"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	has := func(row map[string]any, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := row[k]; !ok {
				t.Errorf("missing %q in %v", k, row)
			}
		}
	}
	has(m.Leases[0], "id", "kind", "weight", "charge", "use", "session_id", "age_s", "overrun")
	has(m.Waiters[0], "id", "kind", "weight", "position", "waited_s", "deadline_in_s")
	has(m.Recent[0], "id", "weight", "end_reason", "overrun", "waited_ms", "ended_at")
	if _, ok := m.Recent[0]["kind"]; ok {
		t.Error("an empty kind must be omitted")
	}
}
