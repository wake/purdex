package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stopModule advances the shared fake clock by dur in Stop and Close.
type stopModule struct {
	name     string
	dur      time.Duration
	clock    *fakeClock
	stopErr  error
	closeErr error
}

func (m *stopModule) Name() string                  { return m.name }
func (m *stopModule) Dependencies() []string        { return nil }
func (m *stopModule) RegisterRoutes(*http.ServeMux) {}
func (m *stopModule) Init(*Core) error              { return nil }
func (m *stopModule) Start(context.Context) error   { return nil }
func (m *stopModule) Stop(context.Context) error    { m.clock.t = m.clock.t.Add(m.dur); return m.stopErr }
func (m *stopModule) Close() error                  { m.clock.t = m.clock.t.Add(m.dur); return m.closeErr }

func newStopCore(mods ...*stopModule) (*Core, *logCollector) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	lc := &logCollector{}
	c := New(CoreDeps{})
	c.now = clk.now
	c.logf = lc.logf
	for _, m := range mods {
		m.clock = clk
		c.AddModule(m)
	}
	return c, lc
}

func TestStopModulesLogsSortedSummaryOthersAndSlow(t *testing.T) {
	c, lc := newStopCore(
		&stopModule{name: "agent", dur: ms(120)},
		&stopModule{name: "session", dur: ms(6900)},
		&stopModule{name: "tiny", dur: ms(2)},
	)
	require.NoError(t, c.StopModules(context.Background()))
	joined := strings.Join(lc.lines, "\n")
	assert.Contains(t, joined, "shutdown: slow module stop: session took 6900ms")
	assert.Contains(t, joined, "shutdown: stop 3 modules in 7022ms: session=6900ms agent=120ms others=2ms")
	assert.NotContains(t, joined, "tiny took")
}

func TestStopModulesErrorStillLoggedAndReturned(t *testing.T) {
	boom := errors.New("boom")
	c, lc := newStopCore(&stopModule{name: "a", dur: ms(50), stopErr: boom})
	err := c.StopModules(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, "module a stop: boom", err.Error())
	require.Len(t, lc.lines, 1)
	assert.Equal(t, "shutdown: stop 1 modules in 50ms: a=50ms", lc.lines[0])
}

func TestCloseModulesLogsSummaryOnlyClosers(t *testing.T) {
	boom := errors.New("boom")
	c, lc := newStopCore(
		&stopModule{name: "a", dur: ms(30), closeErr: boom},
		&stopModule{name: "b", dur: ms(10)},
	)
	// a non-Closer module must be skipped and not counted
	c.AddModule(&timedModule{name: "plain", clock: &fakeClock{}})
	err := c.CloseModules()
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, "module a close: boom", err.Error())
	require.Len(t, lc.lines, 1)
	assert.Equal(t, "shutdown: close 2 modules in 40ms: a=30ms b=10ms", lc.lines[0])
}

func TestStopCloseNoModulesNoLog(t *testing.T) {
	c, lc := newStopCore()
	require.NoError(t, c.StopModules(context.Background()))
	require.NoError(t, c.CloseModules())
	assert.Empty(t, lc.lines)
}
