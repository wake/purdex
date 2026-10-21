package qualify

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The view (WWD-7b2-0a, plan "#### WWD-7b2-1"): one named test per rule of the contract, on BuildView.

var t0 = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

const tscLabel = "task:purdex/tsc"

func viewInput() Input {
	return Input{
		Now: t0,
		Host: Host{
			Alias: "mserv", WorkerOn: true,
			Labels:      []string{"host:mserv", "repo:purdex", "tool:node24", "os:linux"},
			Fingerprint: map[string]string{"node": "24.1"},
		},
		Tasks: []Task{{Repo: "purdex", Name: "tsc", Reference: "air26", DefHash: "h1", Needs: []string{"os:linux", "tool:node24"}}},
	}
}

func onlyTask(t *testing.T, v wire.HostQualification) wire.QualificationTask {
	t.Helper()
	if len(v.Tasks) != 1 {
		t.Fatalf("tasks: %+v", v.Tasks)
	}
	return v.Tasks[0]
}

// validCombination is the truth table of the contract: anything else is a bug.
func validCombination(tk wire.QualificationTask) error {
	switch tk.Hold {
	case wire.QualHoldEarned, wire.QualHoldReference:
		if tk.Progress != wire.QualProgressIdle {
			return fmt.Errorf("%s must be idle, is %s", tk.Hold, tk.Progress)
		}
	case wire.QualHoldQuarantined:
		switch tk.Progress {
		case wire.QualProgressPairing, wire.QualProgressWaiting, wire.QualProgressReady:
		case wire.QualProgressBlocked:
			if c := tk.Blocked.Code; c != wire.QualBlockedNoDefinition && c != wire.QualBlockedWorkerOff && c != wire.QualBlockedMissing {
				return fmt.Errorf("quarantined blocked by %s", c)
			}
		case wire.QualProgressIdle:
			if !tk.Gone {
				return errors.New("quarantined idle without gone")
			}
		default:
			return fmt.Errorf("quarantined %s", tk.Progress)
		}
	case wire.QualHoldNone:
		if tk.Progress == wire.QualProgressIdle || tk.Gone {
			return fmt.Errorf("none with %s gone=%v", tk.Progress, tk.Gone)
		}
	default:
		return fmt.Errorf("hold %q", tk.Hold)
	}
	return nil
}

func build(t *testing.T, in Input, rows []wire.EarnedLabel, pairs []Pair, history []Record) wire.HostQualification {
	t.Helper()
	v := BuildView(in, rows, pairs, history, nil)
	for _, tk := range v.Tasks {
		if err := validCombination(tk); err != nil {
			t.Fatalf("%s: %v (%+v)", tk.Label, err, tk)
		}
	}
	return v
}

func earnedRow(state string) wire.EarnedLabel {
	return wire.EarnedLabel{Label: tscLabel, State: state, Fingerprint: map[string]string{"node": "24.1"}, Snapshot: "tree1", GrantedAt: t0.Add(-48 * time.Hour)}
}

func TestView_ReferenceHostHoldsAndIsIdle(t *testing.T) {
	in := viewInput()
	in.Host.Alias = "air26"
	in.Host.Labels = append(in.Host.Labels, tscLabel)
	tk := onlyTask(t, build(t, in, nil, nil, nil))
	if tk.Hold != wire.QualHoldReference || tk.Progress != wire.QualProgressIdle || tk.Reference != "air26" {
		t.Fatalf("%+v", tk)
	}
}

func TestView_ReferenceMissingATool(t *testing.T) {
	in := viewInput()
	in.Host.Alias = "air26"
	in.Host.Labels = []string{"host:air26", "repo:purdex", "os:linux"} // no tool:node24, so the label is not advertised
	tk := onlyTask(t, build(t, in, nil, nil, nil))
	if tk.Hold != wire.QualHoldNone || tk.Progress != wire.QualProgressBlocked || tk.Blocked == nil ||
		tk.Blocked.Code != wire.QualBlockedReferenceMissing || len(tk.Blocked.Labels) != 1 || tk.Blocked.Labels[0] != "tool:node24" {
		t.Fatalf("%+v", tk)
	}
}

func TestView_WorkerOffIsBlocked(t *testing.T) {
	in := viewInput()
	in.Host.WorkerOn = false
	tk := onlyTask(t, build(t, in, nil, nil, nil))
	if tk.Hold != wire.QualHoldNone || tk.Progress != wire.QualProgressBlocked || tk.Blocked.Code != wire.QualBlockedWorkerOff || len(tk.Blocked.Labels) != 0 {
		t.Fatalf("%+v", tk)
	}
}

func TestView_NoDefinitionIsBlocked(t *testing.T) {
	in := viewInput()
	in.Tasks[0].DefHash = ""
	tk := onlyTask(t, build(t, in, nil, nil, nil))
	if tk.Progress != wire.QualProgressBlocked || tk.Blocked.Code != wire.QualBlockedNoDefinition {
		t.Fatalf("%+v", tk)
	}
}

func TestView_MissingRepoAndToolNamesBoth(t *testing.T) {
	in := viewInput()
	in.Host.Labels = []string{"host:mserv", "os:linux"}
	tk := onlyTask(t, build(t, in, nil, nil, nil))
	if tk.Progress != wire.QualProgressBlocked || tk.Blocked.Code != wire.QualBlockedMissing ||
		len(tk.Blocked.Labels) != 2 || tk.Blocked.Labels[0] != "repo:purdex" || tk.Blocked.Labels[1] != "tool:node24" {
		t.Fatalf("%+v", tk)
	}
}

func TestView_PairInFlightIsPairingWithItsIds(t *testing.T) {
	in := viewInput()
	pairs := []Pair{{Label: tscLabel, Candidate: "j-c", Reference: "j-r", Since: t0.Add(-time.Minute)}}
	for _, workerOn := range []bool{true, false} { // a worker turned off meanwhile still has the pair in flight
		in.Host.WorkerOn = workerOn
		tk := onlyTask(t, build(t, in, nil, pairs, nil))
		if tk.Hold != wire.QualHoldNone || tk.Progress != wire.QualProgressPairing || tk.Pair == nil ||
			tk.Pair.Candidate != "j-c" || tk.Pair.Reference != "j-r" || !tk.Pair.Since.Equal(t0.Add(-time.Minute)) {
			t.Fatalf("worker %v: %+v", workerOn, tk)
		}
	}
}

func TestView_EarnedIsIdleEvenWithWorkerOff(t *testing.T) {
	in := viewInput()
	for _, workerOn := range []bool{true, false} {
		in.Host.WorkerOn = workerOn
		tk := onlyTask(t, build(t, in, []wire.EarnedLabel{earnedRow(wire.EarnedLabelEarned)}, nil, nil))
		if tk.Hold != wire.QualHoldEarned || tk.Progress != wire.QualProgressIdle || tk.Earned == nil ||
			tk.Earned.Snapshot != "tree1" || tk.Earned.Fingerprint["node"] != "24.1" || !tk.Earned.GrantedAt.Equal(t0.Add(-48*time.Hour)) {
			t.Fatalf("worker %v: %+v", workerOn, tk)
		}
	}
}

func TestView_QuarantinedNothingInTheWayIsReady(t *testing.T) {
	tk := onlyTask(t, build(t, viewInput(), []wire.EarnedLabel{earnedRow(wire.EarnedLabelQuarantined)}, nil, nil))
	if tk.Hold != wire.QualHoldQuarantined || tk.Progress != wire.QualProgressReady || tk.Earned == nil {
		t.Fatalf("%+v", tk)
	}
}

func TestView_QuarantinedWithAPairIsPairing(t *testing.T) {
	pairs := []Pair{{Label: tscLabel, Candidate: "j-c", Reference: "j-r", Since: t0}}
	tk := onlyTask(t, build(t, viewInput(), []wire.EarnedLabel{earnedRow(wire.EarnedLabelQuarantined)}, pairs, nil))
	if tk.Hold != wire.QualHoldQuarantined || tk.Progress != wire.QualProgressPairing || tk.Pair == nil {
		t.Fatalf("%+v", tk)
	}
}

func lastAttempt(in *Input, outcome Outcome, ago time.Duration, fp map[string]string) {
	in.Last = map[string]Attempt{tscLabel: {At: in.Now.Add(-ago), Outcome: outcome, Fingerprint: fp}}
}

func TestView_WaitBoundaryIs24Hours(t *testing.T) {
	fp := map[string]string{"node": "24.1"}
	in := viewInput()
	lastAttempt(&in, OutcomeMismatch, 24*time.Hour-time.Second, fp)
	if tk := onlyTask(t, build(t, in, nil, nil, nil)); tk.Progress != wire.QualProgressWaiting {
		t.Fatalf("23:59:59: %+v", tk)
	}
	lastAttempt(&in, OutcomeMismatch, 24*time.Hour, fp)
	if tk := onlyTask(t, build(t, in, nil, nil, nil)); tk.Progress != wire.QualProgressReady || tk.Last != nil || tk.RetryAt != nil {
		t.Fatalf("24:00:00: %+v", tk)
	}
}

func TestView_ChangedFingerprintEndsTheWait(t *testing.T) {
	in := viewInput()
	lastAttempt(&in, OutcomeMismatch, time.Hour, map[string]string{"node": "23.9"})
	if tk := onlyTask(t, build(t, in, nil, nil, nil)); tk.Progress != wire.QualProgressReady {
		t.Fatalf("%+v", tk)
	}
}

func TestView_RetryAtIsTheAttemptPlus24Hours(t *testing.T) {
	in := viewInput()
	lastAttempt(&in, OutcomeVoid, 2*time.Hour, map[string]string{"node": "24.1"})
	tk := onlyTask(t, build(t, in, nil, nil, nil))
	if tk.RetryAt == nil || !tk.RetryAt.Equal(t0.Add(-2*time.Hour+24*time.Hour)) || tk.Last == nil || tk.Last.Outcome != "void" {
		t.Fatalf("%+v", tk)
	}
}

func TestView_WaitingHasTheWhyOfTheLatestSameOutcomeRecord(t *testing.T) {
	in := viewInput()
	lastAttempt(&in, OutcomeMismatch, time.Hour, map[string]string{"node": "24.1"})
	history := []Record{
		{Label: tscLabel, Outcome: OutcomeMismatch, Why: "old", At: t0.Add(-30 * time.Hour)},
		{Label: tscLabel, Outcome: OutcomeMismatch, Why: "passed differ", At: t0.Add(-time.Hour)},
		{Label: "task:purdex/other", Outcome: OutcomeMismatch, Why: "not mine", At: t0},
	}
	tk := onlyTask(t, build(t, in, nil, nil, history))
	if tk.Last == nil || tk.Last.Why != "passed differ" {
		t.Fatalf("%+v", tk.Last)
	}
}

func TestView_AfterARestartWaitingHasNoWhy(t *testing.T) {
	in := viewInput()
	lastAttempt(&in, OutcomeMismatch, time.Hour, map[string]string{"node": "24.1"})
	tk := onlyTask(t, build(t, in, nil, nil, nil))
	if tk.Progress != wire.QualProgressWaiting || tk.Last == nil || tk.Last.Why != "" {
		t.Fatalf("%+v", tk)
	}
}

func TestView_GoneRowsAreIdle(t *testing.T) {
	in := viewInput()
	in.Tasks = nil
	for _, state := range []string{wire.EarnedLabelEarned, wire.EarnedLabelQuarantined} {
		tk := onlyTask(t, build(t, in, []wire.EarnedLabel{earnedRow(state)}, nil, nil))
		if !tk.Gone || tk.Progress != wire.QualProgressIdle || tk.Hold != state || tk.Reference != "" || tk.Label != tscLabel {
			t.Fatalf("%s: %+v", state, tk)
		}
	}
}

func TestView_AnUnreadableManifestHidesTasksWithoutMakingThemGone(t *testing.T) {
	in := viewInput()
	in.Tasks = nil
	rows := []wire.EarnedLabel{earnedRow(wire.EarnedLabelEarned), {Label: "task:other/x", State: wire.EarnedLabelEarned, GrantedAt: t0}}
	v := BuildView(in, rows, nil, nil, []string{"purdex"})
	if len(v.Unreadable) != 1 || v.Unreadable[0] != "purdex" {
		t.Fatalf("unreadable: %+v", v.Unreadable)
	}
	if len(v.Tasks) != 1 || v.Tasks[0].Label != "task:other/x" || !v.Tasks[0].Gone {
		t.Fatalf("a row of the unreadable repo must be hidden, the other one gone: %+v", v.Tasks)
	}
}

func TestView_RecentIsNewestFirst(t *testing.T) {
	history := []Record{
		{Label: tscLabel, Outcome: OutcomeVoid, Why: "a", At: t0.Add(-2 * time.Hour), Candidate: "c1", Reference: "r1"},
		{Label: tscLabel, Outcome: OutcomeMatch, Why: "", At: t0.Add(-time.Hour)},
	}
	v := build(t, viewInput(), nil, nil, history)
	if len(v.Recent) != 2 || v.Recent[0].Outcome != "match" || v.Recent[1].Candidate != "c1" {
		t.Fatalf("%+v", v.Recent)
	}
}

func TestView_AReferenceHoldWinsOverARowAndAPair(t *testing.T) {
	in := viewInput()
	in.Host.Alias = "air26"
	in.Host.Labels = append(in.Host.Labels, tscLabel)
	pairs := []Pair{{Label: tscLabel, Candidate: "c", Reference: "r", Since: t0}}
	tk := onlyTask(t, build(t, in, []wire.EarnedLabel{earnedRow(wire.EarnedLabelQuarantined)}, pairs, nil))
	if tk.Hold != wire.QualHoldReference || tk.Progress != wire.QualProgressIdle {
		t.Fatalf("%+v", tk)
	}
}

func TestView_EmptyListsAreNotNull(t *testing.T) {
	in := viewInput()
	in.Tasks = nil
	v := BuildView(in, nil, nil, nil, nil)
	if v.Recent == nil || v.Tasks == nil {
		t.Fatalf("%+v", v)
	}
}

func TestUnreadableRepos(t *testing.T) {
	joined := errors.Join(&ManifestError{Repo: "b", Err: errors.New("x")}, errors.New("plain"), &ManifestError{Repo: "a", Err: errors.New("y")}, &ManifestError{Repo: "b", Err: errors.New("z")})
	got := UnreadableRepos(fmt.Errorf("wrapped: %w", joined))
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("%v", got)
	}
	if UnreadableRepos(nil) != nil {
		t.Fatal("nil")
	}
	e := &ManifestError{Repo: "r", Err: errors.New("boom")}
	if e.Error() != "r: boom" || !errors.Is(e, e.Err) {
		t.Fatalf("%q", e.Error())
	}
}

// A quarantined row of a task whose reference became this host (and which lacks what the reference needs) is no quarantined hold:
// quarantined + reference_missing is outside the truth table.
func TestView_AQuarantinedRowOfATaskThatBecameThisHostsReferenceIsNone(t *testing.T) {
	in := viewInput()
	in.Tasks[0].Reference = "mserv"
	in.Host.Labels = []string{"host:mserv", "os:linux"} // no repo:, no tool:node24: the label is not advertised
	tk := onlyTask(t, build(t, in, []wire.EarnedLabel{earnedRow(wire.EarnedLabelQuarantined)}, nil, nil))
	if tk.Hold != wire.QualHoldNone || tk.Earned != nil || tk.Progress != wire.QualProgressBlocked || tk.Blocked.Code != wire.QualBlockedReferenceMissing {
		t.Fatalf("%+v", tk)
	}
}
