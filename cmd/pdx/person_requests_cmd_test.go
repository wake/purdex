package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// RT-1b: --person on lead / adopt / spawn, and the unattended refusal as the requester sees it (exit 13, the text, the code last).

// Mutation gate: the flag not copied into the request → the daemon sees no person_token (red).
func TestLeadCmd_PersonFlagReachesTheRequest(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{Kind: team.KindLead, State: team.StateApproved})
	if code, _, stderr := driveLead(t, context.Background(), d, "--reason", "r", "--person", "tok-1"); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	creates, _, _, _ := d.snapshot()
	if len(creates) != 1 || creates[0].PersonToken != "tok-1" {
		t.Fatalf("creates = %+v, want person_token tok-1", creates)
	}
}

func TestAdoptCmd_PersonFlagReachesTheRequest(t *testing.T) {
	d := newFakeTeamDaemon(adoptApproved())
	if code, _, stderr := driveAdopt(t, context.Background(), d, "_def456", "--person", "tok-2"); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	creates, _, _, _ := d.snapshot()
	if len(creates) != 1 || creates[0].PersonToken != "tok-2" {
		t.Fatalf("creates = %+v, want person_token tok-2", creates)
	}
}

// The create answered 409 unattended_refused / person_token_invalid: exit 13, the code the last word on stderr, and for the
// unattended one the text. Mutation gate: leave the code out of the refusal table → exit 1 (red).
func TestLeadAndAdoptCmd_RefusalsExit13CodeLast(t *testing.T) {
	for _, code := range []string{team.ErrUnattendedRefused, team.ErrPersonTokenInvalid} {
		for name, drive := range map[string]func(*fakeTeamDaemon) (int, string, string){
			"lead": func(d *fakeTeamDaemon) (int, string, string) {
				return driveLead(t, context.Background(), d, "--reason", "r")
			},
			"adopt": func(d *fakeTeamDaemon) (int, string, string) {
				return driveAdopt(t, context.Background(), d, "_def456")
			},
		} {
			d := newFakeTeamDaemon(team.Approval{})
			d.createStatus, d.refuseCode = http.StatusConflict, code
			got, stdout, stderr := drive(d)
			if got != ExitRefused || stdout != "" || lastToken(stderr) != code {
				t.Errorf("%s %s: code=%d stdout=%q stderr=%q", name, code, got, stdout, stderr)
			}
		}
	}
}

// A request the switch-on refused while it waited is closed cancelled / unattended_refused: the requester gets the text and
// exit 13 (was 12 for a lead). Mutation gate: map it to cancelled (12) → red.
func TestLeadAndAdoptCmd_ClosedByTheSwitchOnIsExit13WithTheText(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{Kind: team.KindLead, State: team.StateCancelled, CloseReason: team.ErrUnattendedRefused})
	code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r")
	if code != ExitRefused || stdout != "" || lastToken(stderr) != team.ErrUnattendedRefused || !strings.Contains(stderr, team.UnattendedRefusedText) {
		t.Errorf("lead: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	d = newFakeTeamDaemon(team.Approval{Kind: team.KindAdopt, State: team.StateCancelled, CloseReason: team.ErrUnattendedRefused})
	code, stdout, stderr = driveAdopt(t, context.Background(), d, "_def456")
	if code != ExitRefused || stdout != "" || lastToken(stderr) != team.ErrUnattendedRefused || !strings.Contains(stderr, team.UnattendedRefusedText) {
		t.Errorf("adopt: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
