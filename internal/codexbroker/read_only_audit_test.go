package codexbroker

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// recordingDialer records every Dial call. P1 must never invoke this.
type recordingDialer struct {
	count atomic.Int64
}

func (r *recordingDialer) Dial(network, address string) (net.Conn, error) {
	r.count.Add(1)
	return nil, errors.New("recordingDialer: should never be called in P1")
}

func (r *recordingDialer) CallCount() int64 { return r.count.Load() }

// recordingSignaller records every Kill call. P1 must never invoke this.
type recordingSignaller struct {
	count atomic.Int64
}

func (r *recordingSignaller) Kill(pid int, sig syscall.Signal) error {
	r.count.Add(1)
	return errors.New("recordingSignaller: should never be called in P1")
}

func (r *recordingSignaller) CallCount() int64 { return r.count.Load() }

// recordingProcessLister wraps a real lister and counts List calls. The
// underlying scanner only calls List(); any other invocation here would be
// a regression.
type recordingProcessLister struct {
	inner ProcessLister
	count atomic.Int64
}

func (r *recordingProcessLister) List(ctx context.Context) ([]RawProcess, error) {
	r.count.Add(1)
	return r.inner.List(ctx)
}

func (r *recordingProcessLister) CallCount() int64 { return r.count.Load() }

// snapshotMtimes captures the modification time of every file under root.
// Used to assert that no Scan() side effects mutate fixture state.
func snapshotMtimes(t *testing.T, root string) map[string]time.Time {
	t.Helper()
	out := make(map[string]time.Time)
	var mu sync.Mutex
	var walk func(string)
	walk = func(p string) {
		entries, err := osFS{}.ReadDir(p)
		if err != nil {
			return
		}
		for _, ent := range entries {
			full := filepath.Join(p, ent.Name())
			if ent.IsDir() {
				walk(full)
				continue
			}
			info, err := osFS{}.Stat(full)
			if err != nil {
				continue
			}
			mu.Lock()
			out[full] = info.ModTime()
			mu.Unlock()
		}
	}
	walk(root)
	return out
}

// TestReadOnly_NoFsMutation verifies the scanner makes only read-only fs
// calls and does not mutate fixture state. AC7 (a) + (b) primary evidence.
func TestReadOnly_NoFsMutation(t *testing.T) {
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")

	mtimesBefore := snapshotMtimes(t, stateAbs)
	cxcMtimesBefore := snapshotMtimes(t, cxcAbs)

	rec := NewRecordingFS(NewOsFS())
	dialer := &recordingDialer{}
	sig := &recordingSignaller{}
	rlister := &recordingProcessLister{inner: NewFakeProcessListerFromText(src)}

	s := NewScanner(ScannerOpts{
		FS:              rec,
		Lister:          rlister,
		Dialer:          dialer,
		Signaller:       sig,
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Mtimes unchanged.
	mtimesAfter := snapshotMtimes(t, stateAbs)
	for path, before := range mtimesBefore {
		after, ok := mtimesAfter[path]
		if !ok {
			t.Errorf("file disappeared after Scan: %s", path)
			continue
		}
		if !before.Equal(after) {
			t.Errorf("mtime changed for %s: %v → %v", path, before, after)
		}
	}
	cxcMtimesAfter := snapshotMtimes(t, cxcAbs)
	for path, before := range cxcMtimesBefore {
		after, ok := cxcMtimesAfter[path]
		if !ok {
			t.Errorf("cxc file disappeared: %s", path)
			continue
		}
		if !before.Equal(after) {
			t.Errorf("cxc mtime changed for %s: %v → %v", path, before, after)
		}
	}

	// All recorded fs calls must be in the read-only set.
	allowed := map[string]bool{
		"Open":         true,
		"Stat":         true,
		"Lstat":        true,
		"EvalSymlinks": true,
		"Glob":         true,
		"ReadDir":      true,
	}
	calls := rec.Calls()
	if len(calls) == 0 {
		t.Errorf("no fs calls recorded; did the scanner run?")
	}
	for _, c := range calls {
		if !allowed[c.Op] {
			t.Errorf("forbidden fs op %q on path %q", c.Op, c.Path)
		}
	}
}

// TestReadOnly_NoBrokerSocketConnect: primary defence against future RPC
// regressions. AC7 (a).
func TestReadOnly_NoBrokerSocketConnect(t *testing.T) {
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")

	dialer := &recordingDialer{}
	rec := NewRecordingFS(NewOsFS())
	rlister := &recordingProcessLister{inner: NewFakeProcessListerFromText(src)}
	s := NewScanner(ScannerOpts{
		FS:              rec,
		Lister:          rlister,
		Dialer:          dialer,
		Signaller:       &recordingSignaller{},
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if dialer.CallCount() != 0 {
		t.Errorf("recordingDialer.CallCount() = %d, want 0 (P1 must not RPC any broker)", dialer.CallCount())
	}
}

// TestReadOnly_NoSignalsSent: AC7 (c).
func TestReadOnly_NoSignalsSent(t *testing.T) {
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")

	sig := &recordingSignaller{}
	rec := NewRecordingFS(NewOsFS())
	rlister := &recordingProcessLister{inner: NewFakeProcessListerFromText(src)}
	s := NewScanner(ScannerOpts{
		FS:              rec,
		Lister:          rlister,
		Dialer:          &recordingDialer{},
		Signaller:       sig,
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if sig.CallCount() != 0 {
		t.Errorf("recordingSignaller.CallCount() = %d, want 0 (P1 must not signal any process)", sig.CallCount())
	}
}

// TestReadOnly_ListerOnlyReceivesList: the recording process lister captures
// every method invocation; only List() must be called.
func TestReadOnly_ListerOnlyReceivesList(t *testing.T) {
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")

	rlister := &recordingProcessLister{inner: NewFakeProcessListerFromText(src)}
	s := NewScanner(ScannerOpts{
		FS:              NewOsFS(),
		Lister:          rlister,
		Dialer:          &recordingDialer{},
		Signaller:       &recordingSignaller{},
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if rlister.CallCount() != 1 {
		t.Errorf("processLister List() call count = %d, want 1", rlister.CallCount())
	}
}
