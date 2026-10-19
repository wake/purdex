package queue

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The cost of recording an outcome inside the write transaction of Result (spec §7), measured, not asserted: it runs only with
// WWD_BENCH=1 and prints the p50 and p99 of Result with and without the record, over a task_outcomes table of the sizes a week of
// retention gives. "spread" is rows over many works (the usual shape); "hot" is all rows of the one work the job is of, over four hosts (the
// worst case for the comparison). Run: WWD_BENCH=1 go test ./internal/wwd/queue/ -run TestResultCostOfRecordingOutcome -v
func TestResultCostOfRecordingOutcome(t *testing.T) {
	if os.Getenv("WWD_BENCH") == "" {
		t.Skip("set WWD_BENCH=1 to measure")
	}
	for _, n := range []int{0, 5000, 20000, 100000} {
		for _, shape := range []string{"spread", "hot"} {
			recorded := resultTimes(t, n, shape, true)
			plain := resultTimes(t, n, shape, false)
			t.Logf("rows=%-6d %-6s without record p50=%v p99=%v | with record p50=%v p99=%v", n, shape, pct(plain, 50), pct(plain, 99), pct(recorded, 50), pct(recorded, 99))
		}
	}
}

// resultTimes ends 200 jobs of the fixture's task, timing only the Result call, on a table that already holds n outcomes.
func resultTimes(t *testing.T, n int, shape string, record bool) []time.Duration {
	t.Helper()
	f := newFixture(t)
	f.reference("air26")
	// one real job, to learn the work key the following ones have
	f.endAs(t, "air26", []string{"os:linux"}, 0)
	var key string
	require.NoError(t, f.db.QueryRow(`SELECT work_key FROM task_outcomes LIMIT 1`).Scan(&key))
	tx, err := f.db.Begin()
	require.NoError(t, err)
	for i := 0; i < n; i++ {
		k, host := fmt.Sprintf("spread-%d", i/4), fmt.Sprintf("h%d", i%4)
		if shape == "hot" {
			k = key
		}
		_, err := tx.Exec(`INSERT INTO task_outcomes (job_id, work_key, host, label, tree, task, ok, standing, at) VALUES (?,?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("pre-%d", i), k, host, specLabel, "tree", "spa-related", 1, StandingEarned, f.clk.Now().UnixMilli())
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	labels := []string{"os:linux"}
	if record {
		labels = append(labels, specLabel) // earned: recorded and compared
	}
	var times []time.Duration
	for i := 0; i < 200; i++ {
		id := f.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1"; s.SupersedeKey = "" })
		c := f.claimAs(t, id, "mserv", labels, fpA)
		exit := i % 2 // alternate, so the comparison finds disagreements to write
		state := wire.StateSucceeded
		if exit != 0 {
			state = wire.StateFailed
		}
		start := time.Now()
		_, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: state, Exit: &exit})
		times = append(times, time.Since(start))
		require.NoError(t, err)
	}
	return times
}

func pct(d []time.Duration, p int) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[(len(s)-1)*p/100]
}
