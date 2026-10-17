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
	extra   []Straggler    // processes that appear in the lock holders after the plan
	starts  map[int]string // start times that differ from the default
	cmds    map[int]string // command names that differ from the default
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
					cmd := "cmd"
					if c, ok := h.cmds[pid]; ok {
						cmd = c
					}
					out = append(out, Straggler{PID: pid, Cmd: cmd})
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
		StartTime: func(pid int) (string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if st, ok := h.starts[pid]; ok {
				return st, nil
			}
			return "t0", nil
		},
		Self: 1,
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
	h := &freeFakeHost{alive: map[int]bool{}, holds: map[int]bool{}, ignores: map[int]bool{}, starts: map[int]string{}, cmds: map[int]string{}, lock: f}
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
	res, err := Free(cfg, h.env(), ClassHeavy, 1, tg)
	require.NoError(t, err)
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
	res, err := Free(cfg, h.env(), ClassHeavy, 1, tg)
	require.NoError(t, err)
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
	res, err := Free(cfg, h.env(), ClassHeavy, 1, tg)
	require.NoError(t, err)
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
	_, err = Free(cfg, h.env(), ClassHeavy, 1, tg)
	require.NoError(t, err)
	for _, s := range h.sent {
		assert.NotEqual(t, 1, s.pid)
	}
}

// A pid that got a new process (it ended and the number was reused) between the plan and the signal is not signalled.
func TestFree_AReusedPidIsNotSignalled(t *testing.T) {
	cfg, h := heldSlot(t, ClassHeavy, 1, 0, 100, 101)
	tg, err := PlanFree(cfg, h.env(), ClassHeavy, 1)
	require.NoError(t, err)
	h.mu.Lock()
	h.starts[100] = "a later process" // 100 is another process now
	h.mu.Unlock()
	res, err := Free(cfg, h.env(), ClassHeavy, 1, tg)
	require.NoError(t, err)
	assert.Equal(t, []int{101}, res.Termed)
	for _, s := range h.sent {
		assert.NotEqual(t, 100, s.pid)
	}
}

// A tmux server, a shell or the daemon that holds the lock only by inheriting the file is shown and never ended.
func TestFree_ProcessesThatOnlyInheritedTheFileAreSkipped(t *testing.T) {
	cfg, h := heldSlot(t, ClassHeavy, 1, 0, 100, 101, 102)
	h.cmds[101], h.cmds[102] = "tmux: server", "zsh"
	tg, err := PlanFree(cfg, h.env(), ClassHeavy, 1)
	require.NoError(t, err)
	assert.Len(t, tg.Locked, 1)
	assert.Len(t, tg.Skipped, 2)
	res, err := Free(cfg, h.env(), ClassHeavy, 1, tg)
	require.NoError(t, err)
	assert.Equal(t, []int{100}, res.Termed)
	assert.False(t, res.Free, "the slot stays held by the skipped ones: the report says so")
	for _, s := range h.sent {
		assert.NotContains(t, []int{101, 102}, s.pid)
	}
}

// The slot taken by another command while the question was asked is not freed.
func TestFree_ASlotThatChangedHandsAfterThePlanIsRefused(t *testing.T) {
	cfg, h := heldSlot(t, ClassHeavy, 1, 77, 100)
	tg, err := PlanFree(cfg, h.env(), ClassHeavy, 1)
	require.NoError(t, err)
	rec, _ := json.Marshal(Holder{Class: ClassHeavy, Slot: 1, PID: 88, Argv: []string{"another"}, Started: time.Now()})
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Dir, "heavy-1.json"), rec, 0o644))
	_, err = Free(cfg, h.env(), ClassHeavy, 1, tg)
	assert.ErrorIs(t, err, ErrSlotChanged)
	assert.Empty(t, h.sent, "nothing was signalled")
}

// A process that became a shell/tmux (exec: same pid, same start time) while the question was asked is no longer signalled.
func TestFree_AProcessThatExecdIntoAProtectedCommandIsNotSignalled(t *testing.T) {
	cfg, h := heldSlot(t, ClassHeavy, 1, 0, 100, 101)
	tg, err := PlanFree(cfg, h.env(), ClassHeavy, 1)
	require.NoError(t, err)
	h.mu.Lock()
	h.cmds[100] = "tmux"  // 100 exec'd into tmux
	h.cmds[101] = "other" // 101 into another command
	h.mu.Unlock()
	res, err := Free(cfg, h.env(), ClassHeavy, 1, tg)
	require.NoError(t, err)
	assert.Empty(t, res.Termed)
	assert.Empty(t, h.sent)
}
