package jobscmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/testcmd"
	"github.com/wake/purdex/internal/wwd/wire"
)

// wwdQualify is `pdx wwd qualify <repo>/<task>` and `pdx wwd qualify --status [--json]` (spec §7): this host is asked to earn the task's
// label by running the task and the reference host's run of it on one snapshot and comparing them. The pair is queued into this host's own
// queue and the command does not wait for it: `--status` (and `pdx jobs show` on the two job ids) say how it goes.
func wwdQualify(ctx context.Context, args []string, env Env) int {
	flags, pos, err := splitFlags(args, "status", "json")
	if err == nil {
		err = only(flags, "status", "json")
	}
	status := flags["status"] != ""
	switch {
	case err != nil:
	case status && len(pos) > 0:
		err = fmt.Errorf("--status takes no task")
	case !status && flags["json"] != "":
		err = fmt.Errorf("--json is for --status")
	case !status && len(pos) != 1:
		err = fmt.Errorf("qualify takes one task, <repo>/<task>, or --status")
	case !status && !wire.ValidTaskLabel("task:"+pos[0]):
		err = fmt.Errorf("%q is not <repo>/<task>", pos[0])
	}
	if err != nil {
		return usageErr(env, WWDUsage, err)
	}
	if status {
		return qualifyStatus(ctx, env, flags["json"] != "")
	}
	s, err := env.API.Qualify(ctx, pos[0])
	if err != nil {
		return qualifyFail(env, err)
	}
	fmt.Fprintf(env.Stdout, "queued %s: this host's run %s and the reference's %s; `pdx wwd qualify --status` says how it ends\n",
		testcmd.CleanLine(s.Label), testcmd.CleanLine(s.Candidate), testcmd.CleanLine(s.Reference))
	return ExitOK
}

func qualifyStatus(ctx context.Context, env Env, asJSON bool) int {
	st, err := env.API.QualifyStatus(ctx)
	if err != nil {
		return qualifyFail(env, err)
	}
	if asJSON {
		writeJSON(env.Stdout, st)
		return ExitOK
	}
	if len(st.InFlight) == 0 && len(st.Recent) == 0 {
		fmt.Fprintln(env.Stdout, "no qualification pairs yet")
		return ExitOK
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "LABEL\tSTATE\tAT\tJOBS\tWHY")
	for _, p := range st.InFlight {
		fmt.Fprintf(tw, "%s\trunning\t-\t%s %s\t\n", testcmd.CleanLine(p.Label), testcmd.CleanLine(p.Candidate), testcmd.CleanLine(p.Reference))
	}
	for _, r := range st.Recent {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s %s\t%s\n", testcmd.CleanLine(r.Label), testcmd.CleanLine(r.Outcome), r.At.Local().Format(time.DateTime),
			testcmd.CleanLine(r.Candidate), testcmd.CleanLine(r.Reference), testcmd.CleanLine(r.Why))
	}
	tw.Flush()
	return ExitOK
}

// qualifyFail says why a request was refused in the wwd's own words ({"error": "..."}): a task the host has no qualify for is a usage error,
// the rest (it is the reference, the worker is off, it holds the label, a pair is running, a tool is missing) is a failure of the request.
func qualifyFail(env Env, err error) int {
	var se *client.StatusError
	if errors.As(err, &se) && (se.Code == http.StatusNotFound || se.Code == http.StatusConflict || se.Code == http.StatusInternalServerError) {
		msg := strings.TrimSpace(se.Body)
		var body struct{ Error string }
		if json.Unmarshal([]byte(msg), &body) == nil && body.Error != "" {
			msg = body.Error
		}
		fmt.Fprintf(env.Stderr, "pdx wwd qualify: %s\n", testcmd.CleanLine(msg))
		if se.Code == http.StatusNotFound {
			return ExitUsage
		}
		return ExitFailed
	}
	return fail(env, "wwd qualify", err)
}
