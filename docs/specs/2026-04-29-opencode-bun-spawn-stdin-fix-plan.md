# opencode Plugin `Bun.spawn` stdin Hotfix — Implementation Plan

- **Spec**: `docs/specs/2026-04-29-opencode-bun-spawn-stdin-fix-spec.md`
- **Date**: 2026-04-29
- **Base**: `96bae3ce` (main @ alpha.248)
- **Worktree**: `.claude/worktrees/opencode-spawn-fix`
- **Branch**: `worktree-opencode-spawn-fix`
- **Tracking**: #715
- **Plan revision**: v1.1 (2026-04-29) — incorporates codex plan review job `task-moiufay4-0c5pdm` (3 P2 + 3 P3, 0 P0/P1, all addressed).

## 1. Scope summary

A 1-line fix to the JS template rendered by
`internal/agent/opencode/plugin_template.go:34` (the `stdin` field of
the `Bun.spawn` call inside the rendered `emit()` helper), plus a new
real-Bun integration test that catches the regression.

Total expected diff:

| File | Action | Lines |
|------|--------|-------|
| `internal/agent/opencode/plugin_template.go` | Edit `stdin: JSON.stringify(payload)` line + add 2 follow-up lines (`proc.stdin.write(...)` / `proc.stdin.end()`) | ~ +2 / -1 |
| `internal/agent/opencode/plugin_template_bun_integration_test.go` | New file: real-Bun integration test that proves `emit()` works at runtime | ~ +130 |
| `internal/agent/opencode/hooks_test.go` (existing) | Append one new test `TestCheckHooks_PreFixManagedBodyReportsDrift` that hand-writes a pre-fix body and asserts drift then reinstall convergence | ~ +60 |
| `docs/specs/2026-04-29-opencode-bun-spawn-stdin-fix-spec.md` | New (already in tree) | +330 |
| `docs/specs/2026-04-29-opencode-bun-spawn-stdin-fix-plan.md` | This file | +(this) |

No other files touched.

## 2. Phase split — single phase, 5 ordered tasks

### Task T1 — Add failing real-Bun integration test (TDD red)

**Files:** `internal/agent/opencode/plugin_template_bun_integration_test.go` (new)

**What:**

Create the integration test per spec §4.2 with the codex-plan-review
adjustments below:

1. File header:
   ```go
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
   ```
   (No build tag — the test gates internally per spec §4.2.1 so a
   single `go test ./...` invocation runs whatever the environment
   supports.)

2. One test function `TestRenderManagedPlugin_BunRuntimeEmitsStdin` that:
   - Applies **four** layered skip gates (per spec §4.2.1 + plan-review P3-4):
     1. `runtime.GOOS == "windows"` → skip.
     2. `os.Stat("/bin/sh")` errors → skip (covers exotic POSIX
        environments without a baseline shell).
     3. `exec.LookPath("bun")` errors → skip.
     4. `bun --version` exits non-zero or empty → skip.
   - Builds a temp dir via `t.TempDir()`.
   - Writes a stub `pdx` shell script:
     ```sh
     #!/bin/sh
     cat > "$PDX_TEST_STDIN_CAPTURE"
     ```
     Stub captures stdin via the env var — independent of the
     rendered command line (`pdx hook --agent opencode <event>`).
   - `os.Chmod(stubPath, 0o755)`.
   - `body := renderManagedPlugin(stubPath)` and append a tail that
     triggers one event, wrapped as an IIFE so we don't depend on
     top-level await module semantics, and write to a `.mjs` file so
     Bun parses ESM unambiguously:
     ```js
     ;(async () => {
       const hooks = await PurdexOpenCodeHooks()
       await hooks.event({
         event: {
           type: 'session.created',
           properties: { sessionID: 'test-session' },
         },
       })
     })()
     ```
   - Write `body + tail` to `t.TempDir()/plugin.mjs`.
   - **Single-execution pattern** (plan-review P2-2): use
     `output, err := cmd.CombinedOutput()` exactly once. Do not call
     `cmd.Run()` then `cmd.CombinedOutput()` separately — that would
     spawn Bun twice and lose the first execution's evidence.
   - 5s `context.WithTimeout` and `cmd.Env = append(os.Environ(),
     "PDX_TEST_STDIN_CAPTURE="+capturePath)`.
   - **Failure-mode classification** (plan-review P2-1): when
     `err != nil`, examine `string(output)`:
     - If it contains `ERR_INVALID_ARG_TYPE` or `stdio must be an
       array` → before-fix expected failure. Record via
       `t.Logf("pre-fix TypeError observed (TDD red)")`. (When
       running pre-fix this is the red signal; when running
       post-fix this branch should be unreachable.)
     - Else → unexpected failure (envelope mismatch, deadlock, syntax
       error, …). `t.Fatalf("unexpected bun failure: err=%v output=%s",
       err, output)` so the test surfaces the wrong-reason red.
     - On `err == nil`, proceed to the capture-file assertion below.
   - Read `capturePath`, `json.Unmarshal` into a generic map, assert
     `m["session_id"] == "test-session"`. Mismatch → `t.Fatalf`.

3. Confirm the test fails before the fix in the **expected** way:
   - `go test ./internal/agent/opencode/ -run BunRuntimeEmitsStdin -v`
   - Expect: stderr contains `ERR_INVALID_ARG_TYPE`. We deliberately
     keep this as a `t.Fatalf` (the test is red because we have not
     applied T2 yet) so re-running after T2 turns it green. **This
     is the TDD red.**

**Verify:** failing test exists with the documented stderr-contains
classification, so we know "red because of the bug, not because of a
harness mistake."

**Commit:** `test(opencode): add real-Bun integration test for plugin emit() stdin (#715)`

---

### Task T2 — Apply 1-line stdin fix (TDD green)

**Files:** `internal/agent/opencode/plugin_template.go`

**What:** edit the `emit()` body inside `renderManagedPlugin`:

Before (line 32-39):
```js
async function emit(eventName, payload = {}) {
  const proc = Bun.spawn({
    cmd: [pdxPath, 'hook', '--agent', 'opencode', eventName],
    stdin: JSON.stringify(payload),
    stdout: 'ignore',
    stderr: 'ignore',
  })
  await proc.exited
}
```

After:
```js
async function emit(eventName, payload = {}) {
  const proc = Bun.spawn({
    cmd: [pdxPath, 'hook', '--agent', 'opencode', eventName],
    stdin: 'pipe',
    stdout: 'ignore',
    stderr: 'ignore',
  })
  proc.stdin.write(JSON.stringify(payload))
  proc.stdin.end()
  await proc.exited
}
```

**Do not touch** anything else in the file (regex helpers, constants,
non-emit handlers).

**Verify:**

- `go test ./internal/agent/opencode/ -run BunRuntimeEmitsStdin -v` is now green.
- `go test ./internal/agent/opencode/...` is green (all 8 existing tests + 1 new = 9 pass).

**Commit:** `fix(opencode): use stdin pipe for Bun.spawn in plugin emit() (#715)`

---

### Task T3 — Confirm test parity helpers still pass

**Files:** none (verification only)

**What:** Re-read the existing parity tests to confirm none of their
assertions overlap with the changed lines:

- `extractEmittedEvents` looks for `emit('Name'` — emit() name strings
  unchanged; pass.
- `extractPdxPath` looks for `const pdxPath = "..."` — line untouched;
  pass.
- `TestRenderManagedPlugin_UsesInputModelAndSessionScopedSubagentKeys`
  asserts `const model = input.model`, `const subagentKey = …`, `if
  (activeSubagents.has(subagentKey)) return` — all in
  `chat.message`/`tool.execute.before` handlers; pass.
- `TestValidateSpecsCoverEmitted_*` — about event names, not stdin;
  pass.
- `TestRenderManagedPlugin_ProducesValidBody` — managed marker;
  pass.

**Verify:** `go test ./internal/agent/opencode/...` green from T2 already
covers this; this task is the explicit gate-confirm step.

**No commit** (verification only).

---

### Task T3.5 — Add `CheckHooks` drift unit test (AC3 in-repo)

**Files:** `internal/agent/opencode/hooks_test.go` (append one test)

**What:** lift AC3 (drift detection on pre-fix managed plugins) from
manual mlab steps to a CI-enforceable assertion (plan-review P3-5).
Append after the existing `TestOpenCodeHooks_InstallCheckRemove`:

```go
// TestCheckHooks_PreFixManagedBodyReportsDrift documents AC3 from
// 2026-04-29 spec §7: a managed plugin file shipped before the
// stdin-pipe fix (byte-different from the fixed render) must surface
// as drift via CheckHooks, and a subsequent InstallHooks must
// converge it back. We synthesize the pre-fix body by string-replace
// rather than vendor a snapshot — the contract under test is "if the
// on-disk body differs from renderManagedPlugin's current output by
// even one byte, CheckHooks reports drift."
func TestCheckHooks_PreFixManagedBodyReportsDrift(t *testing.T) {
    home := t.TempDir()
    t.Setenv("HOME", home)
    pinCanonicalPdxPath(t, "/usr/local/bin/pdx")

    p := opencode.NewProvider()

    // Hand-author a plugin file that mirrors a pre-fix body shape:
    // start from the fixed render and replace stdin: 'pipe' with
    // stdin: JSON.stringify(payload). The marker stays intact so
    // CheckHooks treats it as a managed plugin (drifted), not as
    // unmanaged.
    fixed := opencode.RenderManagedPluginForTesting("/usr/local/bin/pdx")
    preFix := strings.Replace(
        fixed,
        "stdin: 'pipe',\n    stdout: 'ignore',",
        "stdin: JSON.stringify(payload),\n    stdout: 'ignore',",
        1,
    )
    if preFix == fixed {
        t.Fatal("synthetic pre-fix body identical to fixed; replace pattern stale")
    }

    pluginDir := filepath.Join(home, ".config", "opencode", "plugins")
    if err := os.MkdirAll(pluginDir, 0o755); err != nil {
        t.Fatalf("mkdir: %v", err)
    }
    pluginPath := filepath.Join(pluginDir, "pdx-agent-hooks.js")
    if err := os.WriteFile(pluginPath, []byte(preFix), 0o644); err != nil {
        t.Fatalf("write pre-fix body: %v", err)
    }

    status, err := p.CheckHooks()
    if err != nil {
        t.Fatalf("CheckHooks: %v", err)
    }
    foundDrift := false
    for name, ev := range status.Events {
        if !ev.Installed {
            foundDrift = true
            t.Logf("drift on event %q (Installed=false)", name)
        }
    }
    if !foundDrift {
        t.Fatal("expected at least one event to report drift on pre-fix body")
    }

    if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
        t.Fatalf("InstallHooks: %v", err)
    }
    after, err := p.CheckHooks()
    if err != nil {
        t.Fatalf("CheckHooks post-reinstall: %v", err)
    }
    for name, ev := range after.Events {
        if !ev.Installed {
            t.Errorf("post-reinstall event %q still drifting", name)
        }
    }

    _ = agent.HookEventSpec{} // import-keeper; remove if unused
}
```

This test relies on a tiny test-only export — see implementation note
below. If a `RenderManagedPluginForTesting` export does not yet exist
add a one-liner:

```go
// In internal/agent/opencode/plugin_template.go (or a sibling test-export file):
func RenderManagedPluginForTesting(p string) string { return renderManagedPlugin(p) }
```

(Use the existing `*_export_test.go` style — there is already a
`hooks_export_test.go` that exports test hooks via `var
SetResolveCanonicalPdxPathForTesting = …`. Mirror that convention.)

**Verify:**

- `go test ./internal/agent/opencode/ -run PreFixManagedBodyReportsDrift -v` green.
- Existing `TestOpenCodeHooks_InstallCheckRemove` still green.

**Commit:** `test(opencode): add CheckHooks drift coverage for pre-fix managed body (#715)`

---

### Task T4 — Local `pdx setup --agent opencode` smoke

**Files:** none (manual)

**What:**

```bash
go build -o /tmp/pdx-fix ./cmd/pdx
HOME=$(mktemp -d) /tmp/pdx-fix setup --agent opencode
cat $HOME/.config/opencode/plugins/pdx-agent-hooks.js | sed -n '30,42p'
```

Expect to see the new `stdin: 'pipe'` + `proc.stdin.write(...)` /
`proc.stdin.end()` lines. The `JSON.stringify` should appear inside
`write(JSON.stringify(payload))`, not in the `stdin` field.

**Verify:** rendered file contains the fixed template; managed marker
intact.

**No commit** (verification only — purely local tempdir, throwaway).

---

### Task T5 — Update kickoff memory pointer

**Files:** `~/.claude/projects/-Users-wake-Workspace-wake-purdex/memory/MEMORY.md`
(via local memory write — done in the conversation, not committed to repo)

**What:** Once the PR is open, update the kickoff entry's status line.
Once merged, archive per kickoff norms.

**No commit.**

## 3. Verification matrix (spec §7 mapping)

| Spec §7 acceptance | How verified | Where |
|---|---|---|
| AC1 — `go test` green w/ Bun | T1+T2 | local + CI runner with bun |
| AC2 — `go test` green w/o Bun (skip path) | manual `PATH= go test …` | local |
| AC3 — pre-fix plugin reports drift, reinstall converges | T3.5 unit test (in-repo CI) + corroborating mlab observation in §4 | repo + mlab |
| AC4 — opencode events reach daemon DB | mlab live verify §4 | mlab |

## 4. mlab live verify (PR test plan, post-T2 commits pushed)

**Owner:** wake (manual). Recorded in PR body as a checklist for
reviewer benefit; this plan does not block on it during code review.

```bash
# On mlab:
cd ~/Workspace/wake/purdex
git fetch origin pull/<PR#>/head:opencode-spawn-fix-pr
git checkout opencode-spawn-fix-pr
go build -o /tmp/pdx-fix ./cmd/pdx

# Verify the rendered plugin updates correctly (covers AC3):
ls -l ~/.config/opencode/plugins/pdx-agent-hooks.js  # pre-fix file age
/tmp/pdx-fix setup --agent opencode
ls -l ~/.config/opencode/plugins/pdx-agent-hooks.js  # rewritten timestamp
sed -n '30,42p' ~/.config/opencode/plugins/pdx-agent-hooks.js  # fixed body

# Restart daemon if running on the broken binary (already W2's daemon
# from PR #710 verification — kill + re-launch with the fix binary).
# Then start opencode in tmux purdex-sync, exchange one prompt.

# AC4 evidence:
sqlite3 ~/.config/pdx/agent_events.db \
  "SELECT root_event_name, latest_decision,
          datetime(started_at/1000000000, 'unixepoch', 'localtime')
   FROM agent_trace_chains
   WHERE root_agent_type='opencode'
   ORDER BY started_at DESC LIMIT 5"
# Expect: at least one new SessionStart row with this run's timestamp.
```

If AC3/AC4 fail on mlab, do not merge — file the actual evidence in
PR comments and treat as a Plan failure (return to T1/T2 with
adjusted assertions).

## 5. Risk register (delta from spec §6)

Plan-specific risks the spec doesn't already cover:

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| The rendered tail script that drives `event()` doesn't actually fire `emit('SessionStart', …)` because the JS hook expects different envelope shape than the inline harness provides | Medium | Read the existing handler at `plugin_template.go:48-86` for the exact `case 'session.created'` shape (`event.properties.sessionID`) — the harness already mirrors that. If T1 fails for envelope reasons (cmd.Run non-zero with shape error rather than TypeError) we adjust the harness in T1 itself before declaring the test "red" for the right reason. |
| The stub `pdx` script's `cat > $PDX_TEST_STDIN_CAPTURE` blocks waiting on stdin EOF; the plugin's `proc.stdin.end()` after `write` should close cleanly, but if `end()` doesn't flush we deadlock | Low | The 5s context timeout covers it. If the deadlock is a real Bun behavior, T1 turns red for a different reason than the TypeError and forces design-time correction (then T2's fix is what makes the deadlock impossible — `end()` is the contract). The deadlock-vs-TypeError distinction is informative. |
| `t.TempDir()` plugin.js is parsed by Bun as a module path that conflicts with package-detection (Bun walks up looking for `package.json`) | Low | We pass an absolute path with no surrounding `package.json`; tested manually before in similar setups. If observed, fall back to a `.mjs` extension. |

## 6. Out-of-scope guardrails (re-stated from spec §2.3)

The implementer must not:

- Edit `plugin_template.go` lines outside the `emit()` body inside
  `renderManagedPlugin` (the only allowed adjacent change is adding
  `RenderManagedPluginForTesting` to `hooks_export_test.go` for T3.5).
- Modify `hooks.go`, `events.go`, `provider.go`, `status.go` runtime
  files, or any test file other than the new
  `plugin_template_bun_integration_test.go` and the appended block in
  `hooks_test.go` for T3.5.
- Touch other agents (`internal/agent/cc/*`, `internal/agent/codex/*`).
- "While we're here" tweak the parity regex helpers or refactor the
  stub-binary harness into a shared `testdata/` helper.
- Update `CHANGELOG.md` inline (the bump PR owns that).
- Bump `VERSION` (a separate bump PR follows merge per repo workflow).

If the implementer hits a real bug in those areas during T1-T2, file
a follow-up issue and stop.

## 7. Definition of done

### 7.1 Code & test verification (PR-blocking)

These are the criteria that determine whether the hotfix is correct:

- All commits in §2 pushed to `origin/worktree-opencode-spawn-fix`.
- T1, T2, T3, T3.5 verifications pass locally.
- `go test ./internal/agent/opencode/...` green with Bun installed and
  green with Bun absent (skip path).

### 7.2 Process gates (workflow, tracked separately)

These are repo-workflow steps; they happen for this PR like any other,
but are not technical correctness criteria:

- PR opened against `main` referencing #715 with the §4 manual checklist.
- Two-round codex review (standard + adversarial); 0 unaddressed P0/P1.
- mlab live verify (AC4) recorded as PASS in PR body.
- Squash-merged to `main`.
- Independent bump PR opens (alpha.249) with `VERSION` + `CHANGELOG.md` updates.
- W2 PR #710 rebased / merged main and §7 live re-run.
