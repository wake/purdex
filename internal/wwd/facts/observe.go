package facts

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Observation of undeclared heavy trees (spec section 8.4). The Tracker watches the process snapshot the collector takes every
// sample and keeps one running total per process tree that belongs to no job: a command a Claude Code session started, or a command
// codex ran. When the tree ends and it used more than a threshold (CPU seconds of the whole tree, or memory at its peak) it is
// handed back as a wire.Observation. Nothing is blocked or killed.
//
// A tree is a process and its descendants, rooted at a process whose parent is an anchor: a session process, or part of codex's
// harness (the app-server broker, its npm wrapper, the codex binary, codex-code-mode-host). The harness itself is never a tree, and
// neither are residents (a dev server is not a command, spec section 9) or anything a job started (it is that job's). A tree stops at
// another tree's root, so a command codex runs inside a shell the session started is counted once.

// ObserveConfig is what the Tracker needs. The zero value of a threshold takes its default.
type ObserveConfig struct {
	Host string
	// MinCPUSeconds: a tree that used at least this much CPU (all its processes together) is recorded. Default 60.
	MinCPUSeconds float64
	// MinMemMB: a tree that held at least this much memory at once is recorded. Default 1024.
	MinMemMB int
	// Interval is the sampling interval, used for the first sample and to bound the time one sample is charged. Default 5 s.
	Interval time.Duration
	// MaxLive bounds the trees watched at once. Default 256.
	MaxLive int
}

func (c ObserveConfig) withDefaults() ObserveConfig {
	if c.MinCPUSeconds <= 0 {
		c.MinCPUSeconds = 60
	}
	if c.MinMemMB <= 0 {
		c.MinMemMB = 1024
	}
	if c.Interval <= 0 {
		c.Interval = 5 * time.Second
	}
	if c.MaxLive <= 0 {
		c.MaxLive = 256
	}
	return c
}

type treeKey struct {
	pid  int
	name string // a pid reused by another program is another tree
}

type tree struct {
	rootPID     int
	started     time.Time
	last        time.Time
	missed      int    // samples in a row the root was not seen
	ident       string // the root's start identity when the tree began ("" when unknown)
	session     string
	via         string
	command     string
	signature   string
	cpuSeconds  float64
	cpuPeak     float64
	memPeakByte uint64
	procsPeak   int
}

func (t *tree) over(cfg ObserveConfig) bool {
	return t.cpuSeconds >= cfg.MinCPUSeconds || int(t.memPeakByte>>20) >= cfg.MinMemMB
}

func (t *tree) observation(host string, ended bool) wire.Observation {
	o := wire.Observation{
		ID:   fmt.Sprintf("o_%d_%d", t.started.UnixMilli(), t.rootPID),
		Host: host, Session: t.session, Via: t.via, Command: t.command, Signature: t.signature,
		StartedAt: t.started, CPUSeconds: t.cpuSeconds, CPUPeak: t.cpuPeak,
		MemPeakMB: int(t.memPeakByte >> 20), ProcsPeak: t.procsPeak,
	}
	if ended {
		end := t.last
		o.EndedAt = &end
	}
	return o
}

// Tracker totals the trees. One value belongs to one goroutine (the sampler's); Live is for the same goroutine.
type Tracker struct {
	cfg     ObserveConfig
	live    map[treeKey]*tree
	harness map[treeKey]bool
	lastAt  time.Time
	// ident tells which process holds a pid (its start time); a tree whose root pid now has another one is a new tree. Tests replace it.
	ident func(pid int) (string, bool)
}

func NewTracker(cfg ObserveConfig) *Tracker {
	return &Tracker{cfg: cfg.withDefaults(), live: map[treeKey]*tree{}, harness: map[treeKey]bool{}, ident: ProcIdent}
}

// isHarness says whether p is part of codex's own machinery: named codex..., the app-server broker, or a node/python wrapper
// running a script of the codex package. Cached by pid and name.
func (t *Tracker) isHarness(p ProcRec, c ProcContext) bool {
	k := treeKey{p.PID, p.Name}
	if v, ok := t.harness[k]; ok {
		return v
	}
	h := strings.HasPrefix(strings.ToLower(path.Base(p.Name)), "codex") || kindOf(p, c) == "codex-broker"
	if !h && c.ArgsOf != nil {
		for i, a := range c.ArgsOf(p.PID) {
			if i >= 3 {
				break
			}
			if strings.Contains(a, "@openai/codex") || strings.Contains(a, "/codex/") || strings.HasSuffix(a, "/codex.js") {
				h = true
				break
			}
		}
	}
	t.harness[k] = h
	return h
}

// Update takes the sample taken at now and returns the trees that ended since the last one and were over a threshold.
func (t *Tracker) Update(now time.Time, snap ProcSnapshot, c ProcContext) []wire.Observation {
	if len(snap.Procs) == 0 { // a read that failed, not a machine with no processes: nothing ends because of it
		return nil
	}
	c.ensureTable(snap)
	dt := t.cfg.Interval
	if !t.lastAt.IsZero() && now.After(t.lastAt) {
		dt = min(now.Sub(t.lastAt), 3*t.cfg.Interval) // a gap (sleep, wwd stopped) is not charged as CPU
	}
	t.lastAt = now

	children := make(map[int][]int, len(snap.Procs))
	for _, p := range snap.Procs {
		children[p.PPID] = append(children[p.PPID], p.PID)
	}
	isRoot := make(map[int]bool)
	via := make(map[int]string)
	for _, p := range snap.Procs {
		if p.PID <= 1 || t.isHarness(p, c) {
			continue
		}
		parent, ok := c.Table[p.PPID]
		if !ok || p.PPID <= 1 {
			continue
		}
		fromCodex := t.isHarness(parent, c)
		fromSession := false
		if c.SessionOf != nil {
			_, fromSession = c.SessionOf(parent.PID)
		}
		if !fromCodex && !fromSession {
			continue
		}
		if kindOf(p, c) != "" { // a resident (dev server, mcp, playwright, a broker) is not a command
			continue
		}
		if _, isJob := jobAbove(p, c); isJob {
			continue
		}
		isRoot[p.PID] = true
		if fromCodex {
			via[p.PID] = wire.ObsViaCodex
		} else {
			via[p.PID] = wire.ObsViaSession
		}
	}

	// A tree that is being watched stays a tree while its root lives, even when the anchor above it has exited and the root went
	// to pid 1: a long command outlives the shell that started it.
	for k, tr := range t.live {
		if p, ok := c.Table[k.pid]; ok && p.Name == k.name && !isRoot[k.pid] {
			isRoot[k.pid] = true
			via[k.pid] = tr.via
		}
	}

	var ended []wire.Observation
	seen := make(map[treeKey]bool, len(isRoot))
	for _, root := range snap.Procs {
		if !isRoot[root.PID] {
			continue
		}
		var cpu float64
		var mem uint64
		n := 0
		stack := []int{root.PID}
		for len(stack) > 0 && n < 5000 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			p := c.Table[pid]
			cpu += p.CPUPct
			mem += p.FootprintBytes
			n++
			for _, ch := range children[pid] {
				if !isRoot[ch] && !t.notACommand(c.Table[ch], c) { // another tree starts there, or it is not this command's
					stack = append(stack, ch)
				}
			}
		}
		k := treeKey{root.PID, root.Name}
		seen[k] = true
		tr := t.live[k]
		id, _ := t.ident(root.PID)
		if tr != nil && id != "" && tr.ident != "" && id != tr.ident { // the pid went to a new process of the same name
			if tr.over(t.cfg) {
				ended = append(ended, tr.observation(t.cfg.Host, true))
			}
			delete(t.live, k)
			tr = nil
		}
		if tr == nil {
			session, ok := sessionFor(root, c)
			if !ok {
				session = "orphan"
			}
			var argv []string
			if c.ArgsOf != nil {
				argv = c.ArgsOf(root.PID)
			}
			if len(argv) == 0 {
				argv = []string{root.Name}
			}
			tr = &tree{
				rootPID: root.PID, ident: id, started: now, session: session, via: via[root.PID],
				command: RedactCommand(argv), signature: CommandSignature(argv),
			}
			t.live[k] = tr
		}
		tr.last = now
		tr.cpuSeconds += cpu / 100 * dt.Seconds()
		tr.cpuPeak = max(tr.cpuPeak, cpu)
		tr.memPeakByte = max(tr.memPeakByte, mem)
		tr.procsPeak = max(tr.procsPeak, n)
	}

	for k, tr := range t.live {
		if seen[k] {
			tr.missed = 0
			continue
		}
		if tr.missed++; tr.missed < observeMissedSamples { // one sample without it may be a read that missed the process
			continue
		}
		if tr.over(t.cfg) {
			ended = append(ended, tr.observation(t.cfg.Host, true))
		}
		delete(t.live, k)
	}
	// forget harness decisions for processes that are gone, and bound what is watched
	for k := range t.harness {
		if !pidPresent(c.Table, k) {
			delete(t.harness, k)
		}
	}
	t.bound()
	sortObservations(ended)
	return ended
}

// observeMissedSamples is how many samples in a row a tree's root must be absent before the tree is taken as ended.
const observeMissedSamples = 2

// notACommand says whether a process below a tree's root is something the tree does not own: a job's process (it is that job's), a
// resident (a dev server is not a command, spec section 9) or part of codex's harness. Its whole subtree stays out of the totals.
func (t *Tracker) notACommand(p ProcRec, c ProcContext) bool {
	if t.isHarness(p, c) || kindOf(p, c) != "" {
		return true
	}
	_, isJob := jobAbove(p, c)
	return isJob
}

func pidPresent(table map[int]ProcRec, k treeKey) bool {
	p, ok := table[k.pid]
	return ok && p.Name == k.name
}

// bound keeps at most MaxLive trees, dropping the least interesting (below both thresholds and the smallest) first.
func (t *Tracker) bound() {
	for len(t.live) > t.cfg.MaxLive {
		var worstK treeKey
		var worst *tree
		for k, tr := range t.live {
			if worst == nil || score(tr, t.cfg) < score(worst, t.cfg) {
				worstK, worst = k, tr
			}
		}
		delete(t.live, worstK)
	}
}

func score(tr *tree, cfg ObserveConfig) float64 {
	return tr.cpuSeconds/cfg.MinCPUSeconds + float64(tr.memPeakByte>>20)/float64(cfg.MinMemMB)
}

// Live are the trees running now that are already over a threshold (EndedAt is null).
func (t *Tracker) Live() []wire.Observation {
	var out []wire.Observation
	for _, tr := range t.live {
		if tr.over(t.cfg) {
			out = append(out, tr.observation(t.cfg.Host, false))
		}
	}
	sortObservations(out)
	return out
}

func sortObservations(o []wire.Observation) {
	sort.SliceStable(o, func(i, j int) bool {
		if !o[i].StartedAt.Equal(o[j].StartedAt) {
			return o[i].StartedAt.Before(o[j].StartedAt)
		}
		return o[i].ID < o[j].ID
	})
}
