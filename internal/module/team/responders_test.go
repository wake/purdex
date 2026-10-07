package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// modPresent reads the one presence record the relay hello handler keeps
// (P5a-2a's modSeen): a session that said hello is present, any other is not.
func TestModPresent_ReadsTheHelloRecord(t *testing.T) {
	f := newFixture(t)
	if f.m.modPresent("sid-1") || f.m.modPresent("") {
		t.Fatal("nobody said hello yet")
	}
	if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "1", Agent: "cc"}); code != http.StatusOK {
		t.Fatalf("hello: %d %s", code, body)
	}
	if !f.m.modPresent("sid-1") || f.m.modPresent("sid-2") {
		t.Fatal("hello must make exactly that session present")
	}
}

// The WS half of RemoteResponders is the host-events subscriber set.
func TestWSResponders_FollowsTheSubscriberSet(t *testing.T) {
	f := newFixture(t)
	if !f.m.responders.Any() {
		t.Fatal("the fixture subscribes one test client: Any() must be true")
	}
	f.core.Events.RemoveTestSubscriber(f.sub)
	if f.m.responders.Any() {
		t.Fatal("no subscriber: Any() must be false")
	}
	// A session's own mod (the terminal side, known by its hello) is not a
	// remote responder: only a host-events subscriber is.
	if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "1", Agent: "cc"}); code != http.StatusOK {
		t.Fatalf("hello: %d %s", code, body)
	}
	if f.m.responders.Any() {
		t.Fatal("a mod's hello is not a remote responder: Any() must stay false")
	}
	if (wsResponders{}).Any() {
		t.Fatal("a nil broadcaster has no responders")
	}
}
