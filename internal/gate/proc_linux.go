//go:build linux

package gate

import (
	"os"
	"strconv"
	"strings"
)

// procTable reads every process from /proc: its parent and its start time in clock ticks since boot.
func procTable() (map[int]procInfo, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	out := make(map[int]procInfo, len(ents))
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// pid (comm) state ppid …: comm may hold spaces and parentheses, so cut after the last ')'
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 20 {
			continue
		}
		ppid, err1 := strconv.Atoi(f[1])
		start, err2 := strconv.ParseInt(f[19], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out[pid] = procInfo{PPID: ppid, Start: start}
	}
	return out, nil
}
