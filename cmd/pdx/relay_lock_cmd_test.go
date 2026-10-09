package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func driveRelayLock(t *testing.T, dataDir string, args ...string) (int, string) {
	t.Helper()
	cfg := writeTestConfigDataDir(t, "127.0.0.1:1", "tok", dataDir)
	var stdout, stderr bytes.Buffer
	code := runRelayCmd(context.Background(), append(args, "--config", cfg), &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

// lock raises the flag with the op id as content (no daemon: the port is dead); unlock is a compare-and-remove by op
// id and is exit 0 whatever it found. Mutation gate: an unlock that removes blindly → another op's flag is lowered (red).
func TestRelayLockCmd_LockUnlockCompareAndRemove(t *testing.T) {
	dataDir := t.TempDir()
	flag := team.HookLockPath(dataDir, team.HookAgentCC, "sid-1")
	if code, out := driveRelayLock(t, dataDir, "lock", "op-a", "--session", "sid-1"); code != ExitOK {
		t.Fatalf("lock: %d %s", code, out)
	}
	if got, _ := os.ReadFile(flag); string(got) != "op-a" {
		t.Fatalf("flag content = %q", got)
	}
	if code, _ := driveRelayLock(t, dataDir, "lock", "op-b", "--session", "sid-1"); code != ExitOK { // raised again, over A's
		t.Fatal("second lock")
	}
	if code, _ := driveRelayLock(t, dataDir, "unlock", "op-a", "--session", "sid-1"); code != ExitOK || !team.HookLockExists(flag) {
		t.Fatalf("A's unlock must leave B's flag (exit %d, exists %v)", code, team.HookLockExists(flag))
	}
	if code, _ := driveRelayLock(t, dataDir, "unlock", "op-b", "--session", "sid-1"); code != ExitOK || team.HookLockExists(flag) {
		t.Fatalf("B's unlock must lower its own flag (exit %d)", code)
	}
	if code, _ := driveRelayLock(t, dataDir, "unlock", "op-b", "--session", "sid-1"); code != ExitOK { // already gone
		t.Fatal("unlock of nothing must be exit 0")
	}
	// a lead request's flag (another id) is never lowered by an unlock
	os.MkdirAll(filepath.Dir(flag), 0o700)
	os.WriteFile(flag, []byte("11111111-2222-4333-8444-555555555555"), 0o600)
	driveRelayLock(t, dataDir, "unlock", "op-a", "--session", "sid-1")
	if !team.HookLockExists(flag) {
		t.Fatal("an unlock lowered a lead request's flag")
	}
}

func TestRelayLockCmd_UsageAndBadOpID(t *testing.T) {
	dataDir := t.TempDir()
	for _, args := range [][]string{
		{"lock"}, {"lock", "op-a"}, {"lock", "op-a", "--session", ""}, {"unlock", "op-a", "extra", "--session", "s"},
		{"lock", "../x", "--session", "s"}, {"lock", "a/b", "--session", "s"}, {"lock", "", "--session", "s"},
		{"lock", "op-a", "--session", "../../x"}, {"lock", "op-a", "--session", "a/b"},
	} {
		if code, out := driveRelayLock(t, dataDir, args...); code != ExitUsage {
			t.Errorf("%v: exit %d (%s), want 2", args, code, out)
		}
	}
	if entries, _ := os.ReadDir(dataDir); len(entries) != 0 {
		t.Fatalf("a refused lock wrote %v", entries)
	}
	// an I/O error is exit 1: the flag's directory cannot be made under a file
	blocker := filepath.Join(dataDir, team.HookLocksDir)
	os.WriteFile(blocker, []byte("x"), 0o600)
	if code, out := driveRelayLock(t, dataDir, "lock", "op-a", "--session", "s"); code != ExitError || !strings.Contains(out, "cannot raise") {
		t.Fatalf("I/O error: %d %s", code, out)
	}
}
