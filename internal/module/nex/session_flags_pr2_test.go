package nex

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"
)

// U18 PR-2 (#1647): terminal → worker. The reading goes into execution.Request.Model / Effort (nexen v0.21.0, wake/nexen#131),
// so every turn of the worker starts with the session's model and effort. nexen is linked in-process, so the pinned version IS
// the capability (`delegate.model_effort` is a statement about that very library); there is no older daemon to detect.

// Mutations: Model / Effort not set on the Request in the handoff → red.
func TestHandoff_PassesTheReadingToTheWorker(t *testing.T) {
	env := newHandoffEnv(t)
	env.setUsage(hoSessionID, opusID, "xhigh")
	env.svc.result = execution.Result{State: store.StateIdle}
	status, body := env.post(t, hoCode, goodBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	reqs := env.svc.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, opusID, reqs[0].Model)
	assert.Equal(t, "xhigh", reqs[0].Effort)
}

// No reading → today's request: both fields empty. Mutation: default a value in → red.
func TestHandoff_WithoutAReadingTheWorkerStartsAsBefore(t *testing.T) {
	env := newHandoffEnv(t)
	env.svc.result = execution.Result{State: store.StateIdle}
	status, _ := env.post(t, hoCode, goodBody())
	require.Equal(t, http.StatusOK, status)
	reqs := env.svc.Requests()
	require.Len(t, reqs, 1)
	assert.Empty(t, reqs[0].Model)
	assert.Empty(t, reqs[0].Effort)
}

// The rebuild of a worker is the same hand-over of the same session: same fields, same labels. Mutation: not passed → red.
func TestWorkerRebuild_PassesTheReadingToTheWorker(t *testing.T) {
	env := newHandoffEnv(t)
	env.setUsage(rbS, opusID, "xhigh")
	env.svc.result = execution.Result{ID: "N1", State: store.StateRunning, EffectiveProfile: "handoff"}
	code, out := rebuildPost(t, env, `{"session_id":"`+rbS+`","cwd":"/w"}`)
	require.Equal(t, 200, code, "%v", out)
	req := env.svc.Requests()[0]
	assert.Equal(t, opusID, req.Model)
	assert.Equal(t, "xhigh", req.Effort)
	assert.Equal(t, opusID, req.Labels[handoffModelLabel], "the take-back reads the labels")
	assert.Equal(t, "xhigh", req.Labels[handoffEffortLabel])
}

// codex R1+attack: a rebuild that replaces a stint reads the replaced execution's labels first (the authoritative copy of what
// the worker was started with); the statusline cache only fills what they lack. With the cache empty (a restart, an eviction)
// the setting is still kept; with a conflict the labels win. Mutation: readingOf(sid, "") → both red.
func TestWorkerRebuild_ReplacedStintLabelsComeFirst(t *testing.T) {
	labels := `{"purdex.model":"` + opusID + `","purdex.effort":"xhigh","source":"purdex"}`
	for name, cache := range map[string]bool{"cache empty": false, "cache disagrees": true} {
		t.Run(name, func(t *testing.T) {
			env := newHandoffEnv(t)
			if cache {
				env.setUsage(rbS, "claude-sonnet-5-5", "low")
			}
			rebuildStore(env).script(store.Execution{ID: "T1", State: store.StateTerminated, SessionID: rbS, Labels: labels})
			env.svc.result = execution.Result{ID: "N1", State: store.StateRunning}
			code, out := rebuildPost(t, env, `{"session_id":"`+rbS+`","cwd":"/w","replace_execution_id":"T1"}`)
			require.Equal(t, 200, code, "%v", out)
			req := env.svc.Requests()[0]
			assert.Equal(t, opusID, req.Model)
			assert.Equal(t, "xhigh", req.Effort)
			assert.Equal(t, opusID, req.Labels[handoffModelLabel], "and the new row records it again")
		})
	}
}

// nexen accepts Bedrock and Vertex ids (':' '@' '/'); purdex's guard for what it passes on follows nexen's, so a session on those
// providers is not silently dropped. team.ValidModel (the member launch line's guard) is left as it is. Everything that could
// not be one argv word — or could be a flag — stays refused. Mutation: the narrow rule → the ids below are dropped (red).
func TestReading_BedrockAndVertexIdsAreKept(t *testing.T) {
	for _, id := range []string{
		"us.anthropic.claude-sonnet-4-5-20250929-v1:0",
		"claude-3-5-sonnet-v2@20241022",
		"arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-sonnet-4-5-v1:0",
	} {
		assert.Equal(t, " --model '"+id+"' --effort high",
			applySessionFlags("claude --resume {id}", sessionReading{Model: id, Effort: "high"})[len("claude --resume {id}"):], id)
		env := newHandoffEnv(t)
		env.setUsage(hoSessionID, id, "high")
		assert.Equal(t, sessionReading{Model: id, Effort: "high"}, env.m.readingOf(hoSessionID, ""), id)
	}
	for _, bad := range []string{"-x", "a b", "a'b", "a;b", "$(x)", "a\nb", "", string(make([]byte, 161))} {
		assert.False(t, validModel(bad) && bad != "", "%q", bad)
	}
	assert.False(t, validModel("-opus"))
	assert.True(t, validModel("claude-opus-5-5[1m]"))
}
