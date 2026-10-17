package gate

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// What a waiting gate tells the one who waits (mostly an agent: it must not take a queue for a hang and kill the command or
// run it another way). The first look and every minute after: who holds the slot, where, for how long, how many are ahead,
// and that the wait is normal.

const normalQueue = "This is a normal queue: it starts by itself when that ends; do not interrupt it or run the command another way."
const normalPressureWait = "This is a normal wait: it starts by itself when the pressure is over; do not interrupt it or run the command another way."

// fmtDur is a duration for people: 45s, 12m, 1h05m.
func fmtDur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

// projectOf is the last two parts of a directory (purdex/spa), the cheapest name for "where".
func projectOf(cwd string) string {
	cwd = filepath.Clean(cwd)
	if cwd == "" || cwd == "." || cwd == "/" {
		return cwd
	}
	parts := strings.Split(strings.Trim(cwd, "/"), "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}

// holderLine says what holds a slot: the command (already cut and scrubbed in the record), its project and its age.
func holderLine(h Holder, now time.Time) string {
	cmd := strings.Join(h.Argv, " ")
	if cmd == "" {
		return "another command"
	}
	if len(cmd) > 100 {
		cmd = cmd[:100] + "…"
	}
	var b strings.Builder
	if !h.Started.IsZero() {
		fmt.Fprintf(&b, "held %s by ", fmtDur(now.Sub(h.Started)))
	} else {
		b.WriteString("held by ")
	}
	fmt.Fprintf(&b, "%q", cmd)
	if h.Cwd != "" {
		fmt.Fprintf(&b, " (%s)", projectOf(h.Cwd))
	}
	if h.Orphaned {
		b.WriteString(" (its gate is gone; the processes it started are still running)")
	}
	return b.String()
}

// waitingLine is the line for a command that waits for a slot of a class; ahead counts the commands in front of it, the
// running ones and the queued ones.
func waitingLine(class Class, ahead int, holders []Holder, now time.Time) string {
	var held string
	switch len(holders) {
	case 0:
		held = "no slot is held right now, the queue is being served"
	default:
		oldest := holders[0]
		for _, h := range holders[1:] {
			if !h.Started.IsZero() && (oldest.Started.IsZero() || h.Started.Before(oldest.Started)) {
				oldest = h
			}
		}
		held = holderLine(oldest, now)
		if len(holders) > 1 {
			held += fmt.Sprintf(" (+%d more)", len(holders)-1)
		}
	}
	return fmt.Sprintf("waiting for a %s slot — %d ahead; %s. %s", class, ahead, held, normalQueue)
}

// heldLine is the line for a command whose ticket was put on hold (queue order, spec §5.2): who did it, why, until when, and that
// nothing is cancelled.
func heldLine(class Class, r RankInfo, now time.Time) string {
	by := r.By
	if by == "" {
		by = "someone"
	}
	until := "until it is moved again"
	if !r.Until.IsZero() {
		until = "for " + fmtDur(r.Until.Sub(now))
	}
	return fmt.Sprintf("this command is on hold in the %s queue (held by %s: %s; %s): it waits and starts by itself afterwards, nothing is cancelled. %s",
		class, by, r.Reason, until, normalQueue)
}

// withFrontNote adds to a waiting line how many of the commands in front were moved there.
func withFrontNote(line string, aheadFront int) string {
	if aheadFront <= 0 {
		return line
	}
	return strings.Replace(line, " ahead;", fmt.Sprintf(" ahead, behind %d moved to the front;", aheadFront), 1)
}

// pausedLine is the line for a command of a class whose slot count is 0.
func pausedLine(class Class) string {
	return fmt.Sprintf("the %s class is paused (slots.%s = 0): this command waits and starts by itself once the count is raised. %s", class, class, normalQueue)
}

// pressureLine is the line for a command held back by the memory pressure, with the numbers.
func pressureLine(p Pressure, a AdmissionConfig, haveReading bool) string {
	if !haveReading {
		return "waiting: the memory pressure of the host could not be read yet. " + normalPressureWait
	}
	return fmt.Sprintf("waiting: the host is under memory pressure (level %d, %.0f pages/s paged out; new work starts at level %d or below and under %.0f pages/s). %s",
		p.Level, p.PageRate, a.MaxPressureLevel, a.PageRateLimit(), normalPressureWait)
}

// grantedLine is the line for a command that got its slot after waiting.
func grantedLine(class Class, waited time.Duration) string {
	return fmt.Sprintf("got a %s slot after %s", class, fmtDur(waited))
}
