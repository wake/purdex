package fleet

import "testing"

var _ LocalSource = (*FakeLocal)(nil)

func TestLocalSourceKey(t *testing.T) {
	if LocalSourceKey != "fleet.local-source" {
		t.Fatalf("LocalSourceKey renamed: %q", LocalSourceKey)
	}
}

func TestFakeLocalEmpty(t *testing.T) {
	f := NewFakeLocal()
	if _, ok := f.Fast(); ok {
		t.Fatal("Fast should be false before set")
	}
	if _, ok := f.Slow(); ok {
		t.Fatal("Slow should be false before set")
	}
}

func TestFakeLocalSet(t *testing.T) {
	f := NewFakeLocal()
	f.SetFast(LocalFast{CPU: 12, NCPU: 10})
	f.SetSlow(LocalSlow{TopCPU: []Proc{{PID: 1, Name: "x", CPU: 150}}})
	if v, ok := f.Fast(); !ok || v.CPU != 12 || v.NCPU != 10 {
		t.Fatalf("fast = %+v %v", v, ok)
	}
	if v, ok := f.Slow(); !ok || len(v.TopCPU) != 1 || v.TopCPU[0].CPU != 150 {
		t.Fatalf("slow = %+v %v", v, ok)
	}
}

func TestFakeLocalChangedCoalesces(t *testing.T) {
	f := NewFakeLocal()
	f.SetFast(LocalFast{CPU: 1})
	f.SetFast(LocalFast{CPU: 2}) // must not block
	<-f.Changed()
	select {
	case <-f.Changed():
		t.Fatal("second signal should have been coalesced")
	default:
	}
	if v, _ := f.Fast(); v.CPU != 2 {
		t.Fatalf("latest value lost: %+v", v)
	}
}

func TestFakeLocalStartStop(t *testing.T) {
	f := NewFakeLocal()
	f.Start()
	f.Start()
	f.Stop()
	if f.Starts() != 2 || f.Stops() != 1 {
		t.Fatalf("starts=%d stops=%d", f.Starts(), f.Stops())
	}
}
