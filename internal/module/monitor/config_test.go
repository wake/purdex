package monitor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultMonitorConfig_ReturnsBoundsAndNoDaemonEnabledFlag(t *testing.T) {
	got := EffectiveMonitorConfig(DefaultMonitorConfig())

	assert.Equal(t, 5000, got.RefreshIntervalMS)
	assert.Equal(t, 1000, got.Bounds.RefreshIntervalMS.Min)
	assert.Equal(t, 60000, got.Bounds.RefreshIntervalMS.Max)
	assert.Equal(t, 10, got.TopProcessLimit)
	assert.Equal(t, 1, got.Bounds.TopProcessLimit.Min)
	assert.Equal(t, 50, got.Bounds.TopProcessLimit.Max)

	assert.NotContains(t, toJSONMap(t, got), "enabled", "daemon config must not own monitor enablement")
}

func TestMonitorConfig_ClampsUnsafeRefreshInterval(t *testing.T) {
	tooLow := EffectiveMonitorConfig(MonitorConfig{RefreshIntervalMS: 10, TopProcessLimit: 10})
	assert.Equal(t, 1000, tooLow.RefreshIntervalMS)

	zero := ClampSubmittedMonitorConfig(MonitorConfig{RefreshIntervalMS: 0, TopProcessLimit: 10})
	assert.Equal(t, 1000, zero.RefreshIntervalMS)

	tooHigh := EffectiveMonitorConfig(MonitorConfig{RefreshIntervalMS: 120000, TopProcessLimit: 10})
	assert.Equal(t, 60000, tooHigh.RefreshIntervalMS)
}

func TestMonitorConfig_ClampsUnsafeTopProcessLimit(t *testing.T) {
	tooLow := EffectiveMonitorConfig(MonitorConfig{RefreshIntervalMS: 5000, TopProcessLimit: -1})
	assert.Equal(t, 1, tooLow.TopProcessLimit)

	zero := ClampSubmittedMonitorConfig(MonitorConfig{RefreshIntervalMS: 5000, TopProcessLimit: 0})
	assert.Equal(t, 1, zero.TopProcessLimit)

	tooHigh := EffectiveMonitorConfig(MonitorConfig{RefreshIntervalMS: 5000, TopProcessLimit: 100})
	assert.Equal(t, 50, tooHigh.TopProcessLimit)
}
