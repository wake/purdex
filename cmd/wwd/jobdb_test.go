package main

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/daemon"
)

func TestNewPerJobDBIsNilWhenOff(t *testing.T) {
	if newPerJobDB(daemon.JobDBConfig{}) != nil {
		t.Fatal("off must give nil, not a typed-nil-wrapping value")
	}
	// every other field set, still off: nothing is built (no Manager, no mysql call)
	if newPerJobDB(daemon.JobDBConfig{Host: "100.64.0.6", Port: 3307, UserHosts: []string{"localhost"}, DefaultsFile: "/a.cnf"}) != nil {
		t.Fatal("off with the other fields set must still give nil")
	}
	if newPerJobDB(daemon.JobDBConfig{Enabled: true, DefaultsFile: "/a.cnf"}) == nil {
		t.Fatal("on must give a database")
	}
}
