package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// GET /v1/qualification (WWD-7b2-0a): registered like GET /v1/exceptions, so the socket forwards ?host= and TCP answers for itself.
func TestGetQualificationAnswersForwardsAndRefusesHostOverTCP(t *testing.T) {
	remoteQ := &fakeQualifier{view: wire.HostQualification{Tasks: []wire.QualificationTask{{Label: "task:purdex/tsc", Hold: wire.QualHoldNone, Progress: wire.QualProgressReady}}, Recent: []wire.QualifyRecord{}}}
	remote := New(Options{HostID: "h2", Alias: "air26", Version: "v0", Token: testToken, Qualifier: remoteQ})
	srv := httptest.NewServer(remote.TCPHandler())
	defer srv.Close()
	local := New(Options{HostID: "h1", Alias: "mserv", Version: "v0", Token: testToken, Qualifier: &fakeQualifier{view: wire.HostQualification{Tasks: []wire.QualificationTask{}, Recent: []wire.QualifyRecord{}}}, Peers: []PeerAddr{{"air26", addr(srv)}}})

	w := call(local.SocketHandler(), http.MethodGet, "/v1/qualification", "")
	got, err := wire.Decode[wire.HostQualification](w.Body.Bytes())
	if w.Code != http.StatusOK || err != nil || got.Host != "mserv" || len(got.Tasks) != 0 {
		t.Fatalf("local: %d %s %v", w.Code, w.Body, err)
	}

	w = call(local.SocketHandler(), http.MethodGet, fwdPath("/v1/qualification", "air26"), "")
	got, err = wire.Decode[wire.HostQualification](w.Body.Bytes())
	if w.Code != http.StatusOK || err != nil || got.Host != "air26" || len(got.Tasks) != 1 {
		t.Fatalf("forwarded: %d %s %v", w.Code, w.Body, err)
	}

	// TCP answers for itself, with the token, and never forwards
	if w := call(remote.TCPHandler(), http.MethodGet, "/v1/qualification", ""); w.Code != http.StatusOK {
		t.Fatalf("TCP: %d %s", w.Code, w.Body)
	}
	if w := call(remote.TCPHandler(), http.MethodGet, "/v1/qualification?host=mserv", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("?host= over TCP must be refused: %d %s", w.Code, w.Body)
	}
	if w := send(remote.TCPHandler(), http.MethodGet, "/v1/qualification", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("the token is required: %d", w.Code)
	}
}

func TestGetQualificationIs404WithoutACoordinatorAnd500WhenItCannotAnswer(t *testing.T) {
	none := New(Options{HostID: "h1", Alias: "mserv", Version: "v0", Token: testToken})
	if w := call(none.SocketHandler(), http.MethodGet, "/v1/qualification", ""); w.Code != http.StatusNotFound {
		t.Fatalf("no coordinator: %d", w.Code)
	}
	broken := qualifyDaemon(t, &fakeQualifier{viewErr: errors.New("reading the earned labels: boom")})
	if w := call(broken.SocketHandler(), http.MethodGet, "/v1/qualification", ""); w.Code != http.StatusInternalServerError {
		t.Fatalf("store error: %d", w.Code)
	}
}
