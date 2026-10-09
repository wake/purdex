// cmd/pdx/adopt_remote.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// A remote adopt (cross-host team spec §4.3): the approval means the user consented, not that the session joined. The CLI
// then waits on the MEMBERSHIP — GET /api/team/adoptions/{approval_id} — and exits 0 when it is active, 13 when it failed
// (the code last on stderr), 14 when the 10 minute void gave up on an unreachable host, 11 when its own bound (--wait,
// counted from the start of the command) runs out while it is still joining.

// adoptionPollWaitS is the long-poll the CLI asks the daemon for each round.
const adoptionPollWaitS = 25

// adoptWaitMembership waits for the membership of the approved remote adopt ap and maps it to output and exit code.
func adoptWaitMembership(ctx context.Context, client *daemonclient.Client, ap team.Approval, p team.AdoptPayload, deadline time.Time, stdout, stderr io.Writer) int {
	fmt.Fprintf(stderr, "已核准；等 %s 收進 team（%s）\n", sanitizeCell(firstNonBlank(p.TargetHostAlias, p.TargetHostID)), ap.ID)
	hung := 0
	for {
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "pdx adopt: 已停止等待；收進仍會繼續（%s）\n", ap.ID)
			return ExitCancelled
		}
		left := time.Until(deadline)
		if left <= 0 {
			fmt.Fprintln(stderr, "pdx adopt: 等待收進逾時，成員仍在 joining（背景會繼續）")
			return ExitTimeout
		}
		wait := min(adoptionPollWaitS, int(left/time.Second)+1)
		var ad team.Adoption
		_, err := client.Do(ctx, http.MethodGet, fmt.Sprintf("%s%s?wait=%d", team.AdoptionsRoute, ap.ID, wait), nil, &ad)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			if errors.Is(err, daemonclient.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded) {
				if hung++; hung >= leadMaxHungPolls {
					fmt.Fprintln(stderr, "pdx adopt: daemon 沒有回應")
					return ExitUnavailable
				}
				continue
			}
			return adoptReportErr(err, stderr)
		}
		hung = 0
		switch ad.State {
		case team.AdoptionJoining:
			continue
		case team.AdoptionActive:
			out, err := json.Marshal(adoptOutput{RequestID: ap.ID, TeamID: p.TeamID, Ref: p.TargetRef, Address: p.TargetAddress, SessionID: p.TargetSessionID})
			if err != nil {
				fmt.Fprintf(stderr, "pdx adopt: %v\n", err)
				return ExitError
			}
			fmt.Fprintln(stdout, string(out))
			return ExitOK
		case team.AdoptionVoid:
			fmt.Fprintln(stderr, "pdx adopt: 遠端主機十分鐘內都連不上，收進已作廢 "+sanitizeCell(firstNonBlank(ad.Code, "remote_unreachable")))
			return ExitMemberFailed
		default: // failed{code}, or a membership that already ended again
			fmt.Fprintf(stderr, "pdx adopt: 遠端收進失敗 %s\n", sanitizeCell(firstNonBlank(ad.Code, ad.State)))
			return ExitRefused
		}
	}
}

func firstNonBlank(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
