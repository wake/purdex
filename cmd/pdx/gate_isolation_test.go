package main

import (
	"testing"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/gate/gatetest"
)

// The tests of this package never reach the real ~/.config/pdx/gate (#2932).
func TestGateDirIsNotTheRealOne(t *testing.T) { gatetest.AssertIsolated(t, gate.DefaultDir()) }
