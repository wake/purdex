package execstat

import (
	"context"
	"os/exec"
	"sort"
	"sync"
	"time"
)

// Every child process the daemon starts goes through Command or CommandContext with a tag that names the call site, so
// GET /api/debug/execstat can say which probes cost what (host-probe dedupe plan, D0). A guard test fails when a non-test
// exec.Command outside this package is not on its list of CLI-only files. Pure observation, like the counters above.

// slotsSec and slotsMin are the lengths of the two rings that make the recent counts: one bucket per second for the last
// minute, one per minute for the last hour.
const (
	slotsSec = 60
	slotsMin = 60
)

// now is the clock; a variable for the tests.
var now = time.Now

type tagStats struct {
	mu    sync.Mutex
	total int64
	dur   time.Duration
	secs  [slotsSec]bucket
	mins  [slotsMin]bucket
}

type bucket struct {
	stamp int64 // the second (or minute) number the count is for
	n     int64
}

var (
	regMu sync.Mutex
	reg   = map[string]*tagStats{}
)

func statsOf(tag string) *tagStats {
	regMu.Lock()
	defer regMu.Unlock()
	s := reg[tag]
	if s == nil {
		s = &tagStats{}
		reg[tag] = s
	}
	return s
}

// count records one fork of tag at the current time.
func (s *tagStats) count() {
	t := now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *tagStats) addDuration(d time.Duration) {
	s.mu.Lock()
	s.dur += d
	s.mu.Unlock()
}

// Observe records one fork of tag that took d (for a fork made by something that is not an exec.Cmd of ours).
func Observe(tag string, d time.Duration) {
	s := statsOf(tag)
	s.count()
	s.addDuration(d)
}

// TagStat is what one tag has cost. Last1 counts the forks of the last 60 seconds; Last5 and Last60 count whole minute
// buckets, the current (part) minute included, so they cover 4–5 and 59–60 minutes.
type TagStat struct {
	Tag     string  `json:"tag"`
	Total   int64   `json:"total"`
	Last1   int64   `json:"last_1m"`
	Last5   int64   `json:"last_5m"`
	Last60  int64   `json:"last_60m"`
	TotalMS float64 `json:"total_ms"`
}

// Tags is every tag seen so far, sorted by name.
func Tags() []TagStat {
	regMu.Lock()
	tags := make([]string, 0, len(reg))
	for t := range reg {
		tags = append(tags, t)
	}
	regMu.Unlock()
	sort.Strings(tags)
	t := now().Unix()
	out := make([]TagStat, 0, len(tags))
	for _, tag := range tags {
		s := statsOf(tag)
		s.mu.Lock()
		st := TagStat{Tag: tag, Total: s.total, TotalMS: float64(s.dur) / float64(time.Millisecond)}
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

// ResetTags forgets every tag; for tests only.
func ResetTags() {
	regMu.Lock()
	reg = map[string]*tagStats{}
	regMu.Unlock()
}

// Cmd is an *exec.Cmd whose Run, Output, CombinedOutput and Start/Wait record the fork under a tag: the count when it is
// made, the wall time when it ends (success or failure; never the arguments or the output). Everything else is the embedded
// Cmd's, so a call site keeps its own setup (Dir, Env, Stdin, SysProcAttr, WaitDelay, Process).
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

func (c *Cmd) observe(t0 time.Time) { Observe(c.tag, time.Since(t0)) }

// Run implements (*exec.Cmd).Run.
func (c *Cmd) Run() error {
	t0 := time.Now()
	err := c.Cmd.Run()
	c.observe(t0)
	return err
}

// Output implements (*exec.Cmd).Output.
func (c *Cmd) Output() ([]byte, error) {
	t0 := time.Now()
	out, err := c.Cmd.Output()
	c.observe(t0)
	return out, err
}

// CombinedOutput implements (*exec.Cmd).CombinedOutput.
func (c *Cmd) CombinedOutput() ([]byte, error) {
	t0 := time.Now()
	out, err := c.Cmd.CombinedOutput()
	c.observe(t0)
	return out, err
}

// Start implements (*exec.Cmd).Start: the fork is counted when it starts (a command that is never waited for still counts),
// its time when Wait returns.
func (c *Cmd) Start() error {
	t0 := time.Now()
	if err := c.Cmd.Start(); err != nil {
		c.observe(t0)
		return err
	}
	statsOf(c.tag).count()
	c.started = t0
	return nil
}

// Wait implements (*exec.Cmd).Wait.
func (c *Cmd) Wait() error {
	err := c.Cmd.Wait()
	if !c.started.IsZero() {
		statsOf(c.tag).addDuration(time.Since(c.started))
	}
	return err
}
