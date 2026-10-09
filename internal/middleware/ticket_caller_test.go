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
	h := middleware.TokenAuthWith(func() string { return "secret" }, tickets, nil)(probe)
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
