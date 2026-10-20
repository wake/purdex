package facts

import (
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// JobMarkerEnv is the environment variable every process a wwd job starts
// carries (spec §5). Facts (procs[].owner, the air26 owner rule) and the worker
// share JobFromEnv as the one check for "this belongs to a wwd job".
const JobMarkerEnv = "WWD_JOB"

// OriginMarkerEnv is the second part of the mark: the tag of the connection the job came in on (worker/ident.OriginTag of the
// worker's source: 16 hex characters). A job id is only unique per origin, so the mark of a job is (tag, id). WWD_JOB keeps its value.
const OriginMarkerEnv = "WWD_ORIGIN"

var reJobID = regexp.MustCompile(`^j_[0-9A-Za-z_-]{1,64}$`)
var reOriginTag = regexp.MustCompile(`^[0-9a-f]{16}$`)

// MarkFromEnv returns the whole mark out of a process environment: the origin tag and the job id. ok needs a job id. A missing or
// malformed WWD_ORIGIN gives tag "": the mark of a process started before the origin was added (a command that outlived an upgrade),
// which matches any origin during the transition (#3373).
func MarkFromEnv(env []string) (tag, id string, ok bool) {
	id, ok = JobFromEnv(env)
	if !ok {
		return "", "", false
	}
	for _, kv := range env {
		if v, found := strings.CutPrefix(kv, OriginMarkerEnv+"="); found && reOriginTag.MatchString(v) {
			return v, id, true
		}
	}
	return "", id, true
}

// JobFromEnv returns the job id out of a process environment (KEY=VALUE). The
// value must look like a job id (`j_` and up to 64 id characters); anything else
// is not a marker. (Facts are observations, not authorisation: a same-user
// process can still set a well-formed one.)
func JobFromEnv(env []string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, JobMarkerEnv+"="); ok && reJobID.MatchString(v) {
			return v, true
		}
	}
	return "", false
}

// SessionEnvKey is the variable a Claude Code session puts in the environment of everything it starts. A helper that outlives its
// parent (the codex broker is reparented to pid 1) keeps it, so it still says whose it is.
const SessionEnvKey = "CLAUDE_CODE_SESSION_ID"

var reSessionID = regexp.MustCompile(`^[0-9A-Za-z_-]{8,64}$`)

// ValidSessionID says whether s looks like a session id: 8 to 64 characters of letters, digits, `_` and `-` (a Claude Code session id is a
// UUID). It is the one rule for a session id taken from an environment, here and where `pdx test` and `pdx jobs` name the submitter's.
func ValidSessionID(s string) bool { return reSessionID.MatchString(s) }

// SessionIDFromEnv returns the Claude Code session id out of a process environment (KEY=VALUE). It is the only value read from
// an environment besides the job marker, and nothing else of it is kept. Facts are observations: a same-user process can set it
// to anything (spec section 11 does not defend against that).
func SessionIDFromEnv(env []string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, SessionEnvKey+"="); ok && reSessionID.MatchString(v) {
			return v, true
		}
	}
	return "", false
}

// ProcRec is one process as the reader saw it. FootprintBytes is the
// physical footprint (what Activity Monitor calls Memory).
type ProcRec struct {
	PID, PPID, UID int
	Name           string
	CPUPct         float64
	FootprintBytes uint64
	// MemKind says what FootprintBytes is: "" is the physical footprint (macOS),
	// "rss" the resident set (Linux has no footprint).
	MemKind string
	// Start is when the process began (to the microsecond on macOS, to 10 ms on Linux); zero when the reader does not know. Set by
	// Snapshot, not by the bare table of Fresh on a host where that cannot tell it.
	Start time.Time
}

// ProcSnapshot is the whole process table at one moment.
type ProcSnapshot struct {
	Procs []ProcRec
	// Total is how many processes the reader counted in the one pass that made Procs, including the ones it then could not read or that
	// changed hands, which are not in Procs (procs_total).
	Total int
}

// ProcContext are the lookups attribution needs. JobOf reads a process's
// environment, ArgsOf its argv; both are called only for the processes that
// matter (the selected ones, and the residents scan). Table is filled from the
// snapshot when nil.
type ProcContext struct {
	Table map[int]ProcRec
	JobOf func(pid int) (string, bool)
	// OriginOf is the origin tag in the process's own mark (WWD_ORIGIN); ok is false for a process with none. Optional: a nil one means
	// every mark is the old kind.
	OriginOf  func(pid int) (string, bool)
	SessionOf func(pid int) (string, bool) // pid is a Claude Code session process: its ref
	// EnvSessionOf is the ref of the session in the process's own environment (SessionEnvKey). Optional. It is asked only for a
	// process with no session above it, and the reader reads each environment once per process (with the job marker).
	EnvSessionOf func(pid int) (string, bool)
	ArgsOf       func(pid int) []string
	// KindOf is the resident kind of a process, cached by the reader (a process's
	// kind does not change). Optional: when nil it is computed from ArgsOf.
	KindOf func(p ProcRec) string
	// Fresh reads the process table again right now: PID, PPID, Name and Start of every process, nothing per process (no figures, no argv,
	// no uid on Linux), so it is cheap enough to be asked a few times in a second. Optional: nil when the reader cannot. Like the other
	// lookups it must be called from the goroutine that took the snapshot.
	Fresh func() ([]ProcRec, error)
}

// kindOf is the resident kind of p ("" when it is not a long-lived helper).
func kindOf(p ProcRec, c ProcContext) string {
	if c.KindOf != nil {
		return c.KindOf(p)
	}
	var args []string
	if c.ArgsOf != nil {
		args = c.ArgsOf(p.PID)
	}
	return residentKind(p, args)
}

// SelectTop is the union of the top nMem processes by footprint and the top
// nCPU by CPU, each list ordered by its key, the union by pid for stability.
func SelectTop(procs []ProcRec, nMem, nCPU int) []ProcRec {
	picked := map[int]ProcRec{}
	take := func(less func(a, b ProcRec) bool, n int) {
		s := append([]ProcRec(nil), procs...)
		sort.SliceStable(s, func(i, j int) bool { return less(s[i], s[j]) })
		for i := 0; i < n && i < len(s); i++ {
			picked[s[i].PID] = s[i]
		}
	}
	take(func(a, b ProcRec) bool { return a.FootprintBytes > b.FootprintBytes }, nMem)
	take(func(a, b ProcRec) bool { return a.CPUPct > b.CPUPct }, nCPU)
	out := make([]ProcRec, 0, len(picked))
	for _, p := range picked {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

var (
	reVite       = regexp.MustCompile(`(^|[^a-z0-9])vite([^a-z0-9]|$)`)
	reMCP        = regexp.MustCompile(`(^|[^a-z])mcp([^a-z]|$)`)
	rePlaywright = regexp.MustCompile(`playwright`)
	reBroker     = regexp.MustCompile(`broker`)
)

// residentKind says which long-lived helper a process is, "" when it is not.
func residentKind(p ProcRec, args []string) string {
	text := strings.ToLower(p.Name + " " + strings.Join(args, " "))
	switch {
	case strings.Contains(text, "codex") && reBroker.MatchString(text):
		return "codex-broker"
	case rePlaywright.MatchString(text):
		return "playwright"
	case reVite.MatchString(text):
		return "vite"
	case reMCP.MatchString(text):
		return "mcp"
	}
	return ""
}

func isCodexRun(p ProcRec) bool { return strings.ToLower(path.Base(p.Name)) == "codex" }

// sessionAbove walks the parent chain (the process itself first) to the
// nearest Claude Code session process.
func sessionAbove(p ProcRec, c ProcContext) (string, bool) {
	if c.SessionOf == nil {
		return "", false
	}
	cur := p
	for depth := 0; depth < 64; depth++ {
		if ref, ok := c.SessionOf(cur.PID); ok {
			return ref, true
		}
		next, ok := c.Table[cur.PPID]
		if !ok || cur.PPID <= 1 || next.PID == cur.PID {
			return "", false
		}
		cur = next
	}
	return "", false
}

// sessionFor is the session a process belongs to: the one above it, else (for a helper that was reparented away from its session)
// the one in its own environment or in the nearest ancestor's that has one. ok is false when nobody can be named.
func sessionFor(p ProcRec, c ProcContext) (string, bool) {
	if ref, ok := sessionAbove(p, c); ok {
		return ref, true
	}
	if c.EnvSessionOf == nil {
		return "", false
	}
	cur := p
	for depth := 0; depth < 64; depth++ {
		if ref, ok := c.EnvSessionOf(cur.PID); ok {
			return ref, true
		}
		next, ok := c.Table[cur.PPID]
		if !ok || cur.PPID <= 1 || next.PID == cur.PID {
			return "", false
		}
		cur = next
	}
	return "", false
}

// jobAbove finds the wwd job a process belongs to: its own marker, or the
// nearest ancestor's. The kernel hides the environment of system binaries
// (/bin/sleep, /bin/sh), so a marker-less child of a job is still the job's.
// The walk stops at a Claude Code session process.
func jobAbove(p ProcRec, c ProcContext) (string, bool) {
	_, id, ok := jobMarkAbove(p, c)
	return id, ok
}

// jobMarkAbove is jobAbove with the origin tag of the process that carries the mark ("" when its mark has none).
func jobMarkAbove(p ProcRec, c ProcContext) (tag, id string, ok bool) {
	if c.JobOf == nil {
		return "", "", false
	}
	cur := p
	for depth := 0; depth < 64; depth++ {
		if id, ok := c.JobOf(cur.PID); ok {
			if c.OriginOf != nil {
				tag, _ = c.OriginOf(cur.PID)
			}
			return tag, id, true
		}
		if c.SessionOf != nil {
			if _, ok := c.SessionOf(cur.PID); ok {
				return "", "", false
			}
		}
		next, ok := c.Table[cur.PPID]
		if !ok || cur.PPID <= 1 || next.PID == cur.PID {
			return "", "", false
		}
		cur = next
	}
	return "", "", false
}

// Attribute says whose a process is: a wwd job's tree (the marker), a session's
// own tree, a codex run dispatched by a session, a session's long-lived helper
// (orphaned ones are "orphan"), or the system. nil when nobody can be named.
func Attribute(p ProcRec, c ProcContext) *wire.ProcOwner {
	if id, ok := jobAbove(p, c); ok {
		return &wire.ProcOwner{Kind: wire.OwnerJob, ID: id}
	}
	session, hasSession := sessionFor(p, c)
	if c.SessionOf != nil {
		if ref, ok := c.SessionOf(p.PID); ok {
			return &wire.ProcOwner{Kind: wire.OwnerSession, Session: ref}
		}
	}
	if kindOf(p, c) != "" {
		if !hasSession {
			session = "orphan"
		}
		return &wire.ProcOwner{Kind: wire.OwnerResident, Session: session}
	}
	if hasSession {
		if isCodexRun(p) {
			return &wire.ProcOwner{Kind: wire.OwnerCodex, Session: session}
		}
		return &wire.ProcOwner{Kind: wire.OwnerSession, Session: session}
	}
	if p.UID == 0 || p.PID == 1 {
		return &wire.ProcOwner{Kind: wire.OwnerSystem}
	}
	return nil
}

func (c *ProcContext) ensureTable(s ProcSnapshot) {
	if c.Table != nil {
		return
	}
	c.Table = make(map[int]ProcRec, len(s.Procs))
	for _, p := range s.Procs {
		c.Table[p.PID] = p
	}
}

// BuildProcs is the wire list: the top nMem by memory and top nCPU by CPU, each
// attributed.
func BuildProcs(s ProcSnapshot, c ProcContext, nMem, nCPU int) []wire.Proc {
	c.ensureTable(s)
	out := []wire.Proc{}
	for _, p := range SelectTop(s.Procs, nMem, nCPU) {
		kind := p.MemKind
		if kind == "" {
			kind = "footprint"
		}
		out = append(out, wire.Proc{
			PID: p.PID, Name: p.Name, CPUPct: p.CPUPct, MemMB: int(p.FootprintBytes / mib),
			MemKind: kind, Owner: Attribute(p, c),
		})
	}
	return out
}

// BuildResidents groups the long-lived helpers by session and kind. It asks the
// kind of every process, so KindOf (or ArgsOf) must cache: the reader reads a
// process's argv once.
func BuildResidents(s ProcSnapshot, c ProcContext) []wire.Resident {
	c.ensureTable(s)
	type key struct{ session, kind string }
	agg := map[key]*wire.Resident{}
	for _, p := range s.Procs {
		kind := kindOf(p, c)
		if kind == "" {
			continue
		}
		if _, isJob := jobAbove(p, c); isJob {
			continue // residents are the non-job helpers (spec §3.1)
		}
		session, ok := sessionFor(p, c)
		if !ok {
			session = "orphan"
		}
		k := key{session, kind}
		r := agg[k]
		if r == nil {
			r = &wire.Resident{Session: session, Kind: kind}
			agg[k] = r
		}
		r.Count++
		r.MemMB += int(p.FootprintBytes / mib)
	}
	out := make([]wire.Resident, 0, len(agg))
	for _, r := range agg {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Session != out[j].Session {
			return out[i].Session < out[j].Session
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}
