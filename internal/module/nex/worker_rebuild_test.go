package nex

// POST /api/nex/worker-rebuild (conversation entity spec D11).

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/module/agent"
)

// rebuildPost posts a raw body to /api/nex/worker-rebuild and decodes the answer.
func rebuildPost(t *testing.T, env *handoffEnv, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(env.srv.URL+"/api/nex/worker-rebuild", "application/json", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func rebuildErr(t *testing.T, env *handoffEnv, body string, status int, code string) map[string]any {
	t.Helper()
	got, out := rebuildPost(t, env, body)
	require.Equal(t, status, got, "%v", out)
	require.Equal(t, code, out["code"], "%v", out)
	return out
}

// rbS is a canonical-UUID session id (Nexen validates resume ids).
const rbS = "0a1b2c3d-0000-4000-8000-000000000001"

// archiveRemovesRows models Nexen: once archived, a row drops out of List.
func archiveRemovesRows(env *handoffEnv) {
	rs := rebuildStore(env)
	env.svc.onRecord = func(name string) {
		if name != "archive" {
			return
		}
		rs.mu.Lock()
		defer rs.mu.Unlock()
		for i := range rs.listRows {
			rs.listRows[i].ArchivedAt = 1
		}
	}
}

func rebuildStore(env *handoffEnv) *fakeNexStore { return env.m.sys.store.(*fakeNexStore) }

func TestWorkerRebuild(t *testing.T) {
	t.Run("fresh stint for a free session", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.result = execution.Result{ID: "N1", State: store.StateRunning, EffectiveProfile: "handoff"}
		code, out := rebuildPost(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`)
		require.Equal(t, 200, code, "%v", out)
		assert.Equal(t, "N1", out["execution_id"])
		assert.Equal(t, "running", out["state"])
		assert.Equal(t, "handoff", out["effective_profile"])
		req := env.svc.Requests()[0]
		assert.Equal(t, rbS, req.ResumeSessionID)
		assert.Equal(t, "", req.Brief)
		assert.True(t, req.StartIdle)
		assert.Equal(t, "handoff", req.SandboxProfile)
		assert.Equal(t, "/w", req.Mounts[0].Path)
		assert.Equal(t, "purdex://host/host1/rebuild", req.Origin)
		assert.Equal(t, "purdex", req.Labels["source"])
		assert.Equal(t, rbS, req.Labels[purdexSessionLabel], "D17")
		_, has := req.Labels["rebuild_of"]
		assert.False(t, has, "no rebuild_of without a replaced execution")
		assert.True(t, env.m.locks.TryLock(sidLockKey(rbS)), "sid lock released")
	})
	t.Run("replacing a failed stint exits it first", func(t *testing.T) {
		env := newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: rbS})
		rebuildStore(env).listRows = []store.Execution{row("F1", "failed", false, rbS, "", 1)}
		archiveRemovesRows(env)
		env.svc.result = execution.Result{ID: "N1", State: store.StateRunning}
		code, out := rebuildPost(t, env, `{"session_id":"`+rbS+`","cwd":"/w","profile":"readonly","replace_execution_id":"F1"}`)
		require.Equal(t, 200, code, "%v", out)
		assert.Equal(t, []string{"archive"}, env.svc.ArchiveCalls())
		ar := env.svc.archiveReqs[0]
		assert.Equal(t, "F1", ar.ExecutionID)
		assert.Equal(t, "pdx:host1", ar.PrincipalID)
		req := env.svc.Requests()[0]
		assert.Equal(t, "readonly", req.SandboxProfile)
		assert.Equal(t, "F1", req.Labels["rebuild_of"])
		calls := env.svc.Calls()
		assert.Less(t, slices.Index(calls, "archive"), slices.Index(calls, "delegate"), "exit before delegate: %v", calls)
		assert.True(t, env.m.locks.TryLock(sidLockKey(rbS)))
		assert.True(t, env.m.locks.TryLock(takeToTerminalLockKey("F1")))
	})
	t.Run("replacing a row that already exited does not exit it again", func(t *testing.T) {
		env := newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "T1", State: store.StateTerminated, SessionID: rbS})
		env.svc.result = execution.Result{ID: "N1", State: store.StateRunning}
		code, out := rebuildPost(t, env, `{"session_id":"`+rbS+`","cwd":"/w","replace_execution_id":"T1"}`)
		require.Equal(t, 200, code, "%v", out)
		assert.Empty(t, env.svc.ArchiveCalls())
		assert.Empty(t, env.svc.TerminateIDs())
		assert.Len(t, env.svc.Requests(), 1)
	})
	t.Run("the replaced row's exit failing stops before the delegate", func(t *testing.T) {
		env := newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: rbS})
		env.svc.archiveErr = errors.New("boom")
		out := rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","replace_execution_id":"F1"}`, 500, "archive_failed")
		assert.Equal(t, "exit_replaced", out["step"])
		assert.Empty(t, env.svc.Requests())
		assert.True(t, env.m.locks.TryLock(sidLockKey(rbS)))
	})
	t.Run("refusals", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.terminals.live = map[string][]agent.TerminalSession{rbS: {{PaneID: "%3", SessionID: rbS, AgentType: "cc", Verified: true}}}
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`, 409, "session_owned")
		assert.Empty(t, env.svc.Requests())
		assert.True(t, env.m.locks.TryLock(sidLockKey(rbS)), "sid lock released after a refusal")

		env = newHandoffEnv(t)
		rebuildStore(env).listRows = []store.Execution{row("L1", "idle", false, rbS, "", 1)}
		out := rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`, 409, "session_owned")
		assert.Equal(t, "worker", out["owner"])

		// A live worker other than the replaced one still owns S.
		env = newHandoffEnv(t)
		rebuildStore(env).listRows = []store.Execution{
			row("L1", "idle", false, rbS, "", 1),
			row("F1", "failed", false, rbS, "", 2),
		}
		rebuildStore(env).script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: rbS})
		out = rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","replace_execution_id":"F1"}`, 409, "session_owned")
		assert.Equal(t, "L1", out["execution_id"])
		assert.Empty(t, env.svc.ArchiveCalls(), "no exit before the owner check passes")

		env = newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "X1", State: store.StateFailed, SessionID: "OTHER"})
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","replace_execution_id":"X1"}`, 409, "replace_mismatch")

		env = newHandoffEnv(t)
		rs := rebuildStore(env)
		rs.mu.Lock()
		rs.results = []getResult{{err: store.ErrNotFound}}
		rs.mu.Unlock()
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","replace_execution_id":"NOPE"}`, 404, "execution_not_found")

		env = newHandoffEnv(t)
		require.True(t, env.m.locks.TryLock(sidLockKey(rbS)))
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`, 409, "transfer_in_progress")

		env = newHandoffEnv(t)
		rebuildErr(t, env, `{"cwd":"/w"}`, 400, "missing_session_id")
		rebuildErr(t, env, `{"session_id":"`+rbS+`"}`, 400, "missing_cwd")
		rebuildErr(t, env, `{nope`, 400, "malformed_body")

		env = newHandoffEnv(t)
		env.sessions.cwdErr = errors.New("gone")
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`, 409, "cwd_missing")

		// The replaced row is mid-exit / mid-transfer elsewhere.
		env = newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: rbS})
		require.True(t, env.m.locks.TryLock(takeToTerminalLockKey("F1")))
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","replace_execution_id":"F1"}`, 409, "transfer_in_progress")
		assert.Empty(t, env.svc.ArchiveCalls())
		assert.Empty(t, env.svc.Requests())
		assert.True(t, env.m.locks.TryLock(sidLockKey(rbS)), "sid lock released when the replace lock is busy")
	})
	t.Run("rejected is data", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.result = execution.Result{ID: "R1", State: store.StateRejected, RejectReason: "session_expired"}
		code, out := rebuildPost(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`)
		require.Equal(t, 200, code)
		assert.Equal(t, "rejected", out["state"])
		assert.Equal(t, "session_expired", out["reject_reason"])
	})
	t.Run("delegate error is 500", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.err = errors.New("spawn failed")
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`, 500, "delegate_failed")
		assert.True(t, env.m.locks.TryLock(sidLockKey(rbS)))
	})
	t.Run("engine unavailable", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.m.sys.service = nil
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`, 503, "nex_unavailable")
	})
}

func TestWorkerRebuildFixRound1(t *testing.T) {
	t.Run("malformed session id → 400, nothing locked or delegated", func(t *testing.T) {
		env := newHandoffEnv(t)
		rebuildErr(t, env, `{"session_id":"S","cwd":"/w"}`, 400, "invalid_session_id")
		rebuildErr(t, env, `{"session_id":"../../etc","cwd":"/w"}`, 400, "invalid_session_id")
		assert.Empty(t, env.svc.Requests())
		assert.True(t, env.m.locks.TryLock(sidLockKey(rbS)))
	})
	t.Run("upper-case id is normalized: owner of the lower-case form refuses; delegate gets lower-case", func(t *testing.T) {
		upper := strings.ToUpper(rbS)
		env := newHandoffEnv(t)
		rebuildStore(env).listRows = []store.Execution{row("L1", "idle", false, rbS, "", 1)}
		rebuildErr(t, env, `{"session_id":"`+upper+`","cwd":"/w"}`, 409, "session_owned")
		assert.Empty(t, env.svc.Requests())

		env = newHandoffEnv(t)
		require.True(t, env.m.locks.TryLock(sidLockKey(rbS)))
		rebuildErr(t, env, `{"session_id":"`+upper+`","cwd":"/w"}`, 409, "transfer_in_progress")

		env = newHandoffEnv(t)
		env.svc.result = execution.Result{ID: "N1", State: store.StateRunning}
		code, out := rebuildPost(t, env, `{"session_id":"`+upper+`","cwd":"/w"}`)
		require.Equal(t, 200, code, "%v", out)
		req := env.svc.Requests()[0]
		assert.Equal(t, rbS, req.ResumeSessionID)
		assert.Equal(t, rbS, req.Labels[purdexSessionLabel])
	})
	t.Run("a terminal appearing between the exit and the delegate refuses", func(t *testing.T) {
		env := newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: rbS})
		rebuildStore(env).listRows = []store.Execution{row("F1", "failed", false, rbS, "", 1)}
		archiveRemovesRows(env)
		env.terminals.byCall = func(n int) []agent.TerminalSession {
			if n < 2 {
				return nil
			}
			return []agent.TerminalSession{{PaneID: "%3", SessionID: rbS, AgentType: "cc", Verified: true}}
		}
		out := rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","replace_execution_id":"F1"}`, 409, "session_owned")
		assert.Equal(t, "terminal", out["owner"])
		assert.Equal(t, []string{"archive"}, env.svc.ArchiveCalls())
		assert.Empty(t, env.svc.Requests())
	})
	t.Run("a profile the host does not allow → 400, nothing exited", func(t *testing.T) {
		env := newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: rbS})
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","profile":"nope","replace_execution_id":"F1"}`, 400, "invalid_profile")
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","profile":"nope"}`, 400, "invalid_profile")
		assert.Empty(t, env.svc.ArchiveCalls())
		assert.Empty(t, env.svc.Requests())
	})
	t.Run("handoff profile unusable → handoff_unsupported", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.m.opts.Config.Sandbox.MaxProfile = "readonly"
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`, 409, "handoff_unsupported")
		assert.Empty(t, env.svc.Requests())
	})
	// Permission channel plan Task 2, same rule as the handoff: rebuilding an
	// asking row works on a host whose max_profile is handoff_ask.
	t.Run("handoff_ask below handoff: the asking profile rebuilds, plain handoff still refused", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.m.opts.Config.Sandbox.MaxProfile = "handoff_ask"
		env.svc.result = execution.Result{ID: "N1", State: store.StateIdle, EffectiveProfile: "handoff_ask"}
		code, out := rebuildPost(t, env, `{"session_id":"`+rbS+`","cwd":"/w","profile":"handoff_ask"}`)
		require.Equal(t, 200, code, "%v", out)
		require.Len(t, env.svc.Requests(), 1)
		assert.Equal(t, "handoff_ask", env.svc.Requests()[0].SandboxProfile)

		env = newHandoffEnv(t)
		env.m.opts.Config.Sandbox.MaxProfile = "handoff_ask"
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`, 409, "handoff_unsupported")
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","profile":"readonly"}`, 409, "handoff_unsupported")
		assert.Empty(t, env.svc.Requests())

		env = newHandoffEnv(t)
		env.m.opts.Config.Sandbox.MaxProfile = "trusted"
		rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","profile":"handoff_ask"}`, 400, "invalid_profile")
		assert.Empty(t, env.svc.Requests())
	})
	t.Run("replaced row held by a non-pdx holder → held_by at exit_replaced", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.acquireErr = store.ErrLeaseHeld
		held := store.Execution{ID: "E1", State: store.StateIdle, SessionID: rbS, LeaseID: "L-p", LeasePrincipalID: "ploom:agent-7", LeaseExpiresAt: nowMs() + 60_000}
		rebuildStore(env).script(held)
		rebuildStore(env).listRows = []store.Execution{held}
		out := rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","replace_execution_id":"E1"}`, 409, "held_by")
		assert.Equal(t, "exit_replaced", out["step"])
		assert.Empty(t, env.svc.Requests())
		assert.Empty(t, env.svc.ArchiveCalls())
	})
	t.Run("delegate failing after a replace exit reports replaced_exited", func(t *testing.T) {
		env := newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: rbS})
		env.svc.err = errors.New("spawn failed")
		out := rebuildErr(t, env, `{"session_id":"`+rbS+`","cwd":"/w","replace_execution_id":"F1"}`, 500, "delegate_failed")
		assert.Equal(t, true, out["replaced_exited"])
	})
}

func TestWorkerRebuild_StartIdle(t *testing.T) {
	env := newHandoffEnv(t)
	env.svc.result = execution.Result{ID: "N1", State: store.StateIdle}
	code, out := rebuildPost(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, "idle", out["state"])
	req := env.svc.Requests()[0]
	assert.True(t, req.StartIdle)
	assert.Equal(t, "", req.Brief)
}

// PR #1599 A1: a terminal resumes S during the delegate; Q1 exits the idle
// row it made, and no worker turn ever ran.
func TestWorkerRebuild_StartIdleRaceWithTerminalResume(t *testing.T) {
	env := newHandoffEnv(t)
	env.svc.result = execution.Result{ID: "N1", State: store.StateIdle}
	env.svc.lease = store.Lease{ID: "L-race"}
	env.svc.onDelegate = func() {
		rebuildStore(env).mu.Lock()
		rebuildStore(env).listRows = []store.Execution{row("N1", "idle", false, rbS, rbS, 1)}
		rebuildStore(env).mu.Unlock()
		env.terminals.mu.Lock()
		env.terminals.live = map[string][]agent.TerminalSession{rbS: {{FrameID: "F", PaneID: "%9", SessionID: rbS, AgentType: "cc", Verified: true}}}
		env.terminals.mu.Unlock()
	}
	code, out := rebuildPost(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`)
	require.Equal(t, 200, code, "%v", out)
	req := env.svc.Requests()[0]
	assert.True(t, req.StartIdle)
	assert.Equal(t, "", req.Brief)

	env.m.onSessionStart(agent.SessionStartEvent{AgentType: "cc", SessionID: rbS, Source: "resume", TmuxSession: "proj-2", TmuxPaneID: "%9", FrameID: "F"})
	assert.Equal(t, []string{"N1"}, archivedIDs(env), "the idle row is exited")
	assert.Equal(t, 1, countCalls(env.svc.Calls(), "delegate"))
}
