package execstat

import (
	"context"
	"os/exec"
	"sort"
	"sync"
	"time"
)

// Every child process the daemon starts goes through Command or CommandContext with a tag that names the call site, so
// GET /api/debug/execstat can say which probes cost what (host-probe dedupe plan, D0). A guard test fails when a new fork
// path appears outside the helpers. Pure observation, like the counters in execstat.go.
//
// A fork is counted when the child has STARTED: a long-lived child (tmux wait-for, a PTY relay, the peer proxy, a dev
// stream) counts when it starts, not when it exits, and a command that fails to start is counted apart, as a failure.
//
// Next to the forks the registry keeps the things the cost is divided by: Event counts occurrences by name (a hook event,
// a session-list build, a peers envelope, an SSE subscription) and SetGauge/SetGaugeText hold the latest level (the number
// of sessions, the armed codex detectors, the resources mode). The plan reads cost per trigger from the two together.

// slotsSec and slotsMin are the lengths of the two rings that make the recent counts: one bucket per second for the last
// minute, one per minute for the last hour.
const (
	slotsSec = 60
	slotsMin = 60
)

// now is the clock; a variable for the tests.
var now = time.Now

type series struct {
	mu     sync.Mutex
	total  int64
	failed int64 // starts that failed (tags only)
	dur    time.Duration
	last   int64 // the latest second seen: a clock that goes back clears the rings
	secs   [slotsSec]bucket
	mins   [slotsMin]bucket
}

type bucket struct {
	stamp int64 // the second (or minute) number the count is for
	n     int64
}

type registry struct {
	mu sync.Mutex
	m  map[string]*series
}

func (r *registry) get(name string) *series {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]*series{}
	}
	s := r.m[name]
	if s == nil {
		s = &series{}
		r.m[name] = s
	}
	return s
}

func (r *registry) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.m))
	for n := range r.m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (r *registry) reset() {
	r.mu.Lock()
	r.m = nil
	r.mu.Unlock()
}

var (
	tags   registry
	events registry

	gaugeMu sync.Mutex
	gauges  = map[string]Gauge{}
)

// rewind: the clock went back (NTP, a manual change): what the rings hold is no longer in the past, so they start again.
// Callers hold s.mu.
func (s *series) rewind(t int64) {
	if t < s.last {
		s.secs = [slotsSec]bucket{}
		s.mins = [slotsMin]bucket{}
	}
	s.last = t
}

// count records one occurrence at the current time.
func (s *series) count() {
	t := now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rewind(t)
	s.total++
	bump(&s.secs[t%slotsSec], t)
	bump(&s.mins[(t/60)%slotsMin], t/60)
}

func bump(b *bucket, stamp int64) {
	if b.stamp != stamp {
		b.stamp, b.n = stamp, 0
	}
	b.n++
}

func (s *series) addDuration(d time.Duration) {
	s.mu.Lock()
	s.dur += d
	s.mu.Unlock()
}

func (s *series) failedStart() {
	s.mu.Lock()
	s.failed++
	s.mu.Unlock()
}

// Observe records one fork of tag that took d (for a fork made by something that is not a Cmd of ours).
func Observe(tag string, d time.Duration) {
	s := tags.get(tag)
	s.count()
	s.addDuration(d)
}

// Event records one occurrence of a trigger by name ("hook.PdxPreToolUse", "session-list-build", "peers-envelope", "sse-subscribe").
func Event(name string) { events.get(name).count() }

// Gauge is the latest level of something (the number of sessions, the resources mode).
type Gauge struct {
	Name  string    `json:"name"`
	Value int64     `json:"value"`
	Text  string    `json:"text,omitempty"`
	At    time.Time `json:"at"`
}

// SetGauge records the current level of name.
func SetGauge(name string, v int64) {
	gaugeMu.Lock()
	gauges[name] = Gauge{Name: name, Value: v, At: now()}
	gaugeMu.Unlock()
}

// AdjustGauge adds delta to the level of name (a count of things that start and end: SSE subscribers, armed detectors) and
// returns the new level.
func AdjustGauge(name string, delta int64) int64 {
	gaugeMu.Lock()
	defer gaugeMu.Unlock()
	g := gauges[name]
	g.Name, g.Value, g.At = name, g.Value+delta, now()
	gauges[name] = g
	return g.Value
}

// SetGaugeText records a level that is a word (the resources mode).
func SetGaugeText(name, text string) {
	gaugeMu.Lock()
	gauges[name] = Gauge{Name: name, Text: text, At: now()}
	gaugeMu.Unlock()
}

// Series is what one tag or event has cost. Last1 counts the last 60 seconds; Last5 and Last60 count whole minute buckets, the
// current (part) minute included, so they cover 4–5 and 59–60 minutes. StartFailed and TotalMS are the tags' only.
type Series struct {
	Name        string  `json:"name"`
	Total       int64   `json:"total"`
	Last1       int64   `json:"last_1m"`
	Last5       int64   `json:"last_5m"`
	Last60      int64   `json:"last_60m"`
	StartFailed int64   `json:"start_failed,omitempty"`
	TotalMS     float64 `json:"total_ms,omitempty"`
}

func (r *registry) series() []Series {
	names := r.names()
	t := now().Unix()
	out := make([]Series, 0, len(names))
	for _, name := range names {
		s := r.get(name)
		s.mu.Lock()
		s.rewind(t)
		st := Series{Name: name, Total: s.total, StartFailed: s.failed, TotalMS: float64(s.dur) / float64(time.Millisecond)}
		for _, b := range s.secs {
			if b.n > 0 && b.stamp > t-slotsSec && b.stamp <= t {
				st.Last1 += b.n
			}
		}
		m := t / 60
		for _, b := range s.mins {
			if b.n == 0 || b.stamp > m {
				continue
			}
			if b.stamp > m-5 {
				st.Last5 += b.n
			}
			if b.stamp > m-slotsMin {
				st.Last60 += b.n
			}
		}
		s.mu.Unlock()
		out = append(out, st)
	}
	return out
}

// Tags is every fork tag seen so far, sorted by name.
func Tags() []Series { return tags.series() }

// Events is every trigger seen so far, sorted by name.
func Events() []Series { return events.series() }

// Gauges is the latest level of everything set, sorted by name.
func Gauges() []Gauge {
	gaugeMu.Lock()
	out := make([]Gauge, 0, len(gauges))
	for _, g := range gauges {
		out = append(out, g)
	}
	gaugeMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ResetTags forgets every tag, event and gauge; for tests only.
func ResetTags() {
	tags.reset()
	events.reset()
	gaugeMu.Lock()
	gauges = map[string]Gauge{}
	gaugeMu.Unlock()
}

// Cmd is an *exec.Cmd whose Run, Output, CombinedOutput and Start/Wait record the fork under a tag: the count when the child
// has started, its wall time when it ends (success or failure; never the arguments or the output); a command that could not
// be started is counted as a failed start instead. Everything else is the embedded Cmd's, so a call site keeps its own setup
// (Dir, Env, Stdin, SysProcAttr, WaitDelay, Process).
type Cmd struct {
	*exec.Cmd
	tag     string
	started time.Time
}

// Command is exec.Command, counted under tag.
func Command(tag, name string, args ...string) *Cmd {
	return &Cmd{Cmd: exec.Command(name, args...), tag: tag}
}

// CommandContext is exec.CommandContext, counted under tag.
func CommandContext(ctx context.Context, tag, name string, args ...string) *Cmd {
	return &Cmd{Cmd: exec.CommandContext(ctx, name, args...), tag: tag}
}

// Tag is the tag the fork is counted under.
func (c *Cmd) Tag() string { return c.tag }

// finish records a command run to its end: a child that was started is a fork (with its time), one that was not is a failed start.
func (c *Cmd) finish(t0 time.Time) {
	if c.Cmd.Process != nil {
		Observe(c.tag, time.Since(t0))
		return
	}
	tags.get(c.tag).failedStart()
}

// Run implements (*exec.Cmd).Run.
func (c *Cmd) Run() error {
	t0 := time.Now()
	err := c.Cmd.Run()
	c.finish(t0)
	return err
}

// Output implements (*exec.Cmd).Output.
func (c *Cmd) Output() ([]byte, error) {
	t0 := time.Now()
	out, err := c.Cmd.Output()
	c.finish(t0)
	return out, err
}

// CombinedOutput implements (*exec.Cmd).CombinedOutput.
func (c *Cmd) CombinedOutput() ([]byte, error) {
	t0 := time.Now()
	out, err := c.Cmd.CombinedOutput()
	c.finish(t0)
	return out, err
}

// Start implements (*exec.Cmd).Start: the fork is counted once the child has started (a command that is never waited for still
// counts), its time when Wait returns.
func (c *Cmd) Start() error {
	t0 := time.Now()
	if err := c.Cmd.Start(); err != nil {
		tags.get(c.tag).failedStart()
		return err
	}
	c.noteStarted(t0)
	return nil
}

func (c *Cmd) noteStarted(t0 time.Time) {
	tags.get(c.tag).count()
	c.started = t0
}

// NoteStarted is for a caller that starts the embedded Cmd itself (a pty library does): call it once the start succeeded, so the
// fork is counted and Wait adds its time.
func (c *Cmd) NoteStarted() { c.noteStarted(time.Now()) }

// Wait implements (*exec.Cmd).Wait.
func (c *Cmd) Wait() error {
	err := c.Cmd.Wait()
	if !c.started.IsZero() {
		tags.get(c.tag).addDuration(time.Since(c.started))
	}
	return err
}
