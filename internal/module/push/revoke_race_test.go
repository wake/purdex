package push

import (
	"context"
	"net/http"
	"testing"
)

// A request that passed authentication before the revoke and registers after it is refused, and stores nothing.
func TestRegister_ByAPhoneRevokedMeanwhileIsRefused(t *testing.T) {
	e := newRevokeEnv(t)
	must(t, e.push.Start(context.Background()))
	pa := e.pair(t, pairA)
	if rec := e.call(nil, "DELETE", "/api/devices/"+pa.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if rec := e.call(&pa, "POST", "/api/push/devices", regBody(tokA)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked phone registered: %d", rec.Code)
	}
	if n := len(e.adminSees(t)); n != 0 {
		t.Fatalf("stored %d", n)
	}
}
