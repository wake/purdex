package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestFormatModuleTimings(t *testing.T) {
	t.Run("sorted descending, Nms format", func(t *testing.T) {
		got := formatModuleTimings([]moduleTiming{
			{"session", ms(9)}, {"agent", ms(380)}, {"peers", ms(12)},
		}, ms(412))
		assert.Equal(t, "3 modules in 412ms: agent=380ms peers=12ms session=9ms", got)
	})
	t.Run("under 5ms folded into others", func(t *testing.T) {
		got := formatModuleTimings([]moduleTiming{
			{"agent", ms(100)}, {"a", ms(4)}, {"b", ms(1)}, {"edge", ms(5)},
		}, ms(110))
		assert.Equal(t, "4 modules in 110ms: agent=100ms edge=5ms others=5ms", got)
	})
	t.Run("no others when all above threshold", func(t *testing.T) {
		got := formatModuleTimings([]moduleTiming{{"agent", ms(50)}}, ms(50))
		assert.NotContains(t, got, "others")
	})
	t.Run("equal durations keep a stable order", func(t *testing.T) {
		got := formatModuleTimings([]moduleTiming{{"x", ms(10)}, {"y", ms(10)}}, ms(20))
		assert.Equal(t, "2 modules in 20ms: x=10ms y=10ms", got)
	})
}

func TestFormatStepTimings(t *testing.T) {
	got := FormatStepTimings([]StepTiming{{"sweepOnce", ms(12)}, {"replayFromDB", ms(3)}, {"replayStatus", ms(5190)}})
	assert.Equal(t, "sweepOnce=12ms replayFromDB=3ms replayStatus=5190ms", got)
}

// timedModule advances the shared fake clock by dur in Init and Start.
type timedModule struct {
	name     string
	dur      time.Duration
	clock    *fakeClock
	startErr error
}

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }

func (m *timedModule) Name() string                  { return m.name }
func (m *timedModule) Dependencies() []string        { return nil }
func (m *timedModule) RegisterRoutes(*http.ServeMux) {}
func (m *timedModule) Stop(context.Context) error    { return nil }
func (m *timedModule) Init(*Core) error              { m.clock.t = m.clock.t.Add(m.dur); return nil }
func (m *timedModule) Start(context.Context) error {
	m.clock.t = m.clock.t.Add(m.dur)
	return m.startErr
}

type logCollector struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCollector) logf(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
}

func newTimedCore(mods ...func(*fakeClock) *timedModule) (*Core, *logCollector) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	lc := &logCollector{}
	c := New(CoreDeps{})
	c.now = clk.now
	c.logf = lc.logf
	for _, mk := range mods {
		c.AddModule(mk(clk))
	}
	return c, lc
}

func tm(name string, dur time.Duration, startErr error) func(*fakeClock) *timedModule {
	return func(clk *fakeClock) *timedModule {
		return &timedModule{name: name, dur: dur, clock: clk, startErr: startErr}
	}
}

func TestInitModulesLogsTimingSummary(t *testing.T) {
	c, lc := newTimedCore(tm("agent", ms(380), nil), tm("peers", ms(12), nil), tm("session", ms(2), nil))
	require.NoError(t, c.InitModules())
	require.Len(t, lc.lines, 1)
	assert.Equal(t, "startup: init 3 modules in 394ms: agent=380ms peers=12ms others=2ms", lc.lines[0])
}

func TestStartModulesLogsSummaryAndSlowWarning(t *testing.T) {
	c, lc := newTimedCore(tm("agent", ms(5210), nil), tm("peers", ms(2850), nil), tm("tiny", ms(1), nil))
	require.NoError(t, c.StartModules(context.Background()))
	joined := strings.Join(lc.lines, "\n")
	assert.Contains(t, joined, "startup: slow module start: agent took 5210ms")
	assert.Contains(t, joined, "startup: slow module start: peers took 2850ms")
	assert.Contains(t, joined, "startup: start 3 modules in 8061ms: agent=5210ms peers=2850ms others=1ms")
	assert.NotContains(t, joined, "tiny took")
}

func TestStartModulesBelowSlowThresholdNoWarning(t *testing.T) {
	c, lc := newTimedCore(tm("a", ms(999), nil))
	require.NoError(t, c.StartModules(context.Background()))
	for _, l := range lc.lines {
		assert.NotContains(t, l, "slow module")
	}
}

func TestStartModulesFailureStillLogsCompletedTimings(t *testing.T) {
	boom := errors.New("boom")
	c, lc := newTimedCore(tm("agent", ms(300), nil), tm("bad", ms(40), boom), tm("never", ms(50), nil))
	err := c.StartModules(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, "module bad start: boom", err.Error())
	joined := strings.Join(lc.lines, "\n")
	assert.Contains(t, joined, "agent=300ms")
	assert.Contains(t, joined, "bad=40ms")
	assert.NotContains(t, joined, "never")
}

func TestModuleTimingsNoModulesNoLine(t *testing.T) {
	c, lc := newTimedCore()
	require.NoError(t, c.InitModules())
	require.NoError(t, c.StartModules(context.Background()))
	assert.Empty(t, lc.lines)
}
