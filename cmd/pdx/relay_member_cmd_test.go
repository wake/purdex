package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// fakeMemberRelayDaemon serves POST /api/team/relays, the op long-poll and claim/seen.
type fakeMemberRelayDaemon struct {
	mu       sync.Mutex
	creates  []team.RelayCreateRequest
	create   func(n int) (int, any) // status and body of the nth create (default 201 requested)
	polls    []team.RelayOp         // answers of GET /api/relay/ops/{id}, last repeated
	pollN    int
	verbs    []string // "<verb> <body>"
	verbResp func(verb string) (int, any)
	onPoll   func() // called at each long-poll (tests cancel the caller there)
}

func (d *fakeMemberRelayDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "boot_id": "b1"})
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/team/relays":
		var req team.RelayCreateRequest
		json.NewDecoder(r.Body).Decode(&req)
		d.creates = append(d.creates, req)
		status, body := http.StatusCreated, any(team.RelayCreateResponse{Op: team.RelayOp{ID: req.ID, Kind: team.RelayKindMember, State: team.RelayRequested, Ref: "_mem001"}})
		if d.create != nil {
			status, body = d.create(len(d.creates))
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/relay/ops/"):
		i := d.pollN
		if i >= len(d.polls) {
			i = len(d.polls) - 1
		}
		d.pollN++
		if d.onPoll != nil {
			d.onPoll()
		}
		json.NewEncoder(w).Encode(d.polls[i])
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/relay/ops/"):
		verb := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		raw, _ := json.Marshal(b)
		d.verbs = append(d.verbs, verb+" "+string(raw))
		status, body := http.StatusOK, any(team.RelayOp{ID: "op-1", State: team.RelayClaimed})
		if verb == "claim" {
			body = team.RelayClaimResponse{Op: team.RelayOp{ID: "op-1", State: team.RelayClaimed}, Lead: &team.RelayLead{Address: "a/b", Ref: "_lead01", TeamID: "t"}}
		}
		if d.verbResp != nil {
			status, body = d.verbResp(verb)
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	default:
		http.NotFound(w, r)
	}
}

func driveMemberRelay(t *testing.T, d http.Handler, inbox string, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "tok")
	old := relayGetenv
	relayGetenv = func(k string) string {
		if k == "CLAUDE_CODE_MESSAGING_SOCKET" {
			return inbox
		}
		return ""
	}
	defer func() { relayGetenv = old }()
	var stdout, stderr bytes.Buffer
	code := runRelayCmd(context.Background(), append(args, "--config", cfg), &stdout, &stderr, leadClockOpt())
	return code, stdout.String(), stderr.String()
}

func TestRelayCmd_RefDispatchAndUnknownWordIs2(t *testing.T) {
	d := &fakeMemberRelayDaemon{}
	for _, ok := range []string{"_abc123", "mlab/purdex-x", "mlab/_abc123", "purdex-x [abc123]"} {
		if code, _, _ := driveMemberRelay(t, d, "/tmp/x.sock", ok); code != ExitOK {
			t.Errorf("%q: exit %d, want a member relay", ok, code)
		}
	}
	n := len(d.creates)
	// the short and the long ref are built, not spelled: fixture_shapes_test forbids v3-length ref literals
	for _, bad := range []string{"dance", "abc123", "_" + "abc12", "_ABC123", "_" + "abc1234", "purdex-x"} {
		if code, out, errs := driveMemberRelay(t, d, "/tmp/x.sock", bad); code != ExitUsage || out != "" || !strings.Contains(errs, "unknown subcommand") {
			t.Errorf("%q: exit %d out=%q err=%q, want 2", bad, code, out, errs)
		}
	}
	if len(d.creates) != n {
		t.Fatal("a usage error reached the daemon")
	}
}

func TestRelayCmd_MemberRelayPrintsTheOpExit0(t *testing.T) {
	d := &fakeMemberRelayDaemon{}
	code, out, errs := driveMemberRelay(t, d, "/tmp/x.sock", "_mem001")
	var op team.RelayOp
	if code != ExitOK || json.Unmarshal([]byte(out), &op) != nil || op.State != team.RelayRequested || !strings.Contains(errs, op.ID) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	got := d.creates[0]
	if got.Target != "_mem001" || got.OriginInbox != "/tmp/x.sock" || len(got.ID) != 36 || got.ID != op.ID {
		t.Fatalf("request = %+v", got)
	}
	// no session socket: refused before any request
	if code, _, errs := driveMemberRelay(t, d, "", "_mem001"); code != ExitError || !strings.Contains(errs, "CLAUDE_CODE_MESSAGING_SOCKET") {
		t.Fatalf("no socket: %d %q", code, errs)
	}
}

// Mutation gate: map a refusal to 1 → red. The code is the LAST stderr token.
func TestRelayCmd_RefusalsExit13CodeLast(t *testing.T) {
	for _, code := range []string{team.ErrNotLead, team.ErrNotYourMember, team.ErrRelayUnsupported, team.ErrRelayOpen} {
		d := &fakeMemberRelayDaemon{create: func(int) (int, any) {
			return 409, team.APIError{Error: code, Detail: "no\nway", Op: &team.RelayOp{ID: "op-x", State: team.RelayRequested}}
		}}
		c, out, errs := driveMemberRelay(t, d, "/tmp/x.sock", "_mem001")
		toks := strings.Fields(errs)
		if c != ExitRefused || toks[len(toks)-1] != code {
			t.Errorf("%s: exit %d err=%q", code, c, errs)
		}
		if code == team.ErrRelayOpen && !strings.Contains(out, "op-x") {
			t.Errorf("relay_open must print the open op: %q", out)
		}
	}
}

// A lost response: the client retries inside its grace and the SAME id goes out each time.
func TestRelayCmd_ReplayAfterALostResponseIsTheSameOp(t *testing.T) {
	calls := 0
	d := &fakeMemberRelayDaemon{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/team/relays" {
			calls++
			if calls == 1 {
				hj, _ := w.(http.Hijacker)
				c, _, _ := hj.Hijack()
				c.Close() // the response is lost
				return
			}
		}
		d.ServeHTTP(w, r)
	}))
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "tok")
	old := relayGetenv
	relayGetenv = func(string) string { return "/tmp/x.sock" }
	defer func() { relayGetenv = old }()
	var stdout, stderr bytes.Buffer
	if code := runRelayCmd(context.Background(), []string{"_mem001", "--config", cfg}, &stdout, &stderr, leadClockOpt()); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
	if len(d.creates) != 1 || calls != 2 {
		t.Fatalf("creates seen by the daemon %d, attempts %d", len(d.creates), calls)
	}
}

func opIn(state team.RelayState, reason string) team.RelayOp {
	return team.RelayOp{ID: "op-1", Kind: team.RelayKindMember, State: state, Reason: reason}
}

// The five outcomes of --wait, and the awaiting-approval line. Mutation gate: member_gone → 1 (or 14 → 1) red.
func TestRelayCmd_WaitMapping(t *testing.T) {
	for _, c := range []struct {
		name  string
		polls []team.RelayOp
		want  int
	}{
		{"done", []team.RelayOp{opIn(team.RelayRequested, ""), opIn(team.RelayDone, "")}, ExitOK},
		{"failed member_unresponsive", []team.RelayOp{opIn(team.RelayFailed, team.RelayReasonMemberUnresponsive)}, ExitMemberFailed},
		{"failed member_gone", []team.RelayOp{opIn(team.RelayFailed, team.RelayReasonMemberGone)}, ExitMemberFailed},
		{"failed member_unseen", []team.RelayOp{opIn(team.RelayFailed, team.RelayReasonMemberUnseen)}, ExitMemberFailed},
		{"failed member_busy_timeout", []team.RelayOp{opIn(team.RelayFailed, team.RelayReasonMemberBusyTimeout)}, ExitMemberFailed},
		{"failed member_blocked", []team.RelayOp{opIn(team.RelayFailed, team.RelayReasonMemberBlocked)}, ExitMemberFailed},
		{"failed handoff_incomplete", []team.RelayOp{opIn(team.RelayFailed, team.RelayReasonHandoffIncomplete)}, ExitError},
		{"cancelled denied", []team.RelayOp{opIn(team.RelayCancelled, team.RelayReasonDenied)}, ExitDenied},
		{"cancelled timeout", []team.RelayOp{opIn(team.RelayCancelled, team.RelayReasonTimeout)}, ExitTimeout},
		{"cancelled abandoned", []team.RelayOp{opIn(team.RelayCancelled, team.RelayReasonAbandoned)}, ExitCancelled},
	} {
		d := &fakeMemberRelayDaemon{polls: c.polls}
		code, out, _ := driveMemberRelay(t, d, "/tmp/x.sock", "_mem001", "--wait", "5m")
		var op team.RelayOp
		if code != c.want || json.Unmarshal([]byte(strings.TrimSpace(out)), &op) != nil || op.State != c.polls[len(c.polls)-1].State {
			t.Errorf("%s: exit %d (want %d) out=%q", c.name, code, c.want, out)
		}
	}
}

func TestRelayCmd_WaitPrintsTheApprovalLineAndTheBoundWhileAwaitingIs11(t *testing.T) {
	awaiting := team.RelayOp{ID: "op-1", Kind: team.RelayKindMember, State: team.RelayAwaitingApproval}
	d := &fakeMemberRelayDaemon{
		create: func(int) (int, any) { return 201, team.RelayCreateResponse{Op: awaiting} },
		polls:  []team.RelayOp{awaiting},
	}
	code, out, errs := driveMemberRelay(t, d, "/tmp/x.sock", "_mem001", "--wait", "1s")
	if code != ExitTimeout || !strings.Contains(errs, "等待核准：member 額度用完（無人值守）") || !strings.Contains(out, `"state":"awaiting_approval"`) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

// The bound reached while the member is simply working: the op on stdout, exit 0 (mirrors `pdx relay wait`).
func TestRelayCmd_WaitBoundWhileRunningIsExit0WithTheOp(t *testing.T) {
	d := &fakeMemberRelayDaemon{polls: []team.RelayOp{opIn(team.RelayWriting, "")}}
	code, out, _ := driveMemberRelay(t, d, "/tmp/x.sock", "_mem001", "--wait", "1s")
	if code != ExitOK || !strings.Contains(out, `"state":"writing"`) {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestRelayCmd_WaitIsBounded(t *testing.T) {
	d := &fakeMemberRelayDaemon{}
	for _, w := range []string{"10m", "-1s", "x"} {
		if code, _, _ := driveMemberRelay(t, d, "/tmp/x.sock", "_mem001", "--wait", w); code != ExitUsage {
			t.Errorf("--wait %s: exit %d, want 2", w, code)
		}
	}
	if len(d.creates) != 0 {
		t.Fatal("a refused --wait reached the daemon")
	}
}

func TestRelayCmd_ClaimPrintsAndMaps(t *testing.T) {
	d := &fakeMemberRelayDaemon{}
	code, out, _ := driveMemberRelay(t, d, "", "claim", "op-1", "--session", "sid-m")
	var res team.RelayClaimResponse
	if code != ExitOK || json.Unmarshal([]byte(out), &res) != nil || res.Lead == nil || res.Lead.Ref != "_lead01" || d.verbs[0] != `claim {"session_id":"sid-m"}` {
		t.Fatalf("claim: %d %q verbs=%v", code, out, d.verbs)
	}
	for _, c := range []struct{ code, op string }{{team.ErrNotYourOp, ""}, {team.ErrBadTransition, "op-1"}} {
		d := &fakeMemberRelayDaemon{verbResp: func(string) (int, any) {
			e := team.APIError{Error: c.code, Detail: "no"}
			if c.op != "" {
				e.Op = &team.RelayOp{ID: c.op, State: team.RelayWritten}
			}
			return 409, e
		}}
		code, out, errs := driveMemberRelay(t, d, "", "claim", "op-1", "--session", "sid-m")
		toks := strings.Fields(errs)
		if code != ExitRefused || toks[len(toks)-1] != c.code || (c.op != "" && !strings.Contains(out, c.op)) {
			t.Errorf("%s: %d out=%q err=%q", c.code, code, out, errs)
		}
	}
	for _, args := range [][]string{{"claim"}, {"claim", "op-1"}, {"claim", "op-1", "--session", ""}, {"claim", "../x", "--session", "s"}} {
		if code, _, _ := driveMemberRelay(t, d, "", args...); code != ExitUsage {
			t.Errorf("%v: %d, want 2", args, code)
		}
	}
}

func TestRelayCmd_SeenPrintsAndMaps(t *testing.T) {
	d := &fakeMemberRelayDaemon{}
	code, out, _ := driveMemberRelay(t, d, "", "seen", "op-1", "--session", "sid-m")
	if code != ExitOK || !strings.Contains(out, `"id":"op-1"`) || d.verbs[0] != `seen {"session_id":"sid-m"}` {
		t.Fatalf("seen: %d %q verbs=%v", code, out, d.verbs)
	}
	d2 := &fakeMemberRelayDaemon{verbResp: func(string) (int, any) {
		return 409, team.APIError{Error: team.ErrNotYourOp, Detail: "another session's"}
	}}
	if code, _, errs := driveMemberRelay(t, d2, "", "seen", "op-1", "--session", "other"); code != ExitRefused || !strings.HasSuffix(strings.TrimSpace(errs), team.ErrNotYourOp) {
		t.Fatalf("not_your_op: %d %q", code, errs)
	}
}

// An interrupted wait is exit 12 like a cancelled op, but it says the relay goes on and leaves the op alone.
func TestRelayCmd_AnInterruptedWaitSaysTheRelayGoesOn(t *testing.T) {
	d := &fakeMemberRelayDaemon{polls: []team.RelayOp{opIn(team.RelayWriting, "")}}
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "tok")
	old := relayGetenv
	relayGetenv = func(string) string { return "/tmp/x.sock" }
	defer func() { relayGetenv = old }()
	ctx, cancel := context.WithCancel(context.Background())
	d.onPoll = cancel // SIGINT while the first poll is out
	var stdout, stderr bytes.Buffer
	code := runRelayCmd(ctx, []string{"_mem001", "--wait", "5m", "--config", cfg}, &stdout, &stderr, leadClockOpt())
	if code != ExitCancelled || !strings.Contains(stderr.String(), "接力仍在進行") {
		t.Fatalf("code=%d err=%q", code, stderr.String())
	}
}

// #2439: a member that never answered is explained on stderr (the op's JSON stays on stdout), exit 14 as before.
func TestRelayCmd_WaitExplainsWhyAMemberDidNotAnswer(t *testing.T) {
	for reason, want := range map[string]string{
		team.RelayReasonMemberUnseen:      "Mods: Enable hot reloading?",
		team.RelayReasonMemberBlocked:     "等待輸入",
		team.RelayReasonMemberBusyTimeout: "60 分鐘",
	} {
		d := &fakeMemberRelayDaemon{polls: []team.RelayOp{opIn(team.RelayFailed, reason)}}
		code, out, errs := driveMemberRelay(t, d, "/tmp/x.sock", "_mem001", "--wait", "5m")
		if code != ExitMemberFailed || !strings.Contains(errs, want) || !strings.Contains(out, reason) {
			t.Errorf("%s: exit %d out=%q err=%q, want a hint with %q", reason, code, out, errs, want)
		}
	}
	// the old reasons print no hint
	d := &fakeMemberRelayDaemon{polls: []team.RelayOp{opIn(team.RelayFailed, team.RelayReasonMemberGone)}}
	if _, _, errs := driveMemberRelay(t, d, "/tmp/x.sock", "_mem001", "--wait", "5m"); strings.Contains(errs, "Mods:") {
		t.Errorf("member_gone got the dialog hint: %q", errs)
	}
}
