package wire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A pool from a build that does not write liveness decodes, and a peer without it marshals without the field: an older reader's strict
// decode never meets a name it does not know unless it asked for liveness (PoolLivenessQuery).
func TestPeerLivenessIsAbsentUntilFilled(t *testing.T) {
	b, err := json.Marshal(Peer{Info: Info{Alias: "a", Proto: Proto}, Reachable: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "liveness") {
		t.Fatalf("an unfilled liveness must not be on the wire: %s", b)
	}
	old := `{"peers":[{"info":{"host_id":"h","alias":"a","version":"v","proto":3,"platform":"linux","arch":"amd64","worker":{"enabled":false},"labels":[],"fingerprint":{}},"facts":null,"reachable":false}]}`
	if _, err := Decode[Pool]([]byte(old)); err != nil {
		t.Fatalf("a pool without liveness: %v", err)
	}
}

func TestPeerLivenessRoundTripsAndIsChecked(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	for _, l := range []Liveness{LivenessOK, LivenessWwdDown, LivenessHostDown, LivenessUnknown} {
		in := Pool{Peers: []Peer{{Info: Info{Alias: "a", Proto: Proto, Labels: []string{}, Fingerprint: map[string]string{}}, Liveness: l, LivenessAt: &at}}}
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Decode[Pool](b)
		if err != nil {
			t.Fatalf("%q: %v", l, err)
		}
		if out.Peers[0].Liveness != l || out.Peers[0].LivenessAt == nil || !out.Peers[0].LivenessAt.Equal(at) {
			t.Fatalf("%q: %+v", l, out.Peers[0])
		}
	}
	bad := `{"peers":[{"info":{"host_id":"h","alias":"a","version":"v","proto":3,"platform":"linux","arch":"amd64","worker":{"enabled":false},"labels":[],"fingerprint":{}},"facts":null,"reachable":false,"liveness":"asleep"}]}`
	if _, err := Decode[Pool]([]byte(bad)); err == nil {
		t.Fatal("a liveness that is not one of the four was accepted")
	}
}

// The words of the silence alert tell the two apart without a new field on the alert (GET /v1/alerts has the same strict reader).
func TestSilenceHostDownIsRecognisedFromTheDetail(t *testing.T) {
	if !SilenceHostDown(SilenceDetailHostDown("mserv")) {
		t.Fatal("the host-down text is not recognised")
	}
	for _, d := range []string{SilenceDetailWwdDown("mserv"), "mserv 的 wwd 已 5 分鐘連不上", ""} {
		if SilenceHostDown(d) {
			t.Fatalf("%q is not a host down", d)
		}
	}
}
