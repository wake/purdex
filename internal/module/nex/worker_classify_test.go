package nex

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shared cases (testdata/worker-status-cases.json) are also run by the SPA's worker-agent-status.test.ts.
func TestProjectWorker_SharedFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/worker-status-cases.json")
	require.NoError(t, err)
	var cases []struct {
		Name     string          `json:"name"`
		Row      json.RawMessage `json:"row"`
		Expected string          `json:"expected"`
	}
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.Greater(t, len(cases), 10)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			d, err := digestOf(c.Row)
			require.NoError(t, err)
			assert.Equal(t, c.Expected, string(projectWorker(d)))
		})
	}
}

func TestClassifyWorker(t *testing.T) {
	running := rowDigest{State: "running", TurnCount: 1}
	waiting := func(req string) rowDigest { return rowDigest{State: "running", PermissionRequest: req, TurnCount: 1} }
	idle := rowDigest{State: "idle", TurnCount: 1}
	failed := rowDigest{State: "failed", TurnCount: 1}
	rejected := rowDigest{State: "rejected"}
	archived := rowDigest{State: "idle", Archived: true, TurnCount: 1}
	pt := func(d rowDigest) *rowDigest { return &d }

	tests := []struct {
		name   string
		prev   *rowDigest
		cur    rowDigest
		notify bool
		status workerStatus
		key    string
	}{
		{"running to waiting", pt(running), waiting("p1"), true, workerWaiting, "e|waiting|p1"},
		{"waiting to waiting, request id changed", pt(waiting("p1")), waiting("p2"), true, workerWaiting, "e|waiting|p2"},
		{"waiting to waiting, same request id", pt(waiting("p1")), waiting("p1"), false, workerWaiting, ""},
		{"waiting to running (answered)", pt(waiting("p1")), running, false, workerRunning, ""},
		{"waiting to idle", pt(waiting("p1")), idle, true, workerIdle, "e|idle|1"},
		{"running to idle", pt(running), idle, true, workerIdle, "e|idle|1"},
		{"idle to idle, same digest", pt(idle), idle, false, workerIdle, ""},
		{"running to failed", pt(running), failed, true, workerError, "e|error|1"},
		{"failed to failed", pt(failed), failed, false, workerError, ""},
		{"nothing to rejected is a baseline", nil, rejected, false, workerError, ""},
		{"running to rejected", pt(running), rejected, true, workerError, "e|error|0"},
		{"idle to archived is clear", pt(idle), archived, false, workerClear, ""},
		{"running to archived with a request is clear", pt(running), rowDigest{State: "running", Archived: true, PermissionRequest: "p1"}, false, workerClear, ""},
		{"no prev is a baseline (idle)", nil, idle, false, workerIdle, ""},
		{"no prev is a baseline (waiting)", nil, waiting("p1"), false, workerWaiting, ""},
		{"idle to running", pt(idle), running, false, workerRunning, ""},
		{"failed with a request stays error", pt(running), rowDigest{State: "failed", PermissionRequest: "p1", TurnCount: 2}, true, workerError, "e|error|2"},
		{"unarchive to idle is not a second done", pt(archived), idle, false, workerIdle, ""},
		{"unarchive to failed is not a second failure", pt(rowDigest{State: "failed", Archived: true}), failed, false, workerError, ""},
		{"unarchive to waiting, same request", pt(rowDigest{State: "running", Archived: true, PermissionRequest: "p1"}), waiting("p1"), false, workerWaiting, ""},
		{"unarchive to waiting, another request", pt(rowDigest{State: "running", Archived: true, PermissionRequest: "p1"}), waiting("p2"), true, workerWaiting, "e|waiting|p2"},
		{"failed to idle without a running", pt(failed), idle, false, workerIdle, ""},
		{"rejected to idle without a running", pt(rejected), idle, false, workerIdle, ""},
		{"idle to failed without a running", pt(idle), failed, false, workerError, ""},
		{"blank request id keys on the turn", pt(running), rowDigest{State: "running", PermissionBlank: true, TurnCount: 2}, true, workerWaiting, "e|waiting||turn2"},
		{"last_turn_reason error with state idle is done", pt(running), rowDigest{State: "idle", LastTurnReason: "error", TurnCount: 1}, true, workerIdle, "e|idle|1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, key, notify := classifyWorker("e", tc.prev, tc.cur)
			assert.Equal(t, tc.notify, notify)
			assert.Equal(t, tc.status, status)
			assert.Equal(t, tc.key, key)
		})
	}
}
