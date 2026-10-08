package resourcesmod

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/resources"
)

// fakeClock is a settable clock in unix milliseconds.
type fakeClock struct{ ms atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ms.Store(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC).UnixMilli())
	return c
}
func (c *fakeClock) now() time.Time { return time.UnixMilli(c.ms.Load()) }

// fakeSettings is the hostconfig reader.
type fakeSettings struct {
	mu  sync.Mutex
	s   resources.Settings
	err error
}

func (f *fakeSettings) ResourcesSettings() (resources.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return resources.Settings{}, f.err
	}
	return f.s.Effective(), nil
}

func (f *fakeSettings) set(s resources.Settings) {
	f.mu.Lock()
	f.s, f.err = s, nil
	f.mu.Unlock()
}

func (f *fakeSettings) fail(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

// idleRaw is a usable reading of an idle 10-core, 16 GiB host: load 1, about
// 19 % of the memory in use.
func idleRaw() resources.HostRaw {
	r := goodRaw()
	r.Free = 800000
	return r
}

// logSink collects the module's log lines.
type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logSink) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, ln := range l.lines {
		if strings.Contains(ln, sub) {
			n++
		}
	}
	return n
}

// initedModule runs Init the way the daemon does: a core with a data dir and
// a registry holding the settings reader (none when set is nil).
func initedModule(t *testing.T, dir string, set *fakeSettings, sampler resources.Sampler) (*Module, *logSink) {
	t.Helper()
	reg := core.NewServiceRegistry()
	if set != nil {
		reg.Register(resources.SettingsKey, set)
	}
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}, Registry: reg})
	m := newTestModule(sampler, nil)
	logs := &logSink{}
	m.logf = logs.logf
	if err := m.Init(c); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m, logs
}

// coreWithService is a core whose registry holds one service.
func coreWithService(t *testing.T, dir, key string, svc any) *core.Core {
	t.Helper()
	reg := core.NewServiceRegistry()
	reg.Register(key, svc)
	return core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}, Registry: reg})
}

// seed writes rows into a database before a module opens it.
func seed(t *testing.T, path string, fn func(s *leaseStore)) {
	t.Helper()
	s, err := openLeaseStore(path)
	if err != nil {
		t.Fatal(err)
	}
	fn(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
