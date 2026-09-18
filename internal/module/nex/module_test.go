package nex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// fakeClaude returns an executable that Assemble's LookPath accepts. It is
// never actually spawned by these tests.
func fakeClaude(t *testing.T) string {
	t.Helper()
	return writeScript(t, t.TempDir(), "claude", "#!/bin/sh\nexit 0\n")
}

// baseConfig returns a pdx Config whose [nex] section is complete enough
// for Init to reach assemble: one existing repo root, an explicit claude
// binary, no path_prepend, and a temp DataDir. Tests
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
			PathPrepend: []string{},
			Sandbox: pdxconfig.NexSandboxConfig{
				MaxProfile:     "trusted",
				DefaultProfile: "trusted",
			},
		},
	}
}

// newTestCore builds a core whose registry already carries every provider
// Init demands (session provider, owner resolver, prober, CC operator), as
// the session and agent modules would have registered them by the time the
// core reaches nex (Dependencies orders it after both).
func newTestCore(cfg *pdxconfig.Config) *core.Core {
	return newTestCoreWithout(cfg, "")
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
	// session + agent: Init looks their services up in the registry, so the
	// core must init them first (spec §4.4, P-C.3a).
	if got, want := m.Dependencies(), []string{"session", "agent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dependencies() = %v, want %v", got, want)
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

// TestInitWithoutHomeAllAbsolutePathsSucceeds: Init mirrors config.Load's
// HOME rule (codex R2 follow-up). A launchd/Finder-started daemon may have
// no $HOME (os.UserHomeDir then fails on darwin: "$HOME is not defined");
// an enabled [nex] whose roots/claude_bin are absolute and whose
// path_prepend is [] has nothing to expand, so Init must still assemble.
func TestInitWithoutHomeAllAbsolutePathsSucceeds(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("PATH", launchdPath)
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("os.UserHomeDir succeeds with HOME empty on this platform; the HOME-less path is not reachable here")
	}

	cfg := baseConfig(t) // absolute repo root / claude_bin, path_prepend = []
	rec := &fakeAssembleRecord{}
	m := New()
	m.assemble = newFakeAssemble(rec, noopEngine(), nil)
	m.logf = discardLogf
	if err := m.Init(newTestCore(&cfg)); err != nil {
		t.Fatalf("Init() error = %v, want nil without HOME when nothing needs expanding", err)
	}
	if rec.calls != 1 {
		t.Fatalf("assemble called %d times, want 1", rec.calls)
	}
	if got := rec.opts.Config.RepoRoots; !reflect.DeepEqual(got, cfg.Nex.RepoRoots) {
		t.Errorf("Options.Config.RepoRoots = %v, want %v unchanged", got, cfg.Nex.RepoRoots)
	}
}

// TestInitWithoutHomeTildeEntryNamesHOME: with no $HOME, a `~` entry in an
// enabled [nex] cannot be expanded; Init fails with the same HOME-naming
// error config.Load would give, not a misleading "must be an absolute
// path" or a bare "resolving home directory".
func TestInitWithoutHomeTildeEntryNamesHOME(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("PATH", launchdPath)
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("os.UserHomeDir succeeds with HOME empty on this platform; the HOME-less path is not reachable here")
	}

	cfg := baseConfig(t)
	cfg.Nex.PathPrepend = []string{"~/.local/bin"}
	rec := &fakeAssembleRecord{}
	m := New()
	m.assemble = newFakeAssemble(rec, noopEngine(), nil)
	m.logf = discardLogf

	err := m.Init(newTestCore(&cfg))
	if err == nil {
		t.Fatal("Init() error = nil, want an error naming HOME")
	}
	if !strings.HasPrefix(err.Error(), "nex: init:") {
		t.Errorf("Init() error = %q, want prefix %q", err.Error(), "nex: init:")
	}
	if !strings.Contains(err.Error(), "HOME") || !strings.Contains(err.Error(), "nex.path_prepend[0]") {
		t.Errorf("Init() error = %q, want it to name HOME and nex.path_prepend[0]", err.Error())
	}
	if rec.calls != 0 {
		t.Errorf("assemble called %d times, want 0", rec.calls)
	}
}

// TestInitWrapsAssembleError: an assemble failure is a soft-fail (spec I8)
// — Init itself returns nil, and the wrapped, unwrappable cause is recorded
// on m.initErr instead.
func TestInitWrapsAssembleError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)

	cfg := baseConfig(t)
	sentinel := errors.New("boom")
	rec := &fakeAssembleRecord{}
	m := New()
	m.assemble = newFakeAssemble(rec, engine{}, sentinel)
	m.logf = discardLogf

	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.Error(t, m.initErr)
	assert.ErrorIs(t, m.initErr, sentinel)
	assert.Contains(t, m.initErr.Error(), "nex: init: assembling engine:")
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
		// empty too.
		"path_prepend=)",
	} {
		if !strings.Contains(serving, want) {
			t.Errorf("serving line %q missing %q", serving, want)
		}
	}
}

// TestInitPathPolicyLogPrintsPrefixAndCountNotFullPath: the "PATH policy"
// line names the applied prefix and how many elements the original PATH
// had, never the full PATH (which on a developer machine is kilobytes).
func TestInitPathPolicyLogPrintsPrefixAndCountNotFullPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const secret = "/opt/very-long-and-distinctive-path-element"
	t.Setenv("PATH", launchdPath+string(os.PathListSeparator)+secret)
	existing := t.TempDir()

	cfg := baseConfig(t)
	cfg.Nex.PathPrepend = []string{existing}

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

	var policy string
	for _, l := range lines {
		if strings.HasPrefix(l, "nex: PATH policy applied") {
			policy = l
		}
	}
	if policy == "" {
		t.Fatalf("no \"nex: PATH policy applied\" line; got %q", lines)
	}
	if !strings.Contains(policy, "prefix="+existing) {
		t.Errorf("policy line %q missing prefix=%s", policy, existing)
	}
	if !strings.Contains(policy, "path_elements=5") {
		t.Errorf("policy line %q missing path_elements=5 (original PATH element count)", policy)
	}
	if strings.Contains(policy, secret) {
		t.Errorf("policy line %q leaks the full PATH", policy)
	}
}

func TestProfilesTextSpellsOutEmptyAsReadonlyDefault(t *testing.T) {
	got := profilesText("", "")
	want := "max=readonly (nexen fail-closed default),default=readonly (nexen fail-closed default)"
	if got != want {
		t.Errorf("profilesText(\"\", \"\") = %q, want %q", got, want)
	}
	if got := profilesText("trusted", " "); got != "max=trusted,default=readonly (nexen fail-closed default)" {
		t.Errorf("profilesText(\"trusted\", \" \") = %q", got)
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
			wantSubs: []string{"nex: init: host_id is empty"},
		},
		{
			name: "data_dir nex exists but is not writable",
			mutate: func(t *testing.T, cfg *pdxconfig.Config) {
				if os.Geteuid() == 0 {
					t.Skip("running as root: directory modes are not enforced")
				}
				dir := filepath.Join(cfg.DataDir, "nex")
				if err := os.Mkdir(dir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(dir, 0o700) })
			},
			// sqlite reports the unwritable directory as "unable to open
			// database file", not EACCES — the store prefix is what makes
			// the message actionable.
			wantSubs: []string{"nex: init: assembling engine:", "opening store", "unable to open database file"},
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
			require.NoError(t, m.Init(newTestCore(&cfg)))
			require.Error(t, m.initErr)
			t.Logf("m.initErr = %v", m.initErr)
			for _, sub := range tt.wantSubs {
				assert.Contains(t, m.initErr.Error(), sub)
			}
			if strings.Contains(m.initErr.Error(), "nex: init: nex:") {
				t.Errorf("m.initErr = %q, \"nex:\" prefix doubled", m.initErr.Error())
			}
		})
	}
}

// TestInitAssembleFailureIsSoft is spec I8: an engine that fails to assemble
// leaves the daemon alive — Init returns nil, the module records the error,
// every /api/nex path answers 503 nex_unavailable, and Start/Stop/Close are
// no-ops.
func TestInitAssembleFailureIsSoft(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	c := newTestCore(&cfg)
	m := New()
	m.logf = func(string, ...any) {}
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, engine{}, errors.New("boom: store locked"))

	require.NoError(t, m.Init(c), "assemble failure must not fail Init")
	require.Error(t, m.initErr)
	assert.Contains(t, m.initErr.Error(), "nex: init: assembling engine: boom: store locked")

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	for _, path := range []string{"/api/nex/v1/capabilities", "/api/nex/v1/executions", "/api/nex/v1/events?execution_id=x"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, path)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"), path)
		var body map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), path)
		assert.Equal(t, "nex_unavailable", body["code"], path)
		assert.Contains(t, body["error"], "boom: store locked", path)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/nex/v1/executions", strings.NewReader("{}")))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	assert.NoError(t, m.Start(context.Background()))
	assert.NoError(t, m.Stop(context.Background()))
	assert.NoError(t, m.Close())

	st := m.Status()
	assert.Equal(t, false, st["ready"])
	assert.Contains(t, st["init_error"], "boom: store locked")
	assert.Nil(t, st["effective"])
}

// TestInitDataDirFailureIsSoft: the data_dir mkdir path takes the same route.
func TestInitDataDirFailureIsSoft(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	blocker := filepath.Join(t.TempDir(), "file-not-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	cfg.DataDir = blocker // <DataDir>/nex cannot be created under a regular file
	c := newTestCore(&cfg)
	m := New()
	m.logf = func(string, ...any) {}
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	require.NoError(t, m.Init(c))
	require.Error(t, m.initErr)
	assert.Contains(t, m.initErr.Error(), "creating data_dir")
}

// TestInitValidateFailureStaysFatal pins the division of labour: a static
// shape error is still an Init error (config load already rejects it; this
// is the belt to that braces).
func TestInitValidateFailureStaysFatal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	cfg.Nex.RepoRoots = nil
	cfg.Nex.ServiceRoots = nil
	c := newTestCore(&cfg)
	m := New()
	m.logf = func(string, ...any) {}
	err := m.Init(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one root required")
}

// TestStatusReadyReportsEffective: a healthy Init reports the expanded config.
func TestStatusReadyReportsEffective(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	cfg.Nex.ClaudeBin = "" // lazy → reported as ""
	cfg.Nex.Sandbox.MaxProfile = "handoff"
	cfg.Nex.Timeouts.LeaseTTL = "90s"
	c := newTestCore(&cfg)
	m := New()
	m.logf = func(string, ...any) {}
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	require.NoError(t, m.Init(c))
	st := m.Status()
	assert.Equal(t, true, st["ready"])
	assert.Equal(t, "", st["init_error"])
	eff, ok := st["effective"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(cfg.DataDir, "nex"), eff["data_dir"])
	assert.Equal(t, "", eff["claude_bin"])
	assert.Equal(t, "handoff", eff["max_profile"])
	assert.Equal(t, "1m30s", eff["lease_ttl"])
	assert.Equal(t, cfg.Nex.RepoRoots, eff["repo_roots"])
}

// TestInitSoftFailRestoresPath: a soft-failed Init must not leave the
// process PATH carrying the path_prepend policy — the engine is not
// running, so nothing needs it, and every other daemon child would
// otherwise inherit it (spec §4.4.1, I8/I9).
func TestInitSoftFailRestoresPath(t *testing.T) {
	cases := map[string]func(t *testing.T, cfg *pdxconfig.Config, m *Module){
		"assemble failure": func(t *testing.T, cfg *pdxconfig.Config, m *Module) {
			m.assemble = newFakeAssemble(&fakeAssembleRecord{}, engine{}, errors.New("boom"))
		},
		"data_dir failure": func(t *testing.T, cfg *pdxconfig.Config, m *Module) {
			blocker := filepath.Join(t.TempDir(), "file-not-dir")
			require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
			cfg.DataDir = blocker
			m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("PATH", launchdPath)
			cfg := baseConfig(t)
			cfg.Nex.PathPrepend = []string{t.TempDir()}
			m := New()
			m.logf = discardLogf
			setup(t, &cfg, m)
			c := newTestCore(&cfg)

			require.NoError(t, m.Init(c))
			require.Error(t, m.initErr)
			assert.Equal(t, launchdPath, os.Getenv("PATH"))
		})
	}
}
