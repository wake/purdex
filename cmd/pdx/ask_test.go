package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// fakeAskDaemon answers the three ask routes and /api/health. waits is the
// sequence of wait bodies; after it is exhausted the last one repeats.
type fakeAskDaemon struct {
	mu          sync.Mutex
	beginStatus int
	beginBody   any
	// dropFirstBegin: the first begin's body is read and "applied" (the row
	// opened, id ask-1), then the connection dies without a response; every
	// later begin for the same (session, tool_use) is 409 ask_open with it.
	dropFirstBegin bool
	waits          []team.AskWaitResponse
	polls          int
	reports        []team.AskReportRequest
	begins         []team.AskBeginRequest
	srv            *httptest.Server
}

func newFakeAskDaemon(t *testing.T) *fakeAskDaemon {
	t.Helper()
	d := &fakeAskDaemon{beginStatus: http.StatusCreated, beginBody: team.AskBeginResponse{ID: "ask-1"}}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/health":
			_, _ = io.WriteString(w, `{"boot_id":"b1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/ask/begin":
			var req team.AskBeginRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			d.begins = append(d.begins, req)
			if d.dropFirstBegin {
				if len(d.begins) == 1 {
					// The body was read and the row written; then the connection
					// dies without an answer (as lead_test.go's dropFirstCreate).
					if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
						conn.Close()
					}
					return
				}
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(team.APIError{Error: team.ErrAskOpen, Approval: &team.Approval{ID: "ask-1", Kind: team.KindHookAsk, State: team.StateOpen}})
				return
			}
			w.WriteHeader(d.beginStatus)
			_ = json.NewEncoder(w).Encode(d.beginBody)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/ask/wait/"):
			i := d.polls
			d.polls++
			if i >= len(d.waits) {
				i = len(d.waits) - 1
			}
			_ = json.NewEncoder(w).Encode(d.waits[i])
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/ask/report/"):
			var req team.AskReportRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			d.reports = append(d.reports, req)
			_ = json.NewEncoder(w).Encode(team.Approval{ID: strings.TrimPrefix(r.URL.Path, "/api/ask/report/"), Kind: team.KindHookAsk, State: req.State, Hook: req.Hook})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "404 page not found\n")
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func driveAsk(t *testing.T, d *fakeAskDaemon, now func() time.Time, args ...string) (int, string, string) {
	t.Helper()
	cfg := writeTestConfig(t, d.srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runAskCmd(context.Background(), append(args, "--config", cfg), &stdout, &stderr, now, leadClockOpt())
	return code, stdout.String(), stderr.String()
}

func TestRunAskCmd_UsageErrorsExit2(t *testing.T) {
	for _, args := range [][]string{
		{}, {"frob"}, {"begin"}, {"begin", "--session", "s"}, {"begin", "--session", "s", "--tool-use", "t"},
		{"begin", "--session", "s", "--tool-use", "t", "--payload", "{not json"},
		{"begin", "--session", "s", "--tool-use", "t", "--kind", "lead", "--payload", "{}"},
		{"wait"}, {"wait", "a", "b"}, {"report", "a"}, {"report", "a", "approved"}, {"report", "a", "dismissed", "--hook", "{", "--hook-file", "/x"},
		// wait registers --config only (codex round): report's flags are a usage error here, not ignored.
		{"wait", "a", "--hook", "{}"}, {"wait", "a", "--hook-file", "/x"}, {"wait", "a", "--session", "s"},
	} {
		var stdout, stderr bytes.Buffer
		code := runAskCmd(context.Background(), args, &stdout, &stderr, time.Now)
		if code != ExitUsage || !strings.Contains(stderr.String(), "usage: pdx ask") || stdout.Len() != 0 {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunAskCmd_BeginPrintsIDAndSendsBody(t *testing.T) {
	d := newFakeAskDaemon(t)
	payload := `{"questions":[{"question":"q?","header":"h","options":[{"label":"a"},{"label":"b"}],"multiSelect":false}]}`
	code, stdout, stderr := driveAsk(t, d, time.Now, "begin", "--session", "sid-1", "--tool-use", "toolu_1", "--kind", "hook_ask", "--payload", payload)
	if code != ExitOK || strings.TrimSpace(stdout) != `{"id":"ask-1"}` {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(d.begins) != 1 || d.begins[0].SessionID != "sid-1" || d.begins[0].ToolUseID != "toolu_1" || d.begins[0].Kind != team.KindHookAsk || string(d.begins[0].Payload) != payload {
		t.Fatalf("begin body = %+v", d.begins)
	}
}

func TestRunAskCmd_BeginNoRespondersExit13_AskOpenAdopts(t *testing.T) {
	d := newFakeAskDaemon(t)
	d.beginStatus, d.beginBody = http.StatusConflict, team.APIError{Error: team.ErrNoResponders, Detail: "沒有連線中的客戶端可以回答"}
	code, stdout, stderr := driveAsk(t, d, time.Now, "begin", "--session", "s", "--tool-use", "t", "--payload", `{"questions":[1]}`)
	if code != ExitRefused || stdout != "" || !strings.HasPrefix(stderr, "pdx ask: ") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	// The code is the LAST stderr token (the mod's stderrCode() reads it that way).
	if toks := strings.Fields(stderr); len(toks) == 0 || toks[len(toks)-1] != team.ErrNoResponders {
		t.Fatalf("the code must be the last stderr token: %q", stderr)
	}
	d.beginStatus, d.beginBody = http.StatusConflict, team.APIError{Error: team.ErrAskOpen, Approval: &team.Approval{ID: "ask-open-7"}}
	code, stdout, _ = driveAsk(t, d, time.Now, "begin", "--session", "s", "--tool-use", "t", "--payload", `{"questions":[1]}`)
	if code != ExitOK || strings.TrimSpace(stdout) != `{"id":"ask-open-7"}` {
		t.Fatalf("adopt: code=%d stdout=%q", code, stdout)
	}
}

// The POST landed (the row is open) but the response was lost: begin is
// Idempotent(), so the client replays inside the grace, the daemon answers
// 409 ask_open with the row it opened, and the CLI adopts it — stdout
// {"id":"ask-1"} exactly as a 201 would print, exit 0, and on stderr only
// the client's one restart line (as lead's replay test), no error.
// Mutation gates: drop Idempotent() → ErrSentNoResponse → exit 1 and an
// empty stdout (the mod would run the dialog alone while a row is open);
// treat ask_open as an error → exit 13.
func TestRunAskCmd_BeginReplayAfterLostResponseAdoptsTheSameID(t *testing.T) {
	d := newFakeAskDaemon(t)
	d.dropFirstBegin = true
	code, stdout, stderr := driveAsk(t, d, time.Now, "begin", "--session", "s", "--tool-use", "t", "--payload", `{"questions":[1]}`)
	if code != ExitOK || strings.TrimSpace(stdout) != `{"id":"ask-1"}` || stderr != daemonclient.MsgRestarting+"\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.begins) != 2 || d.begins[0].ToolUseID != "t" || d.begins[1].ToolUseID != "t" {
		t.Fatalf("want the dropped begin and one replay for the same tool use, got %+v", d.begins)
	}
}

// R2 attacker: a 2xx or ask_open without an id leaves a row nobody can wait
// on, and an unknown wait state is not an answer the mod can act on — both
// fail with exit 1 and an empty stdout. Mutation gates: drop the id check →
// exit 0 with {"id":""}; drop validWaitState → exit 0 with the bogus state.
func TestRunAskCmd_RejectsEmptyIDAndUnknownWaitState(t *testing.T) {
	d := newFakeAskDaemon(t)
	d.beginBody = team.AskBeginResponse{}
	code, stdout, stderr := driveAsk(t, d, time.Now, "begin", "--session", "s", "--tool-use", "t", "--payload", `{"questions":[1]}`)
	if code != ExitError || stdout != "" || !strings.HasSuffix(strings.TrimSpace(stderr), "invalid_response") {
		t.Fatalf("empty id: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	d.beginStatus, d.beginBody = http.StatusConflict, team.APIError{Error: team.ErrAskOpen, Approval: &team.Approval{}}
	if code, stdout, _ = driveAsk(t, d, time.Now, "begin", "--session", "s", "--tool-use", "t", "--payload", `{"questions":[1]}`); code != ExitError || stdout != "" {
		t.Fatalf("ask_open with empty id: code=%d stdout=%q", code, stdout)
	}
	for _, st := range []string{"", "bogus"} {
		dw := newFakeAskDaemon(t)
		dw.waits = []team.AskWaitResponse{{State: st}}
		code, stdout, stderr := driveAsk(t, dw, time.Now, "wait", "ask-1")
		if code != ExitError || stdout != "" || !strings.HasSuffix(strings.TrimSpace(stderr), "invalid_response") {
			t.Fatalf("state %q: code=%d stdout=%q stderr=%q", st, code, stdout, stderr)
		}
	}
}

// still_open polls repeat inside one round; the round ends with still_open
// after 9 min, or sooner with the daemon's answer, printed verbatim.
func TestRunAskCmd_WaitRoundAndAnswers(t *testing.T) {
	d := newFakeAskDaemon(t)
	d.waits = []team.AskWaitResponse{{State: team.AskStillOpen}, {State: team.AskStillOpen}, {State: team.AskAnsweredRemote, Hook: &team.HookDecision{Answers: map[string]string{"q?": "a"}}}}
	code, stdout, stderr := driveAsk(t, d, time.Now, "wait", "ask-1")
	if code != ExitOK || strings.TrimSpace(stdout) != `{"state":"answered_remote","hook":{"answers":{"q?":"a"}}}` || d.polls != 3 {
		t.Fatalf("code=%d stdout=%q stderr=%q polls=%d", code, stdout, stderr, d.polls)
	}
	// A round whose every poll is still_open ends after askWaitRound: the
	// fake clock jumps 5 min per call, so the third poll crosses 9 min.
	d2 := newFakeAskDaemon(t)
	d2.waits = []team.AskWaitResponse{{State: team.AskStillOpen}}
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	calls := 0
	now := func() time.Time { calls++; return t0.Add(time.Duration(calls-1) * 5 * time.Minute) }
	code, stdout, _ = driveAsk(t, d2, now, "wait", "ask-1")
	if code != ExitOK || strings.TrimSpace(stdout) != `{"state":"still_open"}` || d2.polls != 2 {
		t.Fatalf("round: code=%d stdout=%q polls=%d", code, stdout, d2.polls)
	}
	d3 := newFakeAskDaemon(t)
	d3.waits = []team.AskWaitResponse{{State: team.AskClosed, Reason: "dismissed"}}
	code, stdout, _ = driveAsk(t, d3, time.Now, "wait", "ask-1")
	if code != ExitOK || strings.TrimSpace(stdout) != `{"state":"closed","reason":"dismissed"}` {
		t.Fatalf("closed: code=%d stdout=%q", code, stdout)
	}
}

func TestRunAskCmd_WaitUnsupportedExit21(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = io.WriteString(w, `{"boot_id":"b1"}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runAskCmd(context.Background(), []string{"wait", "ask-1", "--config", cfg}, &stdout, &stderr, time.Now, leadClockOpt())
	if code != ExitUnsupported || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unsupported") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunAskCmd_ReportSendsHookAndPrintsRow(t *testing.T) {
	d := newFakeAskDaemon(t)
	code, stdout, stderr := driveAsk(t, d, time.Now, "report", "ask-1", "answered_local", "--hook", `{"answers":{"q?":"a"}}`)
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var ap team.Approval
	if err := json.Unmarshal([]byte(stdout), &ap); err != nil || ap.ID != "ask-1" || ap.State != team.StateAnsweredLocal || ap.Hook == nil || ap.Hook.Answers["q?"] != "a" {
		t.Fatalf("stdout = %q (%v)", stdout, err)
	}
	if len(d.reports) != 1 || d.reports[0].State != team.StateAnsweredLocal || d.reports[0].Hook.Answers["q?"] != "a" {
		t.Fatalf("report body = %+v", d.reports)
	}
	code, _, _ = driveAsk(t, d, time.Now, "report", "ask-1", "dismissed")
	if code != ExitOK || len(d.reports) != 2 || d.reports[1].State != team.StateDismissed || d.reports[1].Hook != nil {
		t.Fatalf("dismissed: code=%d reports=%+v", code, d.reports)
	}
}

// askTestServer answers /api/health and hands every other request to h.
func askTestServer(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = io.WriteString(w, `{"boot_id":"b1"}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return writeTestConfig(t, srv.URL, "admin-tok")
}

// P8a-1b: the daemon refuses an answered_local without the kind's answer
// (400 bad_request): exit 1, the code as stderr's last token, no stdout.
func TestRunAskCmd_ReportRefusedIsExit1WithTheCodeLast(t *testing.T) {
	cfg := askTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(team.APIError{Error: team.ErrBadRequest, Detail: "answers\nrequired"})
	})
	var stdout, stderr bytes.Buffer
	code := runAskCmd(context.Background(), []string{"report", "ask-1", "answered_local", "--config", cfg}, &stdout, &stderr, time.Now, leadClockOpt())
	if toks := strings.Fields(stderr.String()); code != ExitError || stdout.Len() != 0 || len(toks) == 0 || toks[len(toks)-1] != team.ErrBadRequest {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// askHoldingDaemon holds every ?wait=25 poll until the client goes away
// (onPoll runs first) and answers a ?wait=0 read with final.
func askHoldingDaemon(t *testing.T, onPoll func(), final team.AskWaitResponse) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	cfg := askTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI())
		mu.Unlock()
		if r.URL.Query().Get("wait") == "0" {
			_ = json.NewEncoder(w).Encode(final)
			return
		}
		onPoll()
		<-r.Context().Done()
	})
	return cfg, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

// Three polls with no answer at all ⇒ 20 (spec §9.1), before the round's
// bound: the bound is a cancel, not a ctx deadline, so the client's attempt
// timer (fired here by the fake clock) still ends each poll as ErrNoAnswer.
// Mutation gate: context.WithTimeout for the bound → no attempt timer, the
// polls hang until the bound → still_open, exit 0 → red.
func TestRunAskCmd_WaitThreeHungPollsExit20(t *testing.T) {
	defer func(r time.Duration) { askWaitRound = r }(askWaitRound)
	askWaitRound = 2 * time.Second // a regression is a 2 s red, not a 9 min one
	clock := newLeadClock()
	cfg, seen := askHoldingDaemon(t, clock.fireNext, team.AskWaitResponse{State: team.AskStillOpen})
	var stdout, stderr bytes.Buffer
	code := runAskCmd(context.Background(), []string{"wait", "ask-1", "--config", cfg}, &stdout, &stderr, time.Now, clock.opt())
	if code != ExitUnavailable || stdout.Len() != 0 || len(seen()) != 3 {
		t.Fatalf("code=%d stdout=%q stderr=%q requests=%v", code, stdout.String(), stderr.String(), seen())
	}
}

// The bound fires while a poll is in flight: one short read (wait=0) under a
// fresh context decides, so an answer that landed meanwhile is printed, not
// still_open (as pdx relay wait, PR #1726 A-2).
func TestRunAskCmd_WaitBoundReadsTheFinalState(t *testing.T) {
	defer func(r time.Duration) { askWaitRound = r }(askWaitRound)
	askWaitRound = 300 * time.Millisecond
	cfg, seen := askHoldingDaemon(t, func() {}, team.AskWaitResponse{State: team.AskAnsweredRemote, Hook: &team.HookDecision{Answers: map[string]string{"q?": "b"}}})
	var stdout, stderr bytes.Buffer
	code := runAskCmd(context.Background(), []string{"wait", "ask-1", "--config", cfg}, &stdout, &stderr, time.Now, leadClockOpt())
	if code != ExitOK || strings.TrimSpace(stdout.String()) != `{"state":"answered_remote","hook":{"answers":{"q?":"b"}}}` {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if s := seen(); len(s) < 2 || !strings.HasSuffix(s[len(s)-1], "?wait=0") {
		t.Fatalf("the bound must end with one short read: %v", s)
	}
}
