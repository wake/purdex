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

// Unit is the system service. wwd itself is small and sits in the default slice;
// the jobs it will run belong to wwd.slice (spec §5) and are limited there. The
// limits here only keep a misbehaving wwd from taking the production services
// down with it.
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
MemoryMax=512M
TasksMax=256
NoNewPrivileges=yes
ProtectSystem=full

[Install]
WantedBy=multi-user.target
`, o.User, o.Binary, o.Config, o.Path)
}
