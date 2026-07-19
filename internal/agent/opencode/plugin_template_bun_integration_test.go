package opencode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRenderManagedPlugin_BunRuntimeEmitsStdin proves the rendered emit()
// helper actually drives Bun.spawn correctly at runtime. Plan
// 2026-04-29 §T1: existing string-shape tests cannot catch the
// TypeError: ERR_INVALID_ARG_TYPE that ships with stdin: <raw string>,
// because they never invoke a real Bun. We render the plugin against
// a stub pdx shell binary, append an async IIFE that fires one
// session.created event, run the result with `bun <script.mjs>`, and
// assert the stub captured the JSON payload on stdin.
//
// The test skips on Windows, on hosts without /bin/sh, on hosts
// without bun on PATH, and on hosts where `bun --version` fails. Each
// gate is a skip rather than a fail so a single `go test ./...`
// invocation works across CI environments.
func TestRenderManagedPlugin_BunRuntimeEmitsStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("opencode plugin runtime is POSIX-only; skipping real-Bun integration test")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("/bin/sh unavailable (%v); skipping real-Bun integration test", err)
	}
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not on PATH; skipping real-Bun integration test")
	}
	if out, err := exec.Command(bunPath, "--version").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skipf("bun --version failed (%v); skipping real-Bun integration test", err)
	}

	tmp := t.TempDir()
	stubPath := filepath.Join(tmp, "pdx-stub")
	capturePath := filepath.Join(tmp, "stdin-capture")
	stubBody := "#!/bin/sh\ncat > \"$PDX_TEST_STDIN_CAPTURE\"\n"
	if err := os.WriteFile(stubPath, []byte(stubBody), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	// Belt-and-braces chmod in case umask stripped exec bit.
	if err := os.Chmod(stubPath, 0o755); err != nil {
		t.Fatalf("chmod stub: %v", err)
	}

	body := renderManagedPlugin(stubPath)
	tail := `
;(async () => {
  const hooks = await PurdexOpenCodeHooks()
  await hooks.event({
    event: {
      type: 'session.created',
      properties: { sessionID: 'test-session' },
    },
  })
})()
`
	scriptPath := filepath.Join(tmp, "plugin.mjs")
	if err := os.WriteFile(scriptPath, []byte(body+tail), 0o644); err != nil {
		t.Fatalf("write plugin.mjs: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bunPath, scriptPath)
	cmd.Env = append(os.Environ(), "PDX_TEST_STDIN_CAPTURE="+capturePath)
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		// Classify the failure: a TypeError mentioning ERR_INVALID_ARG_TYPE
		// (or the literal "stdio must be an array") is the pre-fix red
		// signal. Any other failure (envelope mismatch, deadlock, syntax
		// error) means the harness itself is wrong; surface it explicitly
		// so a future contributor can repair it without conflating with
		// the actual regression.
		out := string(output)
		if strings.Contains(out, "ERR_INVALID_ARG_TYPE") || strings.Contains(out, "stdio must be an array") {
			t.Fatalf("pre-fix Bun TypeError observed; apply T2 stdin-pipe fix to turn this green: %s", out)
		}
		t.Fatalf("unexpected bun failure: err=%v output=%s", runErr, out)
	}

	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v (bun output: %s)", err, output)
	}
	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("unmarshal captured stdin: %v (raw=%q)", err, string(captured))
	}
	if got := payload["session_id"]; got != "test-session" {
		t.Fatalf("captured stdin session_id = %v, want %q (raw=%q)", got, "test-session", string(captured))
	}
}

// TestRenderManagedPlugin_BunRuntimeGatesChildSessionLifecycle drives a
// realistic parent+subagent lifecycle through the real rendered JS under
// Bun and proves the child-session gate holds end-to-end (guards against
// "mirror and JS drift wrong together"). The stub pdx appends every
// invocation as `eventName<TAB>stdin` JSONL; the event name comes from
// argv[4] of [pdxPath,'hook','--agent','opencode',eventName]. emit() awaits
// proc.exited, so appends are ordered and non-interleaved.
//
// Sequence: parent created -> child created(parentID) -> child idle ->
// child error -> child deleted -> parent idle -> parent deleted. Only the
// three parent-level events (PdxSessionStart, PdxStop, PdxSessionEnd) must
// reach the stub; every child-derived event must be gated.
func TestRenderManagedPlugin_BunRuntimeGatesChildSessionLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("opencode plugin runtime is POSIX-only; skipping real-Bun integration test")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("/bin/sh unavailable (%v); skipping real-Bun integration test", err)
	}
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not on PATH; skipping real-Bun integration test")
	}
	if out, err := exec.Command(bunPath, "--version").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skipf("bun --version failed (%v); skipping real-Bun integration test", err)
	}

	tmp := t.TempDir()
	stubPath := filepath.Join(tmp, "pdx-stub")
	capturePath := filepath.Join(tmp, "events-capture")
	// argv[4] is the event name; append "<name>\t<stdin>\n" as one JSONL row.
	stubBody := "#!/bin/sh\nprintf '%s\\t' \"$4\" >> \"$PDX_TEST_EVENT_CAPTURE\"\ncat >> \"$PDX_TEST_EVENT_CAPTURE\"\nprintf '\\n' >> \"$PDX_TEST_EVENT_CAPTURE\"\n"
	if err := os.WriteFile(stubPath, []byte(stubBody), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	if err := os.Chmod(stubPath, 0o755); err != nil {
		t.Fatalf("chmod stub: %v", err)
	}

	body := renderManagedPlugin(stubPath)
	tail := `
;(async () => {
  const hooks = await PurdexOpenCodeHooks()
  const fire = (ev) => hooks.event({ event: ev })
  await fire({ type: 'session.created', properties: { sessionID: 'parent1', info: { id: 'parent1' } } })
  await fire({ type: 'session.created', properties: { sessionID: 'child1', info: { id: 'child1', parentID: 'parent1' } } })
  await fire({ type: 'session.status', properties: { sessionID: 'child1', status: { type: 'idle' } } })
  await fire({ type: 'session.error', properties: { sessionID: 'child1', error: { name: 'ProviderError', data: { message: 'boom' } } } })
  await fire({ type: 'session.deleted', properties: { sessionID: 'child1' } })
  await fire({ type: 'session.status', properties: { sessionID: 'parent1', status: { type: 'idle' } } })
  await fire({ type: 'session.deleted', properties: { sessionID: 'parent1' } })
})()
`
	scriptPath := filepath.Join(tmp, "plugin.mjs")
	if err := os.WriteFile(scriptPath, []byte(body+tail), 0o644); err != nil {
		t.Fatalf("write plugin.mjs: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bunPath, scriptPath)
	cmd.Env = append(os.Environ(), "PDX_TEST_EVENT_CAPTURE="+capturePath)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("unexpected bun failure: err=%v output=%s", runErr, string(output))
	}

	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		name, payloadJSON, found := strings.Cut(line, "\t")
		if !found {
			t.Fatalf("malformed capture row (no tab): %q", line)
		}
		names = append(names, name)
		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload for %q: %v (raw=%q)", name, err, payloadJSON)
		}
		if sid, _ := payload["session_id"].(string); sid != "parent1" {
			t.Fatalf("event %q session_id = %q, want parent1 (no child event may leak)", name, sid)
		}
	}

	want := []string{"PdxSessionStart", "PdxStop", "PdxSessionEnd"}
	if len(names) != len(want) {
		t.Fatalf("captured events = %v, want exactly %v (child lifecycle must be gated)", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Fatalf("captured events = %v, want %v", names, want)
		}
	}
}
