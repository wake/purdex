package hosttransfer

import (
	"encoding/json"
	"testing"
)

// A claimed pairing keeps its status and nothing else: the rows (which hold device tokens) are dropped from memory.
func TestPairingTombstone_HoldsNoRows(t *testing.T) {
	clk := newFakeClock()
	s := newStore(clk.now, randomishGen())
	code, _, err := s.CreatePairing(json.RawMessage(`[{"token":"pdxd_secret"}]`), pairingMaxTTL)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Claim("100.64.0.1", code); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[code]; !e.claimed || e.payload != nil {
		t.Fatalf("tombstone = %+v", e)
	}
}
