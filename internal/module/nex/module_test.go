package nex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lab.protype.tw/wake/nexen"

	pdxconfig "github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// Compile-time interface checks: Module must be a core.Module and a
// core.Closer so the daemon's lifecycle driver actually calls Close.
var (
	_ core.Module = (*Module)(nil)
	_ core.Closer = (*Module)(nil)
)

const launchdPath = "/usr/bin:/bin:/usr/sbin:/sbin"

// writeScript writes an executable shell script named name under dir and
// returns its path.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("writing %s: %v", p, err)
	}
	return p
}

// fakeCswap returns a cswap stand-in that prints an empty JSON object.
func fakeCswap(t *testing.T) string {
	t.Helper()
	return writeScript(t, t.TempDir(), "cswap", "#!/bin/sh\necho '{}'\n")
}

// fakeClaude returns an executable that Assemble's LookPath accepts. It is
// never actually spawned by these tests.
func fakeClaude(t *testing.T) string {
	t.Helper()
	return writeScript(t, t.TempDir(), "claude", "#!/bin/sh\nexit 0\n")
}

// baseConfig returns a pdx Config whose [nex] section is complete enough
// for Init to reach assemble: one existing repo root, a fake cswap, an
// explicit claude binary, no path_prepend, and a temp DataDir. Tests
// override individual fields for their failure case.
func baseConfig(t *testing.T) pdxconfig.Config {
	t.Helper()
	return pdxconfig.Config{
		HostID:  "host1",
		DataDir: t.TempDir(),
		Nex: pdxconfig.NexConfig{
			Enabled:     true,
			RepoRoots:   []string{t.TempDir()},
			ClaudeBin:   fakeClaude(t),
			CswapBin:    fakeCswap(t),
			PathPrepend: []string{},
			Sandbox: pdxconfig.NexSandboxConfig{
				MaxProfile:     "trusted",
				DefaultProfile: "trusted",
			},
		},
	}
}

func newTestCore(cfg *pdxconfig.Config) *core.Core {
	return core.New(core.CoreDeps{Config: cfg})
}

// fakeEngine builds an engine plus a fake assembleFn that records what it
// was called with.
type fakeAssembleRecord struct {
	calls    int
	pathSeen string
	opts     nexen.Options
}

func newFakeAssemble(rec *fakeAssembleRecord, eng engine, err error) assembleFn {
	return func(_ context.Context, opts nexen.Options) (engine, error) {
		rec.calls++
		rec.pathSeen = os.Getenv("PATH")
		rec.opts = opts
		return eng, err
	}
}

func noopEngine() engine {
	return engine{
		handler:  http.NotFoundHandler(),
		shutdown: func(context.Context) error { return nil },
		close:    func() error { return nil },
	}
}

// discardLogf swallows log lines so tests do not spam the runner output.
func discardLogf(string, ...any) {}

func TestNameAndDependencies(t *testing.T) {
	m := New()
	if got := m.Name(); got != "nex" {
		t.Errorf("Name() = %q, want %q", got, "nex")
	}
	if got := m.Dependencies(); got != nil {
		t.Errorf("Dependencies() = %v, want nil", got)
	}
}

// TestInitAppliesPathPolicyBeforeAssemble pins the Init order: `~` in
// path_prepend is expanded against $HOME first, then the policy filters
// through the real isDir and prepends, and only then is assemble called —
// so assemble observes the final PATH.
func TestInitAppliesPathPolicyBeforeAssemble(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("PATH", launchdPath)

	cfg := baseConfig(t)
	cfg.Nex.PathPrepend = []string{"~/.local/bin", existing, missing}

	sep := string(os.PathListSeparator)
	wantPrefix := localBin + sep + existing + sep

	rec := &fakeAssembleRecord{}
	m := New()
	m.assemble = newFakeAssemble(rec, noopEngine(), nil)
	m.logf = discardLogf
	if err := m.Init(newTestCore(&cfg)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if rec.calls != 1 {
		t.Fatalf("assemble called %d times, want 1", rec.calls)
	}
	if !strings.HasPrefix(rec.pathSeen, wantPrefix) {
		t.Errorf("PATH seen by assemble = %q, want prefix %q", rec.pathSeen, wantPrefix)
	}
	if strings.Contains(rec.pathSeen, missing) {
		t.Errorf("PATH seen by assemble = %q, must not contain missing dir %q", rec.pathSeen, missing)
	}
	if !strings.HasSuffix(rec.pathSeen, launchdPath) {
		t.Errorf("PATH seen by assemble = %q, want suffix %q", rec.pathSeen, launchdPath)
	}
	first := rec.pathSeen

	// A second Init on a fresh Module (same process PATH, already policed)
	// must be idempotent: PATH is identical.
	rec2 := &fakeAssembleRecord{}
	m2 := New()
	m2.assemble = newFakeAssemble(rec2, noopEngine(), nil)
	m2.logf = discardLogf
	if err := m2.Init(newTestCore(&cfg)); err != nil {
		t.Fatalf("second Init() error = %v", err)
	}
	if rec2.pathSeen != first {
		t.Errorf("second Init PATH = %q, want identical to first %q", rec2.pathSeen, first)
	}
	if got := os.Getenv("PATH"); got != first {
		t.Errorf("process PATH after second Init = %q, want %q", got, first)
	}
}

// TestInitEmptyPathPrependLeavesPathUntouched is I9 driven through Init.
func TestInitEmptyPathPrependLeavesPathUntouched(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)

	cfg := baseConfig(t)
	cfg.Nex.PathPrepend = []string{}

	rec := &fakeAssembleRecord{}
	m := New()
	m.assemble = newFakeAssemble(rec, noopEngine(), nil)
	m.logf = discardLogf
	if err := m.Init(newTestCore(&cfg)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if rec.pathSeen != launchdPath {
		t.Errorf("PATH seen by assemble = %q, want untouched %q", rec.pathSeen, launchdPath)
	}
	if got := os.Getenv("PATH"); got != launchdPath {
		t.Errorf("process PATH after Init = %q, want untouched %q", got, launchdPath)
	}
}

// TestInitHandsBuildOptionsToAssembleAndCreatesDataDir: the Options
// assemble receives are exactly buildOptions' output for the expanded
// config, and <DataDir>/nex exists by the time assemble runs.
func TestInitHandsBuildOptionsToAssembleAndCreatesDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", launchdPath)

	cfg := baseConfig(t)
	cfg.Nex.RepoRoots = []string{"~/repo"}

	rec := &fakeAssembleRecord{}
	m := New()
	m.assemble = newFakeAssemble(rec, noopEngine(), nil)
	m.logf = discardLogf
	if err := m.Init(newTestCore(&cfg)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	want, err := buildOptions(cfg.HostID, cfg.DataDir, cfg.Nex.Expanded(home), core.ShutdownBudget)
	if err != nil {
		t.Fatalf("buildOptions() error = %v", err)
	}
	got := rec.opts
	if got.Config == nil {
		t.Fatal("assemble received nil Options.Config")
	}
	if !reflect.DeepEqual(*got.Config, *want.Config) {
		t.Errorf("Options.Config mismatch\n got: %+v\nwant: %+v", *got.Config, *want.Config)
	}
	if got.PublicPrefix != want.PublicPrefix {
		t.Errorf("Options.PublicPrefix = %q, want %q", got.PublicPrefix, want.PublicPrefix)
	}
	if got.ClaudeBin != want.ClaudeBin {
		t.Errorf("Options.ClaudeBin = %q, want %q", got.ClaudeBin, want.ClaudeBin)
	}
	if got.Auth == nil {
		t.Fatal("Options.Auth is nil")
	}
	req, _ := http.NewRequest("GET", "/api/nex/v1/capabilities", nil)
	principal, err := got.Auth.Authenticate(req)
	if err != nil || principal != "pdx:host1" {
		t.Errorf("Options.Auth.Authenticate() = %q, %v; want %q, nil", principal, err, "pdx:host1")
	}
	// `~/repo` was expanded before buildOptions.
	if wantRoot := filepath.Join(home, "repo"); !reflect.DeepEqual(got.Config.RepoRoots, []string{wantRoot}) {
		t.Errorf("Options.Config.RepoRoots = %v, want [%s]", got.Config.RepoRoots, wantRoot)
	}

	wantDataDir := filepath.Join(cfg.DataDir, "nex")
	if got.Config.DataDir != wantDataDir {
		t.Errorf("Options.Config.DataDir = %q, want %q", got.Config.DataDir, wantDataDir)
	}
	info, err := os.Stat(wantDataDir)
	if err != nil {
		t.Fatalf("stat %s after Init: %v", wantDataDir, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory after Init", wantDataDir)
	}
}

// TestInitWrapsAssembleError: an assemble failure surfaces with the
// "nex: init:" prefix and the cause unwrappable.
func TestInitWrapsAssembleError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)

	cfg := baseConfig(t)
	sentinel := errors.New("boom")
	rec := &fakeAssembleRecord{}
	m := New()
	m.assemble = newFakeAssemble(rec, engine{}, sentinel)
	m.logf = discardLogf

	err := m.Init(newTestCore(&cfg))
	if err == nil {
		t.Fatal("Init() error = nil, want non-nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Init() error = %v, want it to wrap %v", err, sentinel)
	}
	if !strings.HasPrefix(err.Error(), "nex: init:") {
		t.Errorf("Init() error = %q, want prefix %q", err.Error(), "nex: init:")
	}
}

func TestStartLogsServingLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)

	cfg := baseConfig(t)
	rec := &fakeAssembleRecord{}
	m := New()
	m.assemble = newFakeAssemble(rec, noopEngine(), nil)
	var lines []string
	m.logf = func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	if err := m.Init(newTestCore(&cfg)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	var serving string
	for _, l := range lines {
		if strings.HasPrefix(l, "nex: serving "+RoutePrefix) {
			serving = l
		}
	}
	if serving == "" {
		t.Fatalf("Start() did not log a %q line; got %q", "nex: serving "+RoutePrefix, lines)
	}
	for _, want := range []string{
		"host_id=host1",
		"data_dir=" + filepath.Join(cfg.DataDir, "nex"),
		"claude_bin=" + cfg.Nex.ClaudeBin,
		"profiles=",
		"trusted",
		// PathPrepend is empty in baseConfig, so the applied prefix is
		// empty too — distinct from the earlier "PATH policy" log line,
		// which still carries the full PATH.
		"path_prepend=)",
	} {
		if !strings.Contains(serving, want) {
			t.Errorf("serving line %q missing %q", serving, want)
		}
	}
}

func TestStopDelegatesToEngine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)

	cfg := baseConfig(t)
	sentinel := errors.New("shutdown failed")
	type ctxKey struct{}
	var gotCtx context.Context
	eng := noopEngine()
	eng.shutdown = func(ctx context.Context) error {
		gotCtx = ctx
		return sentinel
	}
	rec := &fakeAssembleRecord{}
	m := New()
	m.assemble = newFakeAssemble(rec, eng, nil)
	m.logf = discardLogf
	if err := m.Init(newTestCore(&cfg)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	if err := m.Stop(ctx); err != sentinel {
		t.Errorf("Stop() error = %v, want the engine's error %v unchanged", err, sentinel)
	}
	if gotCtx == nil || gotCtx.Value(ctxKey{}) != "marker" {
		t.Errorf("Stop() did not pass its ctx through to engine.shutdown")
	}
}

func TestCloseDelegatesToEngine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)

	cfg := baseConfig(t)
	sentinel := errors.New("close failed")
	closed := 0
	eng := noopEngine()
	eng.close = func() error {
		closed++
		return sentinel
	}
	rec := &fakeAssembleRecord{}
	m := New()
	m.assemble = newFakeAssemble(rec, eng, nil)
	m.logf = discardLogf
	if err := m.Init(newTestCore(&cfg)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if err := m.Close(); err != sentinel {
		t.Errorf("Close() error = %v, want the engine's error %v unchanged", err, sentinel)
	}
	if closed != 1 {
		t.Errorf("engine.close called %d times, want 1", closed)
	}
}

// TestInitRealAssembleFailures is spec I12: with the production assemble
// (the real nexen.Assemble), each misconfiguration makes Init fail with an
// error naming the cause. None of these reach a claude spawn.
func TestInitRealAssembleFailures(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(t *testing.T, cfg *pdxconfig.Config)
		wantSubs []string
	}{
		{
			name: "claude_bin path does not exist",
			mutate: func(t *testing.T, cfg *pdxconfig.Config) {
				cfg.Nex.ClaudeBin = filepath.Join(t.TempDir(), "no-such-claude")
			},
			wantSubs: []string{"nex: init:", "locating claude binary", "no-such-claude", "no such file"},
		},
		{
			name: "claude_bin exists but is not executable",
			mutate: func(t *testing.T, cfg *pdxconfig.Config) {
				p := filepath.Join(t.TempDir(), "claude-noexec")
				if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				cfg.Nex.ClaudeBin = p
			},
			wantSubs: []string{"nex: init:", "locating claude binary", "claude-noexec", "permission denied"},
		},
		{
			name: "claude_bin empty and PATH has no claude",
			mutate: func(t *testing.T, cfg *pdxconfig.Config) {
				t.Setenv("PATH", t.TempDir())
				cfg.Nex.ClaudeBin = ""
				cfg.Nex.PathPrepend = []string{}
			},
			wantSubs: []string{"nex: init:", "locating claude binary", `"claude"`, "executable file not found in $PATH"},
		},
		{
			name: "repo root is a regular file",
			mutate: func(t *testing.T, cfg *pdxconfig.Config) {
				p := filepath.Join(t.TempDir(), "not-a-dir")
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
				cfg.Nex.RepoRoots = []string{p}
			},
			wantSubs: []string{"nex: init:", "not-a-dir", "not a directory"},
		},
		{
			name: "data_dir nex entry is a regular file",
			mutate: func(t *testing.T, cfg *pdxconfig.Config) {
				if err := os.WriteFile(filepath.Join(cfg.DataDir, "nex"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantSubs: []string{"nex: init:", "data_dir", "not a directory"},
		},
		{
			name: "empty host_id",
			mutate: func(t *testing.T, cfg *pdxconfig.Config) {
				cfg.HostID = ""
			},
			wantSubs: []string{"nex: init:", "host_id is empty"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("PATH", launchdPath)
			cfg := baseConfig(t)
			tt.mutate(t, &cfg)

			m := New() // production assemble: the real nexen.Assemble
			m.logf = discardLogf
			err := m.Init(newTestCore(&cfg))
			if err == nil {
				t.Fatal("Init() error = nil, want non-nil")
			}
			t.Logf("Init() error = %v", err)
			for _, sub := range tt.wantSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("Init() error = %q, want it to contain %q", err.Error(), sub)
				}
			}
		})
	}
}
