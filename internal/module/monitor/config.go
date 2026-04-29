package monitor

import "github.com/wake/purdex/internal/config"

const (
	DefaultRefreshIntervalMS = 5000
	MinRefreshIntervalMS     = 1000
	MaxRefreshIntervalMS     = 60000

	DefaultTopProcessLimit = 10
	MinTopProcessLimit     = 1
	MaxTopProcessLimit     = 50
)

type MonitorConfig = config.MonitorConfig

type Bound struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type ConfigBounds struct {
	RefreshIntervalMS Bound `json:"refresh_interval_ms"`
	TopProcessLimit   Bound `json:"top_process_limit"`
}

type EffectiveConfig struct {
	RefreshIntervalMS int          `json:"refresh_interval_ms"`
	TopProcessLimit   int          `json:"top_process_limit"`
	Bounds            ConfigBounds `json:"bounds"`
}

func DefaultMonitorConfig() MonitorConfig {
	return MonitorConfig{
		RefreshIntervalMS: DefaultRefreshIntervalMS,
		TopProcessLimit:   DefaultTopProcessLimit,
	}
}

func EffectiveMonitorConfig(cfg MonitorConfig) EffectiveConfig {
	refreshInterval := cfg.RefreshIntervalMS
	if refreshInterval == 0 {
		refreshInterval = DefaultRefreshIntervalMS
	}

	topProcessLimit := cfg.TopProcessLimit
	if topProcessLimit == 0 {
		topProcessLimit = DefaultTopProcessLimit
	}

	return EffectiveConfig{
		RefreshIntervalMS: clamp(refreshInterval, MinRefreshIntervalMS, MaxRefreshIntervalMS),
		TopProcessLimit:   clamp(topProcessLimit, MinTopProcessLimit, MaxTopProcessLimit),
		Bounds:            monitorConfigBounds(),
	}
}

func ClampSubmittedMonitorConfig(cfg MonitorConfig) EffectiveConfig {
	return EffectiveConfig{
		RefreshIntervalMS: clamp(cfg.RefreshIntervalMS, MinRefreshIntervalMS, MaxRefreshIntervalMS),
		TopProcessLimit:   clamp(cfg.TopProcessLimit, MinTopProcessLimit, MaxTopProcessLimit),
		Bounds:            monitorConfigBounds(),
	}
}

func monitorConfigBounds() ConfigBounds {
	return ConfigBounds{
		RefreshIntervalMS: Bound{Min: MinRefreshIntervalMS, Max: MaxRefreshIntervalMS},
		TopProcessLimit:   Bound{Min: MinTopProcessLimit, Max: MaxTopProcessLimit},
	}
}

func (cfg EffectiveConfig) persistedConfig() MonitorConfig {
	return MonitorConfig{
		RefreshIntervalMS: cfg.RefreshIntervalMS,
		TopProcessLimit:   cfg.TopProcessLimit,
	}
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
