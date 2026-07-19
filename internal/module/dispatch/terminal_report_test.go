package dispatch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/module/execution"
	"github.com/wake/purdex/internal/module/stream"
	"github.com/wake/purdex/internal/relay"
)

func TestTerminalReporter_CompletedBuildsSeq3WithArtifacts(t *testing.T) {
	r := terminalReporter{}
	arts := []execution.Artifact{
		{Kind: "diff", Pointer: "pdx://dmn_1/execution/exc_9/diff", Meta: map[string]any{"files": 2, "add": 10, "del": 3}},
		{Kind: "transcript", Pointer: "pdx://dmn_1/execution/exc_9/transcript"},
	}
	// launch_state=launched → the execution had a running(2) report, so terminal is 3.
	exec := &execution.Execution{ExecutionID: "exc_9", DispatchID: "dsp_9", LaunchState: execution.LaunchLaunched}

	env, err := r.BuildTerminal(exec, execution.StatusCompleted, arts, "", "")
	require.NoError(t, err)

	require.Equal(t, seqTerminal, env.Seq)
	require.Equal(t, "completed", env.Status)
	payload := decodePayload(t, env)
	require.Equal(t, "completed", payload["status"])
	require.EqualValues(t, seqTerminal, payload["seq"])
	list, ok := payload["artifacts"].([]any)
	require.True(t, ok, "artifacts[] present")
	require.Len(t, list, 2)
	first := list[0].(map[string]any)
	require.Equal(t, "diff", first["kind"])
	require.Equal(t, "pdx://dmn_1/execution/exc_9/diff", first["pointer"])
	// completed carries no error object.
	require.NotContains(t, payload, "error")
}

func TestTerminalReporter_FailedCarriesError(t *testing.T) {
	exec := &execution.Execution{ExecutionID: "exc_f", DispatchID: "dsp_f", LaunchState: execution.LaunchLaunched}

	env, err := terminalReporter{}.BuildTerminal(exec, execution.StatusFailed, nil, "execution_error", "boom")
	require.NoError(t, err)

	require.Equal(t, "failed", env.Status)
	errObj, ok := decodePayload(t, env)["error"].(map[string]any)
	require.True(t, ok, "failed carries error object")
	require.Equal(t, "execution_error", errObj["code"])
	require.Equal(t, "boom", errObj["message"])
}

// F4: an execution that never reached launched never had a running(2) report, so
// its recovered terminal report must ride seq=2 — not 3, which would leave a hole
// Ploom's ordered projection waits on forever.
func TestTerminalReporter_NeverLaunched_UsesSeq2(t *testing.T) {
	for _, ls := range []execution.LaunchState{execution.LaunchLaunching, execution.LaunchNone} {
		exec := &execution.Execution{ExecutionID: "exc_n", DispatchID: "dsp_n", LaunchState: ls}
		env, err := terminalReporter{}.BuildTerminal(exec, execution.StatusFailed, nil, "launch_failed", "gone")
		require.NoError(t, err)
		require.Equal(t, seqFailed, env.Seq, "launch_state=%s", ls)
		require.EqualValues(t, seqFailed, decodePayload(t, env)["seq"])
	}
}

// gitRepoWithChange creates a repo with a committed file, returns repo+head, then
// modifies the file so a diff exists.
func gitRepoWithChange(t *testing.T) (repo, head string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f.txt"), []byte("a\nb\n"), 0o644))
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	head = run("rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f.txt"), []byte("a\nB\nc\n"), 0o644))
	return repo, head
}

// fakeSeam captures the registered terminal handler and serves fixed results.
type fakeSeam struct {
	handler stream.TerminalHandler
	results map[string]stream.ResultEvent
}

func (f *fakeSeam) SetTerminalHandler(h stream.TerminalHandler) { f.handler = h }
func (f *fakeSeam) LastResult(code string) (stream.ResultEvent, bool) {
	r, ok := f.results[code]
	return r, ok
}

// TestWireTerminal_HandlerReportsCompleted drives the full P.8 seam→report path:
// wireTerminal registers a handler; firing it for a running execution's
// session_code marks the row terminal and lands a seq=3 completed report (with
// the diff artifact) in the real outbox.
func TestWireTerminal_HandlerReportsCompleted(t *testing.T) {
	repo, head := gitRepoWithChange(t)

	store, err := execution.OpenExecution(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	// Insert a launched (running) row with a known session_code.
	e, created, err := store.UpsertByDispatch(execution.NewExecution{
		DispatchID:   "dsp_e2e",
		RepoLocation: repo,
		Provider:     "claude",
		SessionName:  "pdx-exec-e2e",
		LaunchState:  execution.LaunchLaunching,
		HeadAtStart:  head,
	})
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, store.MarkLaunched(e.ExecutionID, "sc_e2e"))

	outbox := store.Outbox()

	m := &DispatchModule{sender: NewSender(outbox, nil)}
	seam := &fakeSeam{results: map[string]stream.ResultEvent{"sc_e2e": {IsError: false, Subtype: "success"}}}
	m.wireTerminal(store, seam, "dmn_1")
	require.NotNil(t, seam.handler, "wireTerminal must register a handler")

	seam.handler("sc_e2e", relay.TerminalEvent{ExitCode: 0})

	// Row is now terminal completed.
	got, ok, err := store.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, execution.StatusCompleted, got.Status)
	require.Equal(t, string(execution.OutcomeResult), got.OutcomeSource.String)

	// A seq=3 completed report with artifacts landed in the outbox.
	rec, ok, err := outbox.RecordBySeq(e.ExecutionID, seqTerminal)
	require.NoError(t, err)
	require.True(t, ok, "terminal report enqueued at seq=%d", seqTerminal)
	require.Equal(t, "completed", rec.Status)
	require.Contains(t, string(rec.Payload), `"artifacts"`)
	require.Contains(t, string(rec.Payload), `pdx://dmn_1/execution/`+e.ExecutionID+`/diff`)
}
