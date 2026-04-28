# opencode Plugin `Bun.spawn` stdin Hotfix Spec

- **Version**: 1.0.0-alpha.248 (target bump after merge)
- **Date**: 2026-04-29
- **Base**: `96bae3ce` (main @ alpha.248)
- **Author**: claude-code + wake
- **Status**: Draft (revised after codex spec review job `task-moiu936r-x7a8nh`; 5 P2 + 2 P3 findings, 0 P0/P1, all addressed)
- **Tracking**: #715 (bug, daemon)
- **Worktree**: `.claude/worktrees/opencode-spawn-fix`
- **Branch**: `worktree-opencode-spawn-fix`

## 1. Context

The opencode hook plugin (`internal/agent/opencode/plugin_template.go`)
renders a JS template that defines a single `emit(eventName, payload)`
helper. Every Bus / strong-hook callback funnels through it to spawn
`pdx hook --agent opencode <eventName>` and pipe the JSON payload via
stdin.

The current rendering (line 34) passes a **bare JSON string** as the
`stdin` field of `Bun.spawn`:

```js
const proc = Bun.spawn({
  cmd: [pdxPath, 'hook', '--agent', 'opencode', eventName],
  stdin: JSON.stringify(payload),
  stdout: 'ignore',
  stderr: 'ignore',
})
await proc.exited
```

`Bun.spawn` rejects raw strings — per the Bun runtime docs
(https://bun.sh/docs/runtime/child-process) the `stdin` field accepts:

```
'pipe' | 'inherit' | 'ignore'
| BunFile | ArrayBufferView | Blob
| ReadableStream | Response | Request
| number  // file descriptor
```

A bare `string` is not in that union, so the first call throws
synchronously inside the plugin:

```
TypeError: stdio must be an array of
code: ERR_INVALID_ARG_TYPE
```

### 1.1 Discovery & impact

- Pre-existing since commit `ffdd4e14` (2026-04-21, initial opencode
  integration). 7 days in production.
- Found during W2 PR #710 §7 live verification on mlab — opencode
  session inside tmux `purdex-sync` raised the TypeError on every event.
- `agent_events.db` confirms zero `agent_type='opencode'` rows have
  ever reached the daemon since `ffdd4e14`. No opencode trace chain
  has ever closed.
- Existing tests in `plugin_template_test.go` only diff the rendered
  template **as text** (`extractEmittedEvents`, `validateSpecsCoverEmitted`,
  parity checks). They never invoke `bun` — so CI silently passed for
  a week.

### 1.2 Why hotfix-PR (not folded into W2)

W2 PR #710 (`lights-w2-naming`) ships catalog-naming separation and
does not touch `plugin_template.go`. Folding the fix in:

1. Expands W2 scope mid-review and risks codex flagging scope drift.
2. Couples merge timing — W2 cannot ship while §7 live cannot
   complete; conversely the spawn fix should be reviewable on its own
   for a 7-day-old runtime bug.

The hotfix is small (1-line template change + 1 integration test) and
ships from `main` (`96bae3ce`, alpha.248). After merge, W2 rebases and
re-runs §7 live.

## 2. Requirements

### 2.1 Functional

| ID  | Requirement |
|-----|-------------|
| F1  | The rendered managed plugin must call `Bun.spawn` with a stdin shape Bun 1.3.x accepts, such that `emit('SessionStart', {…})` against a real `bun` runtime exits with status 0 and the stub `pdx` binary observes the JSON payload on stdin. |
| F2  | The fix must preserve the existing `emit()` JS surface — the function's name, signature `(eventName, payload = {})`, and `await proc.exited` semantics must remain so the rest of the rendered handlers (`session.created`, `tool.execute.before`, …) call sites do not change. |
| F3  | The managed marker (`pdx-managed:opencode-hooks:v1`) must remain unchanged so existing installed plugins still match `CheckHooks` detection logic. |
| F4  | The byte-exact template-equality assumption used by `CheckHooks` (in `hooks.go`) must continue to hold: the rendered body for a given `pdxPath` is deterministic and stable. |

### 2.2 Non-functional

| ID  | Requirement |
|-----|-------------|
| N1  | Existing `plugin_template_test.go` cases must keep passing without modification of their assertions (no test deletion to make this green). |
| N2  | The new integration test must skip cleanly when `bun` is not on `PATH` (`exec.LookPath` gate) so CI environments without Bun do not fail. |
| N3  | The integration test must run in <5s on a developer machine (Bun cold start ~200ms; we exec it once). |
| N4  | The fix must not introduce any new runtime dependency in `pdx` itself — Bun is already required at runtime by opencode, the fix only changes how we hand stdin to it. |

### 2.3 Out of scope (explicit)

- Refactoring `plugin_template.go`'s template structure, regex helpers,
  or `validateSpecsCoverEmitted`.
- Changes to `hooks.go` (`InstallHooks` / `CheckHooks`).
- Catalog naming or event-spec edits (these are W2 / Phase 3 concerns).
- Other agent plugins (cc, codex) — no shared code path.
- Pdx-prefix migration of opencode plugin file (Phase 3 work).

## 3. Fix decision

Two candidates from issue #715:

| Option | Code | Pros | Cons |
|--------|------|------|------|
| A — `'pipe'` + write/end | `stdin: 'pipe'`, then `proc.stdin.write(payload); proc.stdin.end(); await proc.exited;` | Most explicit lifecycle; matches Bun docs; works on every Bun release that has stdin streams (since 0.5.x); easy to reason about for buffer / large payload edge cases. | One extra logical step; two more lines in the rendered body. |
| B — `TextEncoder` to TypedArray | `stdin: new TextEncoder().encode(JSON.stringify(payload))` | Single field, smaller diff. | Relies on `Bun.spawn` accepting `Uint8Array` — supported in current Bun but a less-traveled path; behavior with very large payloads less documented. |

**Decision: Option A.**

Rationale:
- The payloads we send are small JSON objects (typically <1 KB session
  metadata), so neither option has a real performance edge.
- Option A's `write/end` lifecycle is explicit; future contributors
  reading the rendered template will recognize the pattern from any
  Node/Bun child-process tutorial. Option B's TypedArray form is less
  common as a stdin payload and would benefit from a comment to
  explain — extra cognitive cost relative to the fix's tiny scope.
- Option A matches what the issue's "suggested fix" leads with — the
  spec-review feedback can override, but absent that we follow the
  documented preference.
- On the `await` question: per Bun docs `proc.stdin.write(...)` on a
  `pipe` is a synchronous write to a `FileSink` (returns the number of
  bytes written), so we deliberately do not `await` it. `end()` is
  also synchronous. The only async point is `await proc.exited`. If a
  future Bun version makes `write` return a Promise, the rendered
  body still emits at-least-once because every event flows through
  `await proc.exited` regardless. We keep the no-await form so the
  diff is minimal and readers do not need to chase a Promise that
  current Bun does not produce.

The rendered body becomes:

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

## 4. Test strategy

### 4.1 Existing tests (no changes)

All eight existing tests in `plugin_template_test.go` remain untouched:

- `TestValidateSpecsCoverEmitted_Equal` / `_EmitNotInSpec` /
  `_SpecNotInEmit` / `_IgnoresNonInstallableSpecs` — exercise regex
  parity helpers. They scan emit() *names*, not stdin shape; the fix
  doesn't change names.
- `TestOpenCodeCheckHooks_ExcludesNonInstallableSpecs` — installs
  managed plugin and re-checks. Driven by managed marker presence.
- `TestRenderManagedPlugin_ProducesValidBody` — checks managed marker.
- `TestTemplateSpecsParity` — same parity check.
- `TestExtractEmittedEvents` / `TestExtractPdxPath_RoundtripEscapedLiterals` /
  `TestRenderManagedPlugin_UsesInputModelAndSessionScopedSubagentKeys` —
  string-shape assertions on the rendered body.

`TestRenderManagedPlugin_UsesInputModelAndSessionScopedSubagentKeys`
asserts certain literal substrings (e.g. `const subagentKey = …`); none
of those overlap with the `stdin:` line that we change, so this test
keeps its meaning.

### 4.2 New integration test — `TestRenderManagedPlugin_BunRuntimeEmitsStdin`

A real-runtime test that proves the rendered body's `emit()` works in
Bun. Lives in a separate file (`plugin_template_bun_integration_test.go`)
to keep the fast unit suite untouched and so a future build tag could
isolate it if needed.

#### 4.2.1 Skip gate

Three layered gates — each gate skips (not fails) when the
precondition is not met, so CI environments without Bun and Windows
runners both pass cleanly:

```go
if runtime.GOOS == "windows" {
    t.Skip("opencode plugin runtime is POSIX-only; skipping real-runtime integration test")
}
bunPath, err := exec.LookPath("bun")
if err != nil {
    t.Skip("bun not on PATH; skipping real-runtime integration test")
}
// Probe bun executability so a stale/broken binary doesn't poison the suite.
out, err := exec.Command(bunPath, "--version").Output()
if err != nil || strings.TrimSpace(string(out)) == "" {
    t.Skipf("bun --version failed (%v); skipping real-runtime integration test", err)
}
```

The `LookPath` pattern matches existing usage in
`internal/module/agent/handler.go:973`, `internal/agent/process_info.go:66`.
The version probe protects against a `bun` shim that is on PATH but
not a working executable (e.g. a stale wrapper script in CI).

#### 4.2.2 Stub `pdx` binary

Write a tiny shell script into `t.TempDir()` that captures stdin to a
known path and exits 0:

```sh
#!/bin/sh
cat > "$STDIN_CAPTURE_PATH"
```

Marked executable with `os.Chmod(0o755)`. The `runtime.GOOS == "windows"`
gate above keeps this off Windows runners — opencode itself targets
POSIX and the kickoff context is Mini-Lab macOS.

#### 4.2.3 Render + execute

1. `body := renderManagedPlugin(stubPath)` against the stub.
2. Write `body` plus a small tail (`await (await PurdexOpenCodeHooks()).event({event:{type:'session.created', properties:{sessionID:'test-session'}}})`) into a `.js` file inside `t.TempDir()`.
3. `exec.CommandContext(ctx, bunPath, scriptPath)` with a 5s timeout.
   We invoke `bun <script>` directly rather than `bun run <script>` —
   the latter goes through Bun's package-script resolver and adds
   non-trivial CLI overhead; direct execution is faster and exercises
   the same `Bun.spawn` runtime path that opencode uses in production.
4. Assert `cmd.Run()` returns nil (exit 0).
5. Read `STDIN_CAPTURE_PATH` and assert it equals
   `{"session_id":"test-session"}` (or contains `session_id` —
   payload key order is JSON-stable, but keep the assertion tolerant
   by using `json.Unmarshal` round-trip).

#### 4.2.4 Negative-test path (mark optional)

Adding a "before-fix" failing test would require either pinning the
old broken template or skipping conditionally — both add maintenance
cost for limited value. The asserted positive path on real Bun is the
authoritative regression catch: if anyone reverts to a stdin shape
Bun rejects, this test fails immediately.

### 4.3 Manual mlab live verify (PR test plan)

Per kickoff §"在 mlab 端 live verify":

```bash
go build -o /tmp/pdx ./cmd/pdx
/tmp/pdx setup --agent opencode
cat ~/.config/opencode/plugins/pdx-agent-hooks.js | head -20
# Expect: stdin: 'pipe' / proc.stdin.write / .end() — no JSON.stringify in stdin field

# Restart daemon if needed; start opencode in tmux purdex-sync
# Observe agent_events.db has new opencode rows after a session start
sqlite3 ~/.config/pdx/agent_events.db "SELECT root_event_name, latest_decision, datetime(started_at/1000000000, 'unixepoch', 'localtime') FROM agent_trace_chains WHERE root_agent_type='opencode' ORDER BY started_at DESC LIMIT 5"
```

## 5. Phase split

**Single phase** — the change is too small to warrant subdivision.

| Step | What | Verify |
|------|------|--------|
| 1 | Add failing integration test (TDD red) | `go test ./internal/agent/opencode/ -run BunRuntimeEmitsStdin` fails (Bun TypeError) |
| 2 | Apply 1-line stdin fix in `plugin_template.go` | Same test passes; entire `go test ./internal/agent/opencode/` suite green |
| 3 | Run mlab live verify (manual, in PR test plan) | New rows appear in `agent_trace_chains` for `agent_type='opencode'` |

## 6. Risks

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| `bun` version on developer machine differs from version bundled with opencode → behavior diverges | Low | Bun's `Bun.spawn({stdin:'pipe'})` API has been stable since 0.5.x. Any version that opencode ships with supports it. |
| The integration test masks payload-encoding issues (e.g. UTF-8 surrogate edge cases) by only testing ASCII | Low | Out of scope — the fix is structural (stdin field shape). Future test additions can deepen coverage; a dedicated UTF-8 case is not load-bearing for catching the runtime regression. |
| `proc.stdin.write(...)` returns a Promise in some Bun versions and we ignore it | Very Low | Per Bun docs, `write` on a `FileSink` returns synchronously (number of bytes). `end()` is also sync. `await proc.exited` is the only async point. Even if a future Bun made `write` async, the fix would still emit at-least-once because we await `proc.exited`. |
| Rendered byte change breaks `CheckHooks` for users with managed plugins from before the fix | Expected & desired | A managed plugin from the broken era is byte-different from the fixed render. `hooks.go` `CheckHooks` uses `bytes.Equal` of the on-disk plugin against `renderManagedPlugin(canonicalPath)`, so a pre-fix managed plugin reports `Installed=false` (drift). The remediation is the same as any other drift: re-running `pdx setup --agent opencode` (or the SPA's "reinstall hooks" affordance) writes the fixed body. This is documented behavior, not a regression. The mlab live-verify step explicitly observes a fresh `setup --agent opencode` run before opening a session — so it covers this path implicitly. |

## 7. Acceptance criteria

The hotfix is verified when **all** of the following hold:

1. ☐ `go test ./internal/agent/opencode/...` is green on a machine with `bun` installed — the new integration test runs, exits 0, and the captured stdin file round-trips to the expected payload.
2. ☐ `go test ./internal/agent/opencode/...` is green on a machine without `bun` (and on Windows) — the integration test reports `--- SKIP` with a recognizable reason; all other opencode tests pass unchanged.
3. ☐ A pre-fix managed plugin (rendered from the broken template body) when present in `~/.config/opencode/plugins/pdx-agent-hooks.js` causes `CheckHooks().Events` to report drift (`Installed=false` for at least one event), and a subsequent `pdx setup --agent opencode` rewrites it to the fixed body — exercised in mlab live-verify.
4. ☐ Manual mlab live verify produces at least one new row with `root_agent_type='opencode'` in `agent_trace_chains` after the daemon picks up the fixed binary and a real opencode session emits a `SessionStart`.

### 7.1 Process gates (tracked separately, not part of verification)

These belong to the workflow, not the hotfix's correctness:

- Codex spec review (this doc): 0 unaddressed P0/P1 findings — done in this revision.
- Codex two-round PR review: 0 unaddressed P0/P1 findings.
- PR squash-merged to `main`; `lights-w2-naming` rebases / merges main and re-runs §7 live.
- `go vet ./...` and any repo-wide CI clean **on this PR's diff**. Pre-existing vet noise outside the changed package is not a blocker for this hotfix and should be tracked separately.

## 8. References

- Issue: https://github.com/wake/purdex/issues/715
- W2 PR (downstream beneficiary): https://github.com/wake/purdex/pull/710
- Bug introduced: commit `ffdd4e14` (2026-04-21, initial opencode integration)
- Bun docs (Bun.spawn stdin): https://bun.sh/docs/api/spawn
