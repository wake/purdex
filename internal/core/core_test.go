package core

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orderTracker struct {
	calls []string
}

type fakeModule struct {
	name    string
	tracker *orderTracker
	initErr error
}

func (m *fakeModule) Name() string         { return m.name }
func (m *fakeModule) Dependencies() []string { return nil }
func (m *fakeModule) Init(c *Core) error {
	m.tracker.calls = append(m.tracker.calls, m.name+".Init")
	return m.initErr
}
func (m *fakeModule) RegisterRoutes(mux *http.ServeMux) {
	m.tracker.calls = append(m.tracker.calls, m.name+".RegisterRoutes")
}
func (m *fakeModule) Start(ctx context.Context) error {
	m.tracker.calls = append(m.tracker.calls, m.name+".Start")
	return nil
}
func (m *fakeModule) Stop(_ context.Context) error {
	m.tracker.calls = append(m.tracker.calls, m.name+".Stop")
	return nil
}

func TestCoreLifecycleOrder(t *testing.T) {
	tracker := &orderTracker{}
	c := New(CoreDeps{})
	c.AddModule(&fakeModule{name: "a", tracker: tracker})
	c.AddModule(&fakeModule{name: "b", tracker: tracker})

	err := c.InitModules()
	require.NoError(t, err)
	c.RegisterRoutes(http.NewServeMux())
	err = c.StartModules(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{
		"a.Init", "b.Init",
		"a.RegisterRoutes", "b.RegisterRoutes",
		"a.Start", "b.Start",
	}, tracker.calls)
}

func TestCoreStopReverseOrder(t *testing.T) {
	tracker := &orderTracker{}
	c := New(CoreDeps{})
	c.AddModule(&fakeModule{name: "a", tracker: tracker})
	c.AddModule(&fakeModule{name: "b", tracker: tracker})
	_ = c.InitModules()
	_ = c.StartModules(context.Background())

	tracker.calls = nil // reset
	err := c.StopModules(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"b.Stop", "a.Stop"}, tracker.calls)
}

func TestCoreInitErrorStops(t *testing.T) {
	tracker := &orderTracker{}
	c := New(CoreDeps{})
	c.AddModule(&fakeModule{name: "a", tracker: tracker, initErr: fmt.Errorf("boom")})
	c.AddModule(&fakeModule{name: "b", tracker: tracker})

	err := c.InitModules()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	assert.Equal(t, []string{"a.Init"}, tracker.calls)
}

// fakeCloser implements both Module and Closer interfaces for testing.
type fakeCloser struct {
	*fakeModule
	closeErr error
}

func (m *fakeCloser) Close() error {
	m.tracker.calls = append(m.tracker.calls, m.name+".Close")
	return m.closeErr
}

func TestCloseModulesOnlyCallsImplementers(t *testing.T) {
	tracker := &orderTracker{}
	c := New(CoreDeps{})
	c.AddModule(&fakeModule{name: "a", tracker: tracker})
	c.AddModule(&fakeCloser{fakeModule: &fakeModule{name: "b", tracker: tracker}})
	c.AddModule(&fakeModule{name: "c", tracker: tracker})

	_ = c.InitModules()
	_ = c.StartModules(context.Background())
	tracker.calls = nil // reset

	err := c.CloseModules()
	require.NoError(t, err)
	assert.Equal(t, []string{"b.Close"}, tracker.calls)
}

func TestCloseModulesReverseOrder(t *testing.T) {
	tracker := &orderTracker{}
	c := New(CoreDeps{})
	c.AddModule(&fakeCloser{fakeModule: &fakeModule{name: "a", tracker: tracker}})
	c.AddModule(&fakeCloser{fakeModule: &fakeModule{name: "b", tracker: tracker}})

	_ = c.InitModules()
	_ = c.StartModules(context.Background())
	tracker.calls = nil // reset

	err := c.CloseModules()
	require.NoError(t, err)
	assert.Equal(t, []string{"b.Close", "a.Close"}, tracker.calls)
}

func TestCloseModulesErrorJoining(t *testing.T) {
	tracker := &orderTracker{}
	c := New(CoreDeps{})
	c.AddModule(&fakeCloser{fakeModule: &fakeModule{name: "a", tracker: tracker}, closeErr: fmt.Errorf("err-a")})
	c.AddModule(&fakeCloser{fakeModule: &fakeModule{name: "b", tracker: tracker}, closeErr: fmt.Errorf("err-b")})

	_ = c.InitModules()
	_ = c.StartModules(context.Background())
	tracker.calls = nil // reset

	err := c.CloseModules()
	require.Error(t, err)
	// Both closers should have been called (reverse order), errors joined
	assert.Equal(t, []string{"b.Close", "a.Close"}, tracker.calls)
	assert.Contains(t, err.Error(), "err-b")
	assert.Contains(t, err.Error(), "err-a")
}

func TestCloseModulesNone(t *testing.T) {
	tracker := &orderTracker{}
	c := New(CoreDeps{})
	c.AddModule(&fakeModule{name: "a", tracker: tracker})
	c.AddModule(&fakeModule{name: "b", tracker: tracker})

	_ = c.InitModules()
	_ = c.StartModules(context.Background())
	tracker.calls = nil // reset

	err := c.CloseModules()
	require.NoError(t, err)
	// No modules implement Closer, so no calls are made; tracker.calls remains nil
	assert.Nil(t, tracker.calls)
}
