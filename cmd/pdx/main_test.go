//go:build !integration

package main

import (
	"testing"

	"github.com/wake/purdex/internal/gate/gatetest"
)

// HOME and PDX_GATE_DIR in a temporary directory: no test reaches the real gate (#2932). The integration build has its own
// TestMain (restart_integration_test.go), which does the same.
func TestMain(m *testing.M) { gatetest.Main(m) }
