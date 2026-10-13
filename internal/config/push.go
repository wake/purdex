package config

// PushConfig is the [push] section: phone push notifications through APNs (spec docs/specs/2026-10-09-push-spec.md §3).
// The zero value is "off": the push module is not mounted, `push.v1` is not announced and its routes do not exist.
//
// Boot-only: it is read at daemon start and is deliberately not a field of PUT /api/config, so "configured" and
// "mounted" are the same thing for the life of the process. Changing it means editing config.toml and restarting.
type PushConfig struct {
	// APNsDir is the directory holding config.env (APNS_KEY_ID, APNS_TEAM_ID, APNS_KEY_FILE) and the .p8 key.
	// A leading "~" is expanded by the module at Init, against the daemon user's home.
	APNsDir string `toml:"apns_dir" json:"apns_dir"`
	// ReadFlushMinutes (read sync, badge spec §7.2): how many minutes a phone with pending session reads may go without any
	// push before one is sent for them alone. 0 / absent = DefaultReadFlushMinutes; 1–120 otherwise.
	ReadFlushMinutes int `toml:"read_flush_minutes,omitzero" json:"read_flush_minutes,omitempty"`
}

// DefaultReadFlushMinutes is the read flush interval when [push] read_flush_minutes is not set.
const DefaultReadFlushMinutes = 15

// PushReadFlushMinutes is the configured read flush interval in minutes. valid is false when the value is outside 1–120: the
// default is returned then, and the caller should say so.
func (c Config) PushReadFlushMinutes() (minutes int, valid bool) {
	if c.Push == nil || c.Push.ReadFlushMinutes == 0 {
		return DefaultReadFlushMinutes, true
	}
	if n := c.Push.ReadFlushMinutes; n >= 1 && n <= 120 {
		return n, true
	}
	return DefaultReadFlushMinutes, false
}

// PushAPNsDir is the configured APNs directory, "" when push is off. Config.Push is a pointer so that a config without
// the section is written back without one (the TOML encoder emits an empty table for a struct value).
func (c Config) PushAPNsDir() string {
	if c.Push == nil {
		return ""
	}
	return c.Push.APNsDir
}
