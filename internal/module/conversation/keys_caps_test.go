package conversation

import (
	"testing"

	"github.com/wake/purdex/internal/convfeed"
)

// A live terminal conversation without a mod owner declares send `keys` (K-2 D1b); the owner wins with `prompt`; not live
// stays not_wired; interrupt is unchanged; execution is unchanged. Mutation gate: ignoring live or the owner -> red.
func TestCapabilities_KeysForALiveTerminalWithoutOwner(t *testing.T) {
	e := newEnv(t)
	f := &fakeSender{}
	e.sender(f)
	c := e.mod.capabilitiesFor(sid, "terminal", true)
	if c.Send != "keys" || c.Reasons["send"] != "" || c.Interrupt != "" || c.Reasons["interrupt"] != "not_wired" {
		t.Fatalf("live, no owner: %+v", c)
	}
	f.owner = true
	if c = e.mod.capabilitiesFor(sid, "terminal", true); c.Send != "prompt" || c.Interrupt != "prompt" {
		t.Fatalf("live, owner: %+v", c)
	}
	f.owner = false
	if c = e.mod.capabilitiesFor(sid, "", false); c.Send != "" || c.Reasons["send"] != "not_wired" {
		t.Fatalf("not live: %+v", c)
	}
	if c = e.mod.capabilitiesFor(sid, "execution", true); c.Send != "execution" {
		t.Fatalf("execution: %+v", c)
	}
}

func TestHeaderLive_EndedIsNotLive(t *testing.T) {
	if headerLive(convfeed.Header{Live: true, Status: "ended"}) || !headerLive(convfeed.Header{Live: true, Status: "idle"}) || headerLive(convfeed.Header{Live: false, Status: "idle"}) {
		t.Fatal("headerLive must follow Live and not-ended")
	}
}
