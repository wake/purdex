package daemon

import (
	"net"
	"testing"
)

func TestValidateBindAddr(t *testing.T) {
	ok := []string{"127.0.0.1", "100.64.0.2", "100.127.255.254", "::1"}
	bad := []string{"0.0.0.0", "::", "", "192.168.1.5", "10.0.0.1", "100.63.255.255", "100.128.0.1", "example.com", "169.254.1.1"}
	for _, a := range ok {
		if err := ValidateBindAddr(a); err != nil {
			t.Errorf("ValidateBindAddr(%q) = %v, want nil", a, err)
		}
	}
	for _, a := range bad {
		if err := ValidateBindAddr(a); err == nil {
			t.Errorf("ValidateBindAddr(%q) = nil, want error", a)
		}
	}
}

func TestPickBindAddrs(t *testing.T) {
	ifaceAddrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.1.9"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("100.64.0.2"), Mask: net.CIDRMask(32, 32)},
		&net.IPNet{IP: net.ParseIP("0.0.0.0"), Mask: net.CIDRMask(0, 32)},
	}
	got, err := PickBindAddrs("", ifaceAddrs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1", "100.64.0.2"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v want %v", got, want)
	}
	// explicit tailnet address overrides detection
	got, err = PickBindAddrs("100.64.0.4", ifaceAddrs)
	if err != nil || len(got) != 2 || got[1] != "100.64.0.4" {
		t.Fatalf("explicit: %v %v", got, err)
	}
	// explicit bad address is refused
	if _, err := PickBindAddrs("0.0.0.0", ifaceAddrs); err == nil {
		t.Fatal("0.0.0.0 must be refused")
	}
	// no tailnet interface: loopback only
	got, err = PickBindAddrs("", ifaceAddrs[:1])
	if err != nil || len(got) != 1 || got[0] != "127.0.0.1" {
		t.Fatalf("no tailnet: %v %v", got, err)
	}
}
