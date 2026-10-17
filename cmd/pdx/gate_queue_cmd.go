package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wake/purdex/internal/gate"
)

// `pdx gate queue` (spec §5.2, #3034): the queue order. With no rank word it lists the waiting commands of both classes in the order
// they are served; `front|back|hold|normal <pid or ticket>… --reason "<line>" [--until <duration or time>]` moves them. Nothing is
// cancelled: for the owner of a moved command it is only a longer or shorter wait.

const gateQueueUsage = `usage: pdx gate queue [--class heavy|light] [--json] [--config <file>]
       pdx gate queue front|back|hold|normal <pid or ticket>… --reason "<one line>" [--until <duration or RFC 3339 time>] [--config <file>]
  front   served before the normal ones (in the order they were moved)
  back    served after the normal ones
  hold    not served, and not counted ahead of anybody, until set back to normal or --until passes (--until only with hold)
  normal  back in arrival order
  A pid names that gate's ticket in either class. The reason is required. Prints each ticket's old → new rank.
`

const maxReasonBytes = 200

func gateQueue(args []string, stdout, stderr io.Writer) int {
	var (
		rank                          gate.Rank
		keys                          []string
		class, reason, untilText, cfg string
		asJSON                        bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-"), "=")
		isFlag := strings.HasPrefix(a, "-") && len(a) > 1
		value := func() (string, bool) {
			if hasVal {
				return val, true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case !isFlag && rank == "" && len(keys) == 0 && (a == "front" || a == "back" || a == "hold" || a == "normal"):
			rank = gate.Rank(a)
		case !isFlag:
			keys = append(keys, a)
		case name == "json":
			asJSON = true
		case name == "class" || name == "reason" || name == "until" || name == "config":
			v, ok := value()
			if !ok {
				fmt.Fprintf(stderr, "pdx gate queue: --%s needs a value\n", name)
				return 2
			}
			switch name {
			case "class":
				class = v
			case "reason":
				reason = v
			case "until":
				untilText = v
			case "config":
				cfg = v
			}
		default:
			fmt.Fprint(stderr, gateQueueUsage)
			return 2
		}
	}
	g := gate.New(loadGateConfig(cfg, stderr))
	if rank == "" {
		if len(keys) > 0 || reason != "" || untilText != "" {
			fmt.Fprint(stderr, gateQueueUsage)
			return 2
		}
		return gateQueueList(g, class, asJSON, stdout, stderr)
	}
	if len(keys) == 0 {
		fmt.Fprint(stderr, gateQueueUsage)
		return 2
	}
	if asJSON || class != "" {
		fmt.Fprintln(stderr, "pdx gate queue: --json and --class are for listing")
		return 2
	}
	reason = strings.TrimSpace(reason)
	switch {
	case reason == "":
		fmt.Fprintln(stderr, "pdx gate queue: --reason is required (one line: why this ticket is moved)")
		return 2
	case len(reason) > maxReasonBytes || strings.ContainsFunc(reason, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		fmt.Fprintf(stderr, "pdx gate queue: --reason must be one line of at most %d bytes with no control characters\n", maxReasonBytes)
		return 2
	}
	var until time.Time
	if untilText != "" {
		if rank != gate.RankHold {
			fmt.Fprintln(stderr, "pdx gate queue: --until goes with hold only")
			return 2
		}
		var err error
		if until, err = parseUntil(untilText, time.Now()); err != nil {
			fmt.Fprintf(stderr, "pdx gate queue: --until: %v\n", err)
			return 2
		}
	}
	failed := false
	for _, key := range keys {
		found := g.FindWaiters(key)
		if len(found) == 0 {
			fmt.Fprintf(stderr, "%s: no such waiting ticket or gate\n", key)
			failed = true
			continue
		}
		for _, w := range found {
			old, err := g.MoveTicket(w.Class, w.Ticket, rank, g.RankBy(), reason, until)
			switch {
			case errors.Is(err, gate.ErrNotWaiting):
				fmt.Fprintf(stderr, "%s: no longer waiting\n", w.Ticket)
				failed = true
			case err != nil:
				fmt.Fprintf(stderr, "%s: %v\n", w.Ticket, err)
				failed = true
			default:
				fmt.Fprintf(stdout, "%s (pid %d, %s): %s → %s\n", w.Ticket, w.PID, w.Class, old.Rank, rank)
			}
		}
	}
	if failed {
		return 1
	}
	return 0
}

// parseUntil reads a duration ("30m") or an RFC 3339 time; it must lie in the future.
func parseUntil(s string, now time.Time) (time.Time, error) {
	var t time.Time
	if d, err := time.ParseDuration(s); err == nil {
		t = now.Add(d)
	} else if t, err = time.Parse(time.RFC3339, s); err != nil {
		return time.Time{}, fmt.Errorf("%q is neither a duration (30m) nor an RFC 3339 time", s)
	}
	if !t.After(now) {
		return time.Time{}, fmt.Errorf("%q is not in the future", s)
	}
	return t, nil
}

func gateQueueList(g *gate.Gate, class string, asJSON bool, stdout, stderr io.Writer) int {
	if class != "" && class != "heavy" && class != "light" {
		fmt.Fprintf(stderr, "pdx gate queue: --class must be heavy or light, got %q\n", class)
		return 2
	}
	heavy, light := g.Queue()
	if class == "heavy" {
		light = nil
	}
	if class == "light" {
		heavy = nil
	}
	if asJSON {
		b, _ := json.MarshalIndent(map[string][]gate.Waiter{"heavy": nonNil(heavy), "light": nonNil(light)}, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if len(heavy)+len(light) == 0 {
		fmt.Fprintln(stdout, "nobody is waiting")
		return 0
	}
	for _, ws := range [][]gate.Waiter{heavy, light} {
		for _, w := range ws {
			fmt.Fprintf(stdout, "%-5s %2d  %-6s pid %-6d %s\n", w.Class, w.Place, w.Rank, w.PID, strings.Join(w.Argv, " "))
			fmt.Fprintf(stdout, "        ticket %s%s\n", w.Ticket, rankNote(w))
		}
	}
	return 0
}

func nonNil(ws []gate.Waiter) []gate.Waiter {
	if ws == nil {
		return []gate.Waiter{}
	}
	return ws
}

// rankNote says in one line who moved a ticket and why, and a broken rank file.
func rankNote(w gate.Waiter) string {
	var b strings.Builder
	if w.RankBy != "" || w.RankReason != "" {
		fmt.Fprintf(&b, "  [%s by %s: %s", w.Rank, w.RankBy, w.RankReason)
		if !w.HeldUntil.IsZero() {
			fmt.Fprintf(&b, "; until %s", w.HeldUntil.Local().Format("15:04:05"))
		}
		b.WriteString("]")
	}
	if w.RankError != "" {
		fmt.Fprintf(&b, "  [rank file: %s]", w.RankError)
	}
	return b.String()
}
