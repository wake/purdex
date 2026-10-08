package team

import (
	"fmt"
	"strings"
)

// The task down message (plan "Messages"): what `pdx task add` and `reassign`
// send the owner, and what `pdx task show --message` prints so a failed send
// can be repeated by hand. One composer, so the three cannot drift.
//
// It starts with "[pdx task", never with the team notice's "[pdx team]" or
// the relay's "[pdx-relay": a receiving session tells the three apart by that
// prefix.
const (
	// TaskDownPrefixFmt is the first line; the arguments are the task's
	// display id and its subject.
	TaskDownPrefixFmt = "[pdx task %s] %s"
	// TaskDownDoneWhenLabel heads the done-when lines, one "- <line>" each.
	TaskDownDoneWhenLabel = "完成定義："
	// TaskDownReportFmt is the last line; the argument is the display id.
	TaskDownReportFmt = "回報：pdx report ack|progress|ready|done --task %s --summary \"…\"（見 pdx-team skill）"

	// TaskWorstCaseID is the longest display id there is (six hex chars, a
	// dash, the largest 32-bit seq): the CLI composes a task's message with
	// it before the daemon has assigned the real id.
	TaskWorstCaseID = "ffffff-2147483647"
)

// TaskDownMessage composes the down message of t from its display id,
// subject, description and done-when lines. Layout: the header line, a blank
// line, the description and a blank line (when there is one), the
// done-when block and a blank line (when there are lines), the report line.
// A description's trailing line breaks and a blank one are not carried, so a
// brief file's final newline adds no blank line.
func TaskDownMessage(t Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, TaskDownPrefixFmt, t.ID, t.Subject)
	b.WriteString("\n\n")
	if d := strings.TrimRight(t.Description, " \t\r\n"); strings.TrimSpace(d) != "" {
		b.WriteString(d)
		b.WriteString("\n\n")
	}
	if len(t.DoneWhen) > 0 {
		b.WriteString(TaskDownDoneWhenLabel)
		for _, l := range t.DoneWhen {
			b.WriteString("\n- ")
			b.WriteString(l)
		}
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, TaskDownReportFmt, t.ID)
	return b.String()
}
