package daemon

import (
	"net/http"
	"testing"
	"time"
)

// Phase 1 of the contract (spec §13.1): the readers know allow and flags, no writer fills them. The exception route is a writer
// (the exception is published on SSE and shown in the facts), so until 8b-3 it refuses the hold fields rather than let a proto 1
// reader be shown them — and rather than accept them and forget them at the next restart (the exceptions table does not keep them).
func TestPostExceptionRefusesTheHoldFieldsInPhase1(t *testing.T) {
	level := 1
	d, r := stateDaemon(t, "mlab", &level)
	h := d.SocketHandler()
	r.tick()
	until := r.now.Add(time.Hour).Format(time.RFC3339)
	base := `{"name":"hold","until":"` + until + `","pid":null,"holds":["heavy"],`
	for name, tail := range map[string]string{
		"allow":      `"allow":{"classes":["release"],"repos":[]}}`,
		"empty hold": `"allow":{"classes":[],"repos":[]}}`,
		"flags":      `"flags":["release"]}`,
	} {
		w := send(h, "POST", "/v1/exceptions", "", base+tail)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	r.tick()
	if f, _ := d.opts.Sampler.Latest(); len(f.Exception) != 0 {
		t.Fatalf("a refused request must leave no exception: %+v", f.Exception)
	}
	// an exception without them is accepted as before
	if w := send(h, "POST", "/v1/exceptions", "", base[:len(base)-1]+`}`); w.Code != http.StatusOK {
		t.Fatalf("a plain exception: %d %s", w.Code, w.Body)
	}
}

// ...also from a peer over TCP (the same handler).
func TestPostExceptionRefusesTheHoldFieldsOverTCPToo(t *testing.T) {
	level := 1
	d, r := stateDaemon(t, "mlab", &level)
	until := r.now.Add(time.Hour).Format(time.RFC3339)
	w := send(d.TCPHandler(), "POST", "/v1/exceptions", "Bearer "+testToken, `{"name":"hold","until":"`+until+`","pid":null,"holds":["heavy"],"flags":["release"]}`)
	if w.Code == http.StatusOK {
		t.Fatalf("accepted over TCP: %d %s", w.Code, w.Body)
	}
}
