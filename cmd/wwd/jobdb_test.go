package main

import (
	"context"
	"testing"

	"github.com/wake/purdex/internal/wwd/daemon"
)

func TestNewPerJobDBIsNilWhenOff(t *testing.T) {
	// every other field set, still off: nothing is built (no Manager, no mysql call)
	off := newPerJobDB(daemon.JobDBConfig{Host: "100.64.0.6", Port: 3307, UserHosts: []string{"localhost"}, DefaultsFile: "/a.cnf"})
	if off != nil {
		t.Fatal("off must give a nil database")
	}
	// stored in another interface (the way the executor and the loop take it) it must still be nil
	var dropper interface {
		Drop(ctx context.Context, id string) error
	} = off
	if dropper != nil {
		t.Fatal("off must stay nil after it is stored in another interface")
	}
	if newPerJobDB(daemon.JobDBConfig{Enabled: true, DefaultsFile: "/a.cnf"}) == nil {
		t.Fatal("on must give a database")
	}
}
