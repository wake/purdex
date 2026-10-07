package execstat

import (
	"sync"
	"testing"
	"time"
)

func TestObserveAccumulatesCountAndDuration(t *testing.T) {
	var c Counter
	c.Observe(3 * time.Millisecond)
	c.Observe(4 * time.Millisecond)
	n, d := c.Snapshot()
	if n != 2 || d != 7*time.Millisecond {
		t.Fatalf("Snapshot = (%d, %v), want (2, 7ms)", n, d)
	}
}

func TestResetZeroes(t *testing.T) {
	var c Counter
	c.Observe(time.Second)
	c.Reset()
	if n, d := c.Snapshot(); n != 0 || d != 0 {
		t.Fatalf("after Reset = (%d, %v)", n, d)
	}
}

func TestPackageCountersAreIndependent(t *testing.T) {
	Tmux.Reset()
	PS.Reset()
	Tmux.Observe(time.Millisecond)
	if n, _ := PS.Snapshot(); n != 0 {
		t.Fatalf("PS n = %d, want 0", n)
	}
	Tmux.Reset()
}

func TestObserveConcurrent(t *testing.T) {
	var c Counter
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Observe(time.Microsecond)
			}
		}()
	}
	wg.Wait()
	n, d := c.Snapshot()
	if n != 10000 || d != 10000*time.Microsecond {
		t.Fatalf("Snapshot = (%d, %v)", n, d)
	}
}

func TestFormat(t *testing.T) {
	Tmux.Reset()
	PS.Reset()
	Tmux.Observe(30 * time.Millisecond)
	Tmux.Observe(12 * time.Millisecond)
	PS.Observe(5 * time.Millisecond)
	got := Take().String()
	want := "tmux=2(42ms) ps=1(5ms)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	Tmux.Reset()
	PS.Reset()
}

func TestSinceIsDifference(t *testing.T) {
	Tmux.Reset()
	PS.Reset()
	Tmux.Observe(10 * time.Millisecond)
	base := Take()
	Tmux.Observe(5 * time.Millisecond)
	PS.Observe(2 * time.Millisecond)
	got := Take().Sub(base)
	if got.TmuxN != 1 || got.TmuxD != 5*time.Millisecond || got.PSN != 1 || got.PSD != 2*time.Millisecond {
		t.Fatalf("Sub = %+v", got)
	}
	if s := got.String(); s != "tmux=1(5ms) ps=1(2ms)" {
		t.Fatalf("String = %q", s)
	}
	Tmux.Reset()
	PS.Reset()
}
