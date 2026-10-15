package gate

import "time"

// Module 5, lifetime: how long a slot is held. A slot belongs to its command for as long as the command's process tree
// lives; there is no expiry that releases a live job (the lease's max_hold did, and left the job running unmanaged).
// A job that runs long is reported, never killed.
type LifetimeConfig struct {
	Hold       string        // "tree": until the whole tree has ended (the only mode)
	AlertAfter time.Duration // a stderr line (once) when a command has held its slot this long; 0 = never
}

func parseLifetime(r raw) LifetimeConfig {
	return LifetimeConfig{
		Hold:       r.Enum("hold", "tree", "tree"),
		AlertAfter: time.Duration(r.Int("alert_after_min", 60, 0, 24*60)) * time.Minute,
	}
}
