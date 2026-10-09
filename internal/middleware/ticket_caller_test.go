package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
)

// QP-1 task 3: a ticket that remembers its caller makes the WebSocket request that redeems it that caller.

type callerTickets struct{ byTicket map[string]devices.Caller }

func (c *callerTickets) Validate(t string) bool { _, ok := c.ValidateCaller(t); return ok }
func (c *callerTickets) ValidateCaller(t string) (devices.Caller, bool) {
	caller, ok := c.byTicket[t]
	delete(c.byTicket, t) // one-time
	return caller, ok
}

func wsGet(target string) *http.Request {
	req := httptest.NewRequest("GET", target, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	return req
}

// refreshDevices is an authenticator that can also look a device up by id (what a redeemed device ticket is held to).
type refreshDevices struct {
	fakeDevices
	byID    map[string]devices.Principal
	lookups []string
}

func (r *refreshDevices) RefreshPrincipal(id string) (devices.Principal, bool) {
	r.lookups = append(r.lookups, id)
	p, ok := r.byID[id]
	return p, ok
}

func TestTokenAuthWith_ATicketCarriesItsCreatorsIdentity(t *testing.T) {
	p := devices.Principal{ID: "d_aaaaaaaaaaaa", PairingID: "pair", ProfileID: "p_0123456789ab"}
	tickets := &callerTickets{byTicket: map[string]devices.Caller{
		"admin-t": {Admin: true}, "device-t": {Device: &p}, "anon-t": {},
	}}
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, dev := devices.PrincipalFrom(r.Context())
		who := "no-device"
		if dev {
			who = pr.ID + "/" + pr.ProfileID
		}
		admin := "not-admin"
		if devices.IsAdmin(r.Context()) {
			admin = "admin"
		}
		_, _ = w.Write([]byte(admin + "|" + who))
	})
	devs := &refreshDevices{byID: map[string]devices.Principal{p.ID: p}}
	h := middleware.TokenAuthWith(func() string { return "secret" }, tickets, devs)(probe)
	for ticket, want := range map[string]string{"admin-t": "admin|no-device", "device-t": "not-admin|d_aaaaaaaaaaaa/p_0123456789ab", "anon-t": "not-admin|no-device"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, wsGet("/ws/host-events?ticket="+ticket))
		if rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("%s: %d %q, want %q", ticket, rec.Code, rec.Body.String(), want)
		}
	}
	// One time: each is gone now.
	for _, ticket := range []string{"admin-t", "device-t", "anon-t"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, wsGet("/ws/host-events?ticket="+ticket))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s reused: %d", ticket, rec.Code)
		}
	}
}

// A ticket is still honoured only on a real handshake: one on a plain request is neither used nor consumed.
func TestTokenAuthWith_ATicketOnAPlainRequestIsNeitherUsedNorConsumed(t *testing.T) {
	p := devices.Principal{ID: "d_aaaaaaaaaaaa"}
	tickets := &callerTickets{byTicket: map[string]devices.Caller{"t": {Device: &p}}}
	h := middleware.TokenAuthWith(func() string { return "secret" }, tickets, nil)(echo)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/x?ticket=t", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("plain GET with a ticket: %d", rec.Code)
	}
	if _, still := tickets.byTicket["t"]; !still {
		t.Fatal("the ticket was consumed by a request it cannot serve")
	}
}

// A validator without ValidateCaller keeps working as before (bool only, no identity).
func TestTokenAuthWith_ABoolOnlyTicketValidatorStillWorks(t *testing.T) {
	h := middleware.TokenAuthWith(func() string { return "secret" }, &fakeTickets{valid: map[string]bool{"t": true}}, nil)(echo)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, wsGet("/ws/x?ticket=t"))
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
}

// A device's ticket is a snapshot taken up to 30 s earlier: redeeming it asks the store again. Revoked or unknown → refused
// before the handler runs; live → the CURRENT bindings, not the snapshot's. Mutation gate: trust the snapshot → red.
func TestTokenAuthWith_ADeviceTicketIsRefreshedAtRedemption(t *testing.T) {
	snapshot := devices.Principal{ID: "d_aaaaaaaaaaaa", PairingID: "pair", ProfileID: "p_000000000000"}
	current := devices.Principal{ID: "d_aaaaaaaaaaaa", PairingID: "pair", ProfileID: "p_111111111111"}
	ran := 0
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran++
		pr, _ := devices.PrincipalFrom(r.Context())
		_, _ = w.Write([]byte(pr.ProfileID))
	})
	tickets := &callerTickets{byTicket: map[string]devices.Caller{"live": {Device: &snapshot}, "gone": {Device: &snapshot}}}
	devs := &refreshDevices{byID: map[string]devices.Principal{snapshot.ID: current}}
	h := middleware.TokenAuthWith(func() string { return "secret" }, tickets, devs)(probe)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, wsGet("/ws/x?ticket=live"))
	if rec.Code != 200 || rec.Body.String() != "p_111111111111" {
		t.Fatalf("live: %d %q (the principal must be the current one)", rec.Code, rec.Body.String())
	}
	delete(devs.byID, snapshot.ID) // revoked after the ticket was minted
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, wsGet("/ws/x?ticket=gone"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a ticket of a revoked device: %d", rec.Code)
	}
	if ran != 1 {
		t.Fatalf("the handler ran %d times: a revoked device's ticket reached it", ran)
	}
}

// With no way to look a device up, a device ticket is not honoured; an admin ticket never needs one.
func TestTokenAuthWith_ADeviceTicketWithoutALookupIsRefused(t *testing.T) {
	p := devices.Principal{ID: "d_aaaaaaaaaaaa"}
	for name, devs := range map[string]devices.Authenticator{"no authenticator": nil, "bearer-only authenticator": &fakeDevices{}} {
		tickets := &callerTickets{byTicket: map[string]devices.Caller{"d": {Device: &p}, "a": {Admin: true}}}
		h := middleware.TokenAuthWith(func() string { return "secret" }, tickets, devs)(echo)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, wsGet("/ws/x?ticket=d"))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: device ticket %d", name, rec.Code)
		}
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, wsGet("/ws/x?ticket=a"))
		if rec.Code != 200 {
			t.Errorf("%s: admin ticket %d", name, rec.Code)
		}
	}
}

// An admin ticket does not consult the device store at all.
func TestTokenAuthWith_AnAdminTicketDoesNotLookAnyDeviceUp(t *testing.T) {
	devs := &refreshDevices{}
	tickets := &callerTickets{byTicket: map[string]devices.Caller{"a": {Admin: true}}}
	h := middleware.TokenAuthWith(func() string { return "secret" }, tickets, devs)(echo)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, wsGet("/ws/x?ticket=a"))
	if rec.Code != 200 || len(devs.lookups) != 0 {
		t.Fatalf("%d, lookups %v", rec.Code, devs.lookups)
	}
}
