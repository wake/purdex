package hostconfig

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trend_cpu_dppm accepts 0 (cpu takes no part in the direction) and refuses
// anything below it or above 500; the stored default is 0 (G1).
func TestResourcesSettings_TrendCPUCanBeZero(t *testing.T) {
	s, err := normalizeResources(json.RawMessage(`{"trend_cpu_dppm":0}`))
	require.NoError(t, err)
	assert.Equal(t, 0, *s.TrendCPUDPPM)

	unset, err := normalizeResources(json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, 0, *unset.TrendCPUDPPM, "the default")
	assert.Equal(t, 120, *unset.TrendWindowS)
	assert.Equal(t, 20, *unset.TrendMemDPPM)

	for _, bad := range []string{`{"trend_cpu_dppm":-1}`, `{"trend_cpu_dppm":501}`} {
		_, err := normalizeResources(json.RawMessage(bad))
		assert.Error(t, err, bad)
	}
}
