package qualify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// This file is the coordinator of qualification (spec §7): it submits the two halves of a pair to this host's queue, settles them when both
// are done, and grants the label. It is a library: WWD-5b-2b adds it and wires NOTHING (cmd/wwd does not build it, no trigger runs it, no
// route starts it); every way of starting a pair is WWD-5b-2c, which waits for the passed-test digest writers (docs/plans, WWD-5).
// TestNothingWiresTheCoordinatorYet pins that, and 5b-2c is the PR that changes it.
//
// State is in memory. A restart forgets the pairs in flight (their two jobs go on and finish, unobserved) and the rate rule's memory
// (the next automatic try may come before 24 h); a new pair for the same task supersedes the old jobs by their supersede keys, so a
// restart does not leave two pairs running.

// Queue is the part of the origin's queue the coordinator uses (*queue.Queue).
type Queue interface {
	Submit(ctx context.Context, s wire.JobSubmission) (wire.JobSubmissionResponse, error)
	Get(ctx context.Context, id string) (wire.Job, error)
	Cancel(ctx context.Context, id string) (wire.Job, error)
}

// Store is the earned-label table (store.Store).
type Store interface {
	EarnedLabels() ([]wire.EarnedLabel, error)
	SaveEarnedLabel(wire.EarnedLabel) error
}

// Source is what the coordinator reads of this host.
type Source interface {
	// Host is the host now: its alias, whether its worker is on, the labels it advertises and its fingerprint.
	Host() Host
	// Tasks is the tasks of the prepared clones that have a qualify with a reference host.
	Tasks() []Task
	// Prepare takes the snapshot of the task's clone and returns the submission of the task as the manifest declares it, with the qualify
	// args as its args (the caller turns it into the two halves). One call, so both halves share one snapshot.
	Prepare(ctx context.Context, t Task) (wire.JobSubmission, error)
}

// Config is what New needs.
type Config struct {
	Queue  Queue
	Store  Store
	Source Source
	// Now is the clock (default time.Now).
	Now func() time.Time
	// Granted is called after a label was earned, so the daemon advertises it without waiting for the next facts record (RefreshLabels).
	Granted func()
}

// Record is how a pair ended, for `pdx wwd qualify --status` and the log.
type Record struct {
	Label     string
	Outcome   Outcome
	Why       string
	At        time.Time
	Candidate string // job ids
	Reference string
}

// Started is a pair that was queued.
type Started struct {
	Label     string
	Candidate string
	Reference string
}

type openPair struct {
	task      Task
	cand, ref string
	at        time.Time
	fp        map[string]string
	tree      string
}

// Coordinator runs the pairs. Safe for concurrent use: Tick and Request take the same lock, so two triggers racing start one pair.
type Coordinator struct {
	cfg     Config
	mu      sync.Mutex
	pairs   map[string]*openPair
	last    map[string]Attempt
	history []Record
}

const maxHistory = 50

// SessionName is the submitter session of the jobs the coordinator queues.
const SessionName = "wwd-qualify"

// New builds a coordinator. Nothing runs until Tick or Request is called.
func New(cfg Config) *Coordinator {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Coordinator{cfg: cfg, pairs: map[string]*openPair{}, last: map[string]Attempt{}}
}

// Tick settles the pairs that are done, then starts the pairs the automatic trigger asks for. It is meant to be called at the facts
// cadence; every call is cheap when nothing is due.
func (c *Coordinator) Tick(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settle(ctx)
	in, err := c.input()
	if err != nil {
		log.Printf("wwd: qualify: %v", err)
		return
	}
	for _, t := range Decide(in) {
		if _, err := c.start(ctx, t); err != nil {
			log.Printf("wwd: qualify %s: %v", t.Label(), err)
		}
	}
}

// Request starts a pair for <repo>/<task> on a person's word: the checks of the automatic trigger without its 24 h wait.
func (c *Coordinator) Request(ctx context.Context, repoTask string) (Started, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settle(ctx)
	in, err := c.input()
	if err != nil {
		return Started{}, err
	}
	t, err := Manual(in, repoTask)
	if err != nil {
		return Started{}, err
	}
	return c.start(ctx, t)
}

// Status is the pairs in flight (labels, oldest first by label) and the last records, newest first.
func (c *Coordinator) Status() (inFlight []Started, recent []Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.pairs {
		inFlight = append(inFlight, Started{Label: p.task.Label(), Candidate: p.cand, Reference: p.ref})
	}
	slices.SortFunc(inFlight, func(a, b Started) int { return strings.Compare(a.Label, b.Label) })
	for i := len(c.history) - 1; i >= 0; i-- {
		recent = append(recent, c.history[i])
	}
	return inFlight, recent
}

func (c *Coordinator) input() (Input, error) {
	rows, err := c.cfg.Store.EarnedLabels()
	var damaged *store.SkippedRowsError
	if err != nil && !errors.As(err, &damaged) { // damaged rows are left out and the rest is the answer; any other error is no answer
		return Input{}, fmt.Errorf("reading the earned labels: %w", err)
	}
	in := Input{Now: c.cfg.Now(), Host: c.cfg.Source.Host(), Tasks: c.cfg.Source.Tasks(),
		Earned: map[string]string{}, InFlight: map[string]bool{}, Last: map[string]Attempt{}}
	for _, r := range rows {
		in.Earned[r.Label] = r.State
	}
	for l := range c.pairs {
		in.InFlight[l] = true
	}
	for l, a := range c.last {
		in.Last[l] = a
	}
	return in, nil
}

// halves turns the task's own submission into the two halves. The candidate half asks for what a host that has not earned the label can
// offer: no os:/arch: (those are the task's reason to be Mac-only until a host is qualified) and not the task's own label, plus
// host:<candidate>. The reference half keeps every label the task has, plus host:<reference>. Both are dev class, never cacheable, never
// interruptible, and carry supersede keys of their own so that a newer pair replaces an older one that is still around.
func halves(base wire.JobSubmission, t Task, candidate string) (cand, ref wire.JobSubmission) {
	own := t.Label()
	common := func(s wire.JobSubmission, role, host string) wire.JobSubmission {
		s.Class = wire.ClassDev
		s.Cacheable = false
		s.Interruptible = false
		s.AdHoc = false
		s.SupersedeKey = "qualify:" + candidate + ":" + own + ":" + role
		s.Submitter = wire.Submitter{Session: SessionName, Host: candidate}
		s.Labels = append(slices.Clone(s.Labels), "host:"+host)
		return s
	}
	cb := base
	cb.Labels = nil
	for _, l := range base.Labels {
		if l == own || strings.HasPrefix(l, "os:") || strings.HasPrefix(l, "arch:") {
			continue
		}
		cb.Labels = append(cb.Labels, l)
	}
	cand = common(cb, "candidate", candidate)
	rb := base
	rb.Labels = slices.Clone(base.Labels)
	ref = common(rb, "reference", t.Reference)
	return cand, ref
}

func (c *Coordinator) start(ctx context.Context, t Task) (Started, error) {
	host := c.cfg.Source.Host()
	base, err := c.cfg.Source.Prepare(ctx, t)
	if err != nil {
		c.note(t, OutcomeVoid, "preparing the snapshot: "+err.Error(), "", "", host.Fingerprint)
		return Started{}, fmt.Errorf("preparing the snapshot: %w", err)
	}
	cand, ref := halves(base, t, host.Alias)
	cr, err := c.cfg.Queue.Submit(ctx, cand)
	if err != nil {
		c.note(t, OutcomeVoid, "queueing the candidate half: "+err.Error(), "", "", host.Fingerprint)
		return Started{}, fmt.Errorf("queueing the candidate half: %w", err)
	}
	rr, err := c.cfg.Queue.Submit(ctx, ref)
	if err != nil {
		_, _ = c.cfg.Queue.Cancel(ctx, cr.ID) // one half alone proves nothing and would use the host for nothing
		c.note(t, OutcomeVoid, "queueing the reference half: "+err.Error(), cr.ID, "", host.Fingerprint)
		return Started{}, fmt.Errorf("queueing the reference half: %w", err)
	}
	c.pairs[t.Label()] = &openPair{task: t, cand: cr.ID, ref: rr.ID, at: c.cfg.Now(), fp: host.Fingerprint, tree: base.Snapshot.Tree}
	return Started{Label: t.Label(), Candidate: cr.ID, Reference: rr.ID}, nil
}

// settle looks at every pair in flight and finishes those that are done.
func (c *Coordinator) settle(ctx context.Context) {
	for label, p := range c.pairs {
		cj, cerr := c.cfg.Queue.Get(ctx, p.cand)
		rj, rerr := c.cfg.Queue.Get(ctx, p.ref)
		switch {
		case errors.Is(cerr, queue.ErrNotFound) || errors.Is(rerr, queue.ErrNotFound):
			c.finish(label, p, Verdict{Outcome: OutcomeVoid, Why: "a half of the pair is gone from the queue"})
			continue
		case cerr != nil || rerr != nil:
			continue // the queue could not be read just now: look again on the next tick
		}
		cDone, rDone := cj.State.Terminal(), rj.State.Terminal()
		ranC, ranR := ranToAnExit(cj), ranToAnExit(rj)
		switch {
		case cDone && rDone:
			c.finish(label, p, Compare(Half{Want: c.cfg.Source.Host().Alias, Result: cj.Result}, Half{Want: p.task.Reference, Result: rj.Result}))
		case (cDone && !ranC) || (rDone && !ranR):
			// one half ended without running (cancelled, superseded, expired): the pair is void, and the other half is not worth waiting for
			c.cancelQuietly(ctx, p, cDone, rDone)
			c.finish(label, p, Verdict{Outcome: OutcomeVoid, Why: "a half ended without running to an exit"})
		}
	}
}

func ranToAnExit(j wire.Job) bool { return j.Result != nil && j.Result.Exit != nil }

func (c *Coordinator) cancelQuietly(ctx context.Context, p *openPair, candDone, refDone bool) {
	if !candDone {
		_, _ = c.cfg.Queue.Cancel(ctx, p.cand)
	}
	if !refDone {
		_, _ = c.cfg.Queue.Cancel(ctx, p.ref)
	}
}

// finish records the end of a pair; a match earns the label, unless the host's toolchain changed while the pair ran (what both halves
// measured is then not what the host is).
func (c *Coordinator) finish(label string, p *openPair, v Verdict) {
	delete(c.pairs, label)
	now := c.cfg.Now()
	if v.Outcome == OutcomeMatch && !sameFingerprint(p.fp, c.cfg.Source.Host().Fingerprint) {
		v = Verdict{Outcome: OutcomeVoid, Why: "the host's toolchain changed while the pair ran"}
	}
	if v.Outcome == OutcomeMatch {
		err := c.cfg.Store.SaveEarnedLabel(wire.EarnedLabel{Label: label, State: wire.EarnedLabelEarned, Fingerprint: p.fp, Snapshot: p.tree, GrantedAt: now})
		if err != nil {
			log.Printf("wwd: qualify %s: saving the label: %v", label, err)
			v = Verdict{Outcome: OutcomeVoid, Why: "saving the label failed: " + err.Error()}
		} else {
			delete(c.last, label)
			if c.cfg.Granted != nil {
				c.cfg.Granted()
			}
		}
	}
	if v.Outcome != OutcomeMatch {
		c.last[label] = Attempt{At: now, Outcome: v.Outcome, Fingerprint: p.fp}
	}
	c.remember(Record{Label: label, Outcome: v.Outcome, Why: v.Why, At: now, Candidate: p.cand, Reference: p.ref})
}

// note records a pair that never got going.
func (c *Coordinator) note(t Task, o Outcome, why, cand, ref string, fp map[string]string) {
	now := c.cfg.Now()
	c.last[t.Label()] = Attempt{At: now, Outcome: o, Fingerprint: fp}
	c.remember(Record{Label: t.Label(), Outcome: o, Why: why, At: now, Candidate: cand, Reference: ref})
}

func (c *Coordinator) remember(r Record) {
	log.Printf("wwd: qualify %s: %s (%s)", r.Label, r.Outcome, r.Why)
	c.history = append(c.history, r)
	if len(c.history) > maxHistory {
		c.history = c.history[len(c.history)-maxHistory:]
	}
}
