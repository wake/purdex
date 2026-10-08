package team

import (
	"fmt"
	"strconv"
	"strings"
)

// ReportUpPrefixFmt is the first line of the up message (plan "Messages"): the
// kind, the task's display id and the summary. Like the down message it never
// starts with "[pdx team]" or "[pdx-relay".
const ReportUpPrefixFmt = "[report %s %s] %s"

// ReportUpMessage composes what `pdx report` sends the lead, and what
// `pdx report show --message` prints so that a failed send can be repeated by
// hand. One composer, so the two cannot drift. Layout: the header line, the
// kind's fields one per line (needs, pr, reviews, sha), then the body after a
// blank line when it has any text.
func ReportUpMessage(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, ReportUpPrefixFmt, r.Kind, r.Task, r.Summary)
	if r.Needs != "" {
		b.WriteString("\nneeds: " + r.Needs)
	}
	if r.PR != 0 {
		b.WriteString("\npr: #" + strconv.Itoa(r.PR))
	}
	if len(r.Reviews) > 0 {
		b.WriteString("\nreviews: " + strings.Join(r.Reviews, " "))
	}
	if r.SHA != "" {
		b.WriteString("\nsha: " + r.SHA)
	}
	if body := strings.TrimRight(r.Body, " \t\r\n"); strings.TrimSpace(body) != "" {
		b.WriteString("\n\n" + body)
	}
	return b.String()
}
