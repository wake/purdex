package main

// `pdx lease confirm`, `cancel` and `done` (lease v2 amendment A, Task 20): the
// verbs a session uses on its own requests and position. Each names the caller
// by its pid; the daemon finds the agent above it and so the session, and
// refuses a request that is not that session's.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/resources"
)

// positionIDPrefix is how a position id starts; a cancel of one goes to the
// position route.
const positionIDPrefix = "pos_"

// runLeaseVerb implements `pdx lease (confirm|cancel) <id>` and `pdx lease done`.
func runLeaseVerb(ctx context.Context, verb string, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx lease "+verb, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	pos, err := parseTeamFlags(fs, args)
	want := 1
	if verb == "done" {
		want = 0
	}
	if err == nil && len(pos) != want {
		if want == 0 {
			err = fmt.Errorf("unexpected argument %q", pos[0])
		} else {
			err = fmt.Errorf("give exactly one <id>")
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx lease: %v\n%s\n", err, leaseUsage)
		return ExitUsage
	}
	path := "/api/resources/positions/done"
	switch {
	case verb == "confirm":
		path = "/api/resources/leases/" + url.PathEscape(pos[0]) + "/confirm"
	case verb == "cancel" && strings.HasPrefix(pos[0], positionIDPrefix):
		path = "/api/resources/positions/" + url.PathEscape(pos[0]) + "/cancel"
	case verb == "cancel":
		path = "/api/resources/leases/" + url.PathEscape(pos[0]) + "/cancel"
	}
	client, ok := leaseClientT("lease", *cfgPath, stderr, leaseReleaseTimeout, clientOpts)
	if !ok {
		return ExitError
	}
	rctx, cancel := context.WithTimeout(ctx, leaseReleaseTimeout)
	defer cancel()
	var resp resources.PositionResponse
	if _, err := client.Once(rctx, http.MethodPost, path, resources.CallerRequest{HolderPID: os.Getpid()}, &resp); err != nil {
		return reportVerbFailure(err, stderr)
	}
	switch {
	case verb == "confirm":
		fmt.Fprintln(stdout, "pdx lease: 已確認，背景指令開始執行")
	case verb == "cancel":
		fmt.Fprintln(stdout, "pdx lease: 已撤銷")
	case resp.DonePending:
		fmt.Fprintln(stdout, "pdx lease: 位置會在最後一個指令結束後放掉")
	default:
		fmt.Fprintln(stdout, "pdx lease: 位置已放掉")
	}
	return ExitOK
}

// reportVerbFailure prints why a verb was refused and picks the exit code: 13
// for a refusal of the daemon (403, 409, and a request or position that is not
// there), 20/21 when the daemon cannot be used, 1 otherwise.
func reportVerbFailure(err error, stderr io.Writer) int {
	var se *daemonclient.StatusError
	switch {
	case errors.As(err, &se) && (se.Status == http.StatusForbidden):
		fmt.Fprintln(stderr, "pdx lease: 這不是你這個 session 的請求")
		return ExitRefused
	case errors.As(err, &se) && se.Status == http.StatusNotFound && (se.API.Error == resources.ErrNoLease || se.API.Error == resources.ErrNoPosition):
		fmt.Fprintln(stderr, "pdx lease: 找不到這個請求（可能已逾時或已取消）")
		return ExitRefused
	case errors.As(err, &se) && se.Status == http.StatusConflict:
		fmt.Fprintf(stderr, "pdx lease: %s\n", verbConflictText(se.API.Error))
		return ExitRefused
	case errors.Is(err, daemonclient.ErrUnsupported):
		fmt.Fprintln(stderr, "pdx lease: 這個 daemon 還沒有這個功能，請先更新 daemon")
		return ExitUnsupported
	case errors.Is(err, daemonclient.ErrUnavailable):
		fmt.Fprintln(stderr, "pdx lease: daemon 連不上")
		return ExitUnavailable
	}
	fmt.Fprintf(stderr, "pdx lease: 失敗（%s）\n", leaseFailReason(err))
	return ExitError
}

// verbConflictText is what a 409 means to the caller. An unknown code is shown
// quoted so it cannot pass for the line's own structure.
func verbConflictText(code string) string {
	switch code {
	case resources.ErrNotYourTurn:
		return "還沒輪到你，現在不能確認"
	case resources.ErrRunning:
		return "這個指令正在執行，不能撤銷"
	}
	return fmt.Sprintf("被拒絕（%q）", code)
}
