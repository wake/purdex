package main

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/advisor"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/wire"
)

func TestSilenceRulesFollowTheConfig(t *testing.T) {
	off := false
	peers := []daemon.PeerConfig{
		{Alias: "mserv", Addr: "100.64.0.6"},
		{Alias: "air19", Addr: "100.64.0.1", AlertUnreachable: &off},
		{Alias: "air26", Addr: "100.64.0.4"},
	}
	var aliases []string
	var partner bool
	for _, r := range silenceRules(peers, true) {
		if r.Kind() != wire.AlertSilence {
			t.Fatalf("kind %q", r.Kind())
		}
		switch x := r.(type) {
		case advisor.PeerSilence:
			aliases = append(aliases, x.Alias)
		case advisor.PartnerDown:
			partner = true
		}
	}
	if len(aliases) != 2 || aliases[0] != "mserv" || aliases[1] != "air26" {
		t.Errorf("peers watched: %v (air19 has the alert off)", aliases)
	}
	if !partner {
		t.Error("the partner check was asked for")
	}
	for _, r := range silenceRules(peers, false) {
		if _, ok := r.(advisor.PartnerDown); ok {
			t.Error("a host without a Purdex daemon has no partner check")
		}
	}
}
