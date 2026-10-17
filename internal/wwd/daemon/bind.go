package daemon

import (
	"fmt"
	"net"
	"net/netip"
)

// tailnetPrefix is the CGNAT range Tailscale/headscale hand out (100.64.0.0/10).
var tailnetPrefix = netip.MustParsePrefix("100.64.0.0/10")

// ValidateBindAddr accepts only loopback and tailnet addresses. The wildcard
// (0.0.0.0, ::), hostnames and every other interface are refused, so wwd can
// never listen on the LAN or a public interface (spec §2, §11).
func ValidateBindAddr(a string) error {
	ip, err := netip.ParseAddr(a)
	if err != nil {
		return fmt.Errorf("bind address %q: not an IP literal", a)
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() {
		return fmt.Errorf("bind address %q: wildcard is not allowed", a)
	}
	if ip.IsLoopback() || tailnetPrefix.Contains(ip) {
		return nil
	}
	return fmt.Errorf("bind address %q: only loopback and tailnet (100.64.0.0/10) are allowed", a)
}

// PickBindAddrs returns the addresses to listen on: 127.0.0.1 plus the tailnet
// address (explicit, or the first tailnet address found among ifaceAddrs).
// With no tailnet address available it returns loopback only.
func PickBindAddrs(explicitTailnet string, ifaceAddrs []net.Addr) ([]string, error) {
	out := []string{"127.0.0.1"}
	if explicitTailnet != "" {
		if err := ValidateBindAddr(explicitTailnet); err != nil {
			return nil, err
		}
		if explicitTailnet != "127.0.0.1" {
			out = append(out, explicitTailnet)
		}
		return out, nil
	}
	for _, a := range ifaceAddrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if ip.Is4() && tailnetPrefix.Contains(ip) {
			out = append(out, ip.String())
			break
		}
	}
	return out, nil
}
