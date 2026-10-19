package advisor

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

var silT0 = time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)

func peersAt(downs ...PeerDown) func() []PeerDown { return func() []PeerDown { return downs } }

// The edge of "unreachable for ≥ 3 min": just under, at, over.
func TestPeerSilenceThresholdAtTheEdge(t *testing.T) {
	r := PeerSilence{Alias: "mserv"}
	for _, c := range []struct {
		name  string
		down  time.Duration
		holds bool
	}{
		{"just under", SilenceAfter - time.Second, false},
		{"at", SilenceAfter, true},
		{"over", SilenceAfter + time.Minute, true},
	} {
		in := Input{Now: silT0, Peers: peersAt(PeerDown{"mserv", silT0.Add(-c.down)})}
		v, err := r.Eval(in)
		if err != nil || v.Holds != c.holds || v.Key != "silence:mserv" || v.Level != wire.AlertPush {
			t.Errorf("%s: %+v (%v)", c.name, v, err)
		}
	}
	if SilenceAfter != 3*time.Minute {
		t.Errorf("unreachable is 3 minutes for every host: %v", SilenceAfter)
	}
}

func TestPeerSilenceAnsweringPeerClears(t *testing.T) {
	v, err := PeerSilence{Alias: "mserv"}.Eval(Input{Now: silT0, Peers: peersAt(PeerDown{Alias: "mserv"})})
	if err != nil || v.Holds || v.Key != "silence:mserv" {
		t.Fatalf("%+v (%v)", v, err)
	}
}

// Each peer has its own key, and a silent one does not hold another's alert.
func TestPeerSilenceKeysAreOnePerPeer(t *testing.T) {
	in := Input{Now: silT0, Peers: peersAt(PeerDown{"mserv", silT0.Add(-time.Hour)}, PeerDown{"air26", silT0.Add(-time.Second)})}
	a, _ := PeerSilence{Alias: "mserv"}.Eval(in)
	b, _ := PeerSilence{Alias: "air26"}.Eval(in)
	if !a.Holds || b.Holds || a.Key == b.Key {
		t.Fatalf("%+v %+v", a, b)
	}
	if !strings.Contains(a.Detail, "mserv") || !strings.Contains(a.Detail, "60") {
		t.Errorf("detail: %q", a.Detail)
	}
	if len(a.Detail) > wire.MaxAlertDetailBytes {
		t.Errorf("detail too long")
	}
}

// A pool that cannot be read, or a peer that is not in it, is unjudged: the alert is left as it was.
func TestPeerSilenceUnjudged(t *testing.T) {
	if _, err := (PeerSilence{Alias: "mserv"}).Eval(Input{Now: silT0}); err == nil {
		t.Error("no pool view must be an error")
	}
	if _, err := (PeerSilence{Alias: "mserv"}).Eval(Input{Now: silT0, Peers: peersAt()}); err == nil {
		t.Error("an unknown peer must be an error")
	}
}

func TestPartnerDownThresholdAtTheEdge(t *testing.T) {
	for _, c := range []struct {
		name  string
		gap   time.Duration
		holds bool
	}{
		{"just under", SilenceAfter - time.Second, false},
		{"at", SilenceAfter, true},
		{"over", 10 * time.Minute, true},
		{"a read just now", 0, false},
	} {
		seen := silT0.Add(-c.gap)
		v, err := PartnerDown{}.Eval(Input{Now: silT0, PartnerSeen: func() time.Time { return seen }})
		if err != nil || v.Holds != c.holds || v.Key != "partner_down" || v.Level != wire.AlertPush {
			t.Errorf("%s: %+v (%v)", c.name, v, err)
		}
	}
	if _, err := (PartnerDown{}).Eval(Input{Now: silT0}); err == nil {
		t.Error("no partner reading must be an error")
	}
}

func TestSilenceRulesAreOfKindSilence(t *testing.T) {
	if (PeerSilence{}).Kind() != wire.AlertSilence || (PartnerDown{}).Kind() != wire.AlertSilence {
		t.Fatal("kind")
	}
}
