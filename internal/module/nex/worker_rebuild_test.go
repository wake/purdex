package nex

// POST /api/nex/worker-rebuild (conversation entity spec D11).

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
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

func rebuildStore(env *handoffEnv) *fakeNexStore { return env.m.sys.store.(*fakeNexStore) }

func TestWorkerRebuild(t *testing.T) {
	t.Run("fresh stint for a free session", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.result = execution.Result{ID: "N1", State: store.StateRunning, EffectiveProfile: "handoff"}
		code, out := rebuildPost(t, env, `{"session_id":"S","cwd":"/w"}`)
		require.Equal(t, 200, code, "%v", out)
		assert.Equal(t, "N1", out["execution_id"])
		assert.Equal(t, "running", out["state"])
		assert.Equal(t, "handoff", out["effective_profile"])
		req := env.svc.Requests()[0]
		assert.Equal(t, "S", req.ResumeSessionID)
		assert.Equal(t, rebuildBrief, req.Brief)
		assert.Equal(t, "handoff", req.SandboxProfile)
		assert.Equal(t, "/w", req.Mounts[0].Path)
		assert.Equal(t, "purdex://host/host1/rebuild", req.Origin)
		assert.Equal(t, "purdex", req.Labels["source"])
		assert.Equal(t, "S", req.Labels[purdexSessionLabel], "D17")
		_, has := req.Labels["rebuild_of"]
		assert.False(t, has, "no rebuild_of without a replaced execution")
		assert.True(t, env.m.locks.TryLock(sidLockKey("S")), "sid lock released")
	})
	t.Run("replacing a failed stint exits it first", func(t *testing.T) {
		env := newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: "S"})
		env.svc.result = execution.Result{ID: "N1", State: store.StateRunning}
		code, out := rebuildPost(t, env, `{"session_id":"S","cwd":"/w","profile":"readonly","replace_execution_id":"F1"}`)
		require.Equal(t, 200, code, "%v", out)
		assert.Equal(t, []string{"archive"}, env.svc.ArchiveCalls())
		req := env.svc.Requests()[0]
		assert.Equal(t, "readonly", req.SandboxProfile)
		assert.Equal(t, "F1", req.Labels["rebuild_of"])
		calls := env.svc.Calls()
		assert.Less(t, slices.Index(calls, "archive"), slices.Index(calls, "delegate"), "exit before delegate: %v", calls)
		assert.True(t, env.m.locks.TryLock(sidLockKey("S")))
		assert.True(t, env.m.locks.TryLock(takeToTerminalLockKey("F1")))
	})
	t.Run("replacing a row that already exited does not exit it again", func(t *testing.T) {
		env := newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "T1", State: store.StateTerminated, SessionID: "S"})
		env.svc.result = execution.Result{ID: "N1", State: store.StateRunning}
		code, out := rebuildPost(t, env, `{"session_id":"S","cwd":"/w","replace_execution_id":"T1"}`)
		require.Equal(t, 200, code, "%v", out)
		assert.Empty(t, env.svc.ArchiveCalls())
		assert.Empty(t, env.svc.TerminateIDs())
		assert.Len(t, env.svc.Requests(), 1)
	})
	t.Run("the replaced row's exit failing stops before the delegate", func(t *testing.T) {
		env := newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: "S"})
		env.svc.archiveErr = errors.New("boom")
		out := rebuildErr(t, env, `{"session_id":"S","cwd":"/w","replace_execution_id":"F1"}`, 500, "archive_failed")
		assert.Equal(t, "exit_replaced", out["step"])
		assert.Empty(t, env.svc.Requests())
		assert.True(t, env.m.locks.TryLock(sidLockKey("S")))
	})
	t.Run("refusals", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.terminals.live = map[string][]agent.TerminalSession{"S": {{PaneID: "%3", SessionID: "S", AgentType: "cc", Verified: true}}}
		rebuildErr(t, env, `{"session_id":"S","cwd":"/w"}`, 409, "session_owned")
		assert.Empty(t, env.svc.Requests())
		assert.True(t, env.m.locks.TryLock(sidLockKey("S")), "sid lock released after a refusal")

		env = newHandoffEnv(t)
		rebuildStore(env).listRows = []store.Execution{row("L1", "idle", false, "S", "", 1)}
		out := rebuildErr(t, env, `{"session_id":"S","cwd":"/w"}`, 409, "session_owned")
		assert.Equal(t, "worker", out["owner"])

		// A live worker other than the replaced one still owns S.
		env = newHandoffEnv(t)
		rebuildStore(env).listRows = []store.Execution{
			row("F1", "failed", false, "S", "", 1),
			row("L1", "idle", false, "S", "", 2),
		}
		rebuildErr(t, env, `{"session_id":"S","cwd":"/w","replace_execution_id":"F1"}`, 409, "session_owned")
		assert.Empty(t, env.svc.ArchiveCalls(), "no exit before the owner check passes")

		env = newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "X1", State: store.StateFailed, SessionID: "OTHER"})
		rebuildErr(t, env, `{"session_id":"S","cwd":"/w","replace_execution_id":"X1"}`, 409, "replace_mismatch")

		env = newHandoffEnv(t)
		rs := rebuildStore(env)
		rs.mu.Lock()
		rs.results = []getResult{{err: store.ErrNotFound}}
		rs.mu.Unlock()
		rebuildErr(t, env, `{"session_id":"S","cwd":"/w","replace_execution_id":"NOPE"}`, 404, "execution_not_found")

		env = newHandoffEnv(t)
		require.True(t, env.m.locks.TryLock(sidLockKey("S")))
		rebuildErr(t, env, `{"session_id":"S","cwd":"/w"}`, 409, "transfer_in_progress")

		env = newHandoffEnv(t)
		rebuildErr(t, env, `{"cwd":"/w"}`, 400, "missing_session_id")
		rebuildErr(t, env, `{"session_id":"S"}`, 400, "missing_cwd")
		rebuildErr(t, env, `{nope`, 400, "malformed_body")

		env = newHandoffEnv(t)
		env.sessions.cwdErr = errors.New("gone")
		rebuildErr(t, env, `{"session_id":"S","cwd":"/w"}`, 409, "cwd_missing")

		// The replaced row is mid-exit / mid-transfer elsewhere.
		env = newHandoffEnv(t)
		rebuildStore(env).script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: "S"})
		require.True(t, env.m.locks.TryLock(takeToTerminalLockKey("F1")))
		rebuildErr(t, env, `{"session_id":"S","cwd":"/w","replace_execution_id":"F1"}`, 409, "transfer_in_progress")
		assert.Empty(t, env.svc.ArchiveCalls())
		assert.Empty(t, env.svc.Requests())
		assert.True(t, env.m.locks.TryLock(sidLockKey("S")), "sid lock released when the replace lock is busy")
	})
	t.Run("rejected is data", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.result = execution.Result{ID: "R1", State: store.StateRejected, RejectReason: "session_expired"}
		code, out := rebuildPost(t, env, `{"session_id":"S","cwd":"/w"}`)
		require.Equal(t, 200, code)
		assert.Equal(t, "rejected", out["state"])
		assert.Equal(t, "session_expired", out["reject_reason"])
	})
	t.Run("delegate error is 500", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.err = errors.New("spawn failed")
		rebuildErr(t, env, `{"session_id":"S","cwd":"/w"}`, 500, "delegate_failed")
		assert.True(t, env.m.locks.TryLock(sidLockKey("S")))
	})
	t.Run("engine unavailable", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.m.sys.service = nil
		rebuildErr(t, env, `{"session_id":"S","cwd":"/w"}`, 503, "nex_unavailable")
	})
}
