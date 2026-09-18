package nex

// P-C.3a task 2: the nex module's hard dependencies on the session and
// agent modules (spec §4.4). The fakes here are the smallest things that
// satisfy the registry interfaces Init looks up; task 3's handoff tests
// grow them.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/probe"
	pdxconfig "github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
)

type fakeSessionProvider struct{}

func (fakeSessionProvider) ListSessions() ([]session.SessionInfo, error)                { return nil, nil }
func (fakeSessionProvider) GetSession(string) (*session.SessionInfo, error)             { return nil, nil }
func (fakeSessionProvider) UpdateMeta(string, session.MetaUpdate) error                 { return nil }
func (fakeSessionProvider) HandleTerminalWS(http.ResponseWriter, *http.Request, string) {}
func (fakeSessionProvider) TmuxInstance() string                                        { return "" }
func (fakeSessionProvider) SessionExists(string) bool                                   { return false }
func (fakeSessionProvider) ValidateCwd(string) error                                    { return nil }
func (fakeSessionProvider) CreateSession(string, string) (*session.SessionInfo, error) {
	return nil, errors.New("not implemented")
}

type fakeOwnerResolver struct{}

func (fakeOwnerResolver) ResolveSessionOwner(context.Context, string) (agent.PaneOwner, bool, error) {
	return agent.PaneOwner{}, false, nil
}

type fakeProber struct{}

func (fakeProber) IsAliveFor(string, string) bool { return false }
func (fakeProber) CheckReadiness(string, string) (probe.ReadinessResult, bool) {
	return probe.ReadinessResult{}, false
}

type fakeCCOperator struct{}

func (fakeCCOperator) Exit(context.Context, string) error           { return nil }
func (fakeCCOperator) Launch(context.Context, string, string) error { return nil }
func (fakeCCOperator) Interrupt(context.Context, string) error      { return nil }
func (fakeCCOperator) GetStatus(context.Context, string) (*agentcc.StatusInfo, error) {
	return nil, nil
}

// Compile-time checks that the fakes satisfy exactly what Init looks up.
var (
	_ session.SessionProvider = fakeSessionProvider{}
	_ agent.OwnerResolver     = fakeOwnerResolver{}
	_ livenessProber          = fakeProber{}
	_ agentcc.CCOperator      = fakeCCOperator{}
)

// requiredProviders is every registry entry Init demands, keyed as the
// session and agent modules register them. The lock instance is the
// session module's (one per daemon, shared with stream), so it is a
// provider like the others, not something Init builds.
func requiredProviders() map[string]any {
	return map[string]any{
		session.RegistryKey:     fakeSessionProvider{},
		session.HandoffLocksKey: session.NewHandoffLocks(),
		agent.OwnerResolverKey:  fakeOwnerResolver{},
		proberKey:               fakeProber{},
		agentcc.OperatorKey:     fakeCCOperator{},
	}
}

// newTestCoreWithout builds a core whose registry carries every required
// provider except the one named — the "which key is missing" fixture.
func newTestCoreWithout(cfg *pdxconfig.Config, missing string) *core.Core {
	reg := core.NewServiceRegistry()
	for key, svc := range requiredProviders() {
		if key != missing {
			reg.Register(key, svc)
		}
	}
	return core.New(core.CoreDeps{Config: cfg, Registry: reg})
}

func TestInitFailsHardWhenAProviderIsMissing(t *testing.T) {
	for key := range requiredProviders() {
		t.Run(key, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("PATH", launchdPath)
			cfg := baseConfig(t)
			rec := &fakeAssembleRecord{}
			m := New()
			m.assemble = newFakeAssemble(rec, noopEngine(), nil)
			m.logf = discardLogf

			err := m.Init(newTestCoreWithout(&cfg, key))
			require.Error(t, err, "a missing provider is a hard Init error, not a soft-fail")
			assert.Contains(t, err.Error(), "nex: init:")
			assert.Contains(t, err.Error(), key, "the error names the missing registry key")
			assert.Nil(t, m.initErr, "the engine soft-fail path is not involved")
			assert.Equal(t, 0, rec.calls, "assemble must not run without every provider")
		})
	}
}

// TestInitRejectsAProviderOfTheWrongType: a registered value that does not
// implement the expected interface is as fatal as an absent one, and the
// error still names the key.
func TestInitRejectsAProviderOfTheWrongType(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	reg := core.NewServiceRegistry()
	for key, svc := range requiredProviders() {
		reg.Register(key, svc)
	}
	reg.Register(agentcc.OperatorKey, struct{}{})
	m := New()
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	m.logf = discardLogf

	err := m.Init(core.New(core.CoreDeps{Config: &cfg, Registry: reg}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), agentcc.OperatorKey)
	assert.Contains(t, err.Error(), "does not implement")
}

// TestInitWiresProvidersAndLock: a successful Init keeps what it looked up
// (the handoff endpoints of task 3/4 read these fields), and the
// per-session lock is the registry's instance — the same one the session
// module registered and the stream module holds — not a private copy.
func TestInitWiresProvidersAndLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	c := newTestCore(&cfg)
	m := New()
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	m.logf = discardLogf

	require.NoError(t, m.Init(c))
	assert.NotNil(t, m.sessions)
	assert.NotNil(t, m.owners)
	assert.NotNil(t, m.prober)
	assert.NotNil(t, m.ccOps)
	assert.Equal(t, c.Tmux, m.tmux)
	require.NotNil(t, m.locks)
	shared, _ := c.Registry.Get(session.HandoffLocksKey)
	assert.Same(t, shared, m.locks, "the lock is the registered instance, not a new one")
	assert.True(t, m.locks.TryLock("code"))
	assert.False(t, m.locks.TryLock("code"))
}

// TestInitAppliesEngineCallBudgets: Init fills every engine-call budget;
// a zero budget would make each detached context expire on creation. The
// interrupt budget sits above Nexen's own 15 s interruptTimeout so the
// engine's verdict (confirmed / unconfirmed) is what the caller sees.
func TestInitAppliesEngineCallBudgets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	m := New()
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	m.logf = discardLogf
	require.NoError(t, m.Init(newTestCore(&cfg)))

	assert.Equal(t, 30*time.Second, m.delegateTimeout)
	assert.Equal(t, 10*time.Second, m.engineOpTimeout)
	assert.Equal(t, 20*time.Second, m.engineInterruptTimeout)
	assert.Greater(t, m.engineInterruptTimeout, 15*time.Second, "above Nexen's interruptTimeout")
	assert.Equal(t, 5*time.Second, m.leaseCleanupTimeout)
}

// TestPrincipalDelegatesToEngineAuth: m.principal(r) is exactly what the
// engine's own Authenticator would name the caller — the handoff endpoints
// must act as the same principal /api/nex would, or the lease they take
// would not be the SPA's.
func TestPrincipalDelegatesToEngineAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	m := New()
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	m.logf = discardLogf
	require.NoError(t, m.Init(newTestCore(&cfg)))

	r := httptest.NewRequest("POST", "/api/sessions/x/nex-handoff", nil)
	got, err := m.principal(r)
	require.NoError(t, err)
	assert.Equal(t, "pdx:host1", got)

	r.Header.Set(ClientHeader, "tab-1")
	got, err = m.principal(r)
	require.NoError(t, err)
	assert.Equal(t, "pdx:host1/tab-1", got)
}

// TestRealAssembleCarriesServiceAndStore: the widened engine seam is filled
// by the production adapter — a fake assemble leaves them nil, so only the
// real nexen.Assemble proves the wiring.
func TestRealAssembleCarriesServiceAndStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	m := New()
	m.logf = discardLogf
	// Never at the machine's keychain — see mount_test.go's newMountFixture.
	realAssemble := m.assemble
	m.assemble = func(ctx context.Context, opts nexen.Options) (engine, error) {
		opts.DisableHostKeychain = true
		return realAssemble(ctx, opts)
	}
	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.NoError(t, m.initErr)
	t.Cleanup(func() {
		_ = m.Stop(context.Background())
		_ = m.Close()
	})
	assert.NotNil(t, m.sys.service)
	assert.NotNil(t, m.sys.store)
}

// TestCapabilitiesPinResumeSessionIDAndHandoffProfile pins the two
// engine facts the handoff endpoints rely on (spec §4.4): the pinned Nexen
// accepts resume_session_id on delegate, and a host whose max_profile is
// "handoff" advertises that profile as usable.
func TestCapabilitiesPinResumeSessionIDAndHandoffProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	cfg.Nex.Sandbox.MaxProfile = "handoff"
	m := New()
	m.logf = discardLogf
	realAssemble := m.assemble
	m.assemble = func(ctx context.Context, opts nexen.Options) (engine, error) {
		opts.DisableHostKeychain = true
		return realAssemble(ctx, opts)
	}
	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.NoError(t, m.initErr)
	t.Cleanup(func() {
		_ = m.Stop(context.Background())
		_ = m.Close()
	})

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	// No credentials: pdx's Authenticator only names the caller (the outer
	// chain, absent here, is what checks the daemon token).
	mux.ServeHTTP(rec, httptest.NewRequest("GET", RoutePrefix+"/v1/capabilities", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body struct {
		Delegate struct {
			ResumeSessionID bool `json:"resume_session_id"`
		} `json:"delegate"`
		SandboxProfiles []string `json:"sandbox_profiles"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.True(t, body.Delegate.ResumeSessionID, "delegate.resume_session_id: %s", rec.Body.String())
	assert.Contains(t, body.SandboxProfiles, "handoff", "sandbox_profiles: %s", strings.Join(body.SandboxProfiles, ","))
}
