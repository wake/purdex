package core

import "testing"

func TestPairingStateDefault(t *testing.T) {
	var ps PairingState
	if ps.Get() != StateNormal {
		t.Errorf("default should be normal, got %s", ps.Get())
	}
}

func TestPairingStateSetGet(t *testing.T) {
	var ps PairingState
	ps.Set(StatePairing)
	if ps.Get() != StatePairing {
		t.Errorf("want pairing, got %s", ps.Get())
	}
	ps.Set(StatePending)
	if ps.Get() != StatePending {
		t.Errorf("want pending, got %s", ps.Get())
	}
	ps.Set(StateNormal)
	if ps.Get() != StateNormal {
		t.Errorf("want normal, got %s", ps.Get())
	}
}

func TestPairingStateString(t *testing.T) {
	cases := []struct {
		s    StateValue
		want string
	}{
		{StatePairing, "pairing"},
		{StatePending, "pending"},
		{StateNormal, "normal"},
	}
	for _, tc := range cases {
		if tc.s.String() != tc.want {
			t.Errorf("want %s, got %s", tc.want, tc.s.String())
		}
	}
}
