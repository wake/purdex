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

	mu   sync.Mutex
	last map[wire.AlertKind]string // the last failure logged per rule, to log a change only
}

// NewEvaluator returns an evaluator of rules. input says what to judge on now (false: nothing sampled yet); logf may be nil.
func NewEvaluator(book Book, input func() (Input, bool), logf func(string, ...any), rules ...Rule) *Evaluator {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Evaluator{book: book, input: input, logf: logf, rules: rules, last: map[wire.AlertKind]string{}}
}

// Every is how often Run judges. The spec's shortest window is a minute; a push waits for the poll of the deliverer (30 s) anyway.
const Every = 30 * time.Second

// Run judges every Every until ctx ends.
func (e *Evaluator) Run(ctx context.Context) {
	t := time.NewTicker(Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Pass()
		}
	}
}

// Pass judges every rule once. It does not wait for the proto 2 writer gate: alerts are not behind it (spec §13.1), so an unreachable
// host cannot silence the others' alerts.
func (e *Evaluator) Pass() {
	in, ok := e.input()
	if !ok {
		return
	}
	// what is raised is read from the table at every pass, so a restart, or an alert cleared by someone else, cannot leave the evaluator
	// believing something else; when the table cannot be read nothing is judged
	active, err := e.book.Active()
	if err != nil {
		e.fail("", "read the active alerts", err)
		return
	}
	e.ok("")
	in.Raised = func(key string) bool { return active[key] }
	for _, r := range e.rules {
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
