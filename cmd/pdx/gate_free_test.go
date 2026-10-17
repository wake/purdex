package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
)

// freeHost is the processes of a test: the lock is a real flock, dropped when every holder is gone.
type freeHost struct {
	mu    sync.Mutex
	alive map[int]bool
	sent  []string
	lock  *os.File
}

func (h *freeHost) env() gate.FreeEnv {
	return gate.FreeEnv{
		Holders: func(string) []gate.Straggler {
			h.mu.Lock()
			defer h.mu.Unlock()
			var out []gate.Straggler
			for pid, a := range h.alive {
				if a && pid != 500 {
					out = append(out, gate.Straggler{PID: pid, Cmd: "node"})
				}
			}
			return out
		},
		Signal: func(pid int, sig syscall.Signal) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.sent = append(h.sent, sig.String())
			h.alive[pid] = false
			for p, a := range h.alive {
				if a && p != 500 {
					return nil
				}
			}
			if h.lock != nil {
				_ = h.lock.Close()
				h.lock = nil
			}
			return nil
		},
		Alive: func(pid int) bool { h.mu.Lock(); defer h.mu.Unlock(); return h.alive[pid] },
		Wait:  func(time.Duration) {},
		Self:  1,
	}
}

// heldSlotFor makes heavy-1 held under the test's gate dir; pid 500 is the gate (alive or not), 100 and 101 hold the lock.
func heldSlotFor(t *testing.T, dir string, gateAlive bool, argv ...string) *freeHost {
	t.Helper()
	gdir := filepath.Join(dir, "gate")
	require.NoError(t, os.MkdirAll(gdir, 0o755))
	f, err := os.OpenFile(filepath.Join(gdir, "heavy-1.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	rec, _ := json.Marshal(gate.Holder{Class: gate.ClassHeavy, Slot: 1, PID: 500, Argv: argv, Cwd: "/work/p", Started: time.Now().Add(-time.Minute), Session: "sess-9"})
	require.NoError(t, os.WriteFile(filepath.Join(gdir, "heavy-1.json"), rec, 0o644))
	h := &freeHost{alive: map[int]bool{100: true, 101: true, 500: gateAlive}, lock: f}
	t.Cleanup(func() {
		h.mu.Lock()
		if h.lock != nil {
			_ = h.lock.Close()
		}
		h.mu.Unlock()
	})
	old, oldIn := gateFreeEnv, gateStdin
	gateFreeEnv = h.env
	t.Cleanup(func() { gateFreeEnv, gateStdin = old, oldIn })
	return h
}

func TestGateFree_ASlotNobodyHoldsNeedsNothing(t *testing.T) {
	cfg, _ := gateEnv(t, "")
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"free", "heavy-1", "--config", cfg}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "沒有被佔用")
}

func TestGateFree_ABadNameIsUsage(t *testing.T) {
	cfg, _ := gateEnv(t, "")
	var out, errb bytes.Buffer
	assert.Equal(t, 2, gateMain([]string{"free", "heavy", "--config", cfg}, &out, &errb))
	assert.Equal(t, 2, gateMain([]string{"free", "--config", cfg}, &out, &errb))
}

// A slot whose gate is alive is a command running as it should: refused, nothing signalled, unless --force.
func TestGateFree_ALiveGateIsRefusedUnlessForced(t *testing.T) {
	cfg, dir := gateEnv(t, "")
	h := heldSlotFor(t, dir, true, "go", "test", "./...")
	var out, errb bytes.Buffer
	assert.Equal(t, 1, gateMain([]string{"free", "heavy-1", "--yes", "--config", cfg}, &out, &errb))
	assert.Contains(t, errb.String(), "--force")
	assert.Empty(t, h.sent)

	out.Reset()
	errb.Reset()
	assert.Equal(t, 0, gateMain([]string{"free", "heavy-1", "--yes", "--force", "--config", cfg}, &out, &errb), errb.String())
	assert.NotEmpty(t, h.sent)
	assert.Contains(t, out.String(), "已空出")
}

// Anything but y aborts and signals nothing; y goes ahead; --yes skips the question.
func TestGateFree_OnlyYAnswersYes(t *testing.T) {
	cfg, dir := gateEnv(t, "")
	h := heldSlotFor(t, dir, false, "vitest", "run")
	for _, ans := range []string{"n\n", "\n", "yes\n", ""} {
		gateStdin = strings.NewReader(ans)
		var out, errb bytes.Buffer
		assert.Equal(t, 1, gateMain([]string{"free", "heavy-1", "--config", cfg}, &out, &errb), "answer %q", ans)
		assert.Contains(t, out.String(), "已取消")
	}
	assert.Empty(t, h.sent)

	gateStdin = strings.NewReader("y\n")
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"free", "heavy-1", "--config", cfg}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "pid 100")
	assert.Contains(t, out.String(), "已空出")
	assert.Equal(t, []string{"terminated", "terminated"}, h.sent, "SIGTERM was enough: no SIGKILL")
}

// The command shown is scrubbed.
func TestGateFree_TheCommandIsScrubbed(t *testing.T) {
	cfg, dir := gateEnv(t, "")
	heldSlotFor(t, dir, false, "deploy", "--token=hunter2supersecret")
	gateStdin = strings.NewReader("n\n")
	var out, errb bytes.Buffer
	gateMain([]string{"free", "heavy-1", "--config", cfg}, &out, &errb)
	assert.NotContains(t, out.String(), "hunter2supersecret")
	assert.Contains(t, out.String(), "<redacted>")
}
