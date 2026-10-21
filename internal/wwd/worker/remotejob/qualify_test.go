package remotejob

import (
	"slices"
	"testing"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/wire"
)

const qualifyTasks = `
[tasks.tsc]
run    = "make tsc"
args   = "none"
labels = ["os:darwin", "arch:arm64", "tool:node", "task:purdex/tsc"]
locks  = ["db"]
qualify = { args = [], reference = "air26" }
`

// half makes the job the coordinator queues for one half of a pair, as the origin would have sent it.
func half(role, host string, f func(*wire.Job)) wire.Job {
	task := []string{"os:darwin", "arch:arm64", "tool:node", "task:purdex/tsc"}
	j := jobOf("tsc", wire.ClassDev)
	j.Repo = wire.Repo{Name: "purdex"}
	j.Labels = labels.QualifyHalfLabels(task, "task:purdex/tsc", role, host)
	j.Locks = []string{"db"}
	j.Interruptible = true
	j.SupersedeKey = labels.QualifyKey("mserv", "task:purdex/tsc", role)
	j.Submitter = wire.Submitter{Session: labels.QualifySession, Host: "mserv"}
	if f != nil {
		f(&j)
	}
	return j
}

func otherRole(r string) string {
	if r == labels.RoleCandidate {
		return labels.RoleReference
	}
	return labels.RoleCandidate
}

// A half of a qualification pair carries labels the coordinator worked out (host:, no os:/arch:, dev, not cacheable): on another host than
// the origin it must be accepted when it is exactly that, for the role its key says and the host it runs on.
func TestAQualifyHalfIsCheckedAgainstTheRuleThatMadeIt(t *testing.T) {
	root := snapshot(t, qualifyTasks)
	for _, c := range []struct{ role, host string }{{labels.RoleReference, "air26"}, {labels.RoleCandidate, "mserv"}} {
		if _, err := PrepareAt(half(c.role, c.host, nil), root, t.TempDir(), c.host, "mserv"); err != nil {
			t.Errorf("%s half on %s: %v", c.role, c.host, err)
		}
	}
	for _, role := range []string{labels.RoleReference, labels.RoleCandidate} {
		for name, f := range map[string]func(*wire.Job){
			"label added": func(j *wire.Job) { j.Labels = append(j.Labels, "tool:extra") },
			"tool label missing": func(j *wire.Job) {
				j.Labels = slices.DeleteFunc(j.Labels, func(l string) bool { return l == "tool:node" })
			},
			"other host":        func(j *wire.Job) { j.Labels[len(j.Labels)-1] = "host:elsewhere" },
			"two hosts":         func(j *wire.Job) { j.Labels = append(j.Labels, "host:mserv") },
			"cacheable":         func(j *wire.Job) { j.Cacheable = true },
			"not interruptible": func(j *wire.Job) { j.Interruptible = false },
			"lock dropped":      func(j *wire.Job) { j.Locks = nil },
			"the other role":    func(j *wire.Job) { j.SupersedeKey = labels.QualifyKey("mserv", "task:purdex/tsc", otherRole(role)) },
			"candidate of the key is not the submitter's host": func(j *wire.Job) { j.SupersedeKey = labels.QualifyKey("elsewhere", "task:purdex/tsc", role) },
			"foreign task label":                               func(j *wire.Job) { j.SupersedeKey = labels.QualifyKey("mserv", "task:purdex/other", role) },
		} {
			p, err := PrepareAt(half(role, "air26", f), root, t.TempDir(), "air26", "mserv")
			if p != nil || code(t, err) != CodeMetaMismatch {
				t.Errorf("%s, %s: %v, %v", role, name, p, err)
			}
		}
		// a half that runs on a host other than the one named in its labels
		if _, err := PrepareAt(half(role, "air26", nil), root, t.TempDir(), "mserv", "mserv"); code(t, err) != CodeMetaMismatch {
			t.Errorf("%s half for another host: %v", role, err)
		}
		if _, err := PrepareAt(half(role, "air26", nil), root, t.TempDir(), "", "mserv"); code(t, err) != CodeMetaMismatch {
			t.Errorf("%s half where the worker has no name: %v", role, err)
		}
	}
}

// An alias may hold a colon: the pair of such a candidate is accepted all the same.
func TestAQualifyHalfOfACandidateWithAColonInItsAlias(t *testing.T) {
	root := snapshot(t, qualifyTasks)
	for _, role := range []string{labels.RoleReference, labels.RoleCandidate} {
		host := "air26" // the reference host of the task; the candidate runs on its own host
		if role == labels.RoleCandidate {
			host = "rack:a"
		}
		j := half(role, host, func(j *wire.Job) {
			j.SupersedeKey = labels.QualifyKey("rack:a", "task:purdex/tsc", role)
			j.Submitter.Host = "rack:a"
		})
		if _, err := PrepareAt(j, root, t.TempDir(), host, "rack:a"); err != nil {
			t.Errorf("%s: %v", role, err)
		}
	}
}

// A worker without a name accepts no half, not even one whose labels were worked out for the empty host.
func TestAWorkerWithoutANameAcceptsNoHalf(t *testing.T) {
	root := snapshot(t, qualifyTasks)
	j := half(labels.RoleReference, "", func(j *wire.Job) {
		j.Labels = labels.QualifyHalfLabels([]string{"os:darwin", "arch:arm64", "tool:node", "task:purdex/tsc"}, "task:purdex/tsc", labels.RoleReference, "")
	})
	if _, err := PrepareAt(j, root, t.TempDir(), "", "mserv"); code(t, err) != CodeMetaMismatch {
		t.Errorf("%v", err)
	}
}

// Being a qualify half takes both the submitter session and a qualify key; with only one of them the job is an ordinary job and its
// metadata is the task's own or nothing.
func TestOnlyTheSessionAndTheKeyTogetherMakeAQualifyHalf(t *testing.T) {
	root := snapshot(t, qualifyTasks)
	for name, f := range map[string]func(*wire.Job){
		"session only": func(j *wire.Job) { j.SupersedeKey = "tsc" },
		"key only":     func(j *wire.Job) { j.Submitter.Session = "someone" },
		"neither":      func(j *wire.Job) { j.SupersedeKey, j.Submitter.Session = "", "" },
	} {
		if p, err := PrepareAt(half(labels.RoleReference, "air26", f), root, t.TempDir(), "air26", "mserv"); p != nil || code(t, err) != CodeMetaMismatch {
			t.Errorf("%s: %v, %v", name, p, err)
		}
	}
	// and an ordinary job with the task's own metadata is as before
	j := jobOf("tsc", wire.ClassDev)
	j.Labels, j.Locks = []string{"os:darwin", "arch:arm64", "tool:node", "task:purdex/tsc"}, []string{"db"}
	if _, err := PrepareAt(j, root, t.TempDir(), "air26", "mserv"); err != nil {
		t.Errorf("an ordinary job: %v", err)
	}
}

// The class of a half is the task's own capped at merge (labels.QualifyClass); a dev half is accepted too, for a coordinator from before
// the class followed the task.
func TestAQualifyHalfTakesTheTasksClassCappedAtMerge(t *testing.T) {
	for _, c := range []struct {
		task, class wire.JobClass
		ok          bool
	}{
		{wire.ClassMerge, wire.ClassMerge, true},
		{wire.ClassRelease, wire.ClassMerge, true},
		{wire.ClassMerge, wire.ClassDev, true},
		{wire.ClassRelease, wire.ClassDev, true},
		{wire.ClassDev, wire.ClassDev, true},
		{wire.ClassRelease, wire.ClassRelease, false}, // under the task's own cap, so only the half rule refuses it
		{wire.ClassMerge, wire.ClassRelease, false},
		{wire.ClassDev, wire.ClassMerge, false},
		{wire.ClassDev, wire.ClassRelease, false},
	} {
		root := snapshot(t, qualifyTasks+"class = \""+string(c.task)+"\"\n")
		_, err := PrepareAt(half(labels.RoleReference, "air26", func(j *wire.Job) { j.Class = c.class }), root, t.TempDir(), "air26", "mserv")
		if c.ok && err != nil {
			t.Errorf("task %s, half %s: %v", c.task, c.class, err)
		}
		if !c.ok && err == nil {
			t.Errorf("task %s, half %s: accepted", c.task, c.class)
		}
		if !c.ok && c.task == wire.ClassRelease && code(t, err) != CodeMetaMismatch {
			t.Errorf("task %s, half %s: %v", c.task, c.class, err)
		}
	}
}
