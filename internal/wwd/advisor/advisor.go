// Package advisor holds the alert rules of spec §13.3. A rule is a pure function of what wwd already knows (the latest facts, the minute
// history, the start log), so the same rule that runs in the daemon can be replayed over a week of recorded facts to count how often it
// would have fired (Replay) before it is switched on.
package advisor

import (
	"context"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Input is what a rule judges on, taken at one moment.
type Input struct {
	Now   time.Time
	Facts wire.Facts
	// Minutes returns the one-minute rollups at or after since, oldest first; nil: no history is available.
	Minutes func(since time.Time) []wire.Facts
	// Recent returns the 5 s samples at or after since, oldest first (about the last hour is kept); nil: only the minute rollups exist (a
	// replay), and a rule that needs finer time falls back to the minute.
	Recent func(since time.Time) []wire.Facts
	// Orphans is the host's latest process scan (internal/wwd/orphans); nil: none is available (a replay has no process table), and the
	// orphans rule is unjudged. It returns nil before the first scan has run.
	Orphans func() *OrphanReport
	// Peers is the pool poller's view of the peers' reachability (spec §13.3 silence); nil: not available (a replay has no pool).
	Peers func() []PeerDown
	// PartnerSeen is when the local Purdex daemon last read this host's alerts; nil: not available (a replay, or a host without a daemon).
	PartnerSeen func() time.Time
	// StartsSince is the number of wwd starts recorded at or after since; nil: not available.
	StartsSince func(since time.Time) (int, error)
	// WorkRoot is the directory jobs run in: the volume that holds it is the host's work volume (spec §13.3 "disk low").
	WorkRoot string
	// Raised says whether the alert with this key is active now (the evaluator fills it; nil: none is). A rule that must not flap around its
	// threshold uses it to clear later than it raised.
	Raised func(key string) bool
}

// Verdict is a rule's answer: does it hold now, and the alert to raise if it does. Key is the alert's key and is set also when the rule
// does not hold, so the evaluator knows which alert to clear.
type Verdict struct {
	Holds  bool
	Key    string
	Level  wire.AlertLevel
	Detail string
}

// Rule is one row of the table in spec §13.3.
type Rule interface {
	Kind() wire.AlertKind
	// Eval judges the input. An error means the rule could not be judged (a missing volume, an unreadable history): the alert is left as
	// it was, neither raised nor cleared.
	Eval(Input) (Verdict, error)
}

// Book is where verdicts go (daemon.AlertBook).
type Book interface {
	Raise(wire.Alert) error
	Clear(key string) error
	// Active is the keys active in the table now.
	Active() (map[string]bool, error)
}

// Evaluator runs the rules over the host's facts, on a clock of its own.
type Evaluator struct {
	book  Book
	input func() (Input, bool)
	logf  func(string, ...any)
	rules []Rule

	// every is the interval of Run's pass (Every unless WithInterval); sample, when set, is signalled at every new sample (WithSampleSignal);
	// retired are the keys of rules this host does not judge (WithRetired).
	every   time.Duration
	sample  <-chan struct{}
	retired []string
	// passMu makes a pass (Pass, PassSample) one at a time: the 30 s pass and the per-sample pass write the same alerts, and must not run
	// beside each other, whoever calls them.
	passMu sync.Mutex

	mu   sync.Mutex
	last map[wire.AlertKind]string // the last failure logged per rule, to log a change only
}

// NewEvaluator returns an evaluator of rules. input says what to judge on now (false: nothing sampled yet); logf may be nil.
func NewEvaluator(book Book, input func() (Input, bool), logf func(string, ...any), rules ...Rule) *Evaluator {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Evaluator{book: book, input: input, logf: logf, rules: rules, every: Every, last: map[wire.AlertKind]string{}}
}

// WithSampleSignal makes Run also judge the SampleRules whenever a value arrives on ch (the sampler signals a new sample there without
// blocking). It runs in Run's own goroutine, so it is serialized with the 30 s pass.
func (e *Evaluator) WithSampleSignal(ch <-chan struct{}) *Evaluator {
	e.sample = ch
	return e
}

// WithInterval sets how often Run judges everything (a test shortens it); not positive: Every.
func (e *Evaluator) WithInterval(d time.Duration) *Evaluator {
	if d > 0 {
		e.every = d
	}
	return e
}

// WithRetired names the alert keys of rules this host does not judge: every pass clears them if they are active, so a rule that was turned
// off (or taken out in `off`) does not leave its alert standing, and an older build, which would refuse the alert, can be installed.
func (e *Evaluator) WithRetired(keys ...string) *Evaluator {
	e.retired = append(e.retired, keys...)
	return e
}

// Every is how often Run judges. The spec's shortest window is a minute; a push waits for the poll of the deliverer (30 s) anyway.
const Every = 30 * time.Second

// Run judges every Every until ctx ends, and the SampleRules at every sample signal. Both happen in this goroutine, one after the other.
func (e *Evaluator) Run(ctx context.Context) {
	t := time.NewTicker(e.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Pass()
		case <-e.sample:
			e.PassSample()
		}
	}
}

// Pass judges every rule once. It does not wait for the proto 2 writer gate: alerts are not behind it (spec §13.1), so an unreachable
// host cannot silence the others' alerts.
func (e *Evaluator) Pass() {
	e.passMu.Lock()
	defer e.passMu.Unlock()
	e.pass(false)
}

// PassSample judges the rules that ask to be judged at every sample (SampleRule) and no other. With none, it reads nothing.
func (e *Evaluator) PassSample() {
	e.passMu.Lock()
	defer e.passMu.Unlock()
	e.pass(true)
}

// pass judges the rules (only the SampleRules when sampleOnly) once.
func (e *Evaluator) pass(sampleOnly bool) {
	if sampleOnly {
		any := false
		for _, r := range e.rules {
			if sr, ok := r.(SampleRule); ok && sr.EverySample() {
				any = true
			}
		}
		if !any {
			return
		}
	}
	// what is raised is read from the table at every pass, so a restart, or an alert cleared by someone else, cannot leave the evaluator
	// believing something else; when the table cannot be read nothing is judged
	active, err := e.book.Active()
	if err != nil {
		e.fail("", "read the active alerts", err)
		return
	}
	e.ok("")
	// the alerts of rules this host no longer judges are let go first and need no facts: a host that has nothing to hand out (no first sample
	// yet, a source that keeps failing) must still be able to turn a rule off
	if !sampleOnly {
		for _, key := range e.retired {
			if active[key] {
				if err := e.book.Clear(key); err != nil {
					e.fail("", "clear the retired alert "+key, err)
				}
			}
		}
	}
	in, ok := e.input()
	if !ok {
		return
	}
	in.Raised = func(key string) bool { return active[key] }
	for _, r := range e.rules {
		if sampleOnly {
			if sr, ok := r.(SampleRule); !ok || !sr.EverySample() {
				continue
			}
		}
		v, err := r.Eval(in)
		if err != nil {
			e.fail(r.Kind(), "judge", err)
			continue
		}
		if v.Holds {
			err = e.book.Raise(wire.Alert{Key: v.Key, Kind: r.Kind(), Level: v.Level, Detail: v.Detail})
		} else {
			err = e.book.Clear(v.Key)
		}
		if err != nil {
			e.fail(r.Kind(), "write", err)
			continue
		}
		e.ok(r.Kind())
	}
}

func (e *Evaluator) fail(k wire.AlertKind, what string, err error) {
	msg := what + ": " + err.Error()
	e.mu.Lock()
	same := e.last[k] == msg
	e.last[k] = msg
	e.mu.Unlock()
	if !same {
		e.logf("advisor: rule %s: %s", k, msg)
	}
}

func (e *Evaluator) ok(k wire.AlertKind) {
	e.mu.Lock()
	delete(e.last, k)
	e.mu.Unlock()
}
