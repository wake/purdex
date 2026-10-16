package gate

import "strconv"

// Module 4, priority: how a heavy command is scheduled against the rest of the host. nice leaves the agents, the daemon
// and the person ahead of it and still lets it use idle cores (measured in G0: taskpolicy -b ran 10 to 45 % slower
// because it also throttles disk I/O and pins the command to the efficiency cores).
type PriorityConfig struct {
	Mode string // "none", "nice" or "background" (taskpolicy -b)
	Nice int    // the niceness for mode nice
}

func parsePriority(r raw) PriorityConfig {
	return PriorityConfig{
		Mode: r.Enum("mode", "nice", "none", "nice", "background"),
		Nice: r.Int("nice", 10, 1, 20),
	}
}

// Tools are the paths of the helpers a mode needs; an empty one is a host without it.
type Tools struct {
	Nice       string
	Taskpolicy string
}

// Wrap puts the scheduling helper in front of the command words. A mode whose helper is missing leaves the command as it
// is. foreground (--fg) leaves it out on purpose.
func (p PriorityConfig) Wrap(args []string, t Tools, foreground bool) []string {
	if foreground {
		return args
	}
	switch p.Mode {
	case "nice":
		if t.Nice != "" {
			return append([]string{t.Nice, "-n", strconv.Itoa(p.Nice)}, args...)
		}
	case "background":
		if t.Taskpolicy != "" {
			return append([]string{t.Taskpolicy, "-b"}, args...)
		}
	}
	return args
}
