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

## 3. Requirements

- **R1 — Same answers.** For every session the inventory reports the same owner, agent, status and reason as today. Keep every existing guard:
  - frame identity by pid **and** process start time;
  - the post-walk membership re-check against `join-pane` moves (`provenance_handler.go:142-175`);
  - the proxy depth limit;
  - "lookup failed" stays distinct from "no owner" (#988);
  - an expired context is "no answer", never the owners found so far.
- **R2 — Bounded work per pass.**
  - One inventory pass (`/api/peers`, local) takes **one** process-table snapshot and **one** tmux pane listing, shared by all sessions' owner lookups. Owner lookups do no per-PID fork.
  - The post-walk membership re-check is **one** batched tmux read for the pass, not one per pane.
  - The single-session `GET /api/sessions/{code}/provenance` uses the same machinery with its own snapshot.
- **R3 — Darwin without per-PID forks.** The implementer picks one:
  - one `ps -A` with a format that parses unambiguously;
  - or sysctl `kern.proc.all` plus `kern.procargs2`, with no fork at all.

  The result must give the same `PID`, `PPID`, `ExePath`, `Argv` and `StartTime` as today's per-PID reader. Linux keeps its current path unless it also forks per PID; check and say which in the plan.
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

## 5. Delivery

- Full Purdex flow: worktree → this spec as the first commit → plan → codex plan review (with this spec, gpt-5.6-sol) → subagent TDD → PR → codex R1 + R2 (attack → critic) → merge → bump.
- **Deploy (mlab daemon restart): report to the coordinator first.** Other lines (daemon restart button, conversation entity) also need restarts, and the coordinator batches them with the user.

## 6. Not in scope

- Changing what the SPA shows for a partial inventory.
- The remote `scope=all` fan-out.
- Caching across passes.
