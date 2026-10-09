// internal/module/peers/hostcaller_status_test.go
package peers

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/config"
)

// A paired host that answers GET /api/peers with a status other than 200 is reported with its code, so a queue held on its
// capabilities can count a 401 toward unpaired_by_peer.
func TestHostCaller_TeamCapsReportsTheStatusCode(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	h := newHolder(config.PeerHost{Alias: "b", URL: s.URL, HostID: "hostB", Token: "tok1"})
	_, err := callerFor(h).TeamCaps(context.Background(), "hostB")
	var se *CapsStatusError
	if !errors.As(err, &se) || se.Code != http.StatusUnauthorized {
		t.Fatalf("err = %v, want a CapsStatusError{401}", err)
	}
}
