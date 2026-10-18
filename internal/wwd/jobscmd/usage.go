package jobscmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/testcmd"
	"github.com/wake/purdex/internal/wwd/wire"
)

// parseSince reads --since: how far back from now (6h, 90m, 7d) or an RFC 3339 time.
func parseSince(v string, now time.Time) (time.Time, error) {
	bad := fmt.Errorf("--since must be how far back (6h, 90m, 7d) or an RFC 3339 time, got %q", v)
	if n, ok := strings.CutSuffix(v, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days <= 0 {
			return time.Time{}, bad
		}
		return now.Add(-time.Duration(days) * 24 * time.Hour), nil
	}
	if d, err := time.ParseDuration(v); err == nil {
		if d <= 0 {
			return time.Time{}, bad
		}
		return now.Add(-d), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, bad
	}
	return t, nil
}

// mark puts "~" after a figure that is the highest of the 5 s samples (or an integral of them) and not an exact reading.
func mark(f string, sampled bool) string {
	if sampled {
		return f + "~"
	}
	return f
}

// figure is a measurement as a table cell: "-" where nobody measured it (null on the wire), the number otherwise, marked "~" when it
// is a sampled one.
func figure(v *float64, sampled bool) string {
	if v == nil {
		return "-"
	}
	return mark(fmt.Sprintf("%.1f", *v), sampled)
}

func memFigure(v *int, sampled bool) string {
	if v == nil {
		return "-"
	}
	return mark(strconv.Itoa(*v), sampled)
}

func avgCell(u wire.JobUsage) string {
	if a, ok := u.AvgCores(); ok {
		return mark(fmt.Sprintf("%.1f", a), u.CPUSource == wire.CPUSampled) // worked out from the CPU seconds: as exact as they are
	}
	return "-"
}

func ownerCell(u wire.JobUsage) string {
	if u.OwnerAtStart == nil || u.OwnerSeen == nil {
		return "n/a"
	}
	start, seen := "away", ""
	if *u.OwnerAtStart {
		start = "here"
	}
	if *u.OwnerSeen {
		seen = ">seen"
	}
	return start + seen
}

// wwdUsage is `pdx wwd usage`: what this host's finished jobs used and what the host was like around them.
func wwdUsage(ctx context.Context, args []string, env Env) int {
	flags, pos, err := splitFlags(args, "json")
	if err == nil {
		err = only(flags, "json", "since", "after", "limit")
	}
	if err == nil && len(pos) > 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	var q client.UsageQuery
	if err == nil && flags["since"] != "" {
		q.Since, err = parseSince(flags["since"], env.now())
	}
	if err == nil && flags["after"] != "" {
		// the cursor of a truncated answer: --since is then the exact end time that answer gave, not a length of time
		if _, perr := time.Parse(time.RFC3339, flags["since"]); perr != nil {
			err = fmt.Errorf("--after goes with --since <the RFC 3339 time the last answer gave>")
		}
		q.After = flags["after"]
	}
	if err == nil && flags["limit"] != "" {
		var n int
		if n, err = strconv.Atoi(flags["limit"]); err != nil || n < 1 {
			err = fmt.Errorf("--limit must be a whole number above 0, got %q", flags["limit"])
		}
		q.Limit = n
	}
	if err != nil {
		return usageErr(env, WWDUsage, err)
	}
	resp, err := env.API.Usage(ctx, q)
	if err != nil {
		return fail(env, "wwd usage", err)
	}
	if flags["json"] != "" {
		writeJSON(env.Stdout, resp)
		return ExitOK
	}
	if len(resp.Records) == 0 {
		fmt.Fprintf(env.Stdout, "no finished jobs since %s\n", resp.Since.Format(time.RFC3339))
		return ExitOK
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ENDED\tJOB\tTASK\tSLOT\tSOURCE\tRESULT\tSECS\tCPU s\tAVG\tPEAK\tMEM MB\tJOBS\tZONE\tOWNER\tNOTE")
	for _, u := range resp.Records {
		note := ""
		if u.Partial {
			note = "partial"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%.0f\t%s\t%s\t%s\t%s\t%d/%d\t%s>%s\t%s\t%s\n",
			u.EndedAt.Local().Format("01-02 15:04"), testcmd.CleanLine(u.JobID), testcmd.CleanLine(u.Task), u.Slot, u.Source, u.Result,
			u.Duration().Seconds(), figure(u.CPUSeconds, u.CPUSource == wire.CPUSampled), avgCell(u),
			figure(u.PeakCores, u.PeakCoresMeasure == wire.MeasureSampled),
			memFigure(u.MemPeakMB, u.MemPeakMeasure == wire.MeasureSampled),
			u.JobsAtStart, u.JobsPeak, u.ZoneAtStart, u.ZoneWorst, ownerCell(u), note)
	}
	tw.Flush()
	fmt.Fprintln(env.Stdout, "~ = sampled (the 5 s samples: a short spike can be missed); - = not measured; other figures are exact readings. AVG = CPU s / SECS.")
	if resp.Truncated && resp.NextSince != nil && resp.NextAfter != nil {
		fmt.Fprintf(env.Stdout, "more rows: ask again with --since %s --after %s\n", resp.NextSince.Format(time.RFC3339Nano), *resp.NextAfter)
	}
	return ExitOK
}
