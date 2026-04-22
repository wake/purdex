package probe

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	agentpkg "github.com/wake/purdex/internal/agent"
)

// EvidenceRef is probe's lightweight key/value carrier for supporting
// evidence attached to an Outcome. The Module layer maps this into
// observation.EvidenceRef; probe intentionally does not import the
// observation package to avoid a layering inversion.
type EvidenceRef struct {
	Key   string
	Value any
}

// Outcome is the narrow string enum every Watcher reports through its
// Callback.
type Outcome string

const (
	OutcomeAlive            Outcome = "alive"
	OutcomeDead             Outcome = "dead"
	OutcomeActive           Outcome = "active"
	OutcomeIdle             Outcome = "idle"
	OutcomeShellPrompt      Outcome = "shell_prompt"
	OutcomeReady            Outcome = "ready"
	OutcomeError            Outcome = "error"
	OutcomeIdentifyMismatch Outcome = "identify_mismatch"
)

// Kind tags which probe family produced the Outcome (matches probe_kind
// evidence tag the apply pipeline / trace writer consume).
type Kind string

const (
	KindLiveness  Kind = "liveness"
	KindActivity  Kind = "activity"
	KindReadiness Kind = "readiness"
)

// Spec is the minimal configuration a Watcher needs.
type Spec struct {
	Target    string // tmux target (e.g. "sess:")
	AgentType string
	// Interval lets tests override the poll cadence. Zero → default
	// per-kind interval (500ms for liveness, 1s for readiness).
	Interval time.Duration
}

// Callback is invoked by a Watcher every time an Outcome is produced.
type Callback func(outcome Outcome, token string, evidence []EvidenceRef)

// Watcher is the handle a Start* constructor returns.
type Watcher interface {
	Token() string
	Rotate() string
	Stop()
}

// LivenessChecker is the function the Watcher polls to decide whether
// the agent process is still alive.
type LivenessChecker func(agentType, target string) bool

// ActivityStarter arms a one-shot StartWatch call on the underlying
// Prober.
type ActivityStarter func(target string, cb ActivityCallback)

// ReadinessPoller polls the registered readiness checker.
type ReadinessPoller func(agentType, target string) (ReadinessResult, bool)

type baseWatcher struct {
	tokenMu sync.RWMutex
	token   string

	cancel context.CancelFunc

	doneOnce sync.Once
}

func newBaseWatcher(ctx context.Context) (*baseWatcher, context.Context) {
	child, cancel := context.WithCancel(ctx)
	return &baseWatcher{
		token:  uuid.NewString(),
		cancel: cancel,
	}, child
}

func (w *baseWatcher) Token() string {
	w.tokenMu.RLock()
	defer w.tokenMu.RUnlock()
	return w.token
}

func (w *baseWatcher) Rotate() string {
	next := uuid.NewString()
	w.tokenMu.Lock()
	w.token = next
	w.tokenMu.Unlock()
	return next
}

func (w *baseWatcher) Stop() {
	w.doneOnce.Do(func() {
		w.cancel()
	})
}

// StartLiveness spawns a goroutine that polls checker on spec.Interval
// (default 500ms) and emits Outcome transitions (alive ⇄ dead).
func StartLiveness(ctx context.Context, spec Spec, checker LivenessChecker, cb Callback) Watcher {
	w, child := newBaseWatcher(ctx)
	interval := spec.Interval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	go livenessLoop(child, w, spec, interval, checker, cb)
	return w
}

func livenessLoop(ctx context.Context, w *baseWatcher, spec Spec, interval time.Duration, checker LivenessChecker, cb Callback) {
	if checker == nil || cb == nil {
		<-ctx.Done()
		return
	}
	var last bool
	var seeded bool

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	probeID := uuid.NewString()

	fire := func(alive bool) {
		outcome := OutcomeAlive
		if !alive {
			outcome = OutcomeDead
		}
		ev := []EvidenceRef{
			{Key: "probe_id", Value: probeID},
			{Key: "probe_kind", Value: string(KindLiveness)},
		}
		cb(outcome, w.Token(), ev)
	}

	check := func() {
		alive := checker(spec.AgentType, spec.Target)
		if !seeded {
			seeded = true
			last = alive
			fire(alive)
			return
		}
		if alive != last {
			last = alive
			fire(alive)
		}
	}

	check()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// StartActivity wires a one-shot StartWatch loop into the Watcher
// contract. Each time the inner callback fires, the Watcher translates
// ActivitySignal → Outcome, forwards it, and re-arms the watch so the
// caller sees continuous activity transitions.
func StartActivity(ctx context.Context, spec Spec, starter ActivityStarter, stopper func(target string), cb Callback) Watcher {
	w, child := newBaseWatcher(ctx)
	go activityLoop(child, w, spec, starter, stopper, cb)
	return w
}

func activityLoop(ctx context.Context, w *baseWatcher, spec Spec, starter ActivityStarter, stopper func(target string), cb Callback) {
	if starter == nil || cb == nil {
		<-ctx.Done()
		return
	}
	probeID := uuid.NewString()

	for {
		if ctx.Err() != nil {
			return
		}
		done := make(chan struct{})
		starter(spec.Target, func(target string, signal ActivitySignal) {
			defer close(done)
			if ctx.Err() != nil {
				return
			}
			outcome := OutcomeIdle
			switch signal {
			case ActivitySignalRunning:
				outcome = OutcomeActive
			case ActivitySignalIdle:
				outcome = OutcomeIdle
			case ActivitySignalShellPrompt:
				outcome = OutcomeShellPrompt
			}
			ev := []EvidenceRef{
				{Key: "probe_id", Value: probeID},
				{Key: "probe_kind", Value: string(KindActivity)},
				{Key: "activity_signal", Value: string(signal)},
			}
			cb(outcome, w.Token(), ev)
		})
		select {
		case <-ctx.Done():
			if stopper != nil {
				stopper(spec.Target)
			}
			return
		case <-done:
			// loop again to re-arm the next StartWatch
		}
	}
}

// StartReadiness polls poller on spec.Interval (default 1s) and emits
// Outcome transitions.
func StartReadiness(ctx context.Context, spec Spec, poller ReadinessPoller, cb Callback) Watcher {
	w, child := newBaseWatcher(ctx)
	interval := spec.Interval
	if interval <= 0 {
		interval = time.Second
	}
	go readinessLoop(child, w, spec, interval, poller, cb)
	return w
}

func readinessLoop(ctx context.Context, w *baseWatcher, spec Spec, interval time.Duration, poller ReadinessPoller, cb Callback) {
	if poller == nil || cb == nil {
		<-ctx.Done()
		return
	}
	var last agentpkg.Status
	var seeded bool

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	probeID := uuid.NewString()

	statusToOutcome := func(status agentpkg.Status) Outcome {
		switch status {
		case agentpkg.StatusIdle:
			return OutcomeIdle
		case agentpkg.StatusRunning, agentpkg.StatusWaiting:
			return OutcomeReady
		case agentpkg.StatusError:
			return OutcomeError
		default:
			return OutcomeIdle
		}
	}

	fire := func(status agentpkg.Status) {
		ev := []EvidenceRef{
			{Key: "probe_id", Value: probeID},
			{Key: "probe_kind", Value: string(KindReadiness)},
			{Key: "readiness_status", Value: string(status)},
		}
		cb(statusToOutcome(status), w.Token(), ev)
	}

	check := func() {
		res, ok := poller(spec.AgentType, spec.Target)
		if !ok {
			if !seeded {
				seeded = true
				fire(agentpkg.StatusIdle)
			}
			return
		}
		if !seeded {
			seeded = true
			last = res.Status
			fire(res.Status)
			return
		}
		if res.Status != last {
			last = res.Status
			fire(res.Status)
		}
	}

	check()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
