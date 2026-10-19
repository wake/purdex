package jobscmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wake/purdex/internal/wwd/testcmd"
	"github.com/wake/purdex/internal/wwd/wire"
)

// fingerprintCell is a fingerprint as one cell: key=value pairs, sorted by key; "-" when empty.
func fingerprintCell(fp map[string]string) string {
	if len(fp) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(fp))
	for k := range fp {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + fp[k]
	}
	return strings.Join(parts, " ")
}

// wwdLabels is `pdx wwd labels`: the task labels this host earned or lost to a quarantine, with the fingerprint each was earned under.
func wwdLabels(ctx context.Context, args []string, env Env) int {
	flags, pos, err := splitFlags(args, "json")
	if err == nil {
		err = only(flags, "json")
	}
	if err == nil && len(pos) > 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	if err != nil {
		return usageErr(env, WWDUsage, err)
	}
	list, known, err := env.API.EarnedLabels(ctx)
	if err != nil {
		return fail(env, "wwd labels", err)
	}
	if !known && flags["json"] == "" {
		fmt.Fprintln(env.Stdout, "this wwd predates earned labels: none yet")
		return ExitOK
	}
	if flags["json"] != "" {
		writeJSON(env.Stdout, list)
		return ExitOK
	}
	if len(list.Labels) == 0 {
		fmt.Fprintf(env.Stdout, "%s has earned no task labels\n", testcmd.CleanLine(list.Host))
		return ExitOK
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "LABEL\tSTATE\tSINCE\tFINGERPRINT")
	for _, l := range list.Labels {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", testcmd.CleanLine(l.Label), testcmd.CleanLine(l.State),
			l.GrantedAt.Local().Format(time.DateTime), testcmd.CleanLine(fingerprintCell(l.Fingerprint)))
	}
	tw.Flush()
	if hasQuarantined(list.Labels) {
		fmt.Fprintln(env.Stdout, "quarantined = kept on record but not advertised, until the task is qualified again.")
	}
	return ExitOK
}

func hasQuarantined(ls []wire.EarnedLabel) bool {
	for _, l := range ls {
		if l.State == wire.EarnedLabelQuarantined {
			return true
		}
	}
	return false
}
