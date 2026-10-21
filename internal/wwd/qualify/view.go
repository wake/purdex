package qualify

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// This file is the read side of qualification (WWD-7b2-0a): what a host holds of each task label and what happens next, decided here so a
// reader only draws it. It reads no store and no clock; the coordinator gathers the pieces (Qualification).

// Pair is a pair in flight, as the view needs it.
type Pair struct {
	Label     string
	Candidate string // job ids
	Reference string
	Since     time.Time
}

// BuildView answers for one host. in is Decide's input (Earned and InFlight are rebuilt from rows and pairs, so the three cannot disagree);
// rows are the earned-label rows, pairs the pairs in flight, history the records oldest first (the coordinator's order), unreadable the repos
// whose manifest could not be read.
//
// Two axes per task. hold, the label now: reference (this host is the task's reference and advertises the label), else the row's state
// (earned, quarantined), else none. progress, what comes next, in this order:
//
//  1. pairing, while a pair is in flight and the host does not already hold the label (a held label has no pair to wait for);
//  2. idle, when hold is reference or earned, and for a row whose task is gone;
//  3. blocked, the first refusal of Check in Check's order: no_definition, reference_missing (the host is the reference but does not
//     advertise the label), worker_off, missing;
//  4. waiting, when RetryWaits holds;
//  5. ready.
//
// The valid combinations: earned and reference only with idle; quarantined with anything but idle, except idle when the task is gone, and
// blocked only by no_definition, worker_off or missing; none with anything but idle. A row whose repo is in unreadable is not listed: the
// task is hidden, not gone.
func BuildView(in Input, rows []wire.EarnedLabel, pairs []Pair, history []Record, unreadable []string) wire.HostQualification {
	in.Earned = map[string]string{}
	in.InFlight = map[string]bool{}
	rowOf := map[string]wire.EarnedLabel{}
	for _, r := range rows {
		in.Earned[r.Label] = r.State
		rowOf[r.Label] = r
	}
	pairOf := map[string]Pair{}
	for _, p := range pairs {
		in.InFlight[p.Label] = true
		pairOf[p.Label] = p
	}
	hidden := map[string]bool{}
	for _, r := range unreadable {
		hidden[r] = true
	}

	out := wire.HostQualification{Host: in.Host.Alias, Tasks: []wire.QualificationTask{}, Recent: []wire.QualifyRecord{}}
	if len(unreadable) > 0 {
		out.Unreadable = slices.Clone(unreadable)
	}
	seen := map[string]bool{}
	for _, t := range in.Tasks {
		l := t.Label()
		if seen[l] {
			continue
		}
		seen[l] = true
		out.Tasks = append(out.Tasks, taskView(in, t, rowOf, pairOf, history))
	}
	for _, r := range rows {
		if seen[r.Label] || hidden[repoOfLabel(r.Label)] {
			continue
		}
		seen[r.Label] = true
		tk := wire.QualificationTask{Label: r.Label, Gone: true, Hold: r.State, Progress: wire.QualProgressIdle, Earned: earnedView(r)}
		out.Tasks = append(out.Tasks, tk)
	}
	slices.SortFunc(out.Tasks, func(a, b wire.QualificationTask) int { return strings.Compare(a.Label, b.Label) })

	for i := len(history) - 1; i >= 0; i-- {
		h := history[i]
		out.Recent = append(out.Recent, wire.QualifyRecord{Label: h.Label, Outcome: string(h.Outcome), Why: h.Why, At: h.At, Candidate: h.Candidate, Reference: h.Reference})
	}
	return out
}

func taskView(in Input, t Task, rowOf map[string]wire.EarnedLabel, pairOf map[string]Pair, history []Record) wire.QualificationTask {
	l := t.Label()
	tk := wire.QualificationTask{Label: l, Reference: t.Reference, Hold: wire.QualHoldNone}
	switch row, has := rowOf[l]; {
	case t.Reference != "" && t.Reference == in.Host.Alias && slices.Contains(in.Host.Labels, l):
		tk.Hold = wire.QualHoldReference
	case has && row.State == wire.EarnedLabelQuarantined && t.Reference == in.Host.Alias:
		// the manifest made this host the reference after the row was written: a reference is never quarantined, so the old row says nothing
		// now (and quarantined + reference_missing is not a valid combination)
	case has:
		tk.Hold = row.State
		tk.Earned = earnedView(row)
	}
	held := tk.Hold == wire.QualHoldReference || tk.Hold == wire.QualHoldEarned
	if p, ok := pairOf[l]; ok && !held {
		tk.Progress = wire.QualProgressPairing
		tk.Pair = &wire.QualificationPair{Candidate: p.Candidate, Reference: p.Reference, Since: p.Since}
		return tk
	}
	if held {
		tk.Progress = wire.QualProgressIdle
		return tk
	}
	if err := Check(in, t); err != nil {
		tk.Progress = wire.QualProgressBlocked
		tk.Blocked = blockedView(in, t, err)
		return tk
	}
	if RetryWaits(in, t) {
		a := in.Last[l]
		tk.Progress = wire.QualProgressWaiting
		tk.Last = &wire.QualificationLast{Outcome: string(a.Outcome), At: a.At, Why: lastWhy(history, l, a.Outcome)}
		retry := a.At.Add(RetryAfter)
		tk.RetryAt = &retry
		return tk
	}
	tk.Progress = wire.QualProgressReady
	return tk
}

func earnedView(r wire.EarnedLabel) *wire.QualificationEarned {
	return &wire.QualificationEarned{GrantedAt: r.GrantedAt, Fingerprint: r.Fingerprint, Snapshot: r.Snapshot}
}

// blockedView is Check's refusal as the view says it.
func blockedView(in Input, t Task, err error) *wire.QualificationBlocked {
	var missing *MissingError
	switch {
	case errors.Is(err, ErrNoQualify), errors.Is(err, ErrNoDefinition):
		return &wire.QualificationBlocked{Code: wire.QualBlockedNoDefinition}
	case errors.Is(err, ErrReference):
		// the reference host that does not advertise the label: what ReferenceLabels wants and the host lacks
		return &wire.QualificationBlocked{Code: wire.QualBlockedReferenceMissing, Labels: referenceLacks(in, t)}
	case errors.Is(err, ErrWorkerOff):
		return &wire.QualificationBlocked{Code: wire.QualBlockedWorkerOff}
	case errors.As(err, &missing):
		return &wire.QualificationBlocked{Code: wire.QualBlockedMissing, Labels: missing.Labels}
	}
	return &wire.QualificationBlocked{Code: "unknown"} // ErrInFlight / ErrAlreadyHas are answered before Check; a new refusal shows as it is
}

// referenceLacks is the labels the reference host needs to advertise the task label (labels.ReferenceLabels): its repo and every need.
func referenceLacks(in Input, t Task) []string {
	var out []string
	if want := "repo:" + t.Repo; !slices.Contains(in.Host.Labels, want) {
		out = append(out, want)
	}
	for _, n := range t.Needs {
		if !slices.Contains(in.Host.Labels, n) {
			out = append(out, n)
		}
	}
	return out
}

// lastWhy is the why of the newest record of the label with the outcome of the last attempt: the attempt is the newest unsuccessful pair
// (a later match would have forgotten it), so that record is the attempt's. After a restart the history is empty and there is none.
func lastWhy(history []Record, label string, outcome Outcome) string {
	for i := len(history) - 1; i >= 0; i-- {
		if h := history[i]; h.Label == label && h.Outcome == outcome {
			return h.Why
		}
	}
	return ""
}

// repoOfLabel is <repo> of task:<repo>/<task>.
func repoOfLabel(label string) string {
	repo, _, _ := strings.Cut(strings.TrimPrefix(label, "task:"), "/")
	return repo
}
