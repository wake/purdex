package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// leadFlagPath is where `pdx lead request` puts the hard-lock flag for the
// fake daemon's origin session (spec §6.6: <data_dir>/hooklocks/cc/<sid>).
func leadFlagPath(dataDir string) string {
	return filepath.Join(dataDir, team.HookLocksDir, team.HookAgentCC, fakeLeadSessionID)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// The flag is up while the request is open (seen by the daemon at the
// first poll, i.e. after the 201) and gone once the request is approved.
func TestRunLeadCmd_FlagUpWhileOpenGoneAfterApproval(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 3, Roots: []string{"/w"}}})
	dataDir := t.TempDir()
	var seenAtPoll atomic.Bool
	d.onPoll = func(n int) {
		if n == 1 {
			seenAtPoll.Store(fileExists(leadFlagPath(dataDir)))
		}
	}
	code, _, stderr, _ := driveLeadDir(t, context.Background(), d, nil, nil, dataDir, "--reason", "r")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if !seenAtPoll.Load() {
		t.Fatal("the flag must exist while the request is open (at the first poll, after the 201)")
	}
	if fileExists(leadFlagPath(dataDir)) {
		t.Fatal("the flag must be removed once the request closed")
	}
	if fi, err := os.Stat(filepath.Dir(leadFlagPath(dataDir))); err != nil || !fi.IsDir() {
		t.Fatalf("the hooklocks cc directory must have been created (%v)", err)
	}
	if strings.Contains(stderr, "硬鎖") {
		t.Fatalf("no warning expected: %q", stderr)
	}
}

// Every other exit path removes it too: denial, timeout, the signal path
// (leadCancel) and the daemon-unavailable path out of the poll loop.
func TestRunLeadCmd_FlagRemovedOnEveryExitPath(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		d := newFakeTeamDaemon(team.Approval{State: team.StateDenied})
		code, _, _, dataDir := driveLeadDir(t, context.Background(), d, nil, nil, t.TempDir(), "--reason", "r")
		if code != ExitDenied || fileExists(leadFlagPath(dataDir)) {
			t.Fatalf("code=%d flag=%v", code, fileExists(leadFlagPath(dataDir)))
		}
	})
	t.Run("timeout", func(t *testing.T) {
		d := newFakeTeamDaemon(team.Approval{State: team.StateTimeout})
		code, _, _, dataDir := driveLeadDir(t, context.Background(), d, nil, nil, t.TempDir(), "--reason", "r")
		if code != ExitTimeout || fileExists(leadFlagPath(dataDir)) {
			t.Fatalf("code=%d flag=%v", code, fileExists(leadFlagPath(dataDir)))
		}
	})
	t.Run("signal", func(t *testing.T) {
		d := newFakeTeamDaemon(team.Approval{})
		d.hold = true
		dataDir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var upWhileHeld atomic.Bool
		go func() {
			<-d.pollStarted
			upWhileHeld.Store(fileExists(leadFlagPath(dataDir)))
			cancel()
		}()
		code, _, _, _ := driveLeadDir(t, ctx, d, nil, nil, dataDir, "--reason", "r")
		if code != ExitCancelled {
			t.Fatalf("code=%d", code)
		}
		if !upWhileHeld.Load() {
			t.Fatal("the flag must be up while the poll is held")
		}
		if fileExists(leadFlagPath(dataDir)) {
			t.Fatal("the signal path must remove the flag")
		}
	})
	t.Run("hung polls exit 20", func(t *testing.T) {
		clock := newLeadClock()
		d := newFakeTeamDaemon(team.Approval{})
		d.hold = true
		d.onPoll = func(int) { clock.fireNext() } // every poll runs out its attempt timeout
		code, _, _, dataDir := driveLeadDir(t, context.Background(), d, []daemonclient.Option{clock.opt()}, nil, t.TempDir(), "--reason", "r")
		if code != ExitUnavailable || fileExists(leadFlagPath(dataDir)) {
			t.Fatalf("code=%d flag=%v", code, fileExists(leadFlagPath(dataDir)))
		}
	})
}

// A config without data_dir cannot place the flag: one stderr line, the
// request still runs (soft lock only), nothing is written anywhere.
func TestRunLeadCmd_NoDataDirWarnsAndRunsWithoutFlag(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved})
	code, _, stderr, _ := driveLeadDir(t, context.Background(), d, nil, nil, "", "--reason", "r")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "pdx lead: 無法建立硬鎖旗標（data_dir 或 session id 為空），這次只有軟鎖") {
		t.Fatalf("stderr = %q", stderr)
	}
	if fileExists(filepath.Join(team.HookLocksDir, team.HookAgentCC, fakeLeadSessionID)) {
		t.Fatal("nothing may be written relative to the cwd")
	}
}

// A create answered without origin.session_id (an older daemon) is the
// same: warn, no flag.
func TestRunLeadCmd_NoOriginSessionIDWarns(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved})
	d.noOrigin = true
	code, _, stderr, dataDir := driveLeadDir(t, context.Background(), d, nil, nil, t.TempDir(), "--reason", "r")
	if code != ExitOK || !strings.Contains(stderr, "這次只有軟鎖") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if entries, _ := os.ReadDir(dataDir); len(entries) != 0 {
		t.Fatalf("data dir must stay empty: %v", entries)
	}
}
