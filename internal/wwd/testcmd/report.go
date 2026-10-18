package testcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Report is what `pdx test` prints (as JSON with --json): the end of one job, from the queue or from the fast lane.
type Report struct {
	ID       string        `json:"id"`
	Task     string        `json:"task"`
	State    wire.JobState `json:"state"`
	Exit     *int          `json:"exit"`
	Reason   string        `json:"reason"`
	Host     string        `json:"host"`
	CacheHit bool          `json:"cache_hit"`
	Local    bool          `json:"local"`
	Duration float64       `json:"duration_s"`
	Summary  *wire.Summary `json:"summary"`
	LogPath  string        `json:"log_path,omitempty"` // a fast-lane run's log on this host
	Notes    []string      `json:"notes,omitempty"`    // things the caller should know (the run was not recorded, …)
}

// ReportOfJob is the report of a queue job in a terminal state.
func ReportOfJob(j wire.Job) Report {
	r := Report{ID: j.ID, Task: j.Task, State: j.State, Local: false}
	if res := j.Result; res != nil {
		r.Exit, r.Reason, r.Host, r.CacheHit, r.Summary = res.Exit, res.Reason, res.Host, res.CacheHit, res.Summary
		if res.StartedAt != nil && res.FinishedAt != nil {
			r.Duration = res.FinishedAt.Sub(*res.StartedAt).Seconds()
		}
	}
	return r
}

// ExitCode is the process exit code for a finished job (spec §8.1): the command's own exit code when there is one, 124 for a
// timeout, 130 for a cancel or a supersede, 6 for an expiry, 1 for a failure that has no exit code of its own.
func ExitCode(r Report) int {
	switch r.State {
	case wire.StateSucceeded:
		return ExitOK
	case wire.StateCancelled, wire.StateSuperseded:
		return ExitCancelled
	case wire.StateExpired:
		return ExitExpired
	case wire.StateFailed:
		if r.Reason == "timeout" {
			return ExitTimeout
		}
		// The command's own number, unless it is one of ours: 3 to 6 and 124 and up mean something else to the caller (wwd
		// unreachable, usage, no host, expired, timeout, cancelled), so a failed command that exits with one of them is a plain
		// 1. The report keeps the command's real number in `exit`. A command that was killed has no number of its own either.
		if r.Exit != nil && *r.Exit > 0 && !reservedExit(*r.Exit) {
			return *r.Exit
		}
		return ExitFailed
	}
	return ExitFailed
}

// reservedExit reports whether an exit number belongs to `pdx test` itself (spec §8.1).
func reservedExit(n int) bool {
	switch {
	case n >= ExitTimeout: // 124 timeout, 130 cancelled, and the killed-by-signal range 128+
		return true
	case n >= ExitNoWWD && n <= ExitExpired: // 3 wwd unreachable, 4 usage, 5 no host, 6 expired
		return true
	}
	return false
}

const maxListedFailures = 10

// Print writes the report: one JSON object with asJSON, otherwise a short text an agent can read at a glance.
func Print(w io.Writer, r Report, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
		return
	}
	where := r.Host
	switch {
	case r.Local:
		where = "this host, local fast lane"
	case where == "":
		where = "-"
	}
	head := fmt.Sprintf("%s  %s  %s", r.Task, r.State, r.ID)
	if r.ID == "" {
		head = fmt.Sprintf("%s  %s", r.Task, r.State)
	}
	extra := []string{where}
	if r.Duration > 0 {
		extra = append(extra, (time.Duration(r.Duration * float64(time.Second))).Round(100*time.Millisecond).String())
	}
	if r.CacheHit {
		extra = append(extra, "result reused")
	}
	if r.State == wire.StateFailed && r.Exit != nil {
		extra = append(extra, fmt.Sprintf("exit %d", *r.Exit)) // the command's own number, whatever pdx exits with
	}
	if r.Reason != "" {
		extra = append(extra, r.Reason)
	}
	fmt.Fprintf(w, "%s  (%s)\n", head, strings.Join(extra, ", "))
	if s := r.Summary; s != nil {
		if s.Passed+s.Failed+s.Skipped > 0 {
			fmt.Fprintf(w, "passed %d  failed %d  skipped %d\n", s.Passed, s.Failed, s.Skipped)
		}
		for i, f := range s.Failures {
			if i == maxListedFailures {
				fmt.Fprintf(w, "… and %d more failures\n", len(s.Failures)-maxListedFailures)
				break
			}
			loc := f.File
			if loc != "" {
				loc = "  [" + loc + "]"
			}
			fmt.Fprintf(w, "FAIL %s%s\n", f.Name, loc)
			if m := firstLines(f.Message, 3); m != "" {
				fmt.Fprintf(w, "     %s\n", strings.ReplaceAll(m, "\n", "\n     "))
			}
		}
		if r.State != wire.StateSucceeded && len(s.Failures) == 0 && len(s.Tail) > 0 {
			fmt.Fprintln(w, "last output:")
			for _, l := range s.Tail {
				fmt.Fprintf(w, "  %s\n", l)
			}
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	switch {
	case r.LogPath != "":
		fmt.Fprintf(w, "log: %s\n", r.LogPath)
	case r.ID != "" && r.State != wire.StateExpired:
		fmt.Fprintf(w, "log: pdx jobs log %s\n", r.ID)
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	return strings.Join(lines, "\n")
}
