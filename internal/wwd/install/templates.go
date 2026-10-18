package install

import (
	"fmt"
	"strings"
)

// Label is the launchd label of wwd's LaunchAgent.
const Label = "gs.wake.wwd"

// UnitName is the systemd unit of wwd.
const UnitName = "wwd.service"

// PlistOpts are the paths a LaunchAgent needs.
type PlistOpts struct {
	Label, Binary, Config, Log, Path string
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Plist is the LaunchAgent: started at load and kept alive, logging to Log, with
// a PATH that finds the toolchain (a LaunchAgent does not get the shell's).
func Plist(o PlistOpts) string {
	e := xmlEscape
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>-config</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>5</integer>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, e(o.Label), e(o.Binary), e(o.Config), e(o.Path), e(o.Log), e(o.Log))
}

// UnitOpts are the values a systemd unit needs.
type UnitOpts struct {
	User, Binary, Config, Path string
}

// UnitSlice is the slice wwd itself runs in. The jobs it runs belong to wwd.slice
// (spec §5) and are limited there; the two are kept apart, and the name has no
// hyphen on purpose: systemd would take wwd-xxx.slice for a child of wwd.slice and
// put wwd's own process in with the jobs.
const UnitSlice = "wwdd.slice"

// Unit is the system service. Without a Slice= it would land in system.slice with
// the production services (livekit, mysql, ...); it runs in UnitSlice instead.
// The limits here only keep a misbehaving wwd from taking those services down
// with it: at most one core, 512 MB and 256 tasks.
//
// No NoNewPrivileges: the worker starts jobs through `sudo systemd-run` (spec §5),
// and sudo refuses to run when no_new_privs is set. The privilege boundary is
// sudoers (§11), not this flag; TestSystemdUnit keeps it out.
func Unit(o UnitOpts) string {
	return fmt.Sprintf(`[Unit]
Description=wwd - the pool's watcher/worker daemon
After=network-online.target tailscaled.service
Wants=network-online.target

[Service]
Type=simple
User=%s
ExecStart=%s -config %s
Environment=PATH=%s
Restart=always
RestartSec=3
Slice=%s
CPUQuota=100%%
MemoryMax=512M
TasksMax=256
ProtectSystem=full

[Install]
WantedBy=multi-user.target
`, o.User, o.Binary, o.Config, o.Path, UnitSlice)
}
