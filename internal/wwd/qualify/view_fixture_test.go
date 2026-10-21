package qualify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The two wire fixtures of the qualification read are the answer of BuildView for a table of cases, compared byte for byte: the App copies the
// files, so what they hold is the contract. To rewrite them after a change of the table: WWD_UPDATE_FIXTURES=1 go test ./internal/wwd/qualify -run Fixture.
const fixtureDir = "../wire/testdata"

func checkFixture(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join(fixtureDir, name)
	if os.Getenv("WWD_UPDATE_FIXTURES") != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Fatalf("%s differs from the view (WWD_UPDATE_FIXTURES=1 rewrites it)\nfile: %s\nview: %s", name, want, got)
	}
}

func task(name, ref, defHash string, needs ...string) Task {
	return Task{Repo: "purdex", Name: name, Reference: ref, DefHash: defHash, Needs: needs}
}

func row(name, state string, ageH int) wire.EarnedLabel {
	return wire.EarnedLabel{
		Label: "task:purdex/" + name, State: state, Fingerprint: map[string]string{"node": "24.1", "pnpm": "10.2"},
		Snapshot: "tree-" + name, GrantedAt: t0.Add(-time.Duration(ageH) * time.Hour),
	}
}

// The worker switch is the host's, so the cases that need it on (waiting, ready) and off (worker_off) are two answers of the same host
// put in one list.
func TestFixtureHostQualification(t *testing.T) {
	fp := map[string]string{"node": "24.1", "pnpm": "10.2"}
	on := Input{
		Now: t0,
		Host: Host{Alias: "mserv", WorkerOn: true, Fingerprint: fp,
			Labels: []string{"host:mserv", "repo:purdex", "tool:node24", "tool:pnpm10", "os:linux", "task:purdex/ref-ok"}},
		Tasks: []Task{
			task("ref-ok", "mserv", "h", "tool:node24"),
			task("earned-ok", "air26", "h", "tool:node24"),
			task("q-pairing", "air26", "h"),
			task("q-waiting", "air26", "h"),
			task("q-ready", "air26", "h"),
			task("q-missing", "air26", "h", "tool:zig"),
			task("n-pairing", "air26", "h"),
			task("n-waiting", "air26", "h"),
			task("n-ready", "air26", "h", "tool:node24"),
			task("n-no-def", "air26", ""),
			task("n-missing", "air26", "h", "tool:zig", "tool:node24"),
			task("n-ref-missing", "mserv", "h", "tool:zig"),
		},
		Last: map[string]Attempt{
			"task:purdex/q-waiting": {At: t0.Add(-5 * time.Hour), Outcome: OutcomeVoid, Fingerprint: fp},     // no why: a restart forgot it
			"task:purdex/n-waiting": {At: t0.Add(-2 * time.Hour), Outcome: OutcomeMismatch, Fingerprint: fp}, // with the why below
		},
	}
	rows := []wire.EarnedLabel{
		row("earned-ok", wire.EarnedLabelEarned, 72),
		row("q-pairing", wire.EarnedLabelQuarantined, 30), row("q-waiting", wire.EarnedLabelQuarantined, 30),
		row("q-ready", wire.EarnedLabelQuarantined, 30), row("q-missing", wire.EarnedLabelQuarantined, 30),
		row("old-earned", wire.EarnedLabelEarned, 400), row("old-quarantined", wire.EarnedLabelQuarantined, 400),
		{Label: "task:broken/lost", State: wire.EarnedLabelEarned, Fingerprint: fp, Snapshot: "tree-lost", GrantedAt: t0}, // of the repo whose manifest cannot be read: hidden, not gone
	}
	pairs := []Pair{
		{Label: "task:purdex/q-pairing", Candidate: "j_01CAND0000000000000000001", Reference: "j_01REF00000000000000000001", Since: t0.Add(-3 * time.Minute)},
		{Label: "task:purdex/n-pairing", Candidate: "j_01CAND0000000000000000002", Reference: "j_01REF00000000000000000002", Since: t0.Add(-9 * time.Minute)},
	}
	history := []Record{
		{Label: "task:purdex/n-waiting", Outcome: OutcomeMismatch, Why: "the passed counts differ: 41 on the candidate, 40 on the reference", At: t0.Add(-2 * time.Hour), Candidate: "j_01CAND0000000000000000003", Reference: "j_01REF00000000000000000003"},
		{Label: "task:purdex/earned-ok", Outcome: OutcomeMatch, At: t0.Add(-72 * time.Hour), Candidate: "j_01CAND0000000000000000004", Reference: "j_01REF00000000000000000004"},
	}
	// q-waiting has an attempt (the store's) but no record: a restart forgot its why
	view := BuildView(on, rows, pairs, history, []string{"broken"})

	off := on
	off.Host.WorkerOn = false
	off.Tasks = []Task{task("off-none", "air26", "h"), task("off-quarantined", "air26", "h")}
	off.Last = nil
	offView := BuildView(off, []wire.EarnedLabel{row("off-quarantined", wire.EarnedLabelQuarantined, 30)}, nil, nil, nil)

	view.Tasks = append(view.Tasks, offView.Tasks...)
	slices.SortFunc(view.Tasks, func(a, b wire.QualificationTask) int { return strings.Compare(a.Label, b.Label) })

	seen := map[string]bool{}
	for _, tk := range view.Tasks {
		if err := validCombination(tk); err != nil {
			t.Fatalf("%s: %v", tk.Label, err)
		}
		seen[tk.Hold+"/"+tk.Progress] = true
		if tk.Blocked != nil {
			seen[tk.Hold+"/blocked:"+tk.Blocked.Code] = true
		}
		if tk.Last != nil {
			seen[tk.Hold+"/waiting:why="+boolWord(tk.Last.Why != "")] = true
		}
		if tk.Gone {
			seen[tk.Hold+"/gone"] = true
		}
	}
	for _, want := range []string{
		"reference/idle", "earned/idle", "earned/gone", "quarantined/gone",
		"quarantined/pairing", "quarantined/waiting", "quarantined/ready",
		"quarantined/blocked:missing", "quarantined/blocked:worker_off",
		"none/pairing", "none/waiting", "none/ready",
		"none/blocked:no_definition", "none/blocked:reference_missing", "none/blocked:worker_off", "none/blocked:missing",
		"none/waiting:why=true", "quarantined/waiting:why=false",
	} {
		if !seen[want] {
			t.Errorf("the fixture lacks %s", want)
		}
	}
	if len(view.Unreadable) != 1 || len(view.Recent) != 2 {
		t.Fatalf("unreadable/recent: %+v", view)
	}

	checkFixture(t, "pool_host_qualification.json", wire.PoolHostQualification{
		Host: view.Host, Known: true, Tasks: view.Tasks, Unreadable: view.Unreadable, Recent: view.Recent,
	})
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// A host whose wwd predates the route: nothing is claimed.
func TestFixtureHostQualificationUnknown(t *testing.T) {
	checkFixture(t, "pool_host_qualification_unknown.json", wire.PoolHostQualification{
		Host: "mserv", Known: false, Tasks: []wire.QualificationTask{}, Recent: []wire.QualifyRecord{},
	})
}
