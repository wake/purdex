package gate

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// lsofHolders names the processes that have path open, by asking lsof (-F: p<pid> and c<command> lines). It is best
// effort: without lsof, or when it is slow, nobody is named and the slot is released all the same.
func lsofHolders(path string) []Straggler {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-F", "pc", "--", path).Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	return parseLsof(string(out))
}

func parseLsof(out string) []Straggler {
	var res []Straggler
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			if pid, err := strconv.Atoi(line[1:]); err == nil {
				res = append(res, Straggler{PID: pid})
			}
		case 'c':
			if n := len(res); n > 0 {
				res[n-1].Cmd = line[1:]
			}
		}
	}
	return res
}

func describeStragglers(l []Straggler) string {
	parts := make([]string, 0, len(l))
	for _, p := range l {
		parts = append(parts, strconv.Itoa(p.PID)+" ("+p.Cmd+")")
	}
	return strings.Join(parts, ", ")
}
