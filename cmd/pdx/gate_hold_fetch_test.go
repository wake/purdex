package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"

	"github.com/wake/purdex/internal/wwd/wire"
)

// No test of this package reaches the wwd of the host it runs on: what a hold there does to the gate would change the results with the
// state of the machine (the gate asks the wwd for its holds, spec §13.4). A test that wants holds sets its own fetch.
func init() { gateHoldFetch = nil }

// A gate call that does not take a slot (--auto and the command is not heavy) never asks the wwd and never makes the gate directory: the
// holds are looked at only where a slot is really asked for (the 761 gate, #3641).
func TestGate_AutoNonHeavyNeverAsksTheWWD(t *testing.T) {
	var calls atomic.Int32
	gateHoldFetch = func(context.Context) ([]wire.Exception, error) { calls.Add(1); return nil, nil }
	t.Cleanup(func() { gateHoldFetch = nil })
	cfg, dir := gateEnv(t, "[admission]\ndisk_min_gb = 0\n")
	old := gateExec
	gateExec = func(string, []string, []string) error { return nil }
	t.Cleanup(func() { gateExec = old })
	var out, errb bytes.Buffer
	code := gateMain([]string{"--config", cfg, "--auto", "--", "sh", "-c", "true"}, &out, &errb)
	assert.Equal(t, 0, code)
	assert.EqualValues(t, 0, calls.Load(), "the wwd was asked")
	assert.NoDirExists(t, filepath.Join(dir, "gate"))
}

// The gate every slot-taking entry builds asks the wwd for holds (a heavy command waits for any hold), and no such entry builds its gate
// by hand: `pdx gate`, its status and would-wait, and the fast lane of `pdx test` all go through newGate (R2: the fast lane was left out).
func TestNewGateHonoursTheHoldsOfTheWWD(t *testing.T) {
	gateHoldFetch = func(context.Context) ([]wire.Exception, error) {
		return []wire.Exception{{Name: "hold-x", Holds: []wire.Hold{wire.HoldHeavy}}}, nil
	}
	t.Cleanup(func() { gateHoldFetch = nil })
	cfg := gate.Defaults()
	cfg.Dir = t.TempDir()
	w := newGate(cfg).WouldWait(gate.ClassHeavy)
	assert.True(t, w.Wait)
	assert.Equal(t, "hold:hold-x", w.Reason)
}

func TestSlotTakingEntriesBuildTheirGateWithNewGate(t *testing.T) {
	for f, allowed := range map[string]int{"gate_cmd.go": 1, "test_cmd.go": 0} { // gate_cmd.go: the one inside newGate
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		n := 0
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "gate.New(") && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				n++
			}
		}
		assert.Equal(t, allowed, n, "%s builds a gate by hand: use newGate", f)
	}
}
