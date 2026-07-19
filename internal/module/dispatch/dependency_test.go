package dispatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/bridge"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/execution"
	"github.com/wake/purdex/internal/module/stream"
	"github.com/wake/purdex/internal/relay"
	"github.com/wake/purdex/internal/tmux"
)

// F2 — a claimed dispatch that cannot be given a durable execution row plus a
// report chain is a wedge: Ploom moves it to claimed and then never hears another
// word (M0 has no dispatch expiry). So the execution store, the relay gateway and
// the terminal seam are MANDATORY for the consumer: any of them missing must fail
// Init and leave the worker unbuilt, so nothing is ever polled or claimed.

// fakeSeam (terminal_report_test.go) stands in for the stream module's
// terminal-outcome seam.
var _ streamTerminalSeam = (*fakeSeam)(nil)

// depParts selects which dependencies a test core publishes.
type depParts struct {
	store   bool
	gateway bool
	seam    bool
}

func allDeps() depParts { return depParts{store: true, gateway: true, seam: true} }

// newDepCore builds a Core with the Ploom endpoint configured and only the
// selected dependencies registered.
func newDepCore(t *testing.T, parts depParts, ploomURL string) *core.Core {
	t.Helper()
	t.Setenv(envPloomURL, ploomURL)
	t.Setenv(envPloomToken, "tok")

	dir := t.TempDir()
	reg := core.NewServiceRegistry()
	if parts.store {
		store, err := execution.OpenExecution(filepath.Join(dir, "exec.db"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = store.Close() })
		reg.Register(execution.RegistryKey, store)
	}
	if parts.gateway {
		reg.Register(stream.RelayGatewayKey, bridge.New())
	}
	if parts.seam {
		reg.Register(stream.TerminalSeamKey, &fakeSeam{})
	}
	return core.New(core.CoreDeps{
		Config:   &config.Config{DataDir: dir, Bind: "127.0.0.1", Port: 7860, Token: "tok"},
		Tmux:     tmux.NewFakeExecutor(),
		Registry: reg,
	})
}

func TestModule_Init_MissingDependency_FailsClosed(t *testing.T) {
	cases := []struct {
		name  string
		parts depParts
	}{
		{"no execution store", depParts{gateway: true, seam: true}},
		{"no relay gateway", depParts{store: true, seam: true}},
		{"no terminal seam", depParts{store: true, gateway: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			err := m.Init(newDepCore(t, tc.parts, "https://ploom.test"))
			require.ErrorIs(t, err, ErrMissingDependency)
			require.Nil(t, m.worker, "worker must not exist — nothing may poll or claim")
			require.Nil(t, m.client, "no Ploom client is built when deps are missing")
			require.Nil(t, m.sink)
		})
	}
}

// The whole point of failing closed: no HTTP request ever reaches Ploom, so no
// dispatch is ever claimed and left without a runtime row.
func TestModule_DependencyMissing_NeverPollsOrClaims(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := New()
	// Relay gateway missing → Init must fail and Start must be inert.
	require.Error(t, m.Init(newDepCore(t, depParts{store: true, seam: true}, srv.URL)))
	require.NoError(t, m.Start(context.Background()))
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, m.Stop(context.Background()))

	require.Zero(t, atomic.LoadInt64(&hits), "a degraded daemon must never poll or claim")
}

// All dependencies present → the consumer is fully wired (worker, sender, sink,
// reconciler) and the terminal handler is registered on the seam.
func TestModule_Init_AllDependencies_Wires(t *testing.T) {
	seam := &fakeSeam{}
	c := newDepCore(t, depParts{store: true, gateway: true}, "https://ploom.test")
	c.Registry.Register(stream.TerminalSeamKey, seam)

	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })

	require.NotNil(t, m.worker)
	require.NotNil(t, m.sender)
	require.NotNil(t, m.sink)
	require.NotNil(t, m.outbox)
	require.NotNil(t, m.reconciler)
	require.NotNil(t, seam.handler, "terminal handler must be registered")
}

// A registry entry of the wrong type is as fatal as a missing one (fail closed,
// never silently degrade).
func TestModule_Init_WrongTypedDependency_FailsClosed(t *testing.T) {
	c := newDepCore(t, depParts{gateway: true, seam: true}, "https://ploom.test")
	c.Registry.Register(execution.RegistryKey, "not-a-store")

	m := New()
	require.ErrorIs(t, m.Init(c), ErrMissingDependency)
	require.Nil(t, m.worker)
}

// Sanity: the terminal seam we inject is the one the module drives.
func TestModule_Init_TerminalHandlerUsesSeam(t *testing.T) {
	seam := &fakeSeam{}
	c := newDepCore(t, allDeps(), "https://ploom.test")
	c.Registry.Register(stream.TerminalSeamKey, seam)

	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })

	// Unknown session code → handler is a no-op, but it must not panic.
	require.NotPanics(t, func() {
		seam.handler("no-such-code", relay.TerminalEvent{ExitCode: 1})
	})
}
