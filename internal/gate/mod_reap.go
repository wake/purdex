package gate

import "time"

// Module 6, reap: what is left of a command's tree is cleared before its slot is given back (SIGTERM, a grace period,
// SIGKILL), so vitest's workers or a dev server never run on outside a slot.
type ReapConfig struct {
	Enabled bool
	Grace   time.Duration
}

func parseReap(r raw) ReapConfig {
	return ReapConfig{
		Enabled: r.Bool("enabled", true),
		Grace:   time.Duration(r.Int("grace_s", 5, 0, 600)) * time.Second,
	}
}
