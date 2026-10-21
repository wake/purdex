// Package qualify is the pure part of qualification (spec §7): when a host should start a qualification pair for a task, and whether the
// two halves of a pair agree. It reads no store, no queue and no clock; the coordinator (WWD-5b-2b) calls it.
package qualify

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/wire"
)

// RetryAfter is how long the automatic trigger waits after a pair that did not earn the label, unless the host's fingerprint changed.
const RetryAfter = 24 * time.Hour

// Task is a manifest task that has a `qualify` and names a reference host.
type Task struct {
	Repo      string
	Name      string
	Reference string   // the alias of the reference host
	Args      []string // qualify.args: what both halves run
	Needs     []string // the task's environment labels (os:, arch:, tool:); the host must have the tool: ones to be asked to qualify
	// DefHash is the manifest's definition hash of the task (manifest.Task.DefinitionHash): what the pair ran, as a whole. The
	// coordinator compares it again when the pair is done, so a result of an older definition never earns the label.
	DefHash string
	// Summary is the manifest's summary kind of the task (go-json, vitest-json, junit, plain; empty is plain). Compare needs it to tell a task
	// with no test list from a report that was not read. It is not part of Identity: DefHash already covers the summary kind.
	Summary string
	// Commit is the commit the task was listed at, when the clone's manifest is read at a manifest_ref (#3658): the pair is snapshotted at
	// exactly this commit, never at what the ref names by the time Prepare runs. Empty for a clone read in its working tree. It is not part
	// of Identity: a new commit that leaves the qualification the same is not a new qualification.
	Commit string
}

// Identity is a stable string for the task as qualification sees it: where it is, who is its reference, what both halves run, what the
// host needs, and the definition hash. Two tasks with the same identity are the same qualification.
func (t Task) Identity() string {
	needs := slices.Clone(t.Needs)
	slices.Sort(needs)
	return strings.Join([]string{t.Repo, t.Name, t.Reference, t.DefHash, strings.Join(t.Args, "\x00"), strings.Join(needs, "\x00")}, "\x01")
}

// Label is the earned label of the task.
func (t Task) Label() string { return labels.TaskLabel(t.Repo, t.Name) }

// Outcome is how a pair ended.
type Outcome string

const (
	// OutcomeMatch: both halves ran where they were meant to, and agree. The host earns the label.
	OutcomeMatch Outcome = "match"
	// OutcomeMismatch: both ran where they were meant to and disagree. No label; the retry waits (RetryAfter).
	OutcomeMismatch Outcome = "mismatch"
	// OutcomeVoid: the pair proves nothing (a half ran on another host, did not run to an exit, or the reference itself did not
	// pass). No label and no disagreement; the retry waits like a mismatch.
	OutcomeVoid Outcome = "void"
)

// Attempt is the last pair of a task on a host that did not earn the label.
type Attempt struct {
	At          time.Time
	Outcome     Outcome
	Fingerprint map[string]string // the host's fingerprint when the pair was queued
}

// Host is what is known of the host that would qualify.
type Host struct {
	Alias       string
	WorkerOn    bool
	Labels      []string // advertised now
	Fingerprint map[string]string
}

// Input is everything Decide looks at.
type Input struct {
	Now   time.Time
	Host  Host
	Tasks []Task
	// TasksErr is what could not be read of the manifests behind Tasks (nil when all could).
	TasksErr error
	// Earned maps a task label to its state (wire.EarnedLabelEarned or wire.EarnedLabelQuarantined); a label that is not there was never earned.
	Earned map[string]string
	// InFlight holds the task labels that have an unfinished pair on this host.
	InFlight map[string]bool
	// Last holds the last pair that did not earn the label, by task label.
	Last map[string]Attempt
}

// Why a task is not asked for. The errors are the text `pdx wwd qualify` shows.
var (
	ErrNoQualify = errors.New("the task has no qualify")
	// ErrNoDefinition: the task came without the manifest's definition hash. The hash is what lets the coordinator tell, when a pair is
	// done, that the task is still the one that was run; without it that check would compare an empty string with itself.
	ErrNoDefinition = errors.New("the task has no definition hash")
	ErrReference    = errors.New("this host is the task's reference: it holds the label without qualifying")
	ErrWorkerOff    = errors.New("this host's worker is off")
	ErrInFlight     = errors.New("a qualification pair for this task is already running")
	ErrAlreadyHas   = errors.New("this host already holds the label")
)

// MissingError says what the host lacks to run the task at all.
type MissingError struct{ Labels []string }

func (e *MissingError) Error() string {
	return "this host lacks " + strings.Join(e.Labels, ", ")
}

// Check says whether the host may be asked to qualify t now, leaving the retry rule aside (that is the automatic trigger's, and a
// person's request does not wait for it): nil, or why not.
func Check(in Input, t Task) error {
	switch {
	case t.Reference == "" || t.Name == "":
		return ErrNoQualify
	case t.DefHash == "":
		return ErrNoDefinition
	case t.Reference == in.Host.Alias:
		return ErrReference
	case !in.Host.WorkerOn:
		return ErrWorkerOff
	case in.Earned[t.Label()] == wire.EarnedLabelEarned:
		return ErrAlreadyHas
	case in.InFlight[t.Label()]:
		return ErrInFlight
	}
	var missing []string
	if want := "repo:" + t.Repo; !slices.Contains(in.Host.Labels, want) {
		missing = append(missing, want)
	}
	for _, n := range t.Needs {
		if strings.HasPrefix(n, "tool:") && !slices.Contains(in.Host.Labels, n) {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return &MissingError{Labels: missing}
	}
	return nil
}

// RetryWaits says whether the automatic trigger still has to wait for the task: its last pair did not earn the label, less than
// RetryAfter ago, and the host's fingerprint is still the one that pair was queued under. A change in the fingerprint, or exactly
// RetryAfter, ends the wait.
func RetryWaits(in Input, t Task) bool {
	a, ok := in.Last[t.Label()]
	if !ok {
		return false
	}
	if !sameFingerprint(a.Fingerprint, in.Host.Fingerprint) {
		return false
	}
	return in.Now.Sub(a.At) < RetryAfter
}

// Decide is the tasks the automatic trigger starts a pair for now: those Check lets through and whose retry is not waiting, in the order
// of in.Tasks, at most one per label.
func Decide(in Input) []Task {
	var out []Task
	seen := map[string]bool{}
	for _, t := range in.Tasks {
		if seen[t.Label()] || Check(in, t) != nil || RetryWaits(in, t) {
			continue
		}
		seen[t.Label()] = true
		out = append(out, t)
	}
	return out
}

// Manual is the task a person asked for by `<repo>/<task>`: Check, without the retry wait. An unknown name is an error.
func Manual(in Input, repoTask string) (Task, error) {
	for _, t := range in.Tasks {
		if t.Repo+"/"+t.Name == repoTask {
			if err := Check(in, t); err != nil {
				return Task{}, err
			}
			return t, nil
		}
	}
	return Task{}, &UnknownTaskError{Name: repoTask}
}

// UnknownTaskError: the name is not a task with a qualify in this host's prepared clones (a request for it is a 404, not a refusal).
type UnknownTaskError struct{ Name string }

func (e *UnknownTaskError) Error() string {
	return fmt.Sprintf("no task %q with a qualify in this host's prepared clones", e.Name)
}

// sameFingerprint is equality of two fingerprints; an empty one is never the same as anything (a host that reported nothing has not
// shown it is unchanged, so the wait ends).
func sameFingerprint(a, b map[string]string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
