package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- pdx gate queue: list and move (#3034, spec §5.2) ----

type queueWaiter struct {
	Class      string `json:"class"`
	Ticket     string `json:"ticket"`
	Place      int    `json:"place"`
	PID        int    `json:"pid"`
	Rank       string `json:"rank"`
	RankBy     string `json:"rank_by"`
	RankReason string `json:"rank_reason"`
}

// queueGate is the config of the tests that queue commands behind a holder. Admission is off: with it on, the gate holds even the
// first heavy command back while the host is under memory pressure or paging, so on a busy host (air26 runs other sessions' tests
// and builds) the holder is never admitted and the helper's 5 s wait for "重 1/1 使用中" ends the test (#3241). These tests are of the
// queue's order and ranks, not of admission, which the gate package tests with a pressure reader of its own.
const queueGate = "[gate.slots]\nheavy = 1\n" + noAdmission

// waitingGates holds the heavy slot with one command and queues n more behind it; release lets them all go. The commands are
// joined when the test ends (after the sentinel is written, before its directory is removed), so none outlives the test.
func waitingGates(t *testing.T, cfg string, n int) (release func()) {
	t.Helper()
	gofile := filepath.Join(t.TempDir(), "go")
	release = func() { _ = os.WriteFile(gofile, nil, 0o644) }
	var running []chan struct{}
	spawn := func(argv ...string) {
		done := make(chan struct{})
		running = append(running, done)
		go func() {
			defer close(done)
			var o, e bytes.Buffer
			slotGateMain(append([]string{"--config", cfg, "--class", "heavy", "--fg", "--"}, argv...), &o, &e)
		}()
	}
	t.Cleanup(func() {
		release()
		for _, done := range running {
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Errorf("a queued gate command did not end after the release")
				return
			}
		}
	})
	spawn("sh", "-c", "while [ ! -e "+gofile+" ]; do sleep 0.02; done")
	eventuallyOutput(t, func() bool {
		var o, e bytes.Buffer
		gateMain([]string{"status", "--config", cfg}, &o, &e)
		return strings.Contains(o.String(), "重 1/1 使用中")
	})
	for i := 0; i < n; i++ {
		spawn("sh", "-c", "exit 0")
		want := i + 1
		eventuallyOutput(t, func() bool { return len(listQueue(t, cfg)["heavy"]) == want })
	}
	return release
}

func listQueue(t *testing.T, cfg string) map[string][]queueWaiter {
	t.Helper()
	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"queue", "--json", "--config", cfg}, &out, &errb), errb.String())
	var q map[string][]queueWaiter
	require.NoError(t, json.Unmarshal(out.Bytes(), &q))
	return q
}

// The list shows the waiters in serving order with their rank; a move prints old → new; the move is seen by the list and the status.
// Mutation gates: the rank not written → list shows normal (red); no old → new line → red.
func TestGateQueue_MoveAndList(t *testing.T) {
	cfg, _ := gateEnv(t, queueGate)
	t.Setenv("PDX_SESSION_ID", "")
	release := waitingGates(t, cfg, 2)
	q := listQueue(t, cfg)["heavy"]
	require.Len(t, q, 2)
	second := q[1].Ticket

	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"queue", "front", second, "--reason", "merge check", "--config", cfg}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "normal → front")

	q = listQueue(t, cfg)["heavy"]
	assert.Equal(t, second, q[0].Ticket, "the front ticket is served first")
	assert.Equal(t, "front", q[0].Rank)
	assert.Equal(t, "merge check", q[0].RankReason)
	assert.NotEmpty(t, q[0].RankBy)

	out.Reset()
	require.Equal(t, 0, gateMain([]string{"queue", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "front")
	assert.Contains(t, out.String(), "merge check")

	out.Reset()
	require.Equal(t, 0, gateMain([]string{"status", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "順序：front")
	assert.Contains(t, out.String(), "merge check")
	out.Reset()
	require.Equal(t, 0, gateMain([]string{"status", "--json", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), `"rank": "front"`)
	assert.Contains(t, out.String(), `"rank_reason": "merge check"`)

	out.Reset()
	require.Equal(t, 0, gateMain([]string{"queue", "hold", second, "--reason", "wait for the release", "--until", "30m", "--config", cfg}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "front → hold")
	out.Reset()
	require.Equal(t, 0, gateMain([]string{"queue", "normal", second, "--reason", "ok again", "--config", cfg}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "hold → normal")
	release()
}

// The refusals: no reason, a reason that is not one short line, --until without hold or in the past or not a time, an unknown ticket
// (the others still go on and the exit code is 1), an unknown rank word falls to the usage.
func TestGateQueue_Refusals(t *testing.T) {
	cfg, _ := gateEnv(t, queueGate)
	release := waitingGates(t, cfg, 1)
	defer release()
	ticket := listQueue(t, cfg)["heavy"][0].Ticket
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := gateMain(append(append([]string{"queue"}, args...), "--config", cfg), &out, &errb)
		return code, out.String(), errb.String()
	}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no reason":          {[]string{"front", ticket}, "--reason is required"},
		"empty reason":       {[]string{"front", ticket, "--reason", "  "}, "--reason is required"},
		"two lines":          {[]string{"front", ticket, "--reason", "a\nb"}, "one line"},
		"too long":           {[]string{"front", ticket, "--reason", strings.Repeat("x", 201)}, "one line"},
		"until without hold": {[]string{"front", ticket, "--reason", "r", "--until", "5m"}, "hold only"},
		"until in the past":  {[]string{"hold", ticket, "--reason", "r", "--until", "-5m"}, "not in the future"},
		"until nonsense":     {[]string{"hold", ticket, "--reason", "r", "--until", "soon"}, "neither a duration"},
		"no ticket":          {[]string{"front", "--reason", "r"}, "usage"},
		"stray words":        {[]string{"--reason", "r"}, "usage"},
	} {
		code, out, errb := run(c.args...)
		assert.Equal(t, 2, code, name)
		assert.Contains(t, errb, c.want, name)
		assert.Empty(t, out, name)
	}
	assert.Equal(t, "normal", listQueue(t, cfg)["heavy"][0].Rank, "a refused move changes nothing")

	// an unknown ticket and a pid nobody waits with: reported for that one, the real one still moves, exit 1
	code, out, errb := run("back", "no-such-ticket", ticket, "99999999", "--reason", "r")
	assert.Equal(t, 1, code)
	assert.Contains(t, errb, "no-such-ticket: no such waiting ticket or gate")
	assert.Contains(t, errb, "99999999: no such waiting ticket or gate")
	assert.Contains(t, out, "normal → back")
	assert.Equal(t, "back", listQueue(t, cfg)["heavy"][0].Rank)
}

// The list of an empty queue, per class.
func TestGateQueue_EmptyAndClassFilter(t *testing.T) {
	cfg, _ := gateEnv(t, "[gate.admission]\ndisk_min_gb = 0\n")
	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"queue", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "nobody is waiting")
	out.Reset()
	require.Equal(t, 0, gateMain([]string{"queue", "--json", "--class", "light", "--config", cfg}, &out, &errb))
	assert.JSONEq(t, `{"heavy":[],"light":[]}`, out.String())
	assert.Equal(t, 2, gateMain([]string{"queue", "--class", "huge", "--config", cfg}, &out, &errb))
}
