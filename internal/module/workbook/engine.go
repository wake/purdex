package workbook

import (
	"log"
	"sync"
	"time"

	"github.com/wake/purdex/internal/convturns"
	"github.com/wake/purdex/internal/team"
)

// Entry reasons the engine writes (spec §5.1 and §4.2); the store treats a reason as text.
const (
	ReasonNoMod   = "no_mod"  // skipped: the session's mod did not announce workbook.v2
	ReasonBacklog = "backlog" // skipped: more than three turns of the conversation were waiting
	ReasonCap     = "cap"     // skipped: the hourly cap of calls was reached
	ReasonModel   = "model"   // skipped: the model said skip
	ReasonAuth    = "auth"    // failed
	ReasonAPI     = "api"     // failed
	ReasonFormat  = "format"  // failed: the answer was not the expected JSON twice
	ReasonTimeout = "timeout" // failed
	ReasonRefused = "refused" // failed: the call was rejected in the mod
	ReasonLost    = "lost"    // failed: the lease ran out
)

// Limits of the engine (spec §5.1, plan D1 / D2 / D10).
const (
	catchUpWindow   = 6                      // turns read per turn-end event
	catchUpKeep     = 3                      // ended turns recorded at most per event (the backlog limit)
	busyRetries     = 3                      // transcript cache busy: re-queue the event this many times
	settleDelay     = 100 * time.Millisecond // a Stop that comes before the transcript is written: look again this often ...
	settleRetries   = 15                     // ... this many times (1.5 s), then use the hook's own words
	busyDelay       = 2 * time.Second        // ... this far apart
	fallbackBucket  = 120_000                // ms: the time bucket of a fallback turn id
	maxWaitingTurns = 3                      // waiting turn jobs per conversation
	defaultCallCap  = 300                    // calls per host per hour
	leaseSlack      = 10 * time.Second       // a lease lasts timeout_ms + this
	completeTimeout = 30_000                 // ms: timeout_ms of a job's call
	reapEvery       = 5 * time.Second        // how often leases that ran out are looked for
)

// Deps are the engine's collaborators. Every one but Store may be nil (a daemon, or a test, without that module).
type Deps struct {
	Store   *Store
	Turns   convturns.Reader            // nil: a transcript is never readable, the fallback turn id is used
	Lineage team.LineageRootResolver    // nil: a session is its own conversation
	Seats   team.SeatReader             // nil: role is not recorded
	Capable func(sessionID string) bool // whether the session's mod announced workbook.v2 (nil: nobody is)
	HostID  string

	// RefreshSessions lists the sessions whose live stream announced workbook.refresh within 30 s, with when (nil: none).
	RefreshSessions func() []CapSession
	// OnAvailability is told, at each sweep, of the conversations whose refresh_available value changed (nil: nobody).
	OnAvailability func([]AvailabilityChange)

	Now   func() time.Time                                   // test seam
	After func(d time.Duration, f func()) (stop func() bool) // test seam: the busy re-queue timer
	Logf  func(format string, args ...any)
}

// Engine turns turn-end events into entries and hands their summarising out as jobs (spec §4.2, §5.1). It owns no
// transport: JobSource is what the mod's socket routes will call.
type Engine struct {
	d Deps

	life    sync.RWMutex // callbacks hold it shared; Stop takes it to bar new ones and wait for the running ones
	stopped bool

	qmu        sync.Mutex // the queue structures only; never held across a store call
	qstopped   bool
	afterBuild func()                // test seam: between a job's input being built and its lease being checked
	waiting    map[string]*waitState // sessions with an event on a timer (intake.go); guarded by intakeMu
	intakeMu   sync.Mutex            // one catch-up at a time: cursor read, insert and enqueue keep the turns' order
	omu        sync.Mutex
	orphans    map[int64]struct{} // entries whose final state the store refused (settle.go)
	inflight   sync.WaitGroup     // results and reaps being applied; Stop waits for them
	convs      map[string]*convQ
	leases     map[string]*lease
	seq        int64
	hour       int64
	hourN      int
	capLog     bool
	callCap    int

	// pushLine tells the push hold that an entry's line is final: ready true at the push line, false when the entry
	// ended without one. Nil until the push module's waiter (WB-3) is wired.
	pushLine func(entryID int64, ready bool)
	waiter   *PushLines

	rf refreshState // refresh.go
}

// NewEngine builds an engine over the deps.
func NewEngine(d Deps) *Engine {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.After == nil {
		d.After = func(dur time.Duration, f func()) func() bool { return time.AfterFunc(dur, f).Stop }
	}
	if d.Logf == nil {
		d.Logf = log.Printf
	}
	return &Engine{d: d, convs: map[string]*convQ{}, waiting: map[string]*waitState{}, leases: map[string]*lease{}, callCap: defaultCallCap}
}

// SetPushLineHook registers the push-line notification (see Engine.pushLine).
func (e *Engine) SetPushLineHook(fn func(entryID int64, ready bool)) { e.pushLine = fn }

// SetWaiter wires the push hold's waiter (lines.go): it is woken at every push line and every final state, and told when
// an event's intake is done. Set before the engine is subscribed.
func (e *Engine) SetWaiter(w *PushLines) { e.waiter = w }

func (e *Engine) notifyLine(entryID int64, ready bool) {
	if e.waiter != nil {
		e.waiter.wake()
	}
	if e.pushLine != nil {
		e.pushLine(entryID, ready)
	}
}

// capable is the module's capability answer; nobody is capable until a source is wired.
func (e *Engine) capable(sessionID string) bool {
	return e.d.Capable != nil && e.d.Capable(sessionID)
}

// convKey is the conversation of a session: the root of its relay chain (spec §4.1).
func (e *Engine) convKey(sessionID string) (string, error) {
	if e.d.Lineage == nil {
		return sessionID, nil
	}
	return e.d.Lineage.RootSessionOf(sessionID)
}
