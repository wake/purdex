package agent

import (
	"regexp"
	"runtime"
	"strings"
)

// A ps fork is counted under a tag that says what it asked and who asked (#3316): "agent.ps.<what>@<who>", for example
// "agent.ps.start@module/agent.verifyEvent". Before, every fork of this package was one tag, "agent.ps", and 76 a minute could not be
// traced to a caller. The number of tags stays small: <what> is one of five words and <who> is a function of the daemon, not of an
// argument or a pid.

const psTagPrefix = "agent.ps"

// psWhat is what a ps invocation asks, from its arguments: the process table (-A…), or one pid's start time, parent, command name or
// arguments. Anything else is "other".
func psWhat(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-A") || strings.HasPrefix(a, "-ax") || a == "-e" {
			return "table"
		}
	}
	for i, a := range args {
		if a == "-o" && i+1 < len(args) {
			switch strings.TrimSuffix(args[i+1], "=") {
			case "lstart":
				return "start"
			case "ppid":
				return "ppid"
			case "comm":
				return "comm"
			case "args":
				return "args"
			}
		}
	}
	return "other"
}

const psModulePrefix = "github.com/wake/purdex/internal/"

// psAgentPkg is the prefix of the functions of this package (the dot keeps internal/agent/probe, a caller worth naming, out of it).
const psAgentPkg = psModulePrefix + "agent."

// psCaller is the function that asked for the fork: the first frame of the stack that is not this package, the runtime or the counter.
// A fork made on a goroutine this package started itself (the start-text recalibration every 15 s) has only this package's frames, so
// the outermost of them — the goroutine's own function — names it. "" only when the stack has no function of the daemon at all.
func psCaller() string {
	var pcs [24]uintptr
	n := runtime.Callers(3, pcs[:]) // psCaller, the tag's caller, then runPS
	frames := runtime.CallersFrames(pcs[:n])
	outermostAgent := ""
	for {
		f, more := frames.Next()
		fn := f.Function
		switch {
		case fn == "" || strings.HasPrefix(fn, "runtime.") || strings.HasPrefix(fn, psModulePrefix+"execstat."):
		case strings.HasPrefix(fn, psAgentPkg):
			outermostAgent = fn
		default:
			return psShortCaller(fn)
		}
		if !more {
			if outermostAgent != "" {
				return psShortCaller(outermostAgent)
			}
			return ""
		}
	}
}

var (
	psRecvRe     = regexp.MustCompile(`\(\*?[A-Za-z0-9_]+\)\.`) // (*Module). or (Module).
	psClosureRe  = regexp.MustCompile(`(\.func[0-9]+)+(\.[0-9]+)*$`)
	psTagCleanRe = regexp.MustCompile(`[^A-Za-z0-9_./-]`)
)

// psShortCaller turns a full function name into a short tag part: the module path is cut, the receiver and closure numbers dropped.
// "github.com/wake/purdex/internal/module/agent.(*Module).verifyEvent.func1" → "module/agent.verifyEvent".
func psShortCaller(fn string) string {
	s := strings.TrimPrefix(fn, psModulePrefix)
	s = psRecvRe.ReplaceAllString(s, "")
	s = psClosureRe.ReplaceAllString(s, "")
	s = psTagCleanRe.ReplaceAllString(s, "_")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// psTag is the execstat tag of one ps fork.
func psTag(args []string) string {
	tag := psTagPrefix + "." + psWhat(args)
	if who := psCaller(); who != "" {
		tag += "@" + who
	}
	return tag
}
