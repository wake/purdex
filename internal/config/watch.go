package config

import "strings"

// DefaultWakeSource is the host that holds the watcher's wake settings unless config.toml names another.
const DefaultWakeSource = "mlab"

// WatchConfig is the [watch] section of config.toml. Every key may be left out. It is a pointer in Config for the reason Journal is: a config
// without the section is written back without one.
type WatchConfig struct {
	// WakeSource is the alias of the host that is the source of the watcher's wake settings (the one document every host reads and any host
	// can write through). Empty / absent = DefaultWakeSource. Two hosts that disagree each go by their own file, and GET says which it used.
	WakeSource string `toml:"wake_source,omitempty" json:"wake_source,omitempty"`
}

// WakeSource is the alias of the source of the wake settings.
func (c Config) WakeSource() string {
	if c.Watch != nil {
		if s := strings.TrimSpace(c.Watch.WakeSource); s != "" {
			return s
		}
	}
	return DefaultWakeSource
}
