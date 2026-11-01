package dialogue

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
	"github.com/wake/purdex/internal/core"
	agentmod "github.com/wake/purdex/internal/module/agent"
)

type fullReader struct{ *fakeReader }

func (fullReader) LastTurns(ctx context.Context, provider, sid string, n int) ([]convmodel.Turn, error) {
	return nil, nil
}

type fakeSub struct {
	mu    sync.Mutex
	fn    func(agentmod.TurnEndEvent)
	unsub int
}

func (f *fakeSub) SubscribeTurnEnd(fn func(agentmod.TurnEndEvent)) func() {
	f.mu.Lock()
	f.fn = fn
	f.mu.Unlock()
	return func() { f.mu.Lock(); f.unsub++; f.mu.Unlock() }
}

func (f *fakeSub) emit(ev agentmod.TurnEndEvent) {
	f.mu.Lock()
	fn := f.fn
	f.mu.Unlock()
	fn(ev)
}

func newModuleEnv(t *testing.T) (*Module, *fakeSub, *fakeReader, string) {
	t.Helper()
	dir := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}})
	fr := &fakeReader{calls: map[string]int{}, froms: map[string][]string{},
		fn: func(sid, from string, n int) (convturns.TurnsFromResult, error) {
			return fromRes([]convmodel.Turn{basicTurn("t1")}, from), nil
		}}
	sub := &fakeSub{}
	c.Registry.Register(convturns.Key, fullReader{fr})
	c.Registry.Register(agentmod.TerminalSessionsKey, sub)
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()); m.Close() })
	return m, sub, fr, dir
}

func TestModuleNameAndDependencies(t *testing.T) {
	m := New()
	if m.Name() != "dialogue" {
		t.Fatalf("name = %q", m.Name())
	}
	got := map[string]bool{}
	for _, d := range m.Dependencies() {
		got[d] = true
	}
	if len(got) != 3 || !got["agent"] || !got["conversation"] || !got["team"] {
		t.Fatalf("dependencies = %v", m.Dependencies())
	}
}

// 9: a database that cannot be opened must not stop the daemon.
func TestModuleInitSoftFailsWhenDataDirIsAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: f}})
	if err := m.Init(c); err != nil {
		t.Fatalf("Init = %v, want nil", err)
	}
	if st := m.Status(); st["ready"] != false || st["init_error"] == "" {
		t.Fatalf("status = %v", st)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err != nil || m.Close() != nil {
		t.Fatal("Stop/Close of a module that never opened failed")
	}
}

func TestModuleRecordsTurnEndsThenStopsWriting(t *testing.T) {
	m, sub, fr, dir := newModuleEnv(t)
	if st := m.Status(); st["ready"] != true || st["init_error"] != "" {
		t.Fatalf("status = %v", st)
	}
	seg := filepath.Join(dir, "dialogue", sidS1, "001.md")
	sub.emit(stopEv(sidS1, 1))
	waitFor(t, "the dialogue file", func() bool { return fileExists(seg) })
	if fi, err := os.Stat(seg); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("segment mode: %v %v", fi, err)
	}

	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sub.unsub != 1 {
		t.Fatalf("unsubscribed %d times", sub.unsub)
	}
	sub.emit(stopEv(sidS2, 2)) // a late delivery from the hub's queue
	if fileExists(filepath.Join(dir, "dialogue", sidS2, "001.md")) || fr.callCount(sidS2) != 0 {
		t.Fatal("an event after Stop was handled")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil { // idempotent
		t.Fatal(err)
	}
}

func TestModuleStartWithoutTurnEndSourceIsNotReady(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	fr := &fakeReader{calls: map[string]int{}, froms: map[string][]string{}}
	c.Registry.Register(convturns.Key, fullReader{fr})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()); m.Close() })
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st["ready"] != false || st["start_error"] == "" || st["start_error"] == nil {
		t.Fatalf("status = %v", st)
	}
}

func TestModuleStartWithoutConversationReaderDoesNotSubscribe(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	sub := &fakeSub{}
	c.Registry.Register(agentmod.TerminalSessionsKey, sub)
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()); m.Close() })
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sub.fn != nil {
		t.Fatal("subscribed without a reader to read the turns")
	}
	if st := m.Status(); st["ready"] != false || st["start_error"] == "" || st["start_error"] == nil {
		t.Fatalf("status = %v", st)
	}
}
