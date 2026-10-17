package main

import (
	"strings"
	"testing"
)

func TestParseInstallArgs(t *testing.T) {
	o, err := parseInstallArgs([]string{"--alias", "air26", "--token-file", "/x/t", "--no-restart", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if o.alias != "air26" || o.tokenFile != "/x/t" || !o.noRestart || !o.dryRun {
		t.Fatalf("%+v", o)
	}
	// the host must be named: nothing guesses which of the four this is
	if _, err := parseInstallArgs(nil); err == nil || !strings.Contains(err.Error(), "--alias") {
		t.Errorf("a missing alias must be refused, got %v", err)
	}
	if _, err := parseInstallArgs([]string{"--alias", "nowhere"}); err == nil {
		t.Error("an unknown alias must be refused up front")
	}
	if _, err := parseInstallArgs([]string{"--alias", "mlab", "--bogus"}); err == nil {
		t.Error("an unknown flag must be refused")
	}
}

func TestParseUninstallArgs(t *testing.T) {
	purge, err := parseUninstallArgs([]string{"--purge"})
	if err != nil || !purge {
		t.Fatalf("%v %v", purge, err)
	}
	if purge, err := parseUninstallArgs(nil); err != nil || purge {
		t.Fatalf("%v %v", purge, err)
	}
	if _, err := parseUninstallArgs([]string{"--wat"}); err == nil {
		t.Error("an unknown flag must be refused")
	}
}
