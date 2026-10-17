package facts

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// JobMarkerEnv is the environment variable every process a wwd job starts
// carries (spec §5). Facts (procs[].owner, the air26 owner rule) and the worker
// share JobFromEnv as the one check for "this belongs to a wwd job".
const JobMarkerEnv = "WWD_JOB"

var reJobID = regexp.MustCompile(`^j_[0-9A-Za-z_-]{1,64}$`)

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

// ProcRec is one process as the reader saw it. FootprintBytes is the
// physical footprint (what Activity Monitor calls Memory).
type ProcRec struct {
	PID, PPID, UID int
	Name           string
	CPUPct         float64
	FootprintBytes uint64
}

// ProcSnapshot is the whole process table at one moment.
type ProcSnapshot struct{ Procs []ProcRec }

// ProcContext are the lookups attribution needs. JobOf reads a process's
// environment, ArgsOf its argv; both are called only for the processes that
// matter (the selected ones, and the residents scan). Table is filled from the
// snapshot when nil.
type ProcContext struct {
	Table     map[int]ProcRec
	JobOf     func(pid int) (string, bool)
	SessionOf func(pid int) (string, bool) // pid is a Claude Code session process: its ref
	ArgsOf    func(pid int) []string
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

// jobAbove finds the wwd job a process belongs to: its own marker, or the
// nearest ancestor's. The kernel hides the environment of system binaries
// (/bin/sleep, /bin/sh), so a marker-less child of a job is still the job's.
// The walk stops at a Claude Code session process.
func jobAbove(p ProcRec, c ProcContext) (string, bool) {
	if c.JobOf == nil {
		return "", false
	}
	cur := p
	for depth := 0; depth < 64; depth++ {
		if id, ok := c.JobOf(cur.PID); ok {
			return id, true
		}
		if c.SessionOf != nil {
			if _, ok := c.SessionOf(cur.PID); ok {
				return "", false
			}
		}
		next, ok := c.Table[cur.PPID]
		if !ok || cur.PPID <= 1 || next.PID == cur.PID {
			return "", false
		}
		cur = next
	}
	return "", false
}

// Attribute says whose a process is: a wwd job's tree (the marker), a session's
// own tree, a codex run dispatched by a session, a session's long-lived helper
// (orphaned ones are "orphan"), or the system. nil when nobody can be named.
func Attribute(p ProcRec, c ProcContext) *wire.ProcOwner {
	if id, ok := jobAbove(p, c); ok {
		return &wire.ProcOwner{Kind: wire.OwnerJob, ID: id}
	}
	var args []string
	if c.ArgsOf != nil {
		args = c.ArgsOf(p.PID)
	}
	session, hasSession := sessionAbove(p, c)
	if c.SessionOf != nil {
		if ref, ok := c.SessionOf(p.PID); ok {
			return &wire.ProcOwner{Kind: wire.OwnerSession, Session: ref}
		}
	}
	if residentKind(p, args) != "" {
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
		out = append(out, wire.Proc{
			PID: p.PID, Name: p.Name, CPUPct: p.CPUPct, MemMB: int(p.FootprintBytes / mib),
			MemKind: "footprint", Owner: Attribute(p, c),
		})
	}
	return out
}

// BuildResidents groups the long-lived helpers by session and kind. It reads
// the argv of every process, so ArgsOf should cache.
func BuildResidents(s ProcSnapshot, c ProcContext) []wire.Resident {
	c.ensureTable(s)
	type key struct{ session, kind string }
	agg := map[key]*wire.Resident{}
	for _, p := range s.Procs {
		var args []string
		if c.ArgsOf != nil {
			args = c.ArgsOf(p.PID)
		}
		kind := residentKind(p, args)
		if kind == "" {
			continue
		}
		if _, isJob := jobAbove(p, c); isJob {
			continue // residents are the non-job helpers (spec §3.1)
		}
		session, ok := sessionAbove(p, c)
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
