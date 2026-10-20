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

// gateToolDepth is env with the depth of tool raised by one (for the child), and the depth this process found.
func gateToolDepth(env []string, tool string) ([]string, int) {
	var pairs []string
	depth := 0
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, gateToolDepthEnv+"="); ok {
			pairs = strings.Split(v, ",")
			continue
		}
		out = append(out, kv)
	}
	found := false
	for i, p := range pairs {
		name, n, ok := strings.Cut(p, "=")
		if !ok || name != tool {
			continue
		}
		d, err := strconv.Atoi(n)
		if err != nil || d < 0 {
			d = gateToolDepthMax // a value that is not a count from 0 up is not to be trusted: the fuse is blown
		}
		depth = d
		pairs[i] = name + "=" + strconv.Itoa(depth+1)
		found = true
	}
	if !found {
		pairs = append(pairs, tool+"=1")
	}
	var keep []string
	for _, p := range pairs {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return append(out, gateToolDepthEnv+"="+strings.Join(keep, ",")), depth
}

func envOrCurrent(env []string) []string {
	if env == nil {
		return os.Environ()
	}
	return env
}
