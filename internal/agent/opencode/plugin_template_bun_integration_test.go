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
