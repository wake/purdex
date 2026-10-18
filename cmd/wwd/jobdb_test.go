package main

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/daemon"
)

func TestNewPerJobDBIsNilWhenOff(t *testing.T) {
	if newPerJobDB(daemon.JobDBConfig{}) != nil {
		t.Fatal("off must give nil, not a typed-nil-wrapping value")
	}
	if newPerJobDB(daemon.JobDBConfig{Enabled: true, DefaultsFile: "/a.cnf"}) == nil {
		t.Fatal("on must give a database")
	}
}
