package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freeFakeHost is the processes of a test: which are alive, which hold the lock, what dies from which signal, and the lock itself (a real
// flock on a real file, dropped when the last holder is gone).
type freeFakeHost struct {
	mu      sync.Mutex
	alive   map[int]bool
	holds   map[int]bool
	ignores map[int]bool // survives SIGTERM
	sent    []sigAt
	lock    *os.File
	waited  time.Duration
	extra   []Straggler // processes that appear in the lock holders after the plan
}

type sigAt struct {
	pid int
	sig syscall.Signal
	at  time.Duration
}

func (h *freeFakeHost) env() FreeEnv {
	return FreeEnv{
		Holders: func(string) []Straggler {
			h.mu.Lock()
			defer h.mu.Unlock()
			var out []Straggler
			for pid := range h.holds {
				if h.alive[pid] {
					out = append(out, Straggler{PID: pid, Cmd: "cmd"})
				}
			}
			return append(out, h.extra...)
		},
		Signal: func(pid int, sig syscall.Signal) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.sent = append(h.sent, sigAt{pid, sig, h.waited})
			if sig == syscall.SIGKILL || !h.ignores[pid] {
				h.alive[pid] = false
			}
			h.dropLockIfAllGone()
			return nil
		},
		Alive: func(pid int) bool { h.mu.Lock(); defer h.mu.Unlock(); return h.alive[pid] },
		Wait:  func(d time.Duration) { h.mu.Lock(); h.waited += d; h.mu.Unlock() },
		Self:  1,
	}
}

func (h *freeFakeHost) dropLockIfAllGone() {
	for pid := range h.holds {
		if h.alive[pid] {
			return
		}
	}
	if h.lock != nil {
		_ = h.lock.Close()
		h.lock = nil
	}
}

// heldSlot makes the slot class-k held (a real flock) with a record whose gate pid is gatePID.
func heldSlot(t *testing.T, class Class, k, gatePID int, pids ...int) (Config, *freeFakeHost) {
	t.Helper()
	cfg := Defaults()
	cfg.Dir = t.TempDir()
	p := &Pool{Dir: cfg.Dir, Class: class, Size: 2}
	f, err := os.OpenFile(p.slotPath(k), os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	require.NoError(t, flockNBForTest(f))
	rec, _ := json.Marshal(Holder{Class: class, Slot: k, PID: gatePID, Argv: []string{"go", "test", "./..."}, Cwd: "/work/x", Started: time.Now().Add(-time.Hour), Session: "s1"})
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Dir, string(class)+"-"+itoa(k)+".json"), rec, 0o644))
	h := &freeFakeHost{alive: map[int]bool{}, holds: map[int]bool{}, ignores: map[int]bool{}, lock: f}
	for _, pid := range pids {
		h.alive[pid], h.holds[pid] = true, true
	}
	t.Cleanup(func() {
		h.mu.Lock()
		if h.lock != nil {
			_ = h.lock.Close()
		}
		h.mu.Unlock()
	})
	return cfg, h
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func flockNBForTest(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func TestParseSlot(t *testing.T) {
	c, k, err := ParseSlot("heavy-3")
	require.NoError(t, err)
	assert.Equal(t, ClassHeavy, c)
	assert.Equal(t, 3, k)
	for _, bad := range []string{"", "heavy", "heavy-0", "heavy-x", "huge-1", "light-1000", "../heavy-1", "heavy-1 "} {
		_, _, err := ParseSlot(bad)
		assert.Error(t, err, bad)
	}
}

func TestPlanFree_ASlotThatIsNotHeldIsSaidSo(t *testing.T) {
	cfg, h := heldSlot(t, ClassHeavy, 1, 0, 100)
	_, err := PlanFree(cfg, h.env(), ClassHeavy, 2)
	assert.ErrorIs(t, err, ErrSlotFree)
	_, err = PlanFree(cfg, h.env(), ClassLight, 1)
	assert.ErrorIs(t, err, ErrSlotFree)
}

func TestPlanFree_TheTargetNamesTheRecordTheLockHoldersAndWhetherTheGateIsAlive(t *testing.T) {
	cfg, h := heldSlot(t, ClassHeavy, 1, 99, 100, 101)
	h.alive[99] = false // the gate is gone
	tg, err := PlanFree(cfg, h.env(), ClassHeavy, 1)
	require.NoError(t, err)
	assert.False(t, tg.GateAlive)
	assert.Equal(t, "/work/x", tg.Holder.Cwd)
	assert.Len(t, tg.Locked, 2)

	h.alive[99] = true
	tg, err = PlanFree(cfg, h.env(), ClassHeavy, 1)
	require.NoError(t, err)
	assert.True(t, tg.GateAlive, "a gate that is alive is the command running as it should")
}

func TestFree_SIGTERMThatIsEnoughSendsNoSIGKILLAndTheSlotIsFree(t *testing.T) {
	cfg, h := heldSlot(t, ClassHeavy, 1, 0, 100, 101)
	tg, err := PlanFree(cfg, h.env(), ClassHeavy, 1)
	require.NoError(t, err)
	res := Free(cfg, h.env(), ClassHeavy, 1, tg)
	assert.True(t, res.Free)
	assert.ElementsMatch(t, []int{100, 101}, res.Termed)
	assert.Empty(t, res.Killed)
	for _, s := range h.sent {
		assert.Equal(t, syscall.SIGTERM, s.sig)
	}
}

func TestFree_SIGKILLOnlyForThoseStillAliveAfterTheGraceAndNotBefore(t *testing.T) {
	cfg, h := heldSlot(t, ClassHeavy, 1, 0, 100, 101)
	h.ignores[101] = true // ignores SIGTERM
	tg, _ := PlanFree(cfg, h.env(), ClassHeavy, 1)
	res := Free(cfg, h.env(), ClassHeavy, 1, tg)
	assert.True(t, res.Free)
	assert.Equal(t, []int{101}, res.Killed, "100 ended with SIGTERM and is not killed")
	var kill *sigAt
	for i := range h.sent {
		if h.sent[i].sig == syscall.SIGKILL {
			kill = &h.sent[i]
		}
	}
	require.NotNil(t, kill)
	assert.GreaterOrEqual(t, kill.at, FreeGrace, "not before the 5 s")
	assert.Equal(t, 101, kill.pid)
}

func TestFree_AProcessThatHoldsTheLockNowButWasNotListedIsNotTouched(t *testing.T) {
	cfg, h := heldSlot(t, ClassHeavy, 1, 0, 100)
	tg, _ := PlanFree(cfg, h.env(), ClassHeavy, 1)
	h.mu.Lock()
	h.alive[777], h.holds[777] = true, true // appeared after the plan (a new holder)
	h.mu.Unlock()
	res := Free(cfg, h.env(), ClassHeavy, 1, tg)
	assert.False(t, res.Free, "the new holder still has the lock")
	for _, s := range h.sent {
		assert.NotEqual(t, 777, s.pid)
	}
	assert.NotEmpty(t, res.Left, "and it is named")
}

func TestFree_NeverSignalsItselfOrPid1(t *testing.T) {
	cfg, h := heldSlot(t, ClassHeavy, 1, 0, 100)
	h.alive[1], h.holds[1] = true, true
	tg, err := PlanFree(cfg, h.env(), ClassHeavy, 1)
	require.NoError(t, err)
	for _, s := range tg.Locked {
		assert.NotEqual(t, 1, s.PID)
	}
	Free(cfg, h.env(), ClassHeavy, 1, tg)
	for _, s := range h.sent {
		assert.NotEqual(t, 1, s.pid)
	}
}
