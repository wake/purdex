package gateheld

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/gate"
)

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, format)
}

func (l *logs) n() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.lines) }

// host builds a host with a gate: a config file naming the gate directory and its heavy slot count.
func host(t *testing.T, heavy int, extra string) (cfgPath, dir string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "gate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath = filepath.Join(root, "config.toml")
	body := "[gate]\ndir = \"" + dir + "\"\n" + extra + "\n[gate.slots]\nheavy = " + string(rune('0'+heavy)) + "\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PDX_GATE_DIR", "") // the file's dir counts
	return cfgPath, dir
}

func take(t *testing.T, dir string, n int) []*gate.Slot {
	t.Helper()
	p := &gate.Pool{Dir: dir, Class: gate.ClassHeavy, Size: 8, Now: time.Now}
	var out []*gate.Slot
	for i := 0; i < n; i++ {
		s, err := p.TakeLate(gate.WaiterInfo{})
		if err != nil || s == nil {
			t.Fatalf("take %d: %v %v", i, s, err)
		}
		out = append(out, s)
	}
	t.Cleanup(func() {
		for _, s := range out {
			s.Release(nil)
		}
	})
	return out
}

// The gate's heavy slots all held: full. Some free: not full. A slot given back: not full again.
func TestHeavyFullFollowsTheHeldSlots(t *testing.T) {
	cfg, dir := host(t, 2, "")
	var l logs
	r := New(cfg, l.logf)
	if r.HeavyFull() {
		t.Fatal("a gate with nothing held is full")
	}
	slots := take(t, dir, 1)
	if r.HeavyFull() {
		t.Fatal("one of two held is full")
	}
	slots = append(slots, take(t, dir, 1)...)
	if !r.HeavyFull() {
		t.Fatal("two of two held is not full")
	}
	slots[0].Release(nil)
	if r.HeavyFull() {
		t.Fatal("still full after a slot was given back")
	}
	if l.n() != 0 {
		t.Fatalf("a readable gate logged %v", l.lines)
	}
}

// A host with no gate (mserv, air19: no gate directory) is not affected, and says nothing.
func TestNoGateDirectoryIsNotFull(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfg, []byte("[gate]\ndir = \""+filepath.Join(root, "nope")+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PDX_GATE_DIR", "")
	var l logs
	if New(cfg, l.logf).HeavyFull() {
		t.Fatal("a host without a gate was full")
	}
	t.Setenv("PDX_GATE_DIR", filepath.Join(root, "no-such-gate")) // no config file: the default directory, here a place that is not there
	if New(filepath.Join(root, "missing.toml"), l.logf).HeavyFull() {
		t.Fatal("a host without a config file was full")
	}
	if l.n() != 0 {
		t.Fatalf("no gate is not an error: %v", l.lines)
	}
}

// A gate that is turned off holds nothing back.
func TestAGateTurnedOffIsNotFull(t *testing.T) {
	cfg, dir := host(t, 1, "enabled = false")
	take(t, dir, 1)
	var l logs
	if New(cfg, l.logf).HeavyFull() {
		t.Fatal("a disabled gate was full")
	}
}

// A paused class (heavy = 0) has no slot to give: full, by the rule "slots <= held".
func TestAPausedHeavyClassIsFull(t *testing.T) {
	cfg, _ := host(t, 0, "")
	var l logs
	if !New(cfg, l.logf).HeavyFull() {
		t.Fatal("a gate with no heavy slot was not full")
	}
}

// A gate that is there but cannot be read counts as full, and is logged once per change, not once per read.
func TestAnUnreadableGateIsFullAndLoggedOnce(t *testing.T) {
	cfg, dir := host(t, 2, "")
	if err := os.WriteFile(cfg, []byte("[gate\nthis is not toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	var l logs
	r := New(cfg, l.logf)
	for i := 0; i < 5; i++ {
		if !r.HeavyFull() {
			t.Fatal("an unreadable config was not full")
		}
	}
	if l.n() != 1 {
		t.Fatalf("logged %d times for one unchanged error: %v", l.n(), l.lines)
	}
	// repaired: readable again, not full, and a later break is logged again
	body := "[gate]\ndir = \"" + dir + "\"\n[gate.slots]\nheavy = 2\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(cfg, future, future) // the change is noticed even when size and second match
	if r.HeavyFull() {
		t.Fatal("a repaired gate is still full")
	}
	if err := os.WriteFile(cfg, []byte("[gate\nbroken again, longer text so the size differs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !r.HeavyFull() || l.n() != 2 {
		t.Fatalf("full again, logged %d: %v", l.n(), l.lines)
	}
}

// A gate directory that exists but is not a directory (or cannot be looked at) is a gate that cannot be read.
func TestAGateDirThatIsAFileIsFull(t *testing.T) {
	cfg, dir := host(t, 2, "")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var l logs
	if !New(cfg, l.logf).HeavyFull() {
		t.Fatal("a gate dir that is a file was not full")
	}
	if l.n() != 1 || !strings.Contains(l.lines[0], "gate") {
		t.Fatalf("log = %v", l.lines)
	}
}
