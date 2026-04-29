package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
)

type countingTmuxPaneLister struct {
	calls int
}

func (l *countingTmuxPaneLister) ListPanes(context.Context) ([]TmuxPane, error) {
	l.calls++
	return nil, nil
}

type countingProcessCollector struct {
	calls int
}

func (c *countingProcessCollector) ListProcesses(context.Context) ([]Process, error) {
	c.calls++
	return nil, nil
}

func TestModule_ImplementsCoreModule(t *testing.T) {
	var _ core.Module = (*Module)(nil)

	m := New()

	assert.Equal(t, "monitor", m.Name())
	assert.NotContains(t, m.Dependencies(), "agent")
}

func TestModule_RegisterRoutes(t *testing.T) {
	m := New()
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/monitor/snapshot"},
		{method: http.MethodGet, path: "/api/monitor/config"},
		{method: http.MethodPut, path: "/api/monitor/config"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			assert.NotEqual(t, http.StatusNotFound, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		})
	}
}

func TestModule_DoesNotSampleOnStart(t *testing.T) {
	tmuxLister := &countingTmuxPaneLister{}
	processCollector := &countingProcessCollector{}
	m := New(WithCollectors(Collectors{
		TmuxPaneLister:        tmuxLister,
		ProcessTableCollector: processCollector,
	}))

	require.NoError(t, m.Init(core.New(core.CoreDeps{})))
	require.NoError(t, m.Start(context.Background()))

	assert.Zero(t, tmuxLister.calls, "monitor should not list tmux panes during startup")
	assert.Zero(t, processCollector.calls, "monitor should not collect processes during startup")

	require.NoError(t, m.Stop(context.Background()))
}
