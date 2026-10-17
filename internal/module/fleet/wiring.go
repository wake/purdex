package fleetmod

import (
	"context"
	"os"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// SourceEnv picks where the readings come from: "pool" = wwd's pool over its local socket (spec §9), anything else = the
// SSH sessions and the LocalSource as they always were. It is the switch of the WWD-2b migration; the C1 acceptance flips it.
const SourceEnv = "PDX_FLEET_SOURCE"

// poolSource is the pool read from the local wwd socket, or nil when the switch is not "pool".
func poolSource() func(context.Context) (wire.Pool, error) {
	if os.Getenv(SourceEnv) != "pool" {
		return nil
	}
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
