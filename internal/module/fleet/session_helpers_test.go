package fleetmod

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	leaf "github.com/wake/purdex/internal/fleet"
)

// fakeClock is a clock the tests move by hand. A timer fires when Advance passes its deadline.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	c       chan time.Time
	at      time.Time
	stopped bool
	fired   bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) NewTimer(d time.Duration) timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{c: make(chan time.Time, 1), at: f.now.Add(d)}
	f.timers = append(f.timers, t)
	if d <= 0 {
		t.fired = true
		t.c <- f.now
	}
	return t
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }
func (t *fakeTimer) Stop() bool          { was := !t.stopped && !t.fired; t.stopped = true; return was }

// Advance moves time forward and fires every armed timer that falls due.
func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	for _, t := range f.timers {
		if !t.stopped && !t.fired && !t.at.After(f.now) {
			t.fired = true
			t.c <- f.now
		}
	}
}

// armed is how many timers are waiting.
func (f *fakeClock) armed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, t := range f.timers {
		if !t.stopped && !t.fired {
			n++
		}
	}
	return n
}

// created is how many timers were ever made.
func (f *fakeClock) created() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

// recSink records what a session reports.
type recSink struct {
	mu       sync.Mutex
	readings []leaf.Fast
	slows    int
	states   []string
}

func (r *recSink) Reading(f leaf.Fast, s *leaf.Slow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readings = append(r.readings, f)
	if s != nil {
		r.slows++
	}
}

func (r *recSink) State(st leaf.Status, code leaf.Code, _ time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, string(st)+"/"+string(code))
}

func (r *recSink) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.states...)
}

func (r *recSink) readingCount() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.readings) }
func (r *recSink) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.states) == 0 {
		return ""
	}
	return r.states[len(r.states)-1]
}
func (r *recSink) has(s string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.states {
		if x == s {
			return true
		}
	}
	return false
}

// fixtureFrames reads the captured Darwin loop output of the leaf package as frames.
func fixtureFrames(t *testing.T) []string {
	t.Helper()
	f, err := os.Open("../../fleet/testdata/loop-darwin-mlab.txt")
	require.NoError(t, err)
	defer f.Close()
	fr := leaf.NewFrameReader(f)
	var out []string
	for {
		s, err := fr.Next()
		if err != nil {
			break
		}
		out = append(out, s)
	}
	require.NotEmpty(t, out)
	return out
}

// fakeSSH writes an executable `ssh` that logs each start to <dir>/starts, and then runs script (a /bin/sh body). dir also
// holds frames.txt (the first fixture frame) and frame1.txt for the script to cat.
func fakeSSH(t *testing.T, script string) (path, dir string) {
	t.Helper()
	dir = t.TempDir()
	fr := fixtureFrames(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "frame1.txt"), []byte(fr[0]), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "frames.txt"), []byte(fr[0]), 0o644))
	path = filepath.Join(dir, "ssh")
	body := "#!/bin/sh\nD='" + dir + "'\necho x >> \"$D/starts\"\necho $$ > \"$D/pid\"\n" + script + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	return path, dir
}

func startsOf(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, "starts"))
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n")
}

func pidOf(t *testing.T, dir, name string) int {
	t.Helper()
	var n int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return false
		}
		n, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	return n
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 8*time.Second, 10*time.Millisecond, what)
}

func sessionFor(t *testing.T, path string, fc *fakeClock, sk sink, logf func(string, ...any)) sessionCfg {
	t.Helper()
	return sessionCfg{
		LookPath:  func(string) (string, error) { return path, nil },
		Home:      "/h",
		Valid:     func(string, string) bool { return true },
		Clock:     fc,
		Sink:      sk,
		Logf:      logf,
		TermGrace: 300 * time.Millisecond,
	}
}
