package shim

import (
	"testing"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/gate/gatetest"
)

func TestMain(m *testing.M) { gatetest.Main(m) }

// The tests of this package never reach the real ~/.config/pdx/gate (#2932).
func TestGateDirIsNotTheRealOne(t *testing.T) { gatetest.AssertIsolated(t, gate.DefaultDir()) }
