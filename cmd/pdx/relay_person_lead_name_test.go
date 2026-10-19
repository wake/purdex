package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Name pool v2 (spec §5.3): a /team the person typed can bind a lead name into its person token (`--lead-name <code>`) or ask for the
// card (`--card`). Both ride the token request, so the agent that later runs `pdx lead request --person` cannot drop them.

// namedPersonDaemon records the request a token was asked with.
type namedPersonDaemon struct {
	fakeRelayDaemon
	got  team.PersonRequest
	raw  string
	asks int
}

func (p *namedPersonDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == team.PersonRoute {
		body, _ := io.ReadAll(r.Body)
		p.raw = string(body)
		p.asks++
		p.got = team.PersonRequest{} // Unmarshal leaves a key that is absent as it was: start from nothing
		_ = json.Unmarshal([]byte(p.raw), &p.got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(team.PersonResponse{Token: "t-1", ExpiresAt: 99})
		return
	}
	p.fakeRelayDaemon.ServeHTTP(w, r)
}

// Mutation gate: the flag not copied into the request → the daemon sees no lead_name (red).
func TestRelayCmd_PersonLeadNameAndCardReachTheRequest(t *testing.T) {
	d := &namedPersonDaemon{}
	if code, _, stderr := driveRelay(t, context.Background(), d, "person", "--session", "sid-1", "--command", "lead", "--lead-name", "hiro"); code != ExitOK {
		t.Fatalf("lead-name: code=%d stderr=%q", code, stderr)
	}
	if d.got.LeadName != "hiro" || d.got.Card || d.got.Command != "lead" {
		t.Fatalf("sent %+v, want lead with lead_name hiro", d.got)
	}
	if code, _, stderr := driveRelay(t, context.Background(), d, "person", "--session", "sid-1", "--command", "lead", "--card"); code != ExitOK {
		t.Fatalf("card: code=%d stderr=%q", code, stderr)
	}
	if d.got.LeadName != "" || !d.got.Card {
		t.Fatalf("sent %+v, want a card request with no lead_name", d.got)
	}
}

// Neither flag leaves both keys out of the body, so a token for any other command is the request an older daemon already knows.
func TestRelayCmd_PersonWithoutTheFlagsSendsTheOldBody(t *testing.T) {
	d := &namedPersonDaemon{}
	if code, _, stderr := driveRelay(t, context.Background(), d, "person", "--session", "sid-1", "--command", "lead"); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if strings.Contains(d.raw, "lead_name") || strings.Contains(d.raw, "card") {
		t.Fatalf("body = %s, want neither key", d.raw)
	}
}

// The grammar: the flags belong to the lead command, exclude each other, and a name is a lower-case code. Nothing reaches the daemon.
func TestRelayCmd_PersonLeadNameGrammar(t *testing.T) {
	d := &namedPersonDaemon{}
	for _, args := range [][]string{
		{"person", "--session", "s", "--command", "relay", "--lead-name", "hiro"},
		{"person", "--session", "s", "--command", "spawn", "--card"},
		{"person", "--session", "s", "--command", "lead", "--lead-name", "hiro", "--card"},
		{"person", "--session", "s", "--command", "lead", "--lead-name", ""},
		{"person", "--session", "s", "--command", "lead", "--lead-name", "Hiro"},
		{"person", "--session", "s", "--command", "lead", "--lead-name", "hi ro"},
		{"person", "--session", "s", "--command", "lead", "--lead-name", "h"},
		{"person", "--session", "s", "--command", "lead", "--lead-name", "abcdefghijklmnopq"},
	} {
		if code, _, _ := driveRelay(t, context.Background(), d, args...); code != ExitUsage {
			t.Errorf("%v: code=%d, want usage", args, code)
		}
	}
	if d.asks != 0 {
		t.Fatalf("grammar errors reached the daemon %d times", d.asks)
	}
}
