package main

import (
	"os"
	"strconv"
	"strings"
)

// gateToolDepthEnv carries, as tool=n pairs, how many `pdx gate --tool` levels of each tool are above this process. A tool
// that nests legitimately (go generate calling go) stays far below the limit; a shim that resolves to itself climbs fast.
const gateToolDepthEnv = "PDX_GATE_TOOL_DEPTH"

const gateToolDepthMax = 8

// gateToolDepth is env with the depth of tool raised by one (for the child), and the depth this process found. A value that is
// not a count from 0 up counts as the limit, and a tool named twice counts as the larger of its values, so no entry can lower
// what another one blew; the child gets one entry per tool.
func gateToolDepth(env []string, tool string) ([]string, int) {
	var names []string
	depths := map[string]int{}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		v, ok := strings.CutPrefix(kv, gateToolDepthEnv+"=")
		if !ok {
			out = append(out, kv)
			continue
		}
		for _, p := range strings.Split(v, ",") {
			if p == "" {
				continue
			}
			name, n, _ := strings.Cut(p, "=")
			d, err := strconv.Atoi(n)
			if err != nil || d < 0 {
				d = gateToolDepthMax
			}
			if _, seen := depths[name]; !seen {
				names = append(names, name)
			}
			depths[name] = max(depths[name], d)
		}
	}
	depth, seen := depths[tool]
	if !seen {
		names = append(names, tool)
	}
	depths[tool] = depth + 1
	pairs := make([]string, 0, len(names))
	for _, n := range names {
		pairs = append(pairs, n+"="+strconv.Itoa(depths[n]))
	}
	return append(out, gateToolDepthEnv+"="+strings.Join(pairs, ",")), depth
}

func envOrCurrent(env []string) []string {
	if env == nil {
		return os.Environ()
	}
	return env
}
