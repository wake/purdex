package resourcesmod

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

func idleSampler() *fakeSampler {
	return &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return idleRaw(), nil, nil
	}}
}

func TestModule_InitOpensStoreAndFindsSettings(t *testing.T) {
	dir := t.TempDir()
	m, _ := initedModule(t, dir, &fakeSettings{}, idleSampler())
	if m.store == nil || m.settingsSrc == nil {
		t.Fatalf("Init left store=%v settings=%v", m.store, m.settingsSrc)
	}
	if _, err := os.Stat(filepath.Join(dir, "resources.db")); err != nil {
		t.Fatalf("resources.db is not in the data dir: %v", err)
	}
}

func TestModule_DBOpenFailureRunsMeasureOnly(t *testing.T) {
	dir := t.TempDir()
	// A directory where the database file should be: the open fails.
	if err := os.Mkdir(filepath.Join(dir, "resources.db"), 0o755); err != nil {
		t.Fatal(err)
	}
	set := &fakeSettings{}
	set.set(resources.Settings{Mode: resources.ModeLease})
	m, logs := initedModule(t, dir, set, idleSampler())
	if m.store != nil {
		t.Fatal("the store opened on a directory")
	}
	if logs.count("resources.db") != 1 {
		t.Fatalf("want one log line about the database, got %v", logs.lines)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start must not fail without a database: %v", err)
	}
	waitFor(t, "a snapshot", func() bool { return m.latest.Load() != nil && m.latest.Load().Available })

	// The stored setting says lease, but without a database nothing can be
	// queued: the module reports and acts as measure.
	if got := m.latest.Load().Mode; got != resources.ModeMeasure {
		t.Fatalf("published mode = %q, want measure", got)
	}
	snap, raw := getResources(t, m, "")
	if snap.Mode != resources.ModeMeasure {
		t.Fatalf("served mode = %q, want measure", snap.Mode)
	}
	for _, k := range []string{"leases", "waiters", "recent"} {
		if _, ok := raw[k]; ok {
			t.Errorf("field %q must be absent without a database", k)
		}
	}
}

func TestModule_InitWithoutSettingsReaderMeasures(t *testing.T) {
	m, logs := initedModule(t, t.TempDir(), nil, idleSampler())
	if m.settingsSrc != nil {
		t.Fatal("a reader appeared from nowhere")
	}
	if got := m.settings().Mode; got != resources.ModeMeasure {
		t.Fatalf("mode = %q, want measure", got)
	}
	if logs.count("hostconfig.resources-settings") != 1 {
		t.Fatalf("want one log line about the missing reader, got %v", logs.lines)
	}
}

func TestModule_InitSettingsServiceOfTheWrongType(t *testing.T) {
	m := newTestModule(idleSampler(), nil)
	logs := &logSink{}
	m.logf = logs.logf
	// A registered value that is not a SettingsReader is not fatal either.
	c := coreWithService(t, t.TempDir(), resources.SettingsKey, "not a reader")
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	if m.settingsSrc != nil || m.settings().Mode != resources.ModeMeasure {
		t.Fatalf("settingsSrc=%v mode=%q", m.settingsSrc, m.settings().Mode)
	}
}

// The first read failing must already read as measure (fail open), and the
// same failure repeated on every tick and every GET logs once; the way back
// logs once.
func TestSettings_ReadFailureFailsOpenAndLogsOnce(t *testing.T) {
	set := &fakeSettings{}
	set.set(resources.Settings{Mode: resources.ModeLease})
	set.fail(errors.New("decode stored setting: bad json"))
	m, logs := initedModule(t, t.TempDir(), set, idleSampler())

	for i := 0; i < 4; i++ {
		m.tick(context.Background())
		getResources(t, m, "")
	}
	if got := m.latest.Load().Mode; got != resources.ModeMeasure {
		t.Fatalf("published mode = %q, want measure while the setting is unreadable", got)
	}
	if snap, _ := getResources(t, m, ""); snap.Mode != resources.ModeMeasure {
		t.Fatalf("served mode = %q, want measure", snap.Mode)
	}
	if n := logs.count("cannot read the resources setting"); n != 1 {
		t.Fatalf("failure logged %d times, want 1: %v", n, logs.lines)
	}

	set.set(resources.Settings{Mode: resources.ModeLease})
	m.tick(context.Background())
	m.tick(context.Background())
	if got := m.latest.Load().Mode; got != resources.ModeLease {
		t.Fatalf("published mode = %q after recovery, want lease", got)
	}
	if n := logs.count("readable again"); n != 1 {
		t.Fatalf("recovery logged %d times, want 1: %v", n, logs.lines)
	}
}

// Codex attack (medium): the served snapshot is the last tick's, whole. The
// mode, available and reason come from one tick, so a setting changed between
// two ticks cannot produce "mode off, available true".
func TestSettings_ModeShowsInSnapshot(t *testing.T) {
	set := &fakeSettings{}
	m, _ := initedModule(t, t.TempDir(), set, idleSampler())
	for _, mode := range []string{resources.ModeMeasure, resources.ModeAdvise, resources.ModeLease} {
		set.set(resources.Settings{Mode: mode})
		m.tick(context.Background())
		if got := m.latest.Load().Mode; got != mode {
			t.Fatalf("published mode = %q, want %q", got, mode)
		}
		// The setting changes after the tick: the answer stays the tick's.
		set.set(resources.Settings{Mode: resources.ModeOff})
		snap, _ := getResources(t, m, "")
		if snap.Mode != mode || !snap.Available || snap.Reason != "" {
			t.Fatalf("served %q available=%v reason=%q between ticks, want the last tick's %q available", snap.Mode, snap.Available, snap.Reason, mode)
		}
	}
	// The next tick applies off, as one whole state.
	m.tick(context.Background())
	snap, _ := getResources(t, m, "")
	if snap.Mode != resources.ModeOff || snap.Available || snap.Reason != resources.ReasonOff {
		t.Fatalf("after the tick: mode=%q available=%v reason=%q, want off / unavailable / off", snap.Mode, snap.Available, snap.Reason)
	}
}

// Mode off keeps the ticker but skips the reads; switching back needs no
// restart.
func TestSettings_OffSkipsSamplingAndRecovers(t *testing.T) {
	set := &fakeSettings{}
	set.set(resources.Settings{Mode: resources.ModeMeasure})
	s := idleSampler()
	m, _ := initedModule(t, t.TempDir(), set, s)

	m.tick(context.Background())
	good := m.latest.Load()
	if !good.Available || s.calls.Load() != 1 {
		t.Fatalf("setup: snapshot %+v, %d samples", good, s.calls.Load())
	}

	set.set(resources.Settings{Mode: resources.ModeOff})
	for i := 0; i < 3; i++ {
		m.tick(context.Background())
	}
	if n := s.calls.Load(); n != 1 {
		t.Fatalf("mode off took %d samples, want no more than the first", n)
	}
	off := m.latest.Load()
	if off.Available || off.Reason != resources.ReasonOff || off.Mode != resources.ModeOff {
		t.Fatalf("off snapshot = %+v", off)
	}
	if off.Host.Measured != good.Host.Measured {
		t.Fatalf("the last figures should stay visible while off: %+v", off.Host)
	}
	if snap, _ := getResources(t, m, ""); snap.Reason != resources.ReasonOff || snap.Available {
		t.Fatalf("served = %+v", snap)
	}

	set.set(resources.Settings{Mode: resources.ModeLease})
	m.tick(context.Background())
	back := m.latest.Load()
	if !back.Available || back.Reason != "" || back.Mode != resources.ModeLease || s.calls.Load() != 2 {
		t.Fatalf("after leaving off: %+v, %d samples", back, s.calls.Load())
	}
}

// Off is the one mode that needs no database.
func TestSettings_OffWorksWithoutDB(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "resources.db"), 0o755); err != nil {
		t.Fatal(err)
	}
	set := &fakeSettings{}
	set.set(resources.Settings{Mode: resources.ModeOff})
	s := idleSampler()
	m, _ := initedModule(t, dir, set, s)
	m.tick(context.Background())
	if got := m.latest.Load(); got.Reason != resources.ReasonOff || got.Mode != resources.ModeOff || s.calls.Load() != 0 {
		t.Fatalf("snapshot %+v, %d samples", got, s.calls.Load())
	}
}

func TestModule_StopJoinsAndCloseClosesDB(t *testing.T) {
	m, _ := initedModule(t, t.TempDir(), &fakeSettings{}, idleSampler())
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a snapshot", func() bool { return m.latest.Load() != nil })
	if err := m.store.db.Ping(); err != nil {
		t.Fatalf("db not usable while running: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Stop runs before the HTTP server drains (shutdown.go: StopModules,
	// srv.Shutdown, CloseModules), so a request still in flight needs the
	// database: it is closed by Close, not by Stop (codex R1 + attack).
	if err := m.store.db.Ping(); err != nil {
		t.Fatalf("Stop closed resources.db under in-flight requests: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("a second Stop must be a no-op, got %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.store.db.Ping(); err == nil {
		t.Fatal("Close left resources.db open")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("a second Close must be a no-op, got %v", err)
	}
}

// A sampler that ignores its context keeps the loop running past Stop's
// deadline; the database must stay open until the loop is joined.
func TestModule_StopWaitsForTheLoopAndCloseClosesDB(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	entered := make(chan struct{}, 1)
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return idleRaw(), nil, nil
	}}
	m, _ := initedModule(t, t.TempDir(), &fakeSettings{}, s)
	t.Cleanup(unblock) // a failing assertion must not leave the loop stuck for the Stop in Cleanup
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Stop(ctx); err == nil {
		t.Fatal("Stop must report the shared deadline")
	}
	if err := m.store.db.Ping(); err != nil {
		t.Fatalf("the db was closed under a running loop: %v", err)
	}
	unblock()
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.store.db.Ping(); err == nil {
		t.Fatal("the db stayed open after Close")
	}
}

// Codex attack (medium): the dedupe key is the run of failures, not the error
// text. A reader whose failure text changes on every call (a timestamp, a
// SQLite detail) must still log once, and the way back once.
func TestSettings_ChangingFailureTextLogsOnce(t *testing.T) {
	set := &fakeSettings{}
	set.set(resources.Settings{Mode: resources.ModeLease})
	var n atomic.Int64
	set.errFn = func() error { return fmt.Errorf("decode failure #%d", n.Add(1)) }
	m, logs := initedModule(t, t.TempDir(), set, idleSampler())
	for i := 0; i < 5; i++ {
		m.tick(context.Background())
		getResources(t, m, "")
	}
	if got := logs.count("cannot read the resources setting"); got != 1 {
		t.Fatalf("a failure run logged %d times, want 1: %v", got, logs.lines)
	}
	set.set(resources.Settings{Mode: resources.ModeLease})
	m.tick(context.Background())
	getResources(t, m, "")
	if got := logs.count("readable again"); got != 1 {
		t.Fatalf("recovery logged %d times, want 1: %v", got, logs.lines)
	}
	// A second run of failures logs again, once.
	set.errFn = func() error { return fmt.Errorf("decode failure #%d", n.Add(1)) }
	m.tick(context.Background())
	m.tick(context.Background())
	if got := logs.count("cannot read the resources setting"); got != 2 {
		t.Fatalf("two runs of failures logged %d times, want 2: %v", got, logs.lines)
	}
}

// Codex attack (medium): after Stop the module is finished. A Start that
// "succeeds" without a sampler would leave a daemon that serves a frozen
// snapshot, so it says so instead.
func TestModule_StartAfterStopFails(t *testing.T) {
	m, _ := initedModule(t, t.TempDir(), &fakeSettings{}, idleSampler())
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("Start after Stop must fail, not pretend to run")
	}
}

func TestModule_StartAfterStopBeforeStartFails(t *testing.T) {
	m, _ := initedModule(t, t.TempDir(), &fakeSettings{}, idleSampler())
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("Start after a Stop that came first must fail")
	}
	if m.latest.Load() != nil {
		t.Fatal("a refused Start must not sample")
	}
}
