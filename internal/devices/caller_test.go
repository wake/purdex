package devices

import (
	"context"
	"testing"
)

// QP-1 task 3: who a request, or a one-time ticket, speaks for.

func TestCaller_RoundTripsThroughTheContext(t *testing.T) {
	ctx := context.Background()
	if c := CallerFrom(ctx); c.Admin || c.Device != nil {
		t.Fatalf("a plain context has a caller: %+v", c)
	}
	if c := CallerFrom(WithAdmin(ctx)); !c.Admin || c.Device != nil {
		t.Fatalf("admin: %+v", c)
	}
	p := Principal{ID: "d_aaaaaaaaaaaa", PairingID: "pair", ProfileID: "p_0123456789ab"}
	c := CallerFrom(WithPrincipal(ctx, p))
	if c.Admin || c.Device == nil || *c.Device != p {
		t.Fatalf("device: %+v", c)
	}
}

func TestWithCaller_SetsExactlyWhatTheCallerIs(t *testing.T) {
	p := Principal{ID: "d_aaaaaaaaaaaa"}
	for name, c := range map[string]Caller{"anonymous": {}, "admin": {Admin: true}, "device": {Device: &p}} {
		ctx := WithCaller(context.Background(), c)
		got := CallerFrom(ctx)
		if got.Admin != c.Admin || (got.Device == nil) != (c.Device == nil) || (got.Device != nil && *got.Device != *c.Device) {
			t.Errorf("%s: got %+v want %+v", name, got, c)
		}
		if _, isDevice := PrincipalFrom(ctx); isDevice != (c.Device != nil) {
			t.Errorf("%s: PrincipalFrom = %v", name, isDevice)
		}
		if IsAdmin(ctx) != c.Admin {
			t.Errorf("%s: IsAdmin = %v", name, IsAdmin(ctx))
		}
	}
}

// A device is never also the admin, whatever the context held before.
func TestWithCaller_ADeviceCallerClearsTheAdminMark(t *testing.T) {
	p := Principal{ID: "d_aaaaaaaaaaaa"}
	ctx := WithCaller(WithAdmin(context.Background()), Caller{Device: &p})
	if IsAdmin(ctx) {
		t.Fatal("a device caller kept the admin mark of the context it was put on")
	}
}

// The caller carries a copy: changing the principal afterwards changes nothing in what a ticket recorded.
func TestCallerFrom_ReturnsACopy(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{ID: "d_aaaaaaaaaaaa"})
	c := CallerFrom(ctx)
	c.Device.ID = "d_bbbbbbbbbbbb"
	if p, _ := PrincipalFrom(ctx); p.ID != "d_aaaaaaaaaaaa" {
		t.Fatal("mutating the caller changed the context's principal")
	}
}
