package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3843: `wwd help` used to start the daemon (read the config, open wwd.db, record a start, fail to bind and exit). Anything that is not
// the daemon's own flags is a request for the usage, or a mistake, and is answered before wwd.db is opened.

func TestParseCLI(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		action   cliAction
		code     int
		out, err string // substrings
	}{
		{"no arguments are the daemon", nil, cliRun, 0, "", ""},
		{"--config is the daemon's", []string{"--config", "/x/c.toml"}, cliRun, 0, "", ""},
		{"-config=… too", []string{"-config=/x/c.toml"}, cliRun, 0, "", ""},
		{"--version", []string{"--version"}, cliVersion, 0, "", ""},
		{"help", []string{"help"}, cliExit, 0, "usage: wwd", ""},
		{"-h", []string{"-h"}, cliExit, 0, "usage: wwd", ""},
		{"--help", []string{"--help"}, cliExit, 0, "usage: wwd", ""},
		{"-help", []string{"-help"}, cliExit, 0, "usage: wwd", ""},
		{"an unknown subcommand", []string{"frobnicate"}, cliExit, 2, "", "unknown command \"frobnicate\""},
		{"an unknown subcommand after the flags", []string{"--config", "/x", "status"}, cliExit, 2, "", "unknown command \"status\""},
		{"an unknown flag", []string{"--bogus"}, cliExit, 2, "", "usage: wwd"},
		{"help with something after it", []string{"help", "install"}, cliExit, 0, "usage: wwd", ""},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		cfg, action, code := parseCLI(c.args, &out, &errb)
		if action != c.action || code != c.code {
			t.Errorf("%s: action %v code %d, want %v %d (out %q err %q)", c.name, action, code, c.action, c.code, out.String(), errb.String())
		}
		if !strings.Contains(out.String(), c.out) || !strings.Contains(errb.String(), c.err) {
			t.Errorf("%s: out %q err %q", c.name, out.String(), errb.String())
		}
		if c.action == cliExit && c.code != 0 && out.Len() != 0 {
			t.Errorf("%s: a mistake is said on stderr, not stdout: %q", c.name, out.String())
		}
		if c.name == "--config is the daemon's" && cfg != "/x/c.toml" {
			t.Errorf("config %q", cfg)
		}
		if c.name == "-config=… too" && cfg != "/x/c.toml" {
			t.Errorf("config %q", cfg)
		}
	}
}

// The usage names every subcommand that is there, so a new one is not left out of it: each name is handled by the code that handles
// subcommands (-h makes it return before it does anything), and the usage lists it.
func TestTheUsageNamesEverySubcommand(t *testing.T) {
	var out, errb bytes.Buffer
	parseCLI([]string{"help"}, &out, &errb)
	for _, name := range subcommands {
		if !strings.Contains(out.String(), "wwd "+name) {
			t.Errorf("the usage does not name %q:\n%s", name, out.String())
		}
		handled := false
		if h, _ := runInstallCmd([]string{name, "-h"}); h {
			handled = true
		} else if h, _ := runAdvisorCmd([]string{name, "-h"}); h {
			handled = true
		}
		if !handled {
			t.Errorf("%q is in the usage but no handler takes it", name)
		}
	}
	for _, flagName := range []string{"--config", "--version"} {
		if !strings.Contains(out.String(), flagName) {
			t.Errorf("the usage does not name %s", flagName)
		}
	}
}

// Nothing here touches the data directory: parseCLI is a function of the arguments alone. The test points the home somewhere that is
// not there and sees that nothing was made.
func TestTheUsageMakesNothingOnDisk(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	var out, errb bytes.Buffer
	parseCLI([]string{"help"}, &out, &errb)
	parseCLI([]string{"nonsense"}, &out, &errb)
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("%v: something was made", err)
	}
}
