package hosttransfer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

func TestModuleImplementsCoreModule(t *testing.T) {
	var _ core.Module = New()
}

func TestModuleNameAndDependencies(t *testing.T) {
	m := New()
	assert.Equal(t, "hosttransfer", m.Name())
	assert.Nil(t, m.Dependencies())
}

func TestInitGivesAnEmptyStore(t *testing.T) {
	m := New()
	require.NoError(t, m.Init(&core.Core{}))
	require.NotNil(t, m.store)
	_, _, err := m.store.Create([]byte(`[{}]`))
	assert.NoError(t, err)
}

func TestStopDropsEveryPayload(t *testing.T) {
	m := New()
	require.NoError(t, m.Init(&core.Core{}))
	require.NoError(t, m.Start(context.Background()))
	code, _, err := m.store.Create([]byte(`[{"token":"t"}]`))
	require.NoError(t, err)
	store := m.store

	require.NoError(t, m.Stop(context.Background()))
	_, _, err = store.Redeem(code)
	assert.ErrorIs(t, err, ErrInvalidCode, "Stop clears the store it was serving")

	// A new Init starts from nothing.
	require.NoError(t, m.Init(&core.Core{}))
	_, _, err = m.store.Redeem(code)
	assert.ErrorIs(t, err, ErrInvalidCode)
}

// The token Init hands the handlers is read live from the core config under
// CfgMu, like the outer chain's tokenFn: clearing it closes the endpoints,
// setting it opens them, with no re-Init.
func TestInitReadsTheAdminTokenLive(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{Token: ""}, Registry: core.NewServiceRegistry()})
	m := New()
	require.NoError(t, m.Init(c))
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	create := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/host-transfer", strings.NewReader(oneHost)))
		return rec
	}

	assertReason(t, create(), http.StatusForbidden, "no_token")

	c.CfgMu.Lock()
	c.Cfg.Token = "admin-token"
	c.CfgMu.Unlock()
	assert.Equal(t, http.StatusOK, create().Code)

	c.CfgMu.Lock()
	c.Cfg.Token = ""
	c.CfgMu.Unlock()
	assertReason(t, create(), http.StatusForbidden, "no_token")
}

func TestInitWithoutConfigFailsClosed(t *testing.T) {
	m := New()
	require.NoError(t, m.Init(&core.Core{}))
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/host-transfer", strings.NewReader(oneHost)))
	assertReason(t, rec, http.StatusForbidden, "no_token")
}

func TestStopBeforeInitIsSafe(t *testing.T) {
	assert.NoError(t, New().Stop(context.Background()))
}
