package nex

// PR #1586 R1-1: the owner check runs again right before the resume keys.

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/tmux"
)

// awaitRecheck: the two lookups of the transfer plus the one Q1 re-check it
// asks for after releasing its locks (manual_resume.go). The terminal's
// owner here is a stub with no live worker row, so the re-check exits nothing.
func awaitRecheck(t *testing.T, env *handoffEnv) {
	t.Helper()
	require.Eventually(t, func() bool { return env.terminals.Calls() == 3 }, 3*time.Second, time.Millisecond)
}

// ownerAfterFirstLook: no terminal on the first lookup, a verified one on
// every later lookup (an external resume landing in between).
func ownerAfterFirstLook(sid string) func(int) []agent.TerminalSession {
	return func(n int) []agent.TerminalSession {
		if n == 1 {
			return nil
		}
		return []agent.TerminalSession{{PaneID: "%9", SessionID: sid, AgentType: "cc", Verified: true}}
	}
}

func TestTakeToTerminal_OwnerAppearsBeforeResume(t *testing.T) {
	env := newTTEnv(t)
	env.terminals.byCall = ownerAfterFirstLook(tbSessionID)
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "session_owned", body["code"])
	assert.Equal(t, "terminal", body["owner"])
	assert.Equal(t, ttName, body["session_name"])
	assert.Equal(t, true, body["session_killed"])
	assert.Equal(t, false, body["exited"], "the worker was live and is untouched")
	assert.NotContains(t, env.timeline(t), "keys")
	assert.Empty(t, env.svc.terminateCalls)
	assert.Empty(t, env.svc.ArchiveCalls())
	assert.Len(t, env.sessions.Creates(), 1)
	assert.False(t, env.tmux.HasSession(ttName), "created session killed")
	assert.Equal(t, []tmux.KillIfInstanceCall{{SessionID: "$0", Expected: hoInstance}}, env.tmux.KillIfInstanceCalls())
	awaitRecheck(t, env.handoffEnv)
	assert.Equal(t, []releaseCall{{tbExecID, tbLeaseID, tbPrincipal}}, env.svc.releases, "lease released on the early return")
	assert.Eventually(t, func() bool { return env.m.locks.TryLock(sidLockKey(tbSessionID)) }, time.Second, time.Millisecond, "sid lock released")
}

func TestTakeback_OwnerAppearsBeforeResume(t *testing.T) {
	env := newTakebackEnv(t)
	env.terminals.byCall = ownerAfterFirstLook(tbSessionID)
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "session_owned", body["code"])
	assert.Equal(t, false, body["exited"])
	assert.Empty(t, env.tmux.RawKeysSent(), "no keys")
	assert.Empty(t, env.svc.terminateCalls)
	assert.Empty(t, env.svc.ArchiveCalls())
	awaitRecheck(t, env.handoffEnv)
	assert.Equal(t, []releaseCall{{tbExecID, tbLeaseID, tbPrincipal}}, env.svc.releases)
	assert.Eventually(t, func() bool { return env.m.locks.TryLock(sidLockKey(tbSessionID)) }, time.Second, time.Millisecond, "sid lock released")
}
