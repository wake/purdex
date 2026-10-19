package daemon

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// WWD-8 8b-3 (spec §13.4): the exception route takes the hold fields. They are kept (the exceptions table has the columns since schema
// 7), shown in the facts, listed by GET /v1/exceptions and published with the exception. The phase 1 gate (spec §13.1) that refused
// them is lifted: every host runs a wwd that knows them.
func TestPostExceptionTakesTheHoldFields(t *testing.T) {
	level := 1
	d, r := stateDaemon(t, "mlab", &level)
	h := d.SocketHandler()
	r.tick()
	until := r.now.Add(time.Hour).Format(time.RFC3339)
	body := `{"name":"hold","until":"` + until + `","pid":null,"holds":["heavy","light"],"allow":{"classes":["release"],"repos":["purdex"]},"flags":["release","expect_pressure"]}`
	w := send(h, "POST", "/v1/exceptions", "", body)
	if w.Code != http.StatusOK {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	r.tick()
	f, _ := d.opts.Sampler.Latest()
	if len(f.Exception) != 1 {
		t.Fatalf("facts: %+v", f.Exception)
	}
	fe := f.Exception[0]
	wantAllow := &wire.HoldAllow{Classes: []wire.JobClass{wire.ClassRelease}, Repos: []string{"purdex"}}
	if !reflect.DeepEqual(fe.Allow, wantAllow) || !reflect.DeepEqual(fe.Flags, []wire.HoldFlag{wire.HoldFlagRelease, wire.HoldFlagExpectPressure}) {
		t.Fatalf("the facts must show the hold fields: %+v", fe)
	}
	got, err := wire.Decode[[]wire.Exception](send(h, "GET", "/v1/exceptions", "", "").Body.Bytes())
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0].Allow, wantAllow) || len(got[0].Flags) != 2 {
		t.Fatalf("GET /v1/exceptions: %+v %v", got, err)
	}
}

// An empty allow admits nothing and is not an absent one: it is kept and shown as an empty allow, not dropped.
func TestPostExceptionKeepsAnEmptyAllow(t *testing.T) {
	level := 1
	d, r := stateDaemon(t, "mlab", &level)
	h := d.SocketHandler()
	until := r.now.Add(time.Hour).Format(time.RFC3339)
	if w := send(h, "POST", "/v1/exceptions", "", `{"name":"quiet","until":"`+until+`","pid":null,"holds":["heavy"],"allow":{"classes":[],"repos":[]}}`); w.Code != http.StatusOK {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	got, _ := wire.Decode[[]wire.Exception](send(h, "GET", "/v1/exceptions", "", "").Body.Bytes())
	if len(got) != 1 || got[0].Allow == nil || len(got[0].Allow.Classes) != 0 || len(got[0].Allow.Repos) != 0 {
		t.Fatalf("an empty allow: %+v", got)
	}
}

// What the contract refuses is still refused: an unknown class, an unknown flag, a repeated flag.
func TestPostExceptionRefusesABadHoldField(t *testing.T) {
	level := 1
	d, r := stateDaemon(t, "mlab", &level)
	h := d.SocketHandler()
	until := r.now.Add(time.Hour).Format(time.RFC3339)
	base := `{"name":"hold","until":"` + until + `","pid":null,"holds":["heavy"],`
	for name, tail := range map[string]string{
		"unknown class": `"allow":{"classes":["urgent"],"repos":[]}}`,
		"unknown flag":  `"flags":["panic"]}`,
		"repeated flag": `"flags":["release","release"]}`,
	} {
		if w := send(h, "POST", "/v1/exceptions", "", base+tail); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if got, _ := wire.Decode[[]wire.Exception](send(h, "GET", "/v1/exceptions", "", "").Body.Bytes()); len(got) != 0 {
		t.Fatalf("a refused request must leave no exception: %+v", got)
	}
}

// ...also from a peer over TCP (the same handler), with the token.
func TestPostExceptionTakesTheHoldFieldsOverTCPToo(t *testing.T) {
	level := 1
	d, r := stateDaemon(t, "mlab", &level)
	until := r.now.Add(time.Hour).Format(time.RFC3339)
	w := send(d.TCPHandler(), "POST", "/v1/exceptions", "Bearer "+testToken, `{"name":"hold","until":"`+until+`","pid":null,"holds":["heavy"],"flags":["release"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("over TCP: %d %s", w.Code, w.Body)
	}
}
