# P-A acceptance record (spec §6, Task 12)

Date: 2026-09-15 · Branch `worktree-pa-nex-module` · Nexen pin
`lab.protype.tw/wake/nexen v0.0.0-20260914203205-1e5d09014b65` (v0.9.0, main `1e5d090`).

All live steps ran against an **isolated daemon** (`pdx serve --config
<scratch>/config.toml`, port 7899, own `data_dir`, `[nex] enabled = true`,
`repo_roots = [<scratch>/repo]`, `[nex.sandbox] max_profile = "handoff"`).
The production mlab daemon (:7860) was not touched.

## Automated

- `GOWORK=off go build ./... && go vet ./... && go test ./...` — green (34 packages).
- `go.mod` pin unchanged after T7a's `go mod tidy` (module is now a direct require).

## Step 0 — build chain

- Cold cache, launchd-like environment (`env -i HOME PATH=/usr/bin:/bin:/usr/sbin:/sbin:<go dir> GOMODCACHE=<empty tmp> GOCACHE=<tmp> GOWORK=off go build ./cmd/pdx`):
  downloaded `lab.protype.tw/wake/nexen@v0.0.0-20260914203205-1e5d09014b65` through the
  machine-level `go env -w GOPRIVATE` + git `insteadOf` alone (no shell env) — binary 20.9 MB. ✅
- `GOARCH=amd64` and `GOARCH=arm64` builds ✅.
- `pnpm run electron:build` does not build Go (checked `scripts/build-electron.mjs`); the daemon's
  own dev-update `go build` inherits the same `go env` file the cold build proved sufficient. Not re-run.

## Step 1 — enable + start log

`pdx.log`: `nex: PATH policy applied (…)` then `nex: serving /api/nex (host_id=mlab-pa-test, data_dir=…/data/nex, claude_bin=claude (via PATH), profiles=max=handoff,default=trusted, path=…)`.
`GET /api/info` → `"nex":{"configured":true,"mounted":true}`. `GET /api/nex/v1/capabilities` (bearer) →
`sandbox_profiles: [readonly standard trusted handoff]`, `lease.renew.path = /api/nex/v1/executions/{id}/attach/renew`.
Without bearer → 401. `pdx nex --config … host` → `active_account: wake@protype.tw`, quota from cswap. ✅

## Step 2 — delegate → watch → result

`pdx nex delegate --cwd ./repo --brief "Reply with exactly: PA-STEP2-OK…"` → `06GA3ZH92SV6334WHP368909SM running`.
`show` → `idle`, `principal_id: pdx:mlab-pa-test`, `last_turn_reason: final_response`; events contain `PA-STEP2-OK`. ✅
(`pdx nex watch` works; the shell used here lacks `timeout`, so `show`/`events` were used for the record.)

## Step 3 — restart semantics

Long turn = brief asking claude to run `python3 -c "import time; time.sleep(90)"` (a bare `sleep` is
blocked by the Claude Code harness — noted for future acceptance scripts).

- **3a graceful (`SIGTERM`)**: daemon log `received terminated, shutting down…`; python child gone;
  after restart `show` → `idle`, `last_turn_reason: interrupted`; events `execution.interrupt_requested`/
  `execution.interrupted` with `source: daemon_shutdown`. `attach --control` → `send "…secret word?"` →
  `delivery: delivered` → new turn `final_response`, answer `GUAVA-31` (context from before the restart preserved via `--resume`). ✅
- **3b budget-exceeded**: not forced (would require a claude turn that ignores the control_request past the
  10 s budget). Its end state is the same reconcile path proven in 3c. ⚠️ not separately exercised.
- **3c `kill -9`**: daemon killed with the claude child alive; after restart, log shows the reconcile,
  `show` → `idle`, `last_turn_reason: orphaned`, event `execution.turn_orphaned` with `process_killed: true`. ✅

## Step 4 — commander fan-out from this Claude Code session

Two `pdx nex delegate` in parallel (A: no tools; B: 60 s python sleep) → `ls` lists both (`idle` / `running`);
A's events contain `WORKER-A-DONE`; `attach --control` + `interrupt` on B → `state: idle`,
`last_turn_reason: interrupted`, `source: user`. ✅

## Step 5 — handoff round trip

- **execution → tmux**: `claude --resume c3b25460-…` in `./repo` shows the execution's conversation and
  answers the secret word (`GUAVA-31`) interactively. ✅
- **tmux → execution**: ❌ **not possible with Nexen v0.9.0** — `POST /v1/executions` has no field to seed
  an existing claude session; `execution/launch.go` only ever resumes `store.LastSessionID(execID)`
  (a previous turn of the *same* execution). The spec's step 5 (and the earlier memory note "delegate 已有
  `session_id` 欄位") were wrong: `session_id` exists only on the *summary*. This is a Nexen contract
  addition (`delegate.resume_session_id` or similar, subject to the same-cwd rule) and a **P-C prerequisite**;
  tracked as a follow-up issue. The `handoff` profile itself is selectable (`sandbox_profiles` lists it).

## Step 6 — a26

Not run (needs the user at the machine: App local-daemon update, then steps 1–2 there). ⚠️ pending.

## Step 7 — preflight + SSE resume

`OPTIONS /api/nex/v1/events` with `Access-Control-Request-Headers: last-event-id, authorization` → 204,
`Access-Control-Allow-Headers: Authorization, Content-Type, Last-Event-ID`. `GET …/v1/events?execution_id=…`
with `Last-Event-ID: 1` → first frames `id: 2 / execution.running`, `id: 3 / system`, … ✅

## Observations for follow-up

1. Nexen needs a "delegate resuming an existing claude session" input before P-C's tmux → execution handoff (above).
2. The start log prints the full `PATH` twice (policy line + serving line); with a long user PATH that is ~2 KB per line. Trim to the prepended prefix.
3. `pdx nex` prints Nexen's own delegate usage on a bad flag (`-session-id`), which is correct but the wrapper's `pdx nex:` prefix line repeats the error — cosmetic.
