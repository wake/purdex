package main

import (
	"bytes"
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

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
	var out, errb bytes.Buffer
	code := gateMain([]string{"--config", cfg, "--auto", "--", "sh", "-c", "true"}, &out, &errb)
	assert.Equal(t, 0, code)
	assert.EqualValues(t, 0, calls.Load(), "the wwd was asked")
	assert.NoDirExists(t, filepath.Join(dir, "gate"))
}
