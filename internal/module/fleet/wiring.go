package fleetmod

import (
	"context"
	"os"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// poolSource reads the pool from the local wwd socket. It is asked only while the fleet row's source is "pool" (hostconfig
// fleet.source), a setting the Measure re-reads whenever the row changes, so flipping it needs no restart.
func poolSource() func(context.Context) (wire.Pool, error) {
	return client.New(client.DefaultSocket()).Pool
}

// newMeasure builds the measuring side from what Init resolved: the role engine, the local source, the pairing, the
// events broadcaster and the hostconfig readers. A reader that is not in the registry leaves its part empty (no probe
// hosts, no fleet row: only this daemon's own host is measured).
func (m *Module) newMeasure() *Measure {
	home, _ := os.UserHomeDir()
	d := Deps{
		Home: home, Local: m.local, Role: m.engine, Events: m.core.Events,
		SelfHostID: m.hostID, Logf: m.logf, Pool: poolSource(),
	}
	if m.caller != nil {
		d.PeerHostID = m.caller.HostIDOf
	}
	var probeRev func() (int64, error)
	var fleetRev func() (int64, error)
	if svc, ok := m.core.Registry.Get(hostconfig.ProbeHostsKey); ok {
		if r, ok := svc.(hostconfig.ProbeHostsReader); ok {
			d.Probe = r.ProbeHosts
		}
		if r, ok := svc.(interface{ ProbeHostsRevision() (int64, error) }); ok {
			probeRev = r.ProbeHostsRevision
		}
	}
	if svc, ok := m.core.Registry.Get(hostconfig.FleetConfigKey); ok {
		if r, ok := svc.(hostconfig.FleetConfigReader); ok {
			d.Fleet = r.FleetConfig
		}
		if r, ok := svc.(interface{ FleetRevision() (int64, error) }); ok {
			fleetRev = r.FleetRevision
		}
	}
	if probeRev != nil && fleetRev != nil {
		d.Revisions = func() (int64, int64, error) {
			p, err := probeRev()
			if err != nil {
				return 0, 0, err
			}
			f, err := fleetRev()
			return p, f, err
		}
	}
	return NewMeasure(d)
}
