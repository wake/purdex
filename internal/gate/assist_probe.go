package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// remoteProbe is the one ssh made right before a send (plan contracts 9 and 10; args: the command's argv). It says whether the
// host's own gate would start the command now, the load of the host and its CPU idle. macOS only for now: another system answers
// OTHER and is not used (mserv's probe comes with X3).
const remoteProbe = `. ~/Assist/assist.env 2>/dev/null || true
case "$(uname)" in
  Darwin)
    echo "WW $(pdx gate would-wait --auto --json -- "$@" 2>/dev/null | tr -d '\n')"
    echo "LOAD $(sysctl -n vm.loadavg | tr -d '{}') $(sysctl -n hw.ncpu)"
    echo "IDLE $(top -l 2 -s 1 -n 0 | grep 'CPU usage' | tail -1 | awk '{for(i=1;i<=NF;i++) if ($i=="idle") print $(i-1)}' | tr -d '%')"
    ;;
  *) echo OTHER ;;
esac
`

const (
	defaultLoadLimit = 1.0
	defaultIdleMin   = 30
	defaultHoldMin   = 5
	probeTimeout     = 30 * time.Second
)

// AssistProbe is what the one ssh found out.
type AssistProbe struct {
	OK   bool          // the host would start the command now and is within its limits
	Why  string        // one line, when not OK
	Hold time.Duration // how long to keep the host out of the walk (0: only not now)
}

// ProbeAssist asks the host whether to send now. It does not run anything on it and holds nothing itself: the caller writes the hold.
// The host is not a candidate when: the ssh fails (held for the usual failure time), its gate would make the command wait (not held:
// the ticket tries again at the next look), or its load1 or load5 per core is over LoadLimit or its CPU idle under IdleMin (held for
// HoldMin: a boot not yet in load1 would be missed otherwise).
func (g *Gate) ProbeAssist(ctx context.Context, a AssistRun, h AssistHost, argv []string) AssistProbe {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	logical := a.Host // the name the host is known by; the ssh alias is the record's SSH (the scripts use it as ASSIST_HOST)
	if h.SSH != "" {
		a.Host = h.SSH
	}
	var out, errb bytes.Buffer
	cmd := a.sshCmd(ctx, remoteProbe, argv...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return AssistProbe{Why: fmt.Sprintf("ssh to %s failed: %v", logical, err), Hold: holdAfterFailure}
	}
	fields := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), " ")
		fields[k] = strings.TrimSpace(v)
	}
	if _, other := fields["OTHER"]; other {
		return AssistProbe{Why: logical + " has no load probe for its system yet", Hold: holdAfterFailure}
	}
	var ww WouldWait
	if json.Unmarshal([]byte(fields["WW"]), &ww) != nil {
		return AssistProbe{Why: logical + " did not answer would-wait", Hold: holdAfterFailure}
	}
	if ww.Wait {
		return AssistProbe{Why: fmt.Sprintf("%s would make the command wait (%s)", logical, ww.Reason)}
	}
	load := strings.Fields(fields["LOAD"])
	idle, errIdle := strconv.ParseFloat(fields["IDLE"], 64)
	if len(load) < 4 || errIdle != nil {
		return AssistProbe{Why: logical + " did not answer its load", Hold: holdAfterFailure}
	}
	l1, e1 := strconv.ParseFloat(load[0], 64)
	l5, e5 := strconv.ParseFloat(load[1], 64)
	cores, e3 := strconv.ParseFloat(load[3], 64)
	// a number that is not a number (NaN), infinite, negative or out of range is no answer: it must not pass every limit
	if e1 != nil || e5 != nil || e3 != nil || !finite(l1, l5, cores, idle) || l1 < 0 || l5 < 0 || cores < 1 || idle < 0 || idle > 100 {
		return AssistProbe{Why: logical + " did not answer its load", Hold: holdAfterFailure}
	}
	limit, idleMin, hold := h.LoadLimit, float64(h.IdleMin), time.Duration(h.HoldMin)*time.Minute
	if limit <= 0 {
		limit = defaultLoadLimit
	}
	if idleMin <= 0 {
		idleMin = defaultIdleMin
	}
	if hold <= 0 {
		hold = defaultHoldMin * time.Minute
	}
	switch {
	case l1/cores > limit || l5/cores > limit:
		return AssistProbe{Why: fmt.Sprintf("%s is busy (load %.2f / %.2f per %d cores, limit %.2f)", logical, l1, l5, int(cores), limit), Hold: hold}
	case idle < idleMin:
		return AssistProbe{Why: fmt.Sprintf("%s is busy (CPU idle %.0f%%, at least %.0f%% wanted)", logical, idle, idleMin), Hold: hold}
	}
	return AssistProbe{OK: true}
}

func finite(xs ...float64) bool {
	for _, x := range xs {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}
