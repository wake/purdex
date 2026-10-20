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
	"strings"

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
	foundLiteral := false // the task spells the command's whole-tree pattern out (go-all runs `./...`)
	whole := hasWholeTree(canon)
	for _, n := range names {
		args, ok := matchTask(root, cwd, canon, m.Tasks[n])
		if !ok {
			continue
		}
		literal := whole && spellsWholeTree(m.Tasks[n])
		better := found == nil ||
			(literal && !foundLiteral) || // the whole tree is the task that says `./...`, whatever the class of one that takes any package list
			(literal == foundLiteral && classRank(m.Tasks[n].Class) < classRank(m.Tasks[found.Task].Class))
		if !better {
			continue
		}
		foundLiteral = literal
		// a pattern that expands is not for the fast lane, even when a count limit would let the arguments in
		queued := !m.Tasks[n].Local.Permits(len(args)) || (m.Tasks[n].Args == manifest.ArgsPackages && anyRecursive(args))
		found = &Result{Action: ActionRewrite, Task: n, Args: args, Queued: queued}
		if queued {
			found.Labels = append([]string(nil), m.Tasks[n].Labels...)
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

// hintFor is the task nearest to the command: the one whose plain words (a task's flags and their values are not) agree with the command's
// the longest from the front. The command's own flags and their values are not words of the comparison either, so `go test -count=1 ./...`
// is near go-all, which runs `./...`, and not near the first task by name.
func hintFor(m *manifest.Manifest, names []string, cmd []string) string {
	words := plainWords(dropRunFlags(normalizeGoFlags(cmd)))
	best, bestN, bestFull := "", 0, false
	for _, n := range names {
		var lits []string
		for _, w := range m.Tasks[n].Shape() {
			if w.Kind == manifest.ShapeLiteral {
				lits = append(lits, w.Text)
			}
		}
		tw := plainWords(normalize(lits))
		k := 0
		for k < len(tw) && k < len(words) && tw[k] == words[k] {
			k++
		}
		// nearer: more words agree; then the task all of whose words agree (its plain words are the command's, the task asks for no more than the
		// command has) over one that wants other words (e2e's ./cmd/wwd/); then the lower class
		full := k == len(tw)
		if k > bestN || (k == bestN && k > 0 && ((full && !bestFull) || (full == bestFull && classRank(m.Tasks[n].Class) < classRank(m.Tasks[best].Class)))) {
			best, bestN, bestFull = n, k, full
		}
	}
	return best
}

// plainWords are the words of a command that are not flags or the values of flags: `go test -run X -count=1 ./...` is `go test ./...`.
// A word that looks like a path or a pattern at the very end (`./...`) is the command's own word, as in matchTask.
func plainWords(w []string) []string {
	var out []string
	for i := 0; i < len(w); i++ {
		if !strings.HasPrefix(w[i], "-") {
			out = append(out, w[i])
			continue
		}
		if strings.Contains(w[i], "=") || i+1 >= len(w) {
			continue
		}
		// a flag of go that takes a value takes the next word, whatever it looks like; for any other flag the word is guessed at
		if goValueFlags[w[i]] || valueLike(w[i+1], i+1 == len(w)-1) {
			i++
		}
	}
	return out
}

func hasWholeTree(cmd []string) bool {
	for _, w := range cmd {
		if wholeTree(w) {
			return true
		}
	}
	return false
}

// spellsWholeTree says whether the task's own command has the whole-tree pattern as a word.
func spellsWholeTree(t *manifest.Task) bool {
	for _, w := range t.Shape() {
		if w.Kind == manifest.ShapeLiteral && wholeTree(w.Text) {
			return true
		}
	}
	return false
}

func anyRecursive(args []string) bool {
	for _, a := range args {
		if Recursive(a) {
			return true
		}
	}
	return false
}
