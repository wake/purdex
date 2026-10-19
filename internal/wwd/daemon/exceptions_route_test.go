package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// GET /v1/exceptions lists the exceptions in force with their holds (#3479 G4); facts' exceptions[] has no holds. It forwards from the
// socket like the other worker routes, and needs the token on TCP.
func TestGetExceptionsListsTheHoldsAndForwards(t *testing.T) {
	level := 1
	remote, rr := stateDaemon(t, "air26", &level)
	srv := httptest.NewServer(remote.TCPHandler())
	defer srv.Close()
	local, lr := stateDaemon(t, "mlab", &level, PeerAddr{"air26", addr(srv)})
	sock := local.SocketHandler()

	// nothing in force: an empty list, not null
	w := send(sock, "GET", "/v1/exceptions", "", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty: %d %q", w.Code, w.Body)
	}

	until := lr.now.Add(time.Hour).Format(time.RFC3339)
	if w := send(sock, "POST", "/v1/exceptions", "", `{"name":"b-dev","until":"`+until+`","pid":null,"holds":["heavy"]}`); w.Code != 200 {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	if w := send(sock, "POST", "/v1/exceptions", "", `{"name":"a-pkg","until":"`+until+`","pid":null,"holds":["heavy","light"]}`); w.Code != 200 {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	got, err := wire.Decode[[]wire.Exception](send(sock, "GET", "/v1/exceptions", "", "").Body.Bytes())
	if err != nil || len(got) != 2 {
		t.Fatalf("list: %+v %v", got, err)
	}
	byName := map[string][]wire.Hold{got[0].Name: got[0].Holds, got[1].Name: got[1].Holds}
	if len(byName["a-pkg"]) != 2 || len(byName["b-dev"]) != 1 {
		t.Fatalf("the holds must be there: %+v", got)
	}

	// the peer's list, through the forward
	if w := send(sock, "POST", fwdPath("/v1/exceptions", "air26"), "", `{"name":"remote-x","until":"`+until+`","pid":null,"holds":["light"]}`); w.Code != 200 {
		t.Fatalf("forwarded POST: %d %s", w.Code, w.Body)
	}
	rr.tick()
	got, err = wire.Decode[[]wire.Exception](send(sock, "GET", fwdPath("/v1/exceptions", "air26"), "", "").Body.Bytes())
	if err != nil || len(got) != 1 || got[0].Name != "remote-x" {
		t.Fatalf("forwarded GET: %+v %v", got, err)
	}

	// an exception that has ended is not listed
	lr.now = lr.now.Add(2 * time.Hour)
	got, _ = wire.Decode[[]wire.Exception](send(sock, "GET", "/v1/exceptions", "", "").Body.Bytes())
	if len(got) != 0 {
		t.Fatalf("ended exceptions listed: %+v", got)
	}

	if send(remote.TCPHandler(), "GET", "/v1/exceptions", "", "").Code != http.StatusUnauthorized {
		t.Error("the token is required")
	}
}
