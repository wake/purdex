package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
)

// QP-1 task 2: TokenAuth accepts a live device token beside the admin token and puts its principal in the request context.

type fakeDevices struct {
	live  map[string]devices.Principal
	calls []string
}

func (f *fakeDevices) AuthenticateToken(token string) (devices.Principal, bool) {
	f.calls = append(f.calls, token)
	p, ok := f.live[token]
	return p, ok
}

func newDeviceToken(t *testing.T) string {
	t.Helper()
	tok, err := devices.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// echo answers with the principal the request carries ("" for none).
var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	if p, ok := devices.PrincipalFrom(r.Context()); ok {
		_, _ = w.Write([]byte(p.ID + "|" + p.PairingID + "|" + p.ProfileID))
		return
	}
	_, _ = w.Write([]byte("admin"))
})

func do(h http.Handler, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/x", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTokenAuthWith_AdminTokenStillWorksAndCarriesNoPrincipal(t *testing.T) {
	devs := &fakeDevices{}
	h := middleware.TokenAuthWith(func() string { return "secret" }, nil, devs)(echo)
	rec := do(h, "Bearer secret")
	if rec.Code != 200 || rec.Body.String() != "admin" {
		t.Fatalf("admin: %d %q", rec.Code, rec.Body.String())
	}
	if len(devs.calls) != 0 {
		t.Fatal("the admin token was offered to the device authenticator")
	}
}

func TestTokenAuthWith_ALiveDeviceTokenPassesWithItsPrincipal(t *testing.T) {
	tok := newDeviceToken(t)
	devs := &fakeDevices{live: map[string]devices.Principal{tok: {ID: "d_aaaaaaaaaaaa", PairingID: "pair-1", ProfileID: "p_main"}}}
	h := middleware.TokenAuthWith(func() string { return "secret" }, nil, devs)(echo)
	rec := do(h, "Bearer "+tok)
	if rec.Code != 200 || rec.Body.String() != "d_aaaaaaaaaaaa|pair-1|p_main" {
		t.Fatalf("device: %d %q", rec.Code, rec.Body.String())
	}
	if rec := do(h, "bearer "+tok); rec.Code != 200 { // the scheme is case-insensitive as for the admin token
		t.Fatalf("lowercase scheme: %d", rec.Code)
	}
}

func TestTokenAuthWith_RevokedUnknownOrExpiredDeviceTokensAreRefused(t *testing.T) {
	live := newDeviceToken(t)
	gone := newDeviceToken(t) // not in the authenticator: revoked, unknown, or past use_by all look the same here
	devs := &fakeDevices{live: map[string]devices.Principal{live: {ID: "d_aaaaaaaaaaaa"}}}
	h := middleware.TokenAuthWith(func() string { return "secret" }, nil, devs)(echo)
	for name, auth := range map[string]string{"not live": "Bearer " + gone, "no scheme": gone, "empty": "", "wrong scheme": "Basic " + live} {
		if rec := do(h, auth); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

// Only something shaped like a device token is looked up: a random bearer never reaches the store. Mutation gate: look
// every bearer up → red.
func TestTokenAuthWith_OnlyDeviceShapedBearersAreLookedUp(t *testing.T) {
	devs := &fakeDevices{}
	h := middleware.TokenAuthWith(func() string { return "secret" }, nil, devs)(echo)
	for _, b := range []string{"nope", "pdxp_" + strings.Repeat("a", 32), "pdxd_short", "pdxd_" + strings.Repeat("Z", 32)} {
		do(h, "Bearer "+b)
	}
	if len(devs.calls) != 0 {
		t.Fatalf("looked up %d non-device bearers", len(devs.calls))
	}
	do(h, "Bearer "+newDeviceToken(t))
	if len(devs.calls) != 1 {
		t.Fatalf("a device-shaped bearer was looked up %d times", len(devs.calls))
	}
}

// An empty admin token leaves auth off as today: device tokens add nothing there, and nothing carries a principal.
func TestTokenAuthWith_EmptyAdminTokenIsUnchanged(t *testing.T) {
	tok := newDeviceToken(t)
	devs := &fakeDevices{live: map[string]devices.Principal{tok: {ID: "d_aaaaaaaaaaaa"}}}
	h := middleware.TokenAuthWith(func() string { return "" }, nil, devs)(echo)
	for _, auth := range []string{"", "Bearer " + tok, "Bearer whatever"} {
		if rec := do(h, auth); rec.Code != 200 || rec.Body.String() != "admin" {
			t.Fatalf("auth %q: %d %q", auth, rec.Code, rec.Body.String())
		}
	}
	if len(devs.calls) != 0 {
		t.Fatal("the device authenticator was consulted with auth off")
	}
}

func TestTokenAuthWith_NilAuthenticatorIsTokenAuth(t *testing.T) {
	tok := newDeviceToken(t)
	h := middleware.TokenAuthWith(func() string { return "secret" }, nil, nil)(echo)
	if rec := do(h, "Bearer "+tok); rec.Code != http.StatusUnauthorized {
		t.Fatalf("device token with no authenticator: %d", rec.Code)
	}
	if rec := do(h, "Bearer secret"); rec.Code != 200 {
		t.Fatalf("admin: %d", rec.Code)
	}
}

// A device bearer is never taken for the admin token, and the admin token is checked first (a device token equal to it
// cannot exist: different prefix). The principal does not leak between requests.
func TestTokenAuthWith_PrincipalDoesNotLeakBetweenRequests(t *testing.T) {
	tok := newDeviceToken(t)
	devs := &fakeDevices{live: map[string]devices.Principal{tok: {ID: "d_aaaaaaaaaaaa"}}}
	h := middleware.TokenAuthWith(func() string { return "secret" }, nil, devs)(echo)
	if rec := do(h, "Bearer "+tok); !strings.HasPrefix(rec.Body.String(), "d_") {
		t.Fatal("device request lost its principal")
	}
	if rec := do(h, "Bearer secret"); rec.Body.String() != "admin" {
		t.Fatalf("an admin request after a device one carries %q", rec.Body.String())
	}
}

// A WebSocket handshake authenticated by a device bearer carries the principal too (the iOS App connects that way).
func TestTokenAuthWith_BearerOnAWebSocketHandshakeCarriesThePrincipal(t *testing.T) {
	tok := newDeviceToken(t)
	devs := &fakeDevices{live: map[string]devices.Principal{tok: {ID: "d_aaaaaaaaaaaa"}}}
	h := middleware.TokenAuthWith(func() string { return "secret" }, nil, devs)(echo)
	req := httptest.NewRequest("GET", "/ws/host-events", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "d_") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}
