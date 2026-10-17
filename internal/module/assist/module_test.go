package assistmod

import (
	"testing"

	"github.com/wake/purdex/internal/core"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// Init finds what it reads in the registry: the services the peers and team modules publish.
func TestInitLooksUpTheServices(t *testing.T) {
	r := newRig(t)
	c := &core.Core{Registry: core.NewServiceRegistry()}
	c.Registry.Register(peersmod.OriginResolverKey, r.origins)
	c.Registry.Register(team.AssistTeamsKey, r.teams)
	c.Registry.Register(team.SeatReaderKey, r.seats)
	m := New()
	m.dir = r.dir
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if m.origins == nil || m.teams == nil || m.seats == nil {
		t.Fatalf("services not found: %+v", m)
	}
	if m.Name() != "assist" {
		t.Fatal(m.Name())
	}
}
