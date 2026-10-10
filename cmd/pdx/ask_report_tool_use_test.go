package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// #1848: `pdx ask report --session S --tool-use T --since MS <state>` reports by the tool use for a mod that never learned the
// row's id; a 404 means begin's row has not landed yet, and is retried for 45 s (begin's 30 s restart grace plus room).

const byToolUseHook = `{"answers":{"q?":"a"}}`

func byToolUseArgs(extra ...string) []string {
	return append([]string{"report", "--session", "sid-1", "--tool-use", "toolu_1", "--since", "1700000000000", "answered_local", "--hook", byToolUseHook}, extra...)
}

func TestAskReportByToolUse_UsageErrorsExit2(t *testing.T) {
	for _, args := range [][]string{
		{"report", "id-1", "dismissed", "--session", "s", "--tool-use", "t", "--since", "1"},  // an id and a tool use together
		{"report", "dismissed", "--session", "s", "--since", "1"},                             // no --tool-use
		{"report", "dismissed", "--tool-use", "t", "--since", "1"},                            // no --session
		{"report", "dismissed", "--session", "s", "--tool-use", "t"},                          // no --since
		{"report", "dismissed", "--session", "s", "--tool-use", "t", "--since", "0"},          // since must be positive
		{"report", "dismissed", "--session", "s", "--tool-use", "t", "--since", "x"},          // not a number
		{"report", "approved", "--session", "s", "--tool-use", "t", "--since", "1"},           // bad state
		{"report", "dismissed", "extra", "--session", "s", "--tool-use", "t", "--since", "1"}, // stray positional
		{"begin", "--session", "s", "--tool-use", "t", "--payload", "{}", "--since", "1"},     // --since is report's only
		{"report", "a", "dismissed", "--since", "1"},                                          // --since without the tool-use form
		{"wait", "a", "--since", "1"},
	} {
		var stdout, stderr bytes.Buffer
		code := runAskCmd(context.Background(), args, &stdout, &stderr, time.Now)
		if code != ExitUsage || stdout.Len() != 0 {
			t.Errorf("%v: code=%d stdout=%q stderr=%q, want usage (2)", args, code, stdout.String(), stderr.String())
		}
	}
}

// retryClock: a fake clock the retry sleeps advance, so the 45 s ceiling is measured without waiting.
type retryClock struct {
	mu     sync.Mutex
	t      time.Time
	sleeps []time.Duration
}

func (c *retryClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *retryClock) sleep(_ context.Context, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	c.sleeps = append(c.sleeps, d)
}

func withRetryClock(t *testing.T) *retryClock {
	t.Helper()
	c := &retryClock{t: time.Unix(1_700_000_000, 0)}
	old := askRetrySleep
	askRetrySleep = c.sleep
	t.Cleanup(func() { askRetrySleep = old })
	return c
}

func TestAskReportByToolUse_SendsTheToolUseAndPrintsTheRow(t *testing.T) {
	d := newFakeAskDaemon(t)
	c := withRetryClock(t)
	code, stdout, stderr := driveAsk(t, d, c.now, byToolUseArgs()...)
	if code != ExitOK || !strings.Contains(stdout, `"state":"answered_local"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(d.byToolUse) != 1 || d.byToolUse[0].SessionID != "sid-1" || d.byToolUse[0].ToolUseID != "toolu_1" || d.byToolUse[0].Since != 1700000000000 ||
		d.byToolUse[0].State != team.StateAnsweredLocal || d.byToolUse[0].Hook == nil || d.byToolUse[0].Hook.Answers["q?"] != "a" {
		t.Fatalf("sent = %+v", d.byToolUse)
	}
	if len(d.reports) != 0 {
		t.Fatalf("the id route was used: %+v", d.reports)
	}
}

// 404 not_found: begin's row has not landed; retried until it has. Mutation gate: no retry → exit 1 on the first 404 (red).
func TestAskReportByToolUse_RetriesNotFoundUntilTheRowLands(t *testing.T) {
	d := newFakeAskDaemon(t)
	d.notFound = 3
	c := withRetryClock(t)
	code, stdout, stderr := driveAsk(t, d, c.now, byToolUseArgs()...)
	if code != ExitOK || !strings.Contains(stdout, "answered_local") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(d.byToolUse) != 4 || len(c.sleeps) != 3 {
		t.Fatalf("%d attempts, %d sleeps; want 4 and 3", len(d.byToolUse), len(c.sleeps))
	}
}

// Any other refusal is final. Mutation gate: retry on every error → 400 is asked many times (red).
func TestAskReportByToolUse_AnotherRefusalIsNotRetried(t *testing.T) {
	d := newFakeAskDaemon(t)
	d.byToolUseStatus = http.StatusBadRequest
	c := withRetryClock(t)
	code, _, stderr := driveAsk(t, d, c.now, byToolUseArgs()...)
	if code == ExitOK || len(d.byToolUse) != 1 || len(c.sleeps) != 0 {
		t.Fatalf("code=%d, %d attempts, %d sleeps: a 400 must stop at once (stderr %q)", code, len(d.byToolUse), len(c.sleeps), stderr)
	}
}

// Past 45 s the report gives up with the not_found code last on stderr. Mutation gates: no ceiling → never returns; 60 s → the
// attempts count differs.
func TestAskReportByToolUse_GivesUpAfter45Seconds(t *testing.T) {
	d := newFakeAskDaemon(t)
	d.notFound = 1 << 30
	c := withRetryClock(t)
	code, stdout, stderr := driveAsk(t, d, c.now, byToolUseArgs()...)
	if code == ExitOK || stdout != "" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	if toks := strings.Fields(stderr); len(toks) == 0 || toks[len(toks)-1] != team.ErrNotFound {
		t.Fatalf("the code must be the last stderr token: %q", stderr)
	}
	var total time.Duration
	for _, s := range c.sleeps {
		total += s
	}
	if total < 45*time.Second || total > 47*time.Second {
		t.Fatalf("slept %s in all, want about 45 s", total)
	}
}

// --detach starts the same argv without the flag (the loop runs in the detached process).
func TestAskReportByToolUse_DetachStartsItselfWithoutTheFlag(t *testing.T) {
	var started [][]string
	old := startDetachedFn
	startDetachedFn = func(args []string) error { started = append(started, args); return nil }
	t.Cleanup(func() { startDetachedFn = old })
	var stdout, stderr bytes.Buffer
	if code := runAskCmd(context.Background(), byToolUseArgs("--detach"), &stdout, &stderr, time.Now); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if len(started) != 1 || strings.Contains(strings.Join(started[0], " "), "detach") || !strings.Contains(strings.Join(started[0], " "), "--since 1700000000000") {
		t.Fatalf("started = %v", started)
	}
}
