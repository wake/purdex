// cmd/pdx/adopt_remote_test.go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// A remote adopt (cross-host team spec §4.3): after the approval the CLI waits on the membership.

const rtSession = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func remoteAdoptApproved() team.Approval {
	p, _ := json.Marshal(team.AdoptPayload{TeamID: "team-1", LeadSessionID: "sid-lead", TargetRef: "_rt1234", TargetSessionID: rtSession,
		TargetAddress: "air26/_rt1234", TargetHostID: "hostM", TargetHostAlias: "air26"})
	return team.Approval{ID: "11111111-1111-4111-8111-111111111111", Kind: team.KindAdopt, State: team.StateApproved, Payload: p}
}

// adoptionsDaemon answers the adoptions route from a script (the last answer repeats) on top of the fake lead daemon.
type adoptionsDaemon struct {
	*fakeTeamDaemon
	mu      sync.Mutex
	script  []team.Adoption
	asked   int
	queries []string
}

func (d *adoptionsDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, team.AdoptionsRoute) {
		d.fakeTeamDaemon.ServeHTTP(w, r)
		return
	}
	d.mu.Lock()
	ad := d.script[min(d.asked, len(d.script)-1)]
	d.asked++
	d.queries = append(d.queries, r.URL.RawQuery)
	d.mu.Unlock()
	ad.ApprovalID = strings.TrimPrefix(r.URL.Path, team.AdoptionsRoute)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ad)
}

func TestAdoptCmd_RemoteWaitsOnTheMembership(t *testing.T) {
	for _, c := range []struct {
		name       string
		script     []team.Adoption
		wantCode   int
		wantStdout bool
		wantLast   string
	}{
		{"active after joining", []team.Adoption{{State: team.AdoptionJoining}, {State: team.AdoptionActive}}, ExitOK, true, ""},
		{"failed carries its code last", []team.Adoption{{State: team.AdoptionJoining}, {State: team.AdoptionFailed, Code: "adopt_target_not_found"}}, ExitRefused, false, "adopt_target_not_found"},
		{"void is 14", []team.Adoption{{State: team.AdoptionVoid, Code: "remote_unreachable"}}, ExitMemberFailed, false, "remote_unreachable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := &adoptionsDaemon{fakeTeamDaemon: newFakeTeamDaemon(remoteAdoptApproved()), script: c.script}
			code, stdout, stderr := driveAdopt(t, context.Background(), d, "air26/_rt1234")
			if code != c.wantCode || (stdout != "") != c.wantStdout || (c.wantLast != "" && lastToken(stderr) != c.wantLast) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if c.wantStdout {
				var out adoptOutput
				if err := json.Unmarshal([]byte(stdout), &out); err != nil || out.SessionID != rtSession || out.Address != "air26/_rt1234" || out.Ref != "_rt1234" {
					t.Fatalf("output = %+v (%v)", out, err)
				}
			}
			if len(d.queries) == 0 || !strings.HasPrefix(d.queries[0], "wait=") {
				t.Fatalf("the adoptions route was not long-polled: %v", d.queries)
			}
		})
	}
}

// Its own bound runs out while the membership is still joining: 11.
func TestAdoptCmd_RemoteStillJoiningAtItsBoundExits11(t *testing.T) {
	d := &adoptionsDaemon{fakeTeamDaemon: newFakeTeamDaemon(remoteAdoptApproved()), script: []team.Adoption{{State: team.AdoptionJoining}}}
	code, _, stderr := driveAdopt(t, context.Background(), d, "air26/_rt1234", "--wait", "1s")
	if code != ExitTimeout {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

// A same-host adopt never touches the adoptions route.
func TestAdoptCmd_LocalAdoptDoesNotWaitOnMembership(t *testing.T) {
	d := &adoptionsDaemon{fakeTeamDaemon: newFakeTeamDaemon(adoptApproved()), script: []team.Adoption{{State: team.AdoptionFailed}}}
	if code, _, stderr := driveAdopt(t, context.Background(), d, "_def456"); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if d.asked != 0 {
		t.Fatal("a local adopt asked the adoptions route")
	}
}
