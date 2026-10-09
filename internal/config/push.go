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
}

// PushAPNsDir is the configured APNs directory, "" when push is off. Config.Push is a pointer so that a config without
// the section is written back without one (the TOML encoder emits an empty table for a struct value).
func (c Config) PushAPNsDir() string {
	if c.Push == nil {
		return ""
	}
	return c.Push.APNsDir
}
