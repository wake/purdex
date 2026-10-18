// Package install puts wwd on a host: the binary, a starting config, and the
// service (a launchd LaunchAgent on macOS, a systemd unit on Linux). It is
// idempotent, upgrades in place and can be undone. `wwd install` is the one
// entry; `pdx setup --wwd` calls the same code.
package install

import (
	"fmt"
	"strings"
)

// Host is a member of the pool as the config templates know it. The slots and
// switches are starting values (spec §2); change them at run time with
// PUT /v1/worker, not by editing the template.
type Host struct {
	Alias         string
	Addr          string // the tailnet address
	WorkerEnabled bool
	Heavy, Light  int
	OwnerRule     bool // the "owner is active" rule (air26)
}

// Hosts are the four hosts of the pool.
var Hosts = []Host{
	{Alias: "mlab", Addr: "100.64.0.2", WorkerEnabled: true, Heavy: 1, Light: 2},
	{Alias: "air26", Addr: "100.64.0.4", WorkerEnabled: true, Heavy: 1, Light: 2, OwnerRule: true},
	{Alias: "mserv", Addr: "100.64.0.6", WorkerEnabled: true, Heavy: 1, Light: 1},
	{Alias: "air19", Addr: "100.64.0.1", WorkerEnabled: false, Heavy: 1, Light: 1}, // runs no worker by default
}

func hostByAlias(alias string) (Host, bool) {
	for _, h := range Hosts {
		if h.Alias == alias {
			return h, true
		}
	}
	return Host{}, false
}

// ConfigFor is the starting config.toml of a host: its alias and address, the
// worker switch and slots, the owner rule where it applies, and the other three
// hosts as peers. It carries no secret: the pool token is a separate file.
func ConfigFor(alias string) (string, error) {
	h, ok := hostByAlias(alias)
	if !ok {
		names := make([]string, len(Hosts))
		for i, x := range Hosts {
			names[i] = x.Alias
		}
		return "", fmt.Errorf("unknown host alias %q (known: %s)", alias, strings.Join(names, ", "))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# wwd on %s. Written by `wwd install` when this file did not exist; edit it freely, it is never overwritten.\n", h.Alias)
	b.WriteString("# The worker settings below are only the starting values: change them at run time with PUT /v1/worker.\n")
	fmt.Fprintf(&b, "alias = %q\ntailnet_addr = %q\n\n", h.Alias, h.Addr)
	fmt.Fprintf(&b, "[worker]\nenabled = %v\nheavy = %d\nlight = %d\n", h.WorkerEnabled, h.Heavy, h.Light)
	if h.OwnerRule {
		b.WriteString("\n# air26: the worker yields while the owner is using the machine\n[owner]\nenabled = true\n")
	}
	for _, p := range Hosts {
		if p.Alias == h.Alias {
			continue
		}
		fmt.Fprintf(&b, "\n[[peers]]\nalias = %q\naddr = %q\n", p.Alias, p.Addr)
	}
	return b.String(), nil
}
