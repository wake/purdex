package profiles

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

func newTestCore(t *testing.T) *core.Core {
	t.Helper()
	return &core.Core{
		Cfg:    &config.Config{DataDir: t.TempDir()},
		Events: core.NewEventsBroadcaster(),
	}
}

// initModule runs Init and arranges for Stop at the end of the test.
func initModule(t *testing.T, c *core.Core) *Module {
	t.Helper()
	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { m.Stop(context.Background()) })
	return m
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Mode().Perm()
}

func TestModuleNameAndDependencies(t *testing.T) {
	m := New()
	assert.Equal(t, "profiles", m.Name())
	assert.Nil(t, m.Dependencies())
}

func TestModuleImplementsCoreModule(t *testing.T) {
	var _ core.Module = New()
}

func TestInitCreatesTheDatabaseOwnerOnly(t *testing.T) {
	c := newTestCore(t)
	m := initModule(t, c)
	path := filepath.Join(c.Cfg.DataDir, "profiles.db")

	assert.Equal(t, os.FileMode(0o600), fileMode(t, path), "the file holds host tokens")

	// A write makes sqlite create the WAL siblings; they hold the same bytes.
	_, err := m.store.CreateProfile("P")
	require.NoError(t, err)
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			assert.Equal(t, os.FileMode(0o600), fileMode(t, path+suffix), suffix)
		}
	}
}

func TestInitTightensAnExistingLooseDatabase(t *testing.T) {
	c := newTestCore(t)
	path := filepath.Join(c.Cfg.DataDir, "profiles.db")

	first := New()
	require.NoError(t, first.Init(c))
	p, err := first.store.CreateProfile("Kept")
	require.NoError(t, err)
	require.NoError(t, first.Stop(context.Background()))
	require.NoError(t, os.Chmod(path, 0o644))

	second := initModule(t, c)
	assert.Equal(t, os.FileMode(0o600), fileMode(t, path))
	got, found, err := second.store.GetProfile(p.ID)
	require.NoError(t, err)
	require.True(t, found, "reopening an existing database keeps its rows")
	assert.Equal(t, "Kept", got.Name)
}

func TestInitFailsWhenTheDataDirIsMissing(t *testing.T) {
	c := newTestCore(t)
	c.Cfg.DataDir = filepath.Join(c.Cfg.DataDir, "does", "not", "exist")
	assert.Error(t, New().Init(c))
}

func TestInitRegistersTheRoutes(t *testing.T) {
	m := initModule(t, newTestCore(t))
	rr := serve(m, http.MethodGet, "/api/profiles", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"profiles":[]}`, rr.Body.String())
}

func TestStartIsANoOp(t *testing.T) {
	m := initModule(t, newTestCore(t))
	assert.NoError(t, m.Start(context.Background()))
}

func TestStopClosesTheStore(t *testing.T) {
	m := New()
	require.NoError(t, m.Init(newTestCore(t)))
	require.NoError(t, m.Stop(context.Background()))

	_, err := m.store.ListProfiles()
	assert.Error(t, err, "the database is closed")
}

func TestStopBeforeInitIsSafe(t *testing.T) {
	assert.NoError(t, New().Stop(context.Background()))
}

func TestInitWiresBroadcastToCoreEvents(t *testing.T) {
	c := newTestCore(t)
	m := initModule(t, c)
	sub := c.Events.AddTestSubscriber()
	defer c.Events.RemoveTestSubscriber(sub)

	pid := createProfile(t, m, "P")
	putSection(t, m, pid, "hosts", clientA, 0, hashOf("1"), `{}`)

	select {
	case msg := <-sub.SendCh():
		var ev core.HostEvent
		require.NoError(t, json.Unmarshal(msg, &ev))
		assert.Equal(t, "profile", ev.Type)
		assert.Equal(t, "", ev.Session, "not a session event")
		got := decodeKeys(t, []byte(ev.Value))
		assert.JSONEq(t, `"`+pid+`"`, string(got["profileId"]))
		assert.JSONEq(t, `"hosts"`, string(got["section"]))
		assert.JSONEq(t, `1`, string(got["rev"]))
	default:
		t.Fatal("an applied PUT must reach core.Events")
	}
}

func TestBroadcastToleratesACoreWithoutEvents(t *testing.T) {
	c := newTestCore(t)
	c.Events = nil
	m := initModule(t, c)
	pid := createProfile(t, m, "P")
	assert.Equal(t, int64(1), putSection(t, m, pid, "hosts", clientA, 0, hashOf("1"), `{}`))
}
