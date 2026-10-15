package session

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/execstat"
)

func sessionEventTotal(name string) int64 {
	for _, s := range execstat.Events() {
		if s.Name == name {
			return s.Total
		}
	}
	return 0
}

func sessionGauge(name string) (int64, bool) {
	for _, g := range execstat.Gauges() {
		if g.Name == name {
			return g.Value, true
		}
	}
	return 0, false
}

// Each list build is one session-list-build event, and the sessions gauge follows the size of the list the tmux read gave.
func TestListSessionsCountsDenominators(t *testing.T) {
	execstat.ResetTags()
	t.Cleanup(execstat.ResetTags)

	mod, _, fake := newTestModule(t)
	fake.AddSession("dev", "/home/dev")
	fake.AddSession("prod", "/home/prod")

	_, err := mod.ListSessions()
	require.NoError(t, err)
	require.Equal(t, int64(1), sessionEventTotal("session-list-build"))
	v, ok := sessionGauge("sessions")
	require.True(t, ok)
	require.Equal(t, int64(2), v)

	_, err = mod.ListSessions()
	require.NoError(t, err)
	require.Equal(t, int64(2), sessionEventTotal("session-list-build"))
}
