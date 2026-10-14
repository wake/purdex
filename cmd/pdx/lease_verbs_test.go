package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// verbDaemon records the verb calls and answers each with one fixed status.
type verbDaemon struct {
	mu     sync.Mutex
	status int
	body   any
	calls  []verbCall
}

type verbCall struct {
	method, path string
	pid          int
}

func (d *verbDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		_, _ = w.Write([]byte(`{"ok":true,"boot_id":"b1"}`))
		return
	}
	var req resources.CallerRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	d.mu.Lock()
	d.calls = append(d.calls, verbCall{r.Method, r.URL.Path, req.HolderPID})
	status, body := d.status, d.body
	d.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	if body == nil {
		body = resources.PositionResponse{State: resources.StateEnded}
	}
	write(w, answer{status: status, body: body})
}

func driveVerb(t *testing.T, d *verbDaemon, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runLeaseCmd(context.Background(), append(append([]string{}, args...), "--config", cfg), fakeGetenv(nil), &stdout, &stderr, leadNoKeepAlive())
	return code, stdout.String(), stderr.String()
}

// Each verb posts its caller's pid to its route and prints its line.
func TestConfirmCancelDone_SendTheirPidAndPrint(t *testing.T) {
	for name, c := range map[string]struct {
		args       []string
		path, line string
	}{
		"confirm": {[]string{"confirm", "row-1"}, "/api/resources/leases/row-1/confirm", "已確認，背景指令開始執行"},
		"cancel":  {[]string{"cancel", "row-1"}, "/api/resources/leases/row-1/cancel", "已撤銷"},
		"done":    {[]string{"done"}, "/api/resources/positions/done", "位置已放掉"},
	} {
		t.Run(name, func(t *testing.T) {
			d := &verbDaemon{}
			if name == "confirm" {
				d.body = resources.LeaseResponse{ID: "row-1", State: resources.StateHeld, Granted: true}
			}
			code, stdout, stderr := driveVerb(t, d, c.args...)
			assert.Equal(t, ExitOK, code, stderr)
			assert.Contains(t, stdout, c.line)
			require.Len(t, d.calls, 1)
			assert.Equal(t, http.MethodPost, d.calls[0].method)
			assert.Equal(t, c.path, d.calls[0].path)
			assert.Equal(t, os.Getpid(), d.calls[0].pid)
		})
	}
	// A done that the daemon holds back until the last command ends says so.
	d := &verbDaemon{body: resources.PositionResponse{State: resources.PosRunning, DonePending: true}}
	code, stdout, _ := driveVerb(t, d, "done")
	assert.Equal(t, ExitOK, code)
	assert.Contains(t, stdout, "最後一個指令結束後放掉")
}

// A confirm says what became of the request, not just that the daemon answered:
// an ended request (a repeat after the timeout) is not a command that started.
func TestConfirm_ReportsWhatHappened(t *testing.T) {
	ended := &verbDaemon{body: resources.LeaseResponse{ID: "row-1", State: resources.StateEnded, EndReason: resources.EndNoAnswer}}
	code, stdout, stderr := driveVerb(t, ended, "confirm", "row-1")
	assert.Equal(t, ExitRefused, code)
	assert.NotContains(t, stdout, "開始執行")
	assert.Contains(t, stderr, "已經結束（no_answer）")

	waiting := &verbDaemon{body: resources.LeaseResponse{ID: "row-1", State: resources.StateWaiting}}
	code, stdout, _ = driveVerb(t, waiting, "confirm", "row-1")
	assert.Equal(t, ExitOK, code)
	assert.Contains(t, stdout, "一有空")

	odd := &verbDaemon{body: resources.LeaseResponse{ID: "row-1", State: "weird"}}
	code, stdout, stderr = driveVerb(t, odd, "confirm", "row-1")
	assert.Equal(t, ExitError, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "不確定")
}

// A refusal of the daemon is exit 13 with a line; usage errors are 2.
func TestConfirm_RefusedExits13(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		code   string
		line   string
	}{
		"forbidden": {http.StatusForbidden, resources.ErrNotYourSession, "這不是你這個 session 的請求"},
		"not turn":  {http.StatusConflict, resources.ErrNotYourTurn, "還沒輪到你"},
		"running":   {http.StatusConflict, resources.ErrRunning, "正在執行"},
		"missing":   {http.StatusNotFound, resources.ErrNoLease, "找不到這個請求"},
		"unknown":   {http.StatusConflict, "weird\ncode", `被拒絕（"weird\ncode"）`},
	} {
		t.Run(name, func(t *testing.T) {
			d := &verbDaemon{status: c.status, body: resources.APIError{Error: c.code}}
			code, stdout, stderr := driveVerb(t, d, "confirm", "row-1")
			assert.Equal(t, ExitRefused, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, c.line)
		})
	}
	for _, args := range [][]string{{"confirm"}, {"confirm", "a", "b"}, {"cancel"}, {"done", "x"}} {
		code, _, _ := driveVerb(t, &verbDaemon{}, args...)
		assert.Equal(t, ExitUsage, code, "%v", args)
	}
}

// The 409 of a position names the next step (done); the one of a request does not.
func TestCancel_RunningConflictTextFitsTheTarget(t *testing.T) {
	d := &verbDaemon{status: http.StatusConflict, body: resources.APIError{Error: resources.ErrRunning}}
	code, _, stderr := driveVerb(t, d, "cancel", "pos_ab12cd34")
	assert.Equal(t, ExitRefused, code)
	assert.Contains(t, stderr, "這個位置有指令正在執行，不能撤銷；要在它跑完後放掉位置，請用 pdx lease done")

	d = &verbDaemon{status: http.StatusConflict, body: resources.APIError{Error: resources.ErrRunning}}
	code, _, stderr = driveVerb(t, d, "cancel", "row-1")
	assert.Equal(t, ExitRefused, code)
	assert.Contains(t, stderr, "這個指令正在執行，不能撤銷")
	assert.NotContains(t, stderr, "pdx lease done")
}

// A pos_ id is a position: cancel goes to the position route.
func TestCancel_PositionIDGoesToThePositionRoute(t *testing.T) {
	d := &verbDaemon{}
	code, stdout, stderr := driveVerb(t, d, "cancel", "pos_ab12cd34")
	assert.Equal(t, ExitOK, code, stderr)
	assert.Contains(t, stdout, "已撤銷")
	require.Len(t, d.calls, 1)
	assert.Equal(t, "/api/resources/positions/pos_ab12cd34/cancel", d.calls[0].path)
}

func positionsSnapshot() resources.Snapshot {
	snap := fakeSnapshot()
	at := lsNow.UnixMilli()
	snap.Positions = []resources.PositionView{
		{ID: "pos_1", SessionID: "s1", Address: "mlab/purdex-1f-vq", Ref: "_vq3x9a", State: resources.PosIdle, WindowUntil: at + 134_000,
			Commands: 2, SpanS: 41 * 60, Warned: true, NoticeResult: "user_alert",
			Requests: []resources.PositionRequestView{{ID: "r1", Mode: "queue", Command: "pnpm run build", Cwd: "~/Workspace/purdex/spa", State: "held"}}},
		{ID: "pos_2", SessionID: "s2", Address: "mlab/purdex-88-b8", Ref: "_b8k2p1", State: resources.PosQueued, Place: 1,
			Requests: []resources.PositionRequestView{
				{ID: "r2", Mode: "queue", Command: "go test ./...", Cwd: "~/Workspace/purdex", State: "queued"},
				{ID: "r3", Mode: "ask", Command: "npx vitest run", Cwd: "~/Workspace/purdex/spa", State: "asked"}}},
		{ID: "pos_3", SessionID: "0123456789abcdef", State: resources.PosOffered, Delivered: false},
		{ID: "pos_4", SessionID: "s4", Address: "mlab/x\nfake", State: resources.PosOffered, Delivered: true, WindowUntil: at + 45_000},
	}
	return snap
}

func TestLs_PositionsSection(t *testing.T) {
	withLeaseNow(t)
	var out, errOut bytes.Buffer
	require.Equal(t, ExitOK, printLeaseTable(&out, &errOut, positionsSnapshot()))
	got := out.String()
	for _, want := range []string{
		"POSITIONS", "占用", "mlab/purdex-1f-vq (_vq3x9a)", "空窗", "2m14s", "已預告", "通知沒送到",
		"  queue  執行中  pnpm run build  ~/Workspace/purdex/spa",
		"#1", "mlab/purdex-88-b8 (_b8k2p1)", "排隊中",
		"  queue  排隊中  go test ./...  ~/Workspace/purdex",
		"  ask  已詢問  npx vitest run  ~/Workspace/purdex/spa",
		"輪到了（通知中）", "01234567", "輪到了", "45s",
	} {
		assert.Contains(t, got, want)
	}
	assert.NotContains(t, got, "mlab/x\nfake", "a newline in an address cannot start a line of its own")
}

// A daemon from before amendment A sends no positions: the old table, unchanged.
func TestLs_OldDaemonStillPrints(t *testing.T) {
	withLeaseNow(t)
	var out, errOut bytes.Buffer
	require.Equal(t, ExitOK, printLeaseTable(&out, &errOut, lsSnapshot()))
	assert.NotContains(t, out.String(), "POSITIONS")
	assert.Contains(t, out.String(), "WAITERS")
}

func TestReport_PositionLines(t *testing.T) {
	rep := v2Report()
	rep.Positions = resources.ReportPositions{
		Total: 5, ByEndReason: []resources.ReportReasonCount{{Reason: "idle_timeout", Count: 3}, {Reason: "done", Count: 2}},
		Commands: resources.ReportCommandCounts{None: 1, One: 2, Two: 1, Three: 1}, Requeued: 1, Extensions: 2,
		OfferWaitMS:  resources.ReportDist{N: 2, Total: 30000, P50: 10000, P90: 20000, Max: 20000},
		IdleWaitMS:   resources.ReportDist{N: 1, Total: 5000, P50: 5000, P90: 5000, Max: 5000},
		QueuedWaitMS: resources.ReportWait{P50: 60000, P90: 90000, Max: 120000},
		Offers: resources.ReportOffers{Offered: 4, Delivered: 3, Passed: 1, LapsedAfterDelivery: 1,
			DeliveryDelayMS: resources.ReportDist{N: 3, P50: 1000, Max: 4000},
			NoticeResults:   []resources.ReportReasonCount{{Reason: "accepted", Count: 3}, {Reason: "dropped:no_mod", Count: 1}}},
	}
	var out bytes.Buffer
	formatReport(&out, rep, time.UTC)
	got := out.String()
	for _, want := range []string{
		"positions 5  (idle_timeout 3, done 2)",
		"commands per position: none 1, one 2, two 1, three 1, four or more 0; re-queued 1; extensions 2",
		"queued wait  p50 60.0s  p90 90.0s  max 120.0s",
		"offered window n 2 total 30.0s p50 10.0s p90 20.0s max 20.0s; idle window n 1 total 5.0s",
		"offers  4, delivered 3, passed undelivered 1, lapsed after delivery 1; delivery delay p50 1.0s max 4.0s",
		"notices  accepted 3, dropped:no_mod 1",
	} {
		assert.Contains(t, got, want)
	}
	var none bytes.Buffer
	formatReport(&none, v2Report(), time.UTC)
	assert.NotContains(t, none.String(), "positions ")
}

// A daemon that cannot be used is 20, not a generic 1.
func TestVerb_DaemonNotReadyExits20(t *testing.T) {
	d := &verbDaemon{status: http.StatusServiceUnavailable, body: resources.APIError{Error: resources.ErrNotReady}}
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runLeaseCmd(context.Background(), []string{"done", "--config", cfg}, fakeGetenv(nil), &stdout, &stderr, leadNoKeepAlive(), leadClockOpt())
	assert.Equal(t, ExitUnavailable, code, stderr.String())
	assert.Contains(t, stderr.String(), "daemon 連不上")
}
