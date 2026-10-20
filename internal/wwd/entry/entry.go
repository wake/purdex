// Package entry is the part of the hook (wwd spec §8.2) that knows the repo's task manifest: it recognises a test or build command,
// finds the declared task whose command shape it is, and says what the hook should do with it. The hook keeps no copy of these
// rules; it asks `pdx entry match`.
//
// The package is four files: entry.go (the answer and the decision), discover.go (finding the repository's manifest),
// normalize.go (what a command is once its runner is off, and which commands are test or build ones) and match.go (a command
// against a task's shape, and its arguments in the form `pdx test` takes).
package entry

import (
	"sort"

	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/wire"
)

// ManifestPath is the manifest's place in a repository.
const ManifestPath = ".wwd/tasks.toml"

// Action is what the hook does with one command.
type Action string

const (
	// ActionNone leaves the command alone: it is not a test or build command, there is no manifest, or the manifest does not read
	// (Broken says which of the last two).
	ActionNone Action = "none"
	// ActionRewrite runs the command as `pdx test <Task> <Args…>`.
	ActionRewrite Action = "rewrite"
	// ActionAdhoc runs the command as `pdx test --adhoc -- <argv…>`: the manifest's mode is rewrite and no task declares the command.
	// It is never queued (spec §8.3: the fast lane, on a heavy slot; the output streams as it runs), so the hook leaves the call in the
	// foreground.
	ActionAdhoc Action = "adhoc"
	// ActionAdvise lets the command run as it is and says which task to use.
	ActionAdvise Action = "advise"
	// ActionDeny refuses the command and says which task to use.
	ActionDeny Action = "deny"
)

// Result is the answer for one command.
type Result struct {
	Action Action `json:"action"`
	// Task and Args are the task a rewrite runs and its arguments (relative to the task's cwd, as `pdx test` takes them); Args is
	// left out when the task takes none.
	Task string   `json:"task,omitempty"`
	Args []string `json:"args,omitempty"`
	// Queued says a rewrite goes through the queue, where it may wait (the task does not take the local fast lane for this many
	// arguments); the hook then runs the call in the background.
	Queued bool `json:"queued,omitempty"`
	// Labels are the task's labels, given for a queued rewrite: what a host must hold to take the job (GateOnPool judges them against the pool).
	Labels []string `json:"labels,omitempty"`
	// Mode is the manifest's [entry].mode when no task matched ("advise" when the manifest names none).
	Mode string `json:"mode,omitempty"`
	// Hint is the task nearest to the command, for the advice and the refusal.
	Hint string `json:"hint,omitempty"`
	// Reason says why nothing is done (Action none), or why a mode became advice.
	Reason string `json:"reason,omitempty"`
	// Broken marks a manifest that is there but does not read: the answer is none (the hook fails open, a command is never stopped
	// by a broken entry), but the hook can say so instead of staying quiet.
	Broken bool `json:"broken,omitempty"`
}

// Match decides what to do with the command argv, run in cwd.
func Match(cwd string, argv []string) Result {
	canon := normalize(argv)
	if !isFamily(canon) {
		return Result{Action: ActionNone}
	}
	root, m, err := findManifest(cwd)
	if err != nil {
		return Result{Action: ActionNone, Reason: err.Error(), Broken: !isNoManifest(err)}
	}
	names := make([]string, 0, len(m.Tasks))
	for n := range m.Tasks {
		names = append(names, n)
	}
	sort.Strings(names)
	// Tasks of one shape (a development task and a merge task that run the same command) are told apart by class: a command typed by
	// hand is the lowest one, and the first name among equals.
	var found *Result
	for _, n := range names {
		if args, ok := matchTask(root, cwd, canon, m.Tasks[n]); ok {
			if found == nil || classRank(m.Tasks[n].Class) < classRank(m.Tasks[found.Task].Class) {
				found = &Result{Action: ActionRewrite, Task: n, Args: args, Queued: !m.Tasks[n].Local.Permits(len(args))}
				if found.Queued {
					found.Labels = append([]string(nil), m.Tasks[n].Labels...)
				}
			}
		}
	}
	if found != nil {
		return *found
	}
	mode := string(m.Entry.Mode) // the manifest's default is advise
	r := Result{Mode: mode, Hint: hintFor(m, names, canon)}
	switch manifest.EntryMode(mode) {
	case manifest.EntryDeny:
		r.Action = ActionDeny
	case manifest.EntryRewrite:
		// An undeclared command runs as an ad-hoc job (spec §8.3). Whether the call can be turned into one (a plain command with a
		// known directory) is the hook's to see; where it cannot, it advises with the hint.
		r.Action = ActionAdhoc
	default:
		r.Action = ActionAdvise
	}
	return r
}

// classRank orders the job classes: a development task before a merge task before a release one (a task's class is the most a
// caller may ask for; an unset one is dev).
func classRank(c wire.JobClass) int {
	switch c {
	case wire.ClassMerge:
		return 1
	case wire.ClassRelease:
		return 2
	}
	return 0
}

// hintFor is the task nearest to the command: the one whose literal words agree with the command's the longest from the front.
func hintFor(m *manifest.Manifest, names []string, cmd []string) string {
	best, bestN := "", 0
	for _, n := range names {
		var lits []string
		for _, w := range m.Tasks[n].Shape() {
			if w.Kind == manifest.ShapeLiteral {
				lits = append(lits, w.Text)
			}
		}
		tw := normalize(lits)
		k := 0
		for k < len(tw) && k < len(cmd) && tw[k] == cmd[k] {
			k++
		}
		if k > bestN {
			best, bestN = n, k
		}
	}
	return best
}
