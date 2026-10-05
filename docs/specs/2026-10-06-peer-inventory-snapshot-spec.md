# Peer inventory — one process snapshot per pass — spec

Owner (coordinator): `mlab/purdex-9b` (`mlab/_0le0d2`). Implementer: the session this was delegated to. Spec questions go to the coordinator; do not ask the user.

## 1. Problem (user report 2026-10-06)

The status bar's peer segment sometimes shows `tmux:<name>` with agent "—" although Claude Code is running in that pane. The screenshot is `purdex3`. `pdx peers` shows the same session twice:
- a `tmux:purdex3` row with no agent;
- a registry "entry" row `mlab/purdex-dc [isw2lj] … purdex3?`.

It also prints `(partial: N sessions not resolved within budget)`.

## 2. Root cause (measured on mlab, alpha.486, 21 tmux sessions)

- **The data is right.** For `purdex3` (pane `%31`):
  - the pane shell is the parent of CC pid 84592;
  - `~/.claude/sessions/84592.json` names it `purdex-dc` / `b0496bce`;
  - the socket is present;
  - `agent_frames` has pid 84592 on `%31` with the same session id.
- **The whole local inventory runs under one 2 s budget** (`internal/module/peers/module.go:475-483`). That budget covers the tmux-instance probes, the session list, and every owner lookup **in sequence**. Sessions reached after the deadline are left unresolved: agent null, reason "". Their registry entries then render as separate `entry` rows with `?` (`cmd/pdx/peers.go:690-708`). The SPA reads the same `GET /api/peers` (`spa/src/stores/usePeerStore.ts`).
- **One owner lookup costs 83–157 ms.** Measured through `GET /api/sessions/{code}/provenance`, one session at a time:
  - about 125 ms with CC;
  - about 85 ms without an agent;
  - so 21 sessions take about 2.6 s, more than the budget.

  The three runs of `pdx peers` each took the full budget, about 2015 ms, and left 4, 12 and 11 sessions unresolved. Sessions sort by name, so the tail (`purdex-re2`, `purdex3`, `twkc`) is hit almost every time.
- **Why a lookup is slow:**
  - `readProcessInfoPlatform` on darwin forks `ps` four times per PID: `comm`, `args`, `lstart`, `ppid` (`internal/agent/process_info.go`, `process_info_darwin.go:14,22`).
  - `resolvePaneOwners` walks a frame's proxy chain up to `proxyMaxDepth = 5` (`internal/module/agent/frame_ops.go:20`).
  - Each session also costs its own tmux round trips: `panesOfSession`, plus `paneStillInSession` per pane (`provenance_handler.go:213,260`).
  - `newMemoProcReader` memoises within one session's lookup only (`provenance_handler.go:131`).
  - A single fork costs 2–4 ms idle, and more under load.

> **統籌核准的推導（2026-10-06）— two costs measured during planning:**
> - **(a) `panesOfSession` is the largest cost.** It runs once per session. Each run asks tmux (`display-message`, 4.2 ms measured) for the session of **every pane that has a frame**, not only the session's own panes (`provenance_handler.go:269-298`). That is sessions × framed panes round trips: about 21 × 18 × 4.2 ms ≈ 1.6 s.
> - **(b) The registry read forks too.** `/api/peers` reads the CC registry under the same budget (`module.go:483` sets the deadline, `:505` reads). `ipeers.DefaultLiveness().Info` is `agent.ReadProcessInfo`, so each live entry costs four `ps` forks (2.1 ms each, measured): 18 entries ≈ 150 ms idle.

## 3. Requirements

- **R1 — Same answers.** For every session the inventory reports the same owner, agent, status and reason as today. Keep every existing guard:
  - frame identity by pid **and** process start time;
  - the post-walk membership re-check against `join-pane` moves (`provenance_handler.go:142-175`);
  - the proxy depth limit;
  - "lookup failed" stays distinct from "no owner" (#988);
  - an expired context is "no answer", never the owners found so far.

  > **統籌核准的推導（2026-10-06）**
  > - **D5 — a failed pane listing is a failed lookup.**
  >   - A pane that is merely absent from a successful listing is dropped, as a failed per-pane re-read is today.
  >   - A listing that fails as a whole means nothing could be checked. Every session it would have answered reports an error, which the inventory shows as unresolved / `partial`, never as "no owner" (#988).
  > - **D6 — what the batched re-check changes.**
  >   - A session whose panes produced no owners is final when it is walked. It needs no re-check, which matches today: such a session never reached `paneStillInSession`.
  >   - Sessions with candidates are confirmed together by the second listing, and the deadline is read again after that listing. If it has expired, every candidate session is "no answer".
  >   - This differs from today only when tmux hangs. Then the first listing has already spent the budget.
- **R2 — Bounded work per pass.**
  - One inventory pass (`/api/peers`, local) takes **one** process-table snapshot and **one** tmux pane listing, shared by all sessions' owner lookups. Owner lookups do no per-PID fork.
  - The post-walk membership re-check is **one** batched tmux read for the pass, not one per pane.
  - The single-session `GET /api/sessions/{code}/provenance` uses the same machinery with its own snapshot.

  > **統籌核准的推導（2026-10-06）— D4: the registry read in `/api/peers` uses the pass's snapshot too.**
  > - Its `Liveness.PidAlive` and `Liveness.Info` are answered by the same snapshot the owner lookups use, so the pass is one process-table read, cost (b) in §2 included. `Stat` stays `os.Stat`.
  > - Without this, R5's 300 ms is not safe under load.
  > - The send path's registry read (`module.go:343`) is unchanged.
- **R3 — Darwin without per-PID forks.** The implementer picks one:
  - one `ps -A` with a format that parses unambiguously;
  - or sysctl `kern.proc.all` plus `kern.procargs2`, with no fork at all.

  The result must give the same `PID`, `PPID`, `ExePath`, `Argv` and `StartTime` as today's per-PID reader. Linux keeps its current path unless it also forks per PID; check and say which in the plan.

  > **統籌核准的推導（2026-10-06）**
  >
  > **D1 — darwin uses sysctl.**
  > - `kern.proc.all` gives `PID`, `PPID` and the start time. `kern.procargs2` gives argv.
  > - Measured against `ps` on live processes, PPID, `lstart` and argv0/argv agree byte for byte.
  > - **The start time must stay comparable with what frames store**: `process_start_time` is the `ps -o lstart=` text, and the registry compares `ProcessInfo.StartTime` to the second.
  >   - The snapshot's start-time text is `time.Unix(p_starttime.tv_sec, 0)` formatted in `psLstartLayout`, in the local time zone.
  >   - `ProcessInfo.StartTime` is that same instant: truncated to the second, location `time.Local`. That is exactly what parsing `ps` `lstart` gives today.
  >   - The parity test asserts the text, the instant, and the location.
  > - `ps` prints `strftime("%c")` in the daemon's locale. Measured:
  >   - `en_US` gives the layout;
  >   - `zh_TW` gives `二 10月/ 6 05:11:40 2026`;
  >   - the live daemon runs `LANG=LC_ALL=en_US.UTF-8`.
  >
  >   Formatting in Go adds no assumption. Today's reader already fails in any locale whose `%c` differs from `psLstartLayout`: `readProcessStartTime` parses with that layout, so every owner walk comes back empty.
  > - `ps` passes printable ASCII through verbatim. Every other byte goes through an escaping that is not one rule (measured: TAB → `\011`, DEL → `^?`, `0x80` → `M^@`, some non-printing Unicode rewritten, CJK and emoji verbatim). So the snapshot does **not** re-implement it.
  >   - When every argv byte is printable ASCII, the snapshot builds `comm` (argv[0]) and `args` directly.
  >   - Otherwise it reads `ExePath` / `Argv` the way D2's fallback does, through today's `ps`.
  >   - The parity test covers each measured case.
  > - Why not one `ps -A`: `comm` and `args` both contain spaces (`/System/Library/CoreServices/Software Update.app/…`), so no single line splits unambiguously.
  >
  > **D2 — processes whose argument area cannot be read.** `kern.procargs2` fails for other users' processes and for zombies.
  > - For those processes `ps` falls back to its own sources: `proc_pidpath`, and `<defunct>` for a zombie. `x/sys/unix` does not expose `proc_pidpath`.
  > - The snapshot still gives `PID`, `PPID` and `StartTime` from `kern.proc.all`. For `ExePath` and `Argv` only, it runs today's two `ps -p … -o comm=/args=` reads, so every field matches today.
  >
  > *Refined after the caller audit below. The first draft returned `ExePath ""` / `Argv nil` instead. That would change the registry's verdict for a stale entry whose PID another user's process has reused: `registry.go:356` turns an empty `Argv` into "unclassifiable" (unknown, `partial`) where today it is "dead".*
  >
  > Readers of `ExePath` / `Argv`, and whether the snapshot reaches them:
  >
  > | Reader | Reads | Snapshot? |
  > |---|---|---|
  > | owner walk (`ancestor.go` `walkPaneAncestry`, `pane_owner.go` `resolvePaneOwners`) | `PPID` only | yes |
  > | `/api/peers` registry liveness (`registry.go:351-365`: `len(Argv)==0` → unclassifiable; `IsProxyProcess` reads `ExePath`, `Argv[0]`, `"peer-proxy"`; `StartTime` to the second) | all fields | yes (D4) |
  > | hook verification (`verify.go`), `classifyAncestor`, sweep, the frame_ops proxy walk | `PPID` | no — per-PID reader, unchanged |
  > | agent detection (`probe` `IsAliveFor` / `FirstAliveAgentInTree` → `cc` / `codex` / `opencode` `Identify`, which read `ExePath` and `Argv`) | `ExePath`, `Argv` | no — unchanged |
  > | hook client shim check (`cmd/pdx/hook_pid_resolver.go:41`), send-path registry read (`module.go:343`), codexbroker (own `ps` parser) | — | no — unchanged |
  >
  > - Every process the owner walk or a live registry entry reaches belongs to the daemon's own user, is readable, and has plain-ASCII argv (CC, shells, tmux). In practice the fallback does not run.
  >   - A CC started with a non-ASCII prompt argument costs 2 forks, and its fields are still exact.
  > - The fork-count test pins **zero** forks for readable plain-ASCII processes. The parity test pins the fallback against the per-PID reader for PID 1, for a zombie, and for every measured exotic argv.
  >
  > **D10 — a snapshot is a point-in-time view** (from the codex plan review; 推導，待統籌確認).
  > - `Alive`, the start time and `PPID` describe the process table at the moment of the snapshot. Frames and registry entries are judged as of that moment, as today's reader judged them as of its own read.
  > - `ExePath` / `Argv` are read later, on first use. On darwin the reader then re-reads the PID's start time (`kern.proc.pid`, no fork). If the PID is gone or the start time changed, the read fails ("process changed since the snapshot"), so two processes are never mixed into one answer.
  > - A registry entry whose process started in the milliseconds after the snapshot reads as dead for that one poll.
  >
  > **D3 — Linux forks per PID today** (`readProcessPPID` and `readProcessStartTime` are both `ps -p`). So Linux changes too:
  > - one `ps -A -o pid=,ppid=,lstart=` per snapshot (`lstart` is the last column);
  > - `ExePath` / `Argv` keep coming from `/proc/<pid>/exe` and `/proc/<pid>/cmdline` (file reads), with today's errors.
- **R4 — Budget unchanged.** The 2 s inventory budget and its semantics stay as they are, as the backstop for a hung tmux.
- **R5 — Target.** On mlab with ≥ 20 sessions:
  - an inventory pass completes in **≤ 300 ms**;
  - `pdx peers` shows **no** `partial: … not resolved within budget` line in 10 consecutive runs.

## 4. Tests

- **Parity (real processes).** For every live PID, the snapshot reader returns the same fields as the existing per-PID reader. A real-process test runs on darwin and is skipped elsewhere. Pick processes that exist for the whole test (the test's own pid, its parent, a spawned `sleep`).
- **Unit, with a fake snapshot and fake tmux listing:**
  - the owner verdicts for the existing fixtures are unchanged (all existing `pane_owner` / `provenance_handler` / peers module tests stay green without edits to their expectations);
  - a pane moved between the listing and the re-check is dropped, as today;
  - an expired deadline gives "no answer".
- **Fork count.** Inject the command runner and assert one `ps` (or zero, with sysctl) and two tmux listings per inventory pass, whatever the number of sessions.
- **Mutation (deliverable):**
  - drop the re-check → the moved-pane test turns red;
  - per-session snapshots instead of one per pass → the fork-count test turns red.
- **Acceptance on mlab after deploy:**
  - 10× `pdx peers`: no partial line, and each run's wall time is recorded;
  - the status bar of `purdex3` (or any late-alphabet session) shows its peer address.

> **統籌核准的推導（2026-10-06）— D8: what changes in existing tests (setup only).**
> - Fixture helpers (`withProcessTree`, `withLivePids`, …) also install the pass's snapshot seam, as a memoised view over the fixture's process functions, one per pass.
> - Executor wrappers that instrumented `PaneSessionID` instrument the pane listing instead: call 1 enumerates, call 2 re-checks.
> - No assertion changes.
> - One test changes level: `TestResolvePaneOwners_PanePIDUnresolvable_EmptyResultNoError` tests a per-pane `resolvePanePIDFn` call that no longer exists. Its expectations (no owners, no error, no reads) move unchanged to the pass, where an unparseable or missing pane PID is now handled.

## 5. Delivery

- Full Purdex flow: worktree → this spec as the first commit → plan → codex plan review (with this spec, gpt-5.6-sol) → subagent TDD → PR → codex R1 + R2 (attack → critic) → merge → bump.
- **Deploy (mlab daemon restart): report to the coordinator first.** Other lines (daemon restart button, conversation entity) also need restarts, and the coordinator batches them with the user.

> **統籌核准的推導（2026-10-06）— D7: two PRs, one bump.**
> - **PR A — primitives, no behaviour change:** the process snapshot (darwin and Linux) and the tmux `ListAllPanes`.
> - **PR B — the owner pass and `/api/peers`**, with the end-to-end and mutation tests. Stacked on A.
> - Each PR gets codex R1 + R2. One version bump after B merges.

## 6. Not in scope

- Changing what the SPA shows for a partial inventory.
- The remote `scope=all` fan-out.
- Caching across passes.
