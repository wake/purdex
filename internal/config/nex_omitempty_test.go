package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
)

// writeAndRead writes cfg with WriteFile and returns the file's text and what Load reads back from it.
func writeAndRead(t *testing.T, cfg config.Config) (string, config.Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), got
}

// #1057: saving the Nex form must not add keys the user never set. The fields whose default is the zero value (claude_bin,
// timeouts.*, peer.max_pending / wake_template / reply_line) are left out of the file when empty; an absent key reads back
// as the zero value, so the section means the same. Mutation gate: drop omitempty from them → red.
func TestWriteFile_LeavesOutEmptyNexFieldsWhoseDefaultIsZero(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "absent.toml")) // the defaults
	if err != nil {
		t.Fatal(err)
	}
	text, got := writeAndRead(t, cfg)
	for _, absent := range []string{"claude_bin", "[nex.timeouts]", "lease_ttl", "interrupt", "wake_template", "reply_line", "max_pending"} {
		if strings.Contains(text, absent) {
			t.Errorf("an unset %q was written:\n%s", absent, text)
		}
	}
	if !got.Nex.Equal(cfg.Nex) {
		t.Errorf("Nex after a round trip = %+v, want %+v", got.Nex, cfg.Nex)
	}
}

// Values that are set are still written and read back.
func TestWriteFile_KeepsNexFieldsThatAreSet(t *testing.T) {
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "absent.toml"))
	cfg.Nex.ClaudeBin = "/opt/claude"
	cfg.Nex.Timeouts = config.NexTimeoutsConfig{LeaseTTL: "5m", Interrupt: "10s", Turn: "30m"}
	cfg.Nex.Peer.MaxPending = 8
	cfg.Nex.Peer.WakeTemplate = "from {{.FromName}} ({{.FromMode}}, {{.MsgID}}):\n{{.Text}}\n{{.ReplyLine}}"
	cfg.Nex.Peer.ReplyLine = "reply: pdx msg send {{.ReplyTo}} \"...\""
	text, got := writeAndRead(t, cfg)
	for _, present := range []string{`claude_bin = "/opt/claude"`, "[nex.timeouts]", `lease_ttl = "5m"`, "max_pending = 8"} {
		if !strings.Contains(text, present) {
			t.Errorf("%q missing:\n%s", present, text)
		}
	}
	if !got.Nex.Equal(cfg.Nex) {
		t.Errorf("Nex after a round trip = %+v, want %+v", got.Nex, cfg.Nex)
	}
}

// The fields whose default is NOT zero must keep being written, or "off"/"empty" would read back as the default:
// peer.enabled (default true), sandbox profiles (default "trusted"), path_prepend (default list), enabled. Mutation gate:
// add omitempty to any of them → red.
func TestWriteFile_NexFieldsWithANonZeroDefaultRoundTripTheirZeroValue(t *testing.T) {
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "absent.toml"))
	cfg.Nex.Enabled = false
	cfg.Nex.Peer.Enabled = false
	cfg.Nex.Sandbox = config.NexSandboxConfig{}
	cfg.Nex.PathPrepend = []string{}
	_, got := writeAndRead(t, cfg)
	if got.Nex.Peer.Enabled {
		t.Error("peer.enabled = false read back as true (the default)")
	}
	if got.Nex.Sandbox.MaxProfile != "" || got.Nex.Sandbox.DefaultProfile != "" {
		t.Errorf("empty sandbox profiles read back as %+v (the default is trusted)", got.Nex.Sandbox)
	}
	if len(got.Nex.PathPrepend) != 0 {
		t.Errorf("empty path_prepend read back as %v (the default list)", got.Nex.PathPrepend)
	}
}
