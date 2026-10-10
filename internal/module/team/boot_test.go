package teammod

import (
	"context"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #1968: the boot's reconciliation is one phase whose order and failure policy are written in boot.go.

var wantBootOrder = []string{"relay dir", "leases", "roster baseline", "relay", "kill recover", "spawn resume", "unattended"}

func (f *fixture) startTraced() (order []string, err error) {
	f.m.bootTrace = func(step string) { order = append(order, step) }
	err = f.m.Start(context.Background())
	return order, err
}

// The steps run in the stated order. Mutation gate: swap any two steps → the sequence differs (red).
func TestBoot_TheStepsRunInTheStatedOrder(t *testing.T) {
	f := newFixture(t)
	order, err := f.startTraced()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, wantBootOrder) {
		t.Fatalf("boot order = %v, want %v", order, wantBootOrder)
	}
	_ = f.m.Stop(context.Background())
}

// The lease grace comes before the unattended sweep: a row whose lease ran out while the daemon was down is not overdue at the
// sweep, so it is approved (PU-1b3). Mutation gate: run "unattended" before "leases" → the row stays open (red).
func TestBoot_AnExpiredLeaseIsExtendedBeforeTheUnattendedSweepLooksAtIt(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1)) // lease 1_030_000
	f.unatt.set(true)
	f.clock.Add(35_000) // down for longer than the lease
	if _, err := f.startTraced(); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateApproved {
		t.Fatalf("row = %s, want approved by the boot sweep after the lease grace", a.State)
	}
	_ = f.m.Stop(context.Background())
}

// A failing lease grace is the one fatal step: Start returns its error and nothing after it runs.
func TestBoot_AFailingLeaseGraceStopsTheBootBeforeTheLaterSteps(t *testing.T) {
	f := newFixture(t)
	if _, err := f.m.store.db.Exec(`ALTER TABLE approval_requests RENAME TO approval_requests_away`); err != nil {
		t.Fatal(err)
	}
	order, err := f.startTraced()
	if err == nil {
		t.Fatal("Start went on with an unreadable approval table")
	}
	if want := []string{"relay dir", "leases"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("steps run = %v, want %v", order, want)
	}
}

// Every other step logs and lets the boot go on: a relay_ops table that cannot be read does not stop the later steps.
func TestBoot_AFailingRelayStepDoesNotStopTheLaterSteps(t *testing.T) {
	f := newFixture(t)
	if _, err := f.m.store.db.Exec(`ALTER TABLE relay_ops RENAME TO relay_ops_away`); err != nil {
		t.Fatal(err)
	}
	order, err := f.startTraced()
	if err != nil {
		t.Fatalf("Start failed on a non-fatal step: %v", err)
	}
	if !reflect.DeepEqual(order, wantBootOrder) {
		t.Fatalf("steps run = %v, want all of %v", order, wantBootOrder)
	}
	_ = f.m.Stop(context.Background())
}
