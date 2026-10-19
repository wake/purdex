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
	caps       []string // what GET /api/info announces
	infoStatus int      // non-zero: GET /api/info answers that status
	infos      int      // GET /api/info requests
	fakeRelayDaemon
	got  team.PersonRequest
	raw  string
	asks int
}

func (p *namedPersonDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/info" {
		p.infos++
		if p.infoStatus != 0 {
			w.WriteHeader(p.infoStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": p.caps})
		return
	}
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
	d := &namedPersonDaemon{caps: []string{team.CapabilityLeadName}}
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

// Neither flag leaves both keys out of the body, so a token for any other command is the request an older daemon already knows. And it
// asks the daemon nothing else: a daemon without the capability still mints it, the /team of before names. Mutation gate: always
// check the capability → red.
func TestRelayCmd_PersonWithoutTheFlagsSendsTheOldBodyToAnyDaemon(t *testing.T) {
	d := &namedPersonDaemon{} // announces nothing
	if code, _, stderr := driveRelay(t, context.Background(), d, "person", "--session", "sid-1", "--command", "lead"); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if strings.Contains(d.raw, "lead_name") || strings.Contains(d.raw, "card") {
		t.Fatalf("body = %s, want neither key", d.raw)
	}
	if d.infos != 0 {
		t.Fatalf("GET /api/info was asked %d times for a token without the flags", d.infos)
	}
}

// A daemon that did not announce team.lead_name.v1 would decode the two fields and drop them, minting a plain token: a request the
// person meant to wait on the card would be granted at once. So with either flag the CLI checks the capability first and refuses,
// minting nothing; an unreadable /api/info refuses too (fail closed). Mutation gate: skip the check → the token is minted (red).
func TestRelayCmd_PersonFlagsFailClosedWithoutTheCapability(t *testing.T) {
	for name, d := range map[string]*namedPersonDaemon{
		"capability missing": {caps: []string{"team.person.v1"}},
		"no capabilities":    {},
		"info unreadable":    {infoStatus: http.StatusInternalServerError},
	} {
		for _, flag := range [][]string{{"--card"}, {"--lead-name", "hiro"}} {
			args := append([]string{"person", "--session", "sid-1", "--command", "lead"}, flag...)
			code, stdout, stderr := driveRelay(t, context.Background(), d, args...)
			if code == ExitOK || strings.TrimSpace(stdout) != "" {
				t.Errorf("%s %v: code=%d stdout=%q, want a refusal with no token", name, flag, code, stdout)
			}
			if !strings.Contains(stderr, team.CapabilityLeadName) && name != "info unreadable" {
				t.Errorf("%s %v: stderr %q does not name the capability", name, flag, stderr)
			}
		}
		if d.asks != 0 {
			t.Errorf("%s: %d token requests reached the daemon", name, d.asks)
		}
	}
}

// The grammar: the flags belong to the lead command, exclude each other, and a name is a lower-case code. Nothing reaches the daemon.
func TestRelayCmd_PersonLeadNameGrammar(t *testing.T) {
	d := &namedPersonDaemon{caps: []string{team.CapabilityLeadName}}
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
