package workbook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// The real module registered into core is what /api/info announces journal.v1 and journal.confirm.v1 from; with the journal
// switched off neither is announced.
// Mutation gate: drop the journal.confirm.v1 append in capabilityList -> red; announce it without the journal flag -> red.
func TestWire_InfoAnnouncesJournalConfirmForTheRealModule(t *testing.T) {
	caps := func(k *wireKit) []string {
		k.c.AddModule(k.m)
		mux := http.NewServeMux()
		k.c.RegisterCoreRoutes(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("/api/info = %d", rec.Code)
		}
		var body struct {
			Capabilities []string `json:"capabilities"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body.Capabilities
	}
	on := caps(newWire(t, nil))
	if !slices.Contains(on, "journal.v1") || !slices.Contains(on, "journal.confirm.v1") {
		t.Errorf("journal on: capabilities = %v", on)
	}
	off := caps(newWire(t, boolp(false)))
	if slices.Contains(off, "journal.v1") || slices.Contains(off, "journal.confirm.v1") {
		t.Errorf("journal off: capabilities = %v", off)
	}
}
