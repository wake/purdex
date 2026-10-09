package devices

import (
	"net/http"
	"testing"
)

// A revoke subscriber that panics neither skips the next subscriber nor changes the revoke's answer.
func TestRevoke_APanickingSubscriberDoesNotStopTheOthers(t *testing.T) {
	e := newEnv(t)
	heard := 0
	e.mod.SubscribeRevoked(func([]string) { panic("boom") })
	e.mod.SubscribeRevoked(func(ids []string) { heard += len(ids) })
	row, _, err := e.mod.store.Mint(MintRequest{PairingID: pairingA, Label: "iPhone", CreatedBy: "Purdex.app", UseWithin: 600e9})
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.call("DELETE", "/api/devices/"+row.ID, e.admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if heard != 1 {
		t.Fatalf("the second subscriber heard %d ids", heard)
	}
}
