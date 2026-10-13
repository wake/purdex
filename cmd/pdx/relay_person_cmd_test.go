package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// RT-1a T3: `pdx relay person` mints the token, `pdx relay begin --person` sends it, and a refused token is exit 13 with the
// code as the last stderr word.

// Mutation gate: the flag not copied into the request → the daemon sees no person_token (red).
func TestRelayCmd_BeginPersonFlagReachesTheRequest(t *testing.T) {
	pct := 10.0
	d := &fakeRelayDaemon{beginStatus: 201, beginBody: team.RelayBeginResponse{Op: team.RelayOp{ID: "op-1", State: team.RelayClaimed, UsedPercentage: &pct}, RequestID: "req-1"}}
	args := []string{"begin", "--self", "--session", "sid-1", "--used", "10", "--window", "200000", "--manual", "--person", "tok-abc"}
	if code, _, stderr := driveRelay(t, context.Background(), d, args...); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	_, bodies := d.snapshot()
	var sent team.RelayBeginRequest
	_ = json.Unmarshal([]byte(bodies[len(bodies)-1]), &sent)
	if sent.PersonToken != "tok-abc" || !sent.Manual {
		t.Fatalf("sent %+v, want manual with person_token tok-abc", sent)
	}
}

func TestRelayCmd_BeginPersonNeedsManual(t *testing.T) {
	args := []string{"begin", "--self", "--session", "sid-1", "--used", "10", "--window", "200000", "--person", "tok-abc"}
	if code, _, _ := driveRelay(t, context.Background(), &fakeRelayDaemon{}, args...); code != ExitUsage {
		t.Fatalf("code=%d, want usage %d", code, ExitUsage)
	}
}

func TestRelayCmd_BeginRefusedTokenIsExit13WithTheCodeLast(t *testing.T) {
	d := &fakeRelayDaemon{beginStatus: 409, beginBody: team.APIError{Error: team.ErrPersonTokenInvalid, Detail: "the person token is spent"}}
	args := []string{"begin", "--self", "--session", "sid-1", "--used", "10", "--window", "200000", "--manual", "--person", "tok"}
	code, _, stderr := driveRelay(t, context.Background(), d, args...)
	f := strings.Fields(stderr)
	if code != ExitRefused || len(f) == 0 || f[len(f)-1] != team.ErrPersonTokenInvalid {
		t.Fatalf("code=%d stderr=%q, want exit 13 ending %s", code, stderr, team.ErrPersonTokenInvalid)
	}
}

type personDaemon struct {
	fakeRelayDaemon
	status int
	got    string
}

func (p *personDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == team.PersonRoute {
		var req team.PersonRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		p.got = req.SessionID + "/" + req.Command
		w.Header().Set("Content-Type", "application/json")
		if p.status == 0 {
			p.status = 200
		}
		w.WriteHeader(p.status)
		if p.status == 200 {
			_ = json.NewEncoder(w).Encode(team.PersonResponse{Token: "t-1", ExpiresAt: 99})
		} else {
			_ = json.NewEncoder(w).Encode(team.APIError{Error: team.ErrUnknownSession, Detail: "no"})
		}
		return
	}
	p.fakeRelayDaemon.ServeHTTP(w, r)
}

func TestRelayCmd_PersonPrintsTheToken(t *testing.T) {
	d := &personDaemon{}
	code, stdout, stderr := driveRelay(t, context.Background(), d, "person", "--session", "sid-1", "--command", "lead")
	if code != ExitOK || d.got != "sid-1/lead" {
		t.Fatalf("code=%d got=%q stderr=%q", code, d.got, stderr)
	}
	var out team.PersonResponse
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || out.Token != "t-1" {
		t.Fatalf("stdout=%q err=%v", stdout, err)
	}
}

func TestRelayCmd_PersonGrammarAndErrors(t *testing.T) {
	for _, args := range [][]string{{"person"}, {"person", "--session", "s"}, {"person", "--session", "s", "--command", "dance"}, {"person", "--command", "relay"}} {
		if code, _, _ := driveRelay(t, context.Background(), &personDaemon{}, args...); code != ExitUsage {
			t.Fatalf("%v: code=%d, want usage", args, code)
		}
	}
	// a daemon without the route answers 404 → unsupported (the mod then tells the person to retry)
	if code, _, _ := driveRelay(t, context.Background(), &fakeRelayDaemon{}, "person", "--session", "s", "--command", "relay"); code != ExitUnsupported && code != ExitError {
		t.Fatalf("a daemon without the route: code=%d", code)
	}
}
