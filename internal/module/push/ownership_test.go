package push

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/push"
)

// QP-1b-ii task 6: a paired phone registers, lists and removes only its own push registrations; an APNs token registered
// again moves to whoever registers it.

func (e *env) as(p *devices.Principal, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if p != nil {
		req = req.WithContext(devices.WithPrincipal(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func regBody(token string) string {
	return `{"token":"` + token + `","bundle_id":"` + push.BundleID + `","env":"sandbox","platform":"ios","device_name":"iPhone","host_label":"mlab","locale":"zh-TW","prefs":{"tabs":[]}}`
}

func listed(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var b struct {
		Devices []struct {
			DeviceID string `json:"device_id"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, d := range b.Devices {
		ids = append(ids, d.DeviceID)
	}
	return ids
}

// as lets the revoke environment (real devices module, real paired phones) use the same call shape.
func (e *revokeEnv) as(p *devices.Principal, method, path, body string) *httptest.ResponseRecorder {
	if body == "" {
		return e.call(p, method, path, nil)
	}
	return e.call(p, method, path, body)
}

func (e *revokeEnv) phones(t *testing.T) (a, b *devices.Principal) {
	pa, pb := e.pair(t, pairA), e.pair(t, pairB)
	return &pa, &pb
}

func TestOwnership_APhoneListsAndRemovesOnlyItsOwnRegistrations(t *testing.T) {
	e := newRevokeEnv(t)
	phoneA, phoneB := e.phones(t)
	idA, idB, idAdmin := push.DeviceID(tokA), push.DeviceID(tokB), push.DeviceID(strings.Repeat("c3", 32))
	for _, r := range []struct {
		p   *devices.Principal
		tok string
	}{{phoneA, tokA}, {phoneB, tokB}, {nil, strings.Repeat("c3", 32)}} {
		if rec := e.as(r.p, "POST", "/api/push/devices", regBody(r.tok)); rec.Code != http.StatusOK {
			t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
		}
	}
	if got := listed(t, e.as(phoneA, "GET", "/api/push/devices", "")); len(got) != 1 || got[0] != idA {
		t.Fatalf("phone A sees %v, want only %s", got, idA)
	}
	if got := listed(t, e.as(nil, "GET", "/api/push/devices", "")); len(got) != 3 {
		t.Fatalf("the admin sees %v, want all three", got)
	}
	// Another phone's, the admin's and a made-up id are all the same 404 to a phone, and nothing is removed.
	for _, id := range []string{idB, idAdmin, push.DeviceID(strings.Repeat("d4", 32)), "not-an-id", "x"} {
		if rec := e.as(phoneA, "DELETE", "/api/push/devices/"+id, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("phone A deleting %s: %d", id, rec.Code)
		}
	}
	if got := listed(t, e.as(nil, "GET", "/api/push/devices", "")); len(got) != 3 {
		t.Fatalf("a refused delete removed something: %v", got)
	}
	if rec := e.as(phoneA, "DELETE", "/api/push/devices/"+idA, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("phone A deleting its own: %d", rec.Code)
	}
	if got := listed(t, e.as(nil, "GET", "/api/push/devices", "")); len(got) != 2 {
		t.Fatalf("after its own delete: %v", got)
	}
	// The admin still removes anyone's (and an unknown id is still an idempotent 204).
	if rec := e.as(nil, "DELETE", "/api/push/devices/"+idB, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete: %d", rec.Code)
	}
	if rec := e.as(nil, "DELETE", "/api/push/devices/"+idB, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("admin repeat delete: %d", rec.Code)
	}
}

// Registering an APNs token that is already registered moves it to the caller (a re-paired phone keeps its pushes), both
// ways: admin → phone, phone A → phone B, phone → admin.
func TestOwnership_ReRegisteringAnAPNsTokenMovesItToTheCaller(t *testing.T) {
	e := newRevokeEnv(t)
	phoneA, phoneB := e.phones(t)
	id := push.DeviceID(tokA)
	e.as(nil, "POST", "/api/push/devices", regBody(tokA))
	for i, caller := range []*devices.Principal{phoneA, phoneB, nil, phoneA} {
		if rec := e.as(caller, "POST", "/api/push/devices", regBody(tokA)); rec.Code != http.StatusOK {
			t.Fatalf("step %d: %d", i, rec.Code)
		}
		owned := func(p *devices.Principal) bool {
			for _, got := range listed(t, e.as(p, "GET", "/api/push/devices", "")) {
				if got == id {
					return true
				}
			}
			return false
		}
		if caller != nil {
			if !owned(caller) {
				t.Fatalf("step %d: the registration did not move to %s", i, caller.ID)
			}
			for _, other := range []*devices.Principal{phoneA, phoneB} {
				if other.ID != caller.ID && owned(other) {
					t.Fatalf("step %d: %s still sees it", i, other.ID)
				}
			}
		} else if owned(phoneA) || owned(phoneB) {
			t.Fatalf("step %d: the admin's re-registration left it with a phone", i)
		}
	}
}

// A push.db written before QR pairing (no owner column) opens, gains the column, and its rows belong to no phone.
func TestStore_AnOlderDatabaseGainsTheOwnerColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE push_devices (
		token TEXT PRIMARY KEY, device_id TEXT NOT NULL UNIQUE,
		bundle_id TEXT NOT NULL, env TEXT NOT NULL, platform TEXT NOT NULL,
		device_name TEXT NOT NULL, host_label TEXT NOT NULL, locale TEXT NOT NULL, prefs TEXT NOT NULL,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
		last_sent_at INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '');
		INSERT INTO push_devices VALUES ('` + tokA + `','` + push.DeviceID(tokA) + `','b','sandbox','ios','n','h','en','{"tabs":[]}',1,1,0,'')`)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.List()
	if err != nil || len(rows) != 1 || rows[0].OwnerDeviceID != "" {
		t.Fatalf("rows %v err %v", rows, err)
	}
	if _, err := s.Upsert(func() push.Device { d := dev(tokA); d.OwnerDeviceID = "d_aaaaaaaaaaaa"; return d }()); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(path) // opening it again (the column now exists) is fine
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if rows, _ := s2.List(); len(rows) != 1 || rows[0].OwnerDeviceID != "d_aaaaaaaaaaaa" {
		t.Fatalf("owner not kept across a reopen: %v", rows)
	}
}
