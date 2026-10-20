package queue

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

const lbl = "task:purdex/go-changed"

func outcome(job, host, standing string, ok bool, edit ...func(*Outcome)) Outcome {
	o := Outcome{JobID: job, Host: host, Label: lbl, Tree: "7ee", Task: "go-changed", Args: []string{"./a"}, DefHash: "def1", OK: ok,
		Standing: standing, At: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	for _, e := range edit {
		e(&o)
	}
	return o
}

func (f *fixture) record(t *testing.T, o Outcome) []wire.Disagreement {
	t.Helper()
	got, err := f.q.RecordOutcome(bg, o)
	require.NoError(t, err)
	return got
}

func hostsOf(ds []wire.Disagreement) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, d.Host)
	}
	return out
}

// Of two hosts that came out differently on the same work, the non-reference one is the one whose label is quarantined: whichever
// outcome was recorded first.
func TestTheNonReferenceHostIsTheOneQuarantined(t *testing.T) {
	for name, order := range map[string][2]Outcome{
		"reference first": {outcome("j_1", "air26", StandingReference, true), outcome("j_2", "mserv", StandingEarned, false)},
		"earned first":    {outcome("j_1", "mserv", StandingEarned, false), outcome("j_2", "air26", StandingReference, true)},
	} {
		f := newFixture(t)
		assert.Empty(t, f.record(t, order[0]), name)
		got := f.record(t, order[1])
		require.Len(t, got, 1, name)
		assert.Equal(t, "mserv", got[0].Host, name)
		assert.Equal(t, "air26", got[0].OtherHost, name)
		assert.Equal(t, [2]string{"j_1", "j_2"}, got[0].Jobs, name)
		assert.Equal(t, lbl, got[0].Label, name)
	}
}

// With no reference among the two (both earned the label), nobody can tell which is wrong: both labels are quarantined.
func TestWithNoReferenceBothLabelsAreQuarantined(t *testing.T) {
	f := newFixture(t)
	f.record(t, outcome("j_1", "mserv", StandingEarned, true))
	got := f.record(t, outcome("j_2", "mini", StandingEarned, false))
	assert.ElementsMatch(t, []string{"mserv", "mini"}, hostsOf(got))
	for _, d := range got {
		assert.NotEqual(t, d.Host, d.OtherHost)
	}
}

// Nothing is a disagreement that is not two different outcomes of one piece of work on two hosts.
func TestWhatIsNotADisagreement(t *testing.T) {
	for name, o := range map[string]Outcome{
		"the same outcome":               outcome("j_2", "mserv", StandingEarned, true),
		"the same host, another outcome": outcome("j_2", "air26", StandingReference, false),
		"another snapshot":               outcome("j_2", "mserv", StandingEarned, false, func(o *Outcome) { o.Tree = "other" }),
		"other arguments":                outcome("j_2", "mserv", StandingEarned, false, func(o *Outcome) { o.Args = []string{"./b"} }),
		"another definition":             outcome("j_2", "mserv", StandingEarned, false, func(o *Outcome) { o.DefHash = "def2" }),
		"the same arguments, reordered":  outcome("j_2", "mserv", StandingEarned, false, func(o *Outcome) { o.Args = []string{"./a", "./b"} }),
		// a task has one reference host, so two do not arise; if they did, neither is quarantined
		"two references": outcome("j_2", "other-ref", StandingReference, false),
	} {
		f := newFixture(t) // each case against the same first outcome, and nothing else
		f.record(t, outcome("j_1", "air26", StandingReference, true, func(o *Outcome) { o.Args = []string{"./a"} }))
		assert.Empty(t, f.record(t, o), name)
	}
}

// The same job recorded again is the same outcome: nothing is written twice.
func TestAnOutcomeRecordedTwiceIsOne(t *testing.T) {
	f := newFixture(t)
	f.record(t, outcome("j_1", "air26", StandingReference, true))
	assert.Len(t, f.record(t, outcome("j_2", "mserv", StandingEarned, false)), 1)
	assert.Empty(t, f.record(t, outcome("j_2", "mserv", StandingEarned, false)), "the same job again writes no second disagreement")
	var n int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM task_outcomes`).Scan(&n))
	assert.Equal(t, 2, n)
}

// A run of outcomes does not become a run of disagreements: a new outcome is paired with the most recent one of each other host only.
// An outcome an older wwd stored under a host name the alias rule has since closed ("." and "..", #3615) is not paired with a new one: the
// disagreement it would make could not be read back, and the reader of the origin would stop on it for good.
func TestAnOutcomeOfAHostNameTheRuleNoLongerAllowsMakesNoDisagreement(t *testing.T) {
	f := newFixture(t)
	old := outcome("j_old", "air26", StandingReference, true)
	_, err := f.db.Exec(`INSERT INTO task_outcomes (job_id, work_key, host, label, tree, task, ok, standing, at) VALUES (?,?,?,?,?,?,?,?,?)`,
		old.JobID, old.workKey(), ".", old.Label, old.Tree, old.Task, 1, old.Standing, old.At.UnixMilli())
	require.NoError(t, err)
	assert.Empty(t, f.record(t, outcome("j_new", "mserv", StandingEarned, false)))
	// a host the rule allows is still paired
	f.record(t, outcome("j_ref", "air26", StandingReference, true))
	assert.Len(t, f.record(t, outcome("j_bad", "air19", StandingEarned, false)), 1)
}

func TestANewOutcomeIsPairedWithTheLatestOfEachOtherHostOnly(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 5; i++ {
		f.record(t, outcome(fmt.Sprintf("j_ref%d", i), "air26", StandingReference, true))
	}
	got := f.record(t, outcome("j_bad", "mserv", StandingEarned, false))
	require.Len(t, got, 1)
	assert.Equal(t, "j_ref4", got[0].Jobs[0], "the latest of the reference's")
}

func TestAnOutcomeThatIsNotValidIsRefused(t *testing.T) {
	f := newFixture(t)
	for name, o := range map[string]Outcome{
		"bad job id":       outcome("x", "mserv", StandingEarned, true),
		"bad host":         outcome("j_1", "", StandingEarned, true),
		"bad label":        outcome("j_1", "mserv", StandingEarned, true, func(o *Outcome) { o.Label = "os:linux" }),
		"no definition":    outcome("j_1", "mserv", StandingEarned, true, func(o *Outcome) { o.DefHash = "" }),
		"unknown standing": outcome("j_1", "mserv", "trusted", true),
		"no time":          outcome("j_1", "mserv", StandingEarned, true, func(o *Outcome) { o.At = time.Time{} }),
	} {
		_, err := f.q.RecordOutcome(bg, o)
		assert.Error(t, err, name)
	}
}

// What the owner of a label reads: the disagreements that concern it, after the cursor it gave, oldest first, in pieces.
func TestDisagreementsAreReadByHostAndCursor(t *testing.T) {
	f := newFixture(t)
	f.record(t, outcome("j_r", "air26", StandingReference, true))
	for i := 0; i < 5; i++ {
		f.record(t, outcome(fmt.Sprintf("j_m%d", i), "mserv", StandingEarned, false, func(o *Outcome) { o.Tree = fmt.Sprintf("t%d", i) }))
		f.record(t, outcome(fmt.Sprintf("j_r%d", i), "air26", StandingReference, true, func(o *Outcome) { o.Tree = fmt.Sprintf("t%d", i) }))
	}
	// j_m0..j_m4 differ from the reference's outcome of their tree; the reference's j_r only has the first tree's pair with j_m0
	all, more, err := f.q.Disagreements(bg, "mserv", 0, 100)
	require.NoError(t, err)
	assert.False(t, more)
	require.NotEmpty(t, all)
	for i, d := range all {
		assert.Equal(t, "mserv", d.Host)
		assert.NoError(t, d.Validate())
		if i > 0 {
			assert.Greater(t, d.ID, all[i-1].ID, "oldest first")
		}
	}
	none, more, err := f.q.Disagreements(bg, "air26", 0, 100)
	require.NoError(t, err)
	assert.Empty(t, none, "the reference host is never the one quarantined")
	assert.False(t, more)

	first, more, err := f.q.Disagreements(bg, "mserv", 0, 2)
	require.NoError(t, err)
	require.Len(t, first, 2)
	assert.True(t, more, "more were waiting than the limit let through")
	rest, more, err := f.q.Disagreements(bg, "mserv", first[1].ID, 100)
	require.NoError(t, err)
	assert.False(t, more)
	assert.Len(t, rest, len(all)-2)
	assert.Equal(t, all[2].ID, rest[0].ID)
}

// The outcomes and disagreements go with the jobs they describe, after the same retention.
func TestOutcomesAreKeptAsLongAsTheJobs(t *testing.T) {
	f := newFixture(t)
	old := f.clk.Now().Add(-retention - time.Hour)
	f.record(t, outcome("j_r", "air26", StandingReference, true, func(o *Outcome) { o.At = old }))
	f.record(t, outcome("j_m", "mserv", StandingEarned, false, func(o *Outcome) { o.At = old }))
	fresh := f.clk.Now()
	f.record(t, outcome("j_r2", "air26", StandingReference, true, func(o *Outcome) { o.At = fresh; o.Tree = "t2" }))
	f.record(t, outcome("j_m2", "mserv", StandingEarned, false, func(o *Outcome) { o.At = fresh; o.Tree = "t2" }))

	_, err := f.q.Sweep(bg)
	require.NoError(t, err)

	var outcomes, disagreements int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM task_outcomes`).Scan(&outcomes))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM outcome_disagreements`).Scan(&disagreements))
	assert.Equal(t, 2, outcomes, "only the recent pair of outcomes is left")
	assert.Equal(t, 1, disagreements, "and its disagreement")
}

// Version 7 → 8: the two tables appear, the version moves, and the step can run again.
func TestAVersionSevenDatabaseGainsTheOutcomeTables(t *testing.T) {
	f := newFixture(t)
	for _, tbl := range []string{"task_outcomes", "outcome_disagreements"} {
		_, err := f.db.Exec(`DROP TABLE ` + tbl)
		require.NoError(t, err)
	}
	_, err := f.db.Exec(`UPDATE queue_schema SET version = 7`)
	require.NoError(t, err)

	require.NoError(t, Migrate(bg, f.db))
	require.NoError(t, Migrate(bg, f.db))
	var v int
	require.NoError(t, f.db.QueryRow(`SELECT version FROM queue_schema`).Scan(&v))
	assert.Equal(t, schemaVersion, v)
	f.record(t, outcome("j_1", "mserv", StandingEarned, true))
}
