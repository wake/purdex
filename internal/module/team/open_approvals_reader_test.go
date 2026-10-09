package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/team"
)

var _ team.OpenApprovalsReader = (*Module)(nil)

func TestOpenApprovals_ReturnsWhatListOpenReturns(t *testing.T) {
	f := newFixture(t)
	f.createFrom(uid(1), "/tmp/10.sock")
	want, err := f.m.store.ListOpen()
	if err != nil || len(want) == 0 {
		t.Fatalf("setup: %v, %d open", err, len(want))
	}
	got, err := f.m.OpenApprovals()
	if err != nil || len(got) != len(want) || got[0].ID != want[0].ID {
		t.Fatalf("OpenApprovals = %v, %v; want %v", got, err, want)
	}
}

func TestOpenApprovals_SurfacesTheStoreError(t *testing.T) {
	f := newFixture(t)
	if err := f.m.store.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := f.m.OpenApprovals(); err == nil {
		t.Fatalf("closed store: got %v, nil error", got)
	}
}
