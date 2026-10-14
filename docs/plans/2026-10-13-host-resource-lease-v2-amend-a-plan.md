# Host resource lease v2 — amendment A: no session ever waits on a lease (plan)

Spec: `~/.local/share/purdex-lead/lease-v2-spec-amend-queue.md` ("A §n" below), amending
`docs/specs/2026-10-13-host-resource-lease-v2-spec.md` ("v2 §n"). Issue #2759. Coordinator: purdex-1f.
Base: `origin/main` @ `7c2d79653` (alpha.731; P2a = #2791, #2792 merged). `git diff --name-only 094b11860 7c2d79653` lists only `docs/` and `spa/` files, so every Go, CLI and mod citation of revision 1 still holds; each one this revision touches was re-read on `7c2d79653`. Paused input: `origin/feat/lease-v2-p2b` (3 commits on `1208a11d5`). file:line cite `origin/main` unless a branch is named.
The inventory (`lease-v2-inventory.md`, main @ 10116256b) was re-checked for every claim used here; where it is stale, this plan says so.

## Revision 2 (2026-10-14) — what changed

Answers the codex plan review (`lease-v2-amend-a-codex-plan-review.md`, findings 1–23) and applies the coordinator's decisions.

- **Coordinator decisions written in** (marked "coordinator"): Q6 → a running position's second command starts whenever the table grants it, queue or not (§2.5 step 4, #6). Q3 → ask rows of a position released by a limit are re-queued at the back as a new position of the same session (§2.4 P7, T10, #7). Q7 → the cancelled notice only when something of the session is pending (§2.7, T32). Q2 → `PDX_LEASE_CALLER` only in interactive sessions and their subagents (T22–T25, #20). Rollback / drain rule and roll-forward gate (§2.9, §5, #4). Cross-table transitions are single SQLite transactions, with a boot reconcile (§2.4a, T3b, #3). #13 → the smaller option: no persisted alert record before P3a (T34). Q18 → "offer window from delivery" in its own flippable subsection (§2.8), decided by the user on 2026-10-14.
- **State machine:** a fresh row of either mode at an `offered` / `idle` position runs at once, and any row left waiting there is named and noticed (R7, #1). The rule-3 notice needs a release that says it can show it (`notice=1`), so the mod's throw path never warns (§2.6, #2). The offer path does not restart the gap (§2.5 step 2, #5). Second commands count toward V19 when someone waits (§2.5 step 4). New transitions P15 (offer passes undelivered) and P16 (drain).
- **Notices:** a persisted one-slot outbox per position, every promptq outcome handled, restart replay including cancel notices (§2.10, T32, #8, #9).
- **Report / costs:** offered windows are counted as well as idle ones (`offer_wait_ms`, #10). Paths `offer` / `continued` are reported; the free-host grant stays `immediate` (#19).
- **Tests:** V16 boundaries on both sides of 3:00 with a delivery delay and a window reset after a command (#11); own-session guard on all four routes (#17); mutation targets that really turn red (#18); V21 tests tagged (M) (#22); legacy held rows (#14); crash boundaries (#3); retention (#16).
- **P3a:** A5 reads the position queue head (§8, #12).
- **Deploy:** binary-then-setup order for P2c-2 (#15); post-migration checks (#14); both-host acceptance, post-switch verification and a rollback condition (#21).
- **PRs:** re-split to stay ≤ 800 lines / ≤ 20 files: 9 code PRs for P2b′ (plus a docs-only P2b′-0 for the spec and this plan, which alone exceeds 800 lines), 3 for P2c, 3 for P2d (§3).
- **Baseline** moved to `7c2d79653` (#23).

---

## 0. The five facts (measure, don't infer — A §4, v2 §3)

**F1 — How a prompt reaches a session between turns, and mid-turn.**
- The relay seed is a plugin prompt: `submit()` calls `$.prompt.submit({ text })` with no `asUser` (`cmd/pdx/plugin/purdex/hooks/register.js:1226-1229`); the seed goes out from a timer after `/clear` (`register.js:1529-1533`). Every turn-starting call runs from a `$.clock.after` timer, never inside a hook (`register.js:28-38`).
- **The daemon can already make a session's mod submit a prompt.** `internal/promptq` is the daemon side ("sending a prompt … to a Claude Code session through its Purdex mod", `internal/promptq/queue.go:1-13`, `Submit` :349). The modevents module registers it under `promptq.Key` = `"prompt.queue"` (`queue.go:26`; `internal/module/modevents/module.go:70-71`). The owner of a session is the newest live mod stream that announced a capability (`internal/module/modevents/prompt.go:16-18`, `NewestCapableStream`).
- The mod side: one standing long poll of `/mod/v1/prompt/next` per session (`events.js:446-470`). `pqRun` runs a job only for the session id it was made for (`events.js:478-490`). `pqSubmit` answers `busy` while a turn runs and submits only when idle (`events.js:545-550`). The loop exists only in interactive sessions whose `pdx.json` names the mod socket (`events.js:698-706,725,1060`). An old mod drops a job kind it does not know (`parsePromptJob`, `workbook.js:160-175`), and that job then times out as `unknown`.
- **promptq outcomes** (`queue.go:44-52`): `accepted`, `dropped`, `busy` (never ran; may be sent again, `:371-372`), `timeout` (never handed out; withdrawn, may be sent again, `:473-480`), `unknown` (handed out, no result: may or may not have run, `:467-471`; the ledger keeps it 10 min, so the same key is never re-sent), `no_mod` (`:382-385`), and the error `ErrBusy` when 16 jobs already wait for the session (`:386-389`).
- **Mid-turn:** measured on CC 2.1.296. `$.prompt.submit` called while a turn runs "neither refuses nor queues in Claude Code: the call blocks until the session is idle" (`docs/plans/2026-10-10-interface-u3-plan.md:27`). When idle it starts a turn at once, and without `asUser` the model reads it as "The <name> plugin sent a message: …" (`:26`). So a lease notice lands when the turn ends, provided the mod answers `busy` and the daemon asks again. That needs a new job kind (`notice`, without `asUser`) and its own capability. P2d re-measures the new kind first (M-D1).
- **Whether a session's main turn is running** is already known to the daemon: the agent module keeps a `lights.StreamState` per mod stream (`internal/module/agent/light_status.go:32-43` reads `m.modStreams[m.modBySID[sid]]`), whose `TurnID` is the main turn in progress (`internal/lights/state.go:56,210-235`). §2.8 uses it.

**F2 — Adding text to a tool result after `next(e)`, and the release answer.**
- Yes. `leaseCall` returns `{ ...r, context: [...(r.context ?? []), ...notes] }` after `await next(...)` (`lease.js:500-502`). v1 M-R2 measured that this `context` reaches the model after the tool result (`docs/specs/2026-10-08-host-resource-lease-spec.md:53`).
- Today the release runs in `finally` (`lease.js:503-505`), after the return value is built, so its answer cannot reach the result. P2c-2 moves the release before the return on the normal path; `finally` stays for the throw path (`lease.js:492-505`), which has no result to carry a notice. It uses `pdx lease release --client-id <id> --json`, which already prints the raw DELETE answer (`cmd/pdx/lease_acquire.go:333,363-365`).
- The daemon puts the final notice into `LeaseResponse.notice` of that DELETE answer (P2b′-3a), **only when the DELETE says it can show it** (`notice=1`, §2.6). `pdx lease run` reads the same field from its own release by id; today it throws the answer away (`leaseReleaseByID`, `lease_acquire.go:380-386`, deferred at `lease_run.go:113-116`).

**F3 — `run_in_background`, rewriting, exporting an env var on every Bash call.**
- The mod sees the flag: `if (e.run_in_background === true) return next(e)` (`lease.js:472`). v1 measured that the flag is absent on a foreground call and `true` on a background one (`docs/specs/2026-10-08-host-resource-lease-spec.md:145`).
- The hook is registered for every Bash call (`lease.js:508-510`), and `next({ ...e, command })` replaces the command (`lease.js:486,500`). M-R2 measured that the rewritten command is what runs, while the model's own transcript keeps the original (spec v1 `:33,:50-51`). The model notices a rewrite that is not announced (`:54`).
- **Superseded by the M-P2 (a) result and the lead's decision on Q1 (below): the mod does not prefix.** So "export `PDX_LEASE_CALLER`" meant prefixing the command (the tool input has no env field). **Not settled by code (now measured):** `tool.call` wraps the permission check. `tool.call` starts first and `tool.check` / the dialog run inside `next(e)` (`docs/plans/2026-10-08-interface-u1-2-plan.md:170`; `docs/specs/2026-10-06-lead-team-relay-spec.md:265`). So the permission matching and the dialog probably see the **prefixed** command, which can turn an allow-listed or read-only command into a prompt. **M-P2 measures this at the start of P2c-2**, before the mod change. If it does change permissions, see Q1.
  - The installed mod types confirm the order (`~/.config/pdx/cc-plugin/purdex/.claude-plugin/types/claude-code/index.d.ts`): a call "runs through every hook …, the permission check and its dialog, then the tool" (`:3022-3025`); `tool.check`'s input is "the tool's arguments as the permission decision reads them", pinned, while "`tool.call` rewrites a call" (`:12814-12832`). So the decision reads the rewritten command. Its effect on allowlist and read-only matching is still M-P2's to measure. The same types offer `$.tool.check({ tool, input })`, a permission verdict with no dialog and no run (`:3032-3041`), which Q1's guarded variant uses.
- **Interactive or not (coordinator Q2):** `session.start`'s input carries `isInteractive`, "true under the REPL, false for a `-p` run or the SDK" (measured as MP7, `docs/specs/2026-10-06-lead-team-relay-plan-v2.md:7673`). The mod already keys on it: `register.js:1436-1437` and `events.js:1060`. A subagent's Bash call goes through the same mod hooks in the same process, with `e.agentId` set (`events.js:832`). **Not settled by code:** (a) whether `session.start` fires again after `pdx setup` hot-reloads the mod (strong evidence yes: the U1 reporter and the #2441 reload recovery start only from `session.start`, `events.js:698-732`, `register.js:1265-1272,1452`); (b) whether `$.session.id()` in a subagent's call is the main conversation's id. T22 (e)/(f) measures both, with fallbacks.

**F4 — Liveness of a position nobody polls.**
- The sweeper judges a holder with `holderGone`: the pid is absent, or its start differs at second precision (`internal/module/resources/sweeper.go:211-224`). It runs against a fork-free process view, read before `stateMu` and **only when a lease is held** (`sweeper.go:67-72`).
- Where the agent pid and start come from:
  - For a session-new request: `sessionRoot` (`lease_handler.go:262-282`).
  - For `pdx lease run`: the ancestry walk finds the agent pid and its registry start at the match (`session_attr.go:59-66`), but returns only the session id (`:35`).
- `ProcessRoots` lists the live sessions of the registry (`internal/module/peers/origin_resolver.go:409-460`). Every published snapshot already carries `Sessions[]{SessionID, PID}` (`internal/resources/attribute.go:73`, `wire.go:128-131`). A `/clear` keeps the agent pid but gives it a new session id.
- Plan:
  - A position stores `agent_pid` and `agent_start`.
  - The sweeper reads the view while any live position exists.
  - A position ends `abandoned` when `holderGone(agent)` is true, or when two consecutive snapshots list its agent pid under **another** session id (the /clear case, Q9).
  - An unreadable view counts as alive, as today.

**F5 — `resources.db` is live; what happens to existing rows, and to an older daemon.**
- `resource_leases.state` has `CHECK (state IN ('waiting','held','ended'))` (`store.go:111`). Position states cannot live in that table without rebuilding it. So: a new table `resource_positions` (CREATE IF NOT EXISTS), plus ALTER ADD columns on `resource_leases` through `decisionColumns` / `migrateDecisionColumns` (`store.go:521-549, 589-618`).
- **The store has no transaction today:** every write is one guarded UPDATE (`changed`, `store.go:496-506`). Positions add writes that span two tables, so §2.4a adds transactions.
- Existing rows at the deploy:
  - Both hosts are in advise.
  - Advise grants every waiter in its POST's own pass (`admission_pass.go:128-136`).
  - Parking runs only in the lease branch (`:137-158`), and only for `parkable` rows (`:142`). No client on `origin/main` sends `parkable`; only the unmerged P2b branch does (`c798af59c`).
  - So expect held rows (they end as today and have no position), at most transient waiting rows (boot grace 30 s, `boot.go:27`), and no parked rows.
- Defensive rules:
  - Boot ends any `waiting` row with `parked_until > 0` as `abandoned`, because the parking code is gone and nobody polls such a row.
  - Rows with `position_id = ''` and `mode = ''` are wait-mode rows, and a row whose position is missing or ended ends exactly as today (T10).
  - The positions table starts empty.
  - The hostconfig normaliser rejects unknown keys (`internal/module/hostconfig/resources.go:39-41`). So `deadline_s` and `park_s` **stay accepted** (unused), or the stored settings (mlab rev 25, air26 rev 3) would read as invalid, which means mode measure.
- **An older daemon on the new schema** (rollback, §2.9): it selects named columns only (`leaseCols`, `store.go:157-161`), its migration adds only its own missing columns (`store.go:589-600`), and it never reads `resource_positions`. So it opens the database. What it does with live rows is the problem §2.9 solves: its boot grace extends every unparked waiting row (`ExtendWaiting`, `store.go:380-394`, called at `boot.go:27`), and its advise pass grants every live waiter (`admission_pass.go:128-136`), so it would run a background `ask` row without its confirm.
- Before each deploy, the lead runs read-only `sqlite3 "<data>/resources.db" "select state, parked_until>0, count(*) from resource_leases where state!='ended' group by 1,2"` on both hosts. After each deploy, the checks of §5.

---

## 1. Conventions (every PR)

- One branch and one PR per row of §3. Each PR starts from the latest `origin/main` after the previous one merged. Work in a worktree (`EnterWorktree`).
- TDD: test first, see it fail for the right reason, implement, see it pass. One commit per task.
- **Mutation proof is a deliverable.** For each test marked (M): flip or revert the named line, show the test turns red, restore it. Put the list in the PR body. A mutation that a test cannot catch (another layer already enforces the rule) is replaced by one it can, never dropped.
- **Resource rules (mlab OOMs):**
  - Never `go test ./...`, never the full vitest. No SPA file is touched in this line, so no vitest and no `tsc` at all.
  - Run only the affected packages, `-race` only on them: `go test -race -count=1 ./internal/resources/... ./internal/module/resources/...`.
  - Add `./cmd/pdx/...` whenever `cmd/pdx` **or the mod** changes; it includes the plugin package and its guard tests (memory: #2715).
  - Add `./internal/promptq/... ./internal/module/modevents/...` in P2d-1a, `./internal/module/agent/...` in P2d-1b, `./internal/team/... ./internal/module/team/...` in P2d-2, and `./internal/module/peers/...` only if a file there changes.
  - Mod tests: `claude plugin test cmd/pdx/plugin/purdex`.
  - `make lint` before the PR. The lead runs the full suite before release.
- Probes (M-P1, M-P2, M-D1) run in throwaway sessions on an isolated tmux socket (`tmux -L lease-probe`, `unset TMUX`, never a `kill-server` without `-L`), with a throwaway `--plugin-dir` and the worktree's own `bin/pdx` by absolute path. Never in the lead's or a member's live session. Output goes to the worktree's untracked `.scratch/`.
- Every schema change is an ALTER or CREATE IF NOT EXISTS migration with a test that opens an old-schema DB (the P2a schema, `pass_v2_test.go:217` `TestStore_MigratesAPreV2Database` style). All of this line's schema lands in P2b′-1a.
- Work moved under `stateMu` is measured (memory: alpha.610): P2b′-2b records one `passOnce` time with 20 positions and 20 rows × the call frequency in the PR body; P2b′-1b records the time of one transaction (begin, two statements, commit) on the WAL file DB.
- Reviews: codex R1 + R2 (attacker → critic), `--model gpt-5.6-sol`, effort low. Re-review only the increment. Then `pdx report ready` and **wait for the lead's go-ahead** (merge commit, no `--delete-branch`), then `pdx report merged --pr --sha`.
- The amendment spec and this plan enter the repo first, in a docs-only PR (P2b′-0, branch `docs/lease-v2-amend-a`), as `docs/specs/2026-10-13-host-resource-lease-v2-amend-a-spec.md` and `docs/plans/2026-10-13-host-resource-lease-v2-amend-a-plan.md`. Each one's "code facts" line says the inventory lives outside the repo. The plan alone is longer than 800 lines, so it is kept out of the code PRs' budget; it carries what codex already reviewed, so the lead decides whether it gets another review.
- Do not name a new file `zone*.go` (`internal/module/resources/zone_test.go` is a TZ `TestMain`).

---

## 2. The design the code implements

### 2.1 Data

**`resource_positions`** (new table, CREATE IF NOT EXISTS in `migrateDecisionColumns`):

| column | type | meaning |
|---|---|---|
| `id` | TEXT PK | `pos_` + 8 lower-case hex (short: an agent types it in `pdx lease cancel <id>`) |
| `session_id` | TEXT NOT NULL | the CC session |
| `agent_pid`, `agent_start` | INTEGER, TEXT | the session's agent process at creation (F4) |
| `state` | TEXT NOT NULL CHECK IN (`queued`,`offered`,`running`,`idle`,`ended`) | A §3.1 |
| `created_at` | INTEGER | queue order (kept for the whole life: V16) |
| `requeued_from` | TEXT DEFAULT '' | the position a limit released, when its ask rows were re-queued here (P7) |
| `offered_at` | INTEGER DEFAULT 0 | the latest offer, a grant decision (gap) |
| `delivered_at` | INTEGER DEFAULT 0 | when the latest offer's notice was delivered (§2.8); 0 = not yet |
| `passed_at`, `offer_passes` | INTEGER DEFAULT 0 | the last time an offer passed undelivered, and how often (§2.8) |
| `first_started_at` | INTEGER DEFAULT 0 | base of `T` (A §3.3) |
| `window_until` | INTEGER DEFAULT 0 | end of the current 3-min window (offered after delivery / idle); 0 otherwise |
| `commands_started`, `commands_ended` | INTEGER DEFAULT 0 | started commands (V19 guard, §2.5 step 4); `k` |
| `warned` | INTEGER DEFAULT 0 | a final notice is in force |
| `extensions` | INTEGER DEFAULT 0 | final notices given (report) |
| `done_requested` | INTEGER DEFAULT 0 | `pdx lease done` while running |
| `notice_kind` | TEXT DEFAULT '' | the pending notice of the outbox slot (§2.10): `offer`, `idle`, `cancelled`, `drained` |
| `notice_due_at` | INTEGER DEFAULT 0 | when the slot was armed |
| `notice_sent_at` | INTEGER DEFAULT 0 | when its notice was delivered; pending = `notice_kind != '' AND notice_sent_at < notice_due_at` |
| `notice_tries` | INTEGER DEFAULT 0 | fresh promptq keys used for the slot (after `unknown`) |
| `notice_result` | TEXT DEFAULT '' | last outcome, for `ls` and the report (`accepted`, `unknown`, `no_mod`, `member`, `user_alert`, `dropped:<reason>`) |
| `offer_wait_ms`, `idle_wait_ms` | INTEGER DEFAULT 0 | offered / idle window time while someone waited (cost of V16/V17, #10) |
| `offer_zone`, `offer_trend` | TEXT DEFAULT '' | the decision record of the offer |
| `wait_reasons` | TEXT DEFAULT '' | the queue reasons JSON, as rows have |
| `end_reason`, `ended_at` | TEXT, INTEGER | |

- Index: `CREATE UNIQUE INDEX resource_positions_live ON resource_positions(session_id) WHERE state != 'ended'`. This is **one live position per session** (V15), enforced by the database. Insert with `ON CONFLICT DO NOTHING` and re-read, inside the same transaction as the row (§2.4a).
- Index `resource_positions_state(state)`.
- Retention 14 days: `Prune` deletes ended positions with `ended_at` before the cutoff, in the same hourly prune and boot prune as the rows (`store.go:466-473`, `sweeper.go:80-88`, `boot.go:30-32`; T3b, #16).

**`resource_leases`**, ALTER ADD (appended to `decisionColumns`):
- `position_id TEXT NOT NULL DEFAULT ''`
- `mode TEXT NOT NULL DEFAULT ''` (`queue`|`ask`|`wait`; `''` reads as `wait`)
- `command TEXT NOT NULL DEFAULT ''` (≤ 200 bytes)
- `cwd TEXT NOT NULL DEFAULT ''` (≤ 1024 bytes)
- `queued_reason TEXT NOT NULL DEFAULT ''` (`queue`|`own_running`)
- `offered_at INTEGER NOT NULL DEFAULT 0` (when the row was named in a notice)
- `confirmed_at INTEGER NOT NULL DEFAULT 0` (ask rows)
- `notice TEXT NOT NULL DEFAULT ''` (the final notice its release carried)

Plus `CREATE INDEX resource_leases_position ON resource_leases(position_id) WHERE position_id != ''`. Env is never stored. `parked_until`, `parks` and `parkable` stay as dead columns (SQLite cannot drop them cheaply).

### 2.2 Wire (`internal/resources/wire_lease.go`)

- `LeaseRequest` adds `Mode` (`json:"mode,omitempty"`), `Command` (`json:"command,omitempty"`) and `Cwd` (`json:"cwd,omitempty"`). `Parkable` stays accepted and is ignored: POST has `DisallowUnknownFields` (`lease_handler.go:51`).
- Validation:
  - `mode` must be in {"", queue, ask, wait}, else 400.
  - `command` is cut by `resources.CutCommand`: 200 bytes on a rune boundary, control characters become spaces. `cwd` is cut the same way to 1024 bytes.
  - `mode` is part of a replay's identity: a replay with another mode gets 409 `client_id_reused`.
- DELETE (`/api/resources/leases/{id}` and `?client_id=`) accepts `notice=1`: the caller will show a final notice (§2.6). Unknown values are ignored.
- `LeaseResponse` adds:
  - `PositionID` (`position_id,omitempty`; set on every answer about a row of a position, held answers included: the ask client relies on it, R8);
  - `QueuedReason` (`queued_reason,omitempty`: `queue`|`own_running`);
  - `Notice` (`notice,omitempty`: the final notice text, on a release answer only).
  - `Position` is reused as the **queue place** N. `ParkedUntil` stays in the struct, is never set, and old CLIs decode it harmlessly.
- `state` values: `held`; `waiting` (wait-mode, or an ask row not yet granted); `queued` (a queue-mode answer that did not run); `ended`; `none`. `replaced` is no longer produced.
- New end reasons: `cancelled_by_session`, `no_answer`, `idle_timeout`, `done`, `released_after_limit`, `drained` (§2.9). They apply to rows and to positions. `abandoned` is also used for positions.
- New dec paths: `offer` (the first command of an offered position, started in a later pass than the offer) and `continued` (granted inside its idle window), next to `immediate` / `waited` / `unknown_zone`. The free-host case keeps `immediate` (§2.4 P3, #19).
- New error codes:
  - `not_your_session` 403;
  - `no_session` 403 (the caller is not under an agent);
  - `not_your_turn` 409 (confirm on a queued position);
  - `running` 409 (cancel of a running command);
  - `no_position` 404.
- New routes (`module.go:304-311` gains):
  - `POST /api/resources/leases/{id}/confirm` `{holder_pid}`
  - `POST /api/resources/leases/{id}/cancel` `{holder_pid}`
  - `POST /api/resources/positions/{id}/cancel` `{holder_pid}`
  - `POST /api/resources/positions/done` `{holder_pid}`
  - Each answers 200 with the row's `LeaseResponse` or a `PositionView`.
- The caller's session is `sessionOfProcess(holder_pid)` (the P2a walk). It is a guard against mistakes, not against a token holder (A §5).
- `Snapshot` (`wire.go`) adds `Positions []PositionView` (`positions,omitempty`):
  - `PositionView` = `{id, session_id, ref, address, state, place, created_at, window_until, first_started_at, commands, span_s, warned, delivered, notice_result, reason, reason_since, requests: [{id, mode, kind, command, cwd, state: held|queued|waiting|asked, age_s}]}`.
  - `ref` = `ipeers.RefID(session_id)` (`internal/peers/ref.go:114`).
  - `address` = `ResolveOriginBySession(...).Address` (`origin_resolver.go:56`), looked up once per snapshot build through an interface asserted on the same registry object as `RootSource`.
  - `Waiters` keeps only the wait-mode rows (no position).

### 2.3 Modes and who creates a position

- `mode` `queue` (F) or `ask` (B), **and** a session resolved: the request belongs to that session's position. If there is none, a `queued` position is created.
- `mode` `wait` or `''` (S, old clients, non-interactive sessions), or no session found: today's row, a plain FIFO queue entry, long-polled. It never makes or joins a position (compatibility, §5).
- Positions are created **in mode lease only** (Q4). Advise keeps today's behaviour (granted at once, the shadow as is), but rows still store mode, command and cwd, so `pdx lease ls` shows commands in advise as well. Off and measure are unchanged. Live positions that exist when the mode leaves lease are drained (§2.9).

### 2.4 State machine (A §3.1) as code rules

All under `stateMu`; every transition is a compare-and-set, and every transition that writes both tables is **one SQLite transaction** (§2.4a). "rows" are the waiting rows attached to the position.

| # | from → to | trigger (where) | CAS predicate | side effects (same transaction) |
|---|---|---|---|---|
| P1 | — → queued | POST queue/ask with a session and no live position (`handleLeaseCreate`) | INSERT … ON CONFLICT DO NOTHING (unique live index) | re-read the winner; insert the row attached to it; `requestEvent` after commit |
| P2 | queued → offered | `passOnce`: `AdmitV2` picks the position's entry | `id=? AND state='queued'` | `offered_at=now`, `delivered_at=0`, `window_until=0` (§2.8), `passed_at=0`, `m.lastOfferMS=now` (gap), `offer_zone/trend`, reasons flushed into `wait_reasons`; rows named (`offered_at=now`); notice slot `offer` armed (§2.10); `wake` |
| P3 | offered → running | reservation grant (§2.5 step 3) | `state='offered'` and the row's `GrantDecided` | `first_started_at=COALESCE(NULLIF(first_started_at,0),now)`, `window_until=0`, `commands_started+=1`; path `immediate` when the offer was made in this row's own POST pass (free host, V13), else `offer` |
| P4 | idle → running | same | `state='idle'` | path `continued`; `commands_started+=1` |
| P5 | running → running | second command (§2.5 step 4) | `state='running'` and the row's grant | path `immediate`; `commands_started+=1` |
| P6 | running → idle | the **last** held row of the position ends; `AfterCommandEnd` says idle | `state='running'` | the row's end; `commands_ended=k`, `window_until=now+3m`, `warned`, `extensions+=1` on a new warning, `row.notice=text` (notice release only, §2.6); rows still waiting named, slot `idle` armed |
| P7 | running → ended `released_after_limit` | `AfterCommandEnd` says release | `state='running'` | the row's end; queue rows end `released_after_limit`; **ask rows move to a new `queued` position of the same session** (`created_at=now`, `requeued_from=<old id>`, agent copied, the rows' `offered_at` / `confirmed_at` reset) (coordinator Q3); `wake`; pass |
| P8 | running → ended `done` / `abandoned` | last command ends with `done_requested`, or the agent is gone | `state='running'` | the row's end; rows end with the same reason |
| P9 | offered → ended `no_answer` | sweeper: `delivered_at > 0 AND window_until <= now` | `state='offered' AND window_until=?` (a racing start wins) | rows end `no_answer`; slot `cancelled` per Q7 (§2.7); pass |
| P10 | idle → ended `idle_timeout` | sweeper | `state='idle' AND window_until=?` | asked ask rows end `no_answer`, the others `idle_timeout`; slot `cancelled` per Q7; pass |
| P11 | queued/offered/idle → ended `abandoned` | sweeper liveness (F4) | `state IN (…)` | rows end `abandoned` |
| P12 | queued/offered/idle → ended `cancelled_by_session` | `positions/{id}/cancel` | `state IN (…)` | rows end the same |
| P13 | idle/queued/offered → ended `done`; running → `done_requested=1` | `positions/done` | | rows end `done` (B clients exit 15, not run) |
| P14 | queued → ended (the last row's reason) | its last waiting row ended (client cancel, abandoned ask row, `leases/{id}/cancel`) | `state='queued'` and no waiting rows | in the row's end transaction |
| P15 | offered → queued (the offer passes) | sweeper: `delivered_at = 0 AND now >= offered_at + 3m` (§2.8) | `state='offered' AND offered_at=? AND delivered_at=0` | `passed_at=now`, `offer_passes+=1`, `window_until=0`; rows' `offered_at=0`; slot cleared; pass (the next waiter may be offered) |
| P16 | any live → ended `drained` | the sweeper finds live positions while the mode is not lease, or boot does (§2.9) | `state != 'ended'` | waiting rows end `drained`; held rows keep running and end as today; slot `drained` per Q7 |
| D1 | offered, not delivered → delivered | the outbox (§2.10) or the member fallback | `state='offered' AND offered_at=? AND delivered_at=0` | `delivered_at=at`, `window_until=at+3m` (§2.8) |

**Row rules:**

| # | case | result |
|---|---|---|
| R1 | queue-mode POST, position queued (or running and not granted as a second command) | row `waiting`, attached, `lease_until=0`: never polled, **never abandoned by `lease_until`** (excluded in `sweepLeases`, the pass's `live` filter `admission_pass.go:100-106`, and the boot grace `ExtendWaiting` `store.go:380-394`); `queued_reason` = `queue` / `own_running`; answer `state:"queued"`, `position`=N (0 for `own_running`), `queued_reason` |
| R2 | queue-mode POST with the same `command`+`cwd` as a waiting queue row of the same position | **adopt** that row: CAS on the old `client_id` (P2a `Adopt` reshaped, keyed `position_id, command, cwd, mode='queue', state='waiting'`); same row id, then the pass |
| R3 | ask-mode POST not granted | row `waiting`, attached, `lease_until=now+30s`, renewed by its long poll as today; abandoned when not polled (the background process died) |
| R4 | reservation grant | `GrantDecided` (baseline captured outside the lock, as today) |
| R5 | a held row ends (DELETE release, `holder_gone`, `expired`, `vanished`) | `onCommandEnd` → P6 / P7 / P8; a row with no position, or whose position is missing or ended, ends exactly as today (#14) |
| R6 | a position ends | its waiting rows end with its reason (P9/P10 nuance above) |
| R7 | **naming invariant** (#1) | at the end of every pass, and in P1's transaction: any waiting row attached to an `offered` / `idle` position with `offered_at = 0` is named and the slot armed (`offer` / `idle`). A fresh row is normally granted at once (§2.5 step 3), so this catches a row cut by a disk trim or left by a lost CAS; no row ever waits at an offered/idle position unasked |
| R8 | **ask safety net** (client, T17) | an ask client whose request was answered `waiting` with a `position_id` runs its command only on a `held` answer that carries a `position_id`; any other `held` (an older daemon after an undrained rollback, §2.9) is released at once and the client exits 15, not run |

### 2.4a Transactions and the boot reconcile (#3)

- `leaseStore` gains `tx(func(*sql.Tx) error) error` (BeginTx, rollback on any error or a lost CAS, commit). `stateMu` still serialises every writer in the daemon; the transaction makes the pair atomic **on disk**, so a crash or restart between two statements leaves neither.
- The combined operations, each one transaction (T3b): `CreateAttached` (P1 + the row, or the row on the winner), `OfferPosition` (P2 + naming + slot), `GrantInPosition` (P3/P4/P5 + `GrantDecided`), `EndCommand` (the row's end + P6/P7/P8, including P7's re-queue), `EndPositionWithRows` (P9–P13, P16), `EndRowMaybePosition` (a waiting row's end + P14), `PassOffer` (P15), `NameRows` (R7), `ReconcilePositions` (below). Single-table writes (`MarkDelivered`, `SetNoticeResult`, cost accumulators) stay single statements.
- **Boot reconcile** (`ReconcilePositions(now)`, one transaction, run by `boot()` before the sampler, T12b). With transactions these pairs cannot come from a crash; they come from an older daemon after a rollback (§2.9) or a bug, and each one repaired is logged:
  - (a) `running` with no held row → as a sweeper command end (rule 3 treated as rule 4): `idle` with `window_until = now + 3m`, or ended `done` if `done_requested`.
  - (b) `queued` / `offered` / `idle` with a held row → `running` (the held row is the truth).
  - (c) waiting rows whose position is ended → end with the position's reason; whose position is missing → end `abandoned`. Held rows of an ended or missing position keep running and end as plain rows (R5).
  - (d) `queued` with no waiting row → ended `abandoned`.
  - (e) live positions while the mode is not lease → P16 (§2.9).

### 2.5 Admission per pass (lease mode; extends v2 D-3′)

`passOnce` (`admission_pass.go:80`) reads held rows, waiting rows **and live positions** (one more indexed read), then:

1. **Reserved counts as running:** `running := len(held) > 0 || any position in {offered, running, idle}` (M).
2. **Gap (#5):** `lastGrant := max(m.lastGrantMS, max granted_at of held rows whose dec_path != 'offer', m.lastOfferMS)`. The offer is the grant decision and starts the gap (A §3.1); its first command consumes the reservation and **does not** start another gap (the `offer` path never sets `m.lastGrantMS`, `admission_pass.go:212`, and is left out of `lastGrantTime`, `:238-247`). `continued` and second-command grants start a gap as every grant does (new load after a pause, which the zone must be given time to show). At boot, `lastGrantMS = MaxGrantedAt` without `offer`-path rows and `lastOfferMS = MaxOfferedAt` (`boot.go:36-41` gains both).
3. **Reservation grants first.** For each position in `offered`/`idle`, its oldest grantable row is granted at once, **without** a table or gap check. A grantable row is the pass's `fresh` row of that position, of either mode (a B command the session issues during its own turn is not asked: the question exists for a command issued long before its turn, V12), or an ask row with `confirmed_at > 0`. The disk cut still applies (`admission_pass.go:113-121`). At most one per position per pass; the position becomes `running` (P3/P4). These do not use the pass's one `AdmitV2` grant.
4. **Second command** (only the `fresh` row of a `running` position, coordinator Q6): granted (P5) iff `AdmitV2(zone, trend, true, lastGrant, [fresh], now)` grants — **whether or not someone is queued** (A §3.2) — and, when someone waits (§2.6), the position is not `warned` and `commands_started < 3` (it "is still one of the position's commands for V19–V21", A §3.2). Otherwise R1 with `own_running` (F) or R3 (B); the row is named when the position goes idle (P6).
5. **The queue.** Entries are:
   - queued positions, except a passed one that is not eligible (§2.8): `V2Waiter{ID:"pos:"+id, CreatedAt: created_at, Cut: diskHolding && a guarded kind among its rows}`;
   - wait-mode rows with `lease_until > now`, cut as today (baseline, disk).

   `AdmitV2` is unchanged (P1a). A row grant is today's `GrantDecided`. A position grant is P2. If the offered position owns this POST's `fresh` row, step 3 runs for it **in the same pass** (the "free host → runs at once" path, V13, path `immediate`).
6. **Reasons:** entries are keyed by row id or `pos:<id>`. `m.reasons.retain` keeps both. A position's account is flushed into `resource_positions.wait_reasons` at P2 and at its end.
7. `baselinesForWaiting` computes baselines only for rows that can be granted this pass: the fresh row, and rows of offered/idle positions. Eligibility of passed positions (§2.8) is read before the lock too.
8. **Naming** (R7) runs last, in the pass's lock.

`diskPreflight` still runs inline for a POST of a guarded kind (≤ 90 s, only under 15 GiB free). "Never waits" means never waits for the queue.

### 2.6 Limits (A §3.3) as code

Pure, in `internal/resources/position.go`:

```go
const (
    PositionWindow      = 3 * time.Minute  // V16
    PositionMaxCommands = 3                // V19
    PositionMaxSpan     = 30 * time.Minute // V20
)
type AfterEnd int // AfterIdle | AfterIdleWarn | AfterRelease
type NoticeKind string // "" | "overtime" | "three_commands"

// k = commands ended including this one; span = now - first_started_at.
func AfterCommandEnd(k int, span time.Duration, warned, someoneWaiting bool) (AfterEnd, NoticeKind, bool /*warned after*/) {
    if !someoneWaiting { return AfterIdle, "", false }               // 1: no limit; an earlier notice is void
    if warned          { return AfterRelease, "", true }             // 2: the announced last command has ended
    if k+1 >= PositionMaxCommands || span >= PositionMaxSpan {        // 3: (k >= 3, V21, is implied by k+1 >= 3)
        if span >= PositionMaxSpan { return AfterIdleWarn, "overtime", true }
        return AfterIdleWarn, "three_commands", true
    }
    return AfterIdle, "", false                                       // 4
}
```

Rules in the module (`onCommandEnd(pos, row, now, notice bool)`), applied in `endLease` (`lease_handler.go:388-427`) and in the sweeper's held-row ends (`sweeper.go:129-153`):

- **Someone waiting** = another position in `queued` (a passed one included), or a wait-mode row waiting with `lease_until > now` (Q5).
- If another held row of the position remains, only `commands_ended` is incremented. The last one to end decides (A §3.2: two at once).
- **The notice needs a release that shows it (#2):** rule 3 is applied only when the end is a DELETE with `notice=1`, whose answer carries the text. Who sends `notice=1`: the mod's normal path, after `next(e)` resolved (T26), and `pdx lease run` after its child exited (T19). Who does not: the mod's throw path (`finally`, `lease.js:503-505`), `pdx lease run` interrupted before the child started (`lease_run.go:118-123`), old clients, `pdx lease release` typed without `--notice`, and every sweeper end (`holder_gone`, `vanished`, `expired`). Those treat rule 3 as rule 4: idle and not warned. So V21 ("never released without a notice") holds even when nobody could be told; the cost is one more extension. Residual: a `notice=1` answer lost in transit (the 5-s release timeout) leaves `warned` set without the session having read it; the next command is then released without a notice the session saw. Rare (the daemon is local) and accepted.
- `done_requested`: end `done` instead of idle. Agent gone: `abandoned`.
- The cost accumulators (`offer_wait_ms` from the offer until running / passed / ended, `idle_wait_ms` over idle windows): in memory per position, added on each sweeper tick while someone waits, flushed on every transition out of the state (lost on restart, like `wait_reasons`).

### 2.7 Texts (final wording; Chinese as given in the spec)

| id | where | text |
|---|---|---|
| T-queued | F not granted, place N (mod deny, `lease run` stderr) | 「主機資源忙，這個指令還沒執行：排在第 N 位。輪到你時我會通知你，先做別的事；不要改成背景執行或加 `&` 繞過排隊。」 |
| T-queued-own | F, own command running (A §3.2) | 「主機資源忙，這個指令還沒執行：等你自己的上一個指令跑完。輪到你時我會通知你，先做別的事；不要改成背景執行或加 `&` 繞過排隊。」 (composed from the spec's phrase) |
| T-script | appended by `pdx lease run` when it is a script's inner step (§P2c-1a) | 「這支腳本做到一半被排隊，前面的步驟已經執行；之後請用 `pdx lease run --kind <k> -- <整支腳本>` 整支重跑。」 (`<k>` filled; `<整支腳本>` literal) |
| T-offer | P2d, per queue row named (offer, or an `own_running` row named at P6) | 「輪到你了：`<command>`（`<cwd>`）。3 分鐘內照原樣重跑就會立刻執行；之後 3 分鐘內接著下的重指令也會直接跑。不跑請 `pdx lease cancel <id>`。」 (`<id>` = position id; several rows: Q8) |
| T-ask | P2d, per ask row | 「輪到你的背景指令了：`<command>`（`<cwd>`）。要執行就 `pdx lease confirm <id>`，不要就 `pdx lease cancel <id>`；3 分鐘沒回應會取消。」 (`<id>` = row id) |
| T-cancelled | P2d, P9 / P10, **only when the session has an asked ask row it did not answer or a named queue row it did not re-run** (coordinator Q7); otherwise nothing is sent | 「3 分鐘沒有動作，你在佇列裡的位置已經放掉；之後要跑請重新下指令。」 |
| T-drained | P2d, P16, same Q7 condition | proposed: 「主機資源改回不排隊模式，你在佇列裡的位置已經放掉；現在直接重下指令就會執行。」 (QN1) |
| T-final-overtime | release answer `notice` (overtime wins when both hold) | 「因為執行時間過長，這次指令執行完畢後會釋出執行排序，請自行安排執行方式與內容。」 |
| T-final-three | release answer `notice` | 「有其他 session 在排隊，下一個指令會是這次的最後一個；之後還要跑請重新排隊。」 |
| T-ask-cancel | `lease run --ask`, ended not run | `pdx lease: 已取消，指令沒有執行` (exit 15) |

Daemon-owned texts (offer, ask, cancelled, drained, final) live in `internal/resources/position_texts.go`. The clients compose T-queued, T-queued-own and T-script themselves, as P2b did (they know N and whether they are inside a script). CLI lines for the new verbs (plan wording, adjustable):
- `pdx lease: 已確認，背景指令開始執行`
- `pdx lease: 已撤銷`
- `pdx lease: 位置已放掉`
- `pdx lease: 這不是你這個 session 的請求`

### 2.8 Offer window start — decided (user, 2026-10-14; Q18)

**Decided by the user on 2026-10-14: the window counts from delivery. This subsection is the only place the choice lives; the paragraph at its end lists what flipping it would change.**

Recommended (coordinator): the 3-minute offer window counts from when the offer notice is **delivered** to the session (the mod submitted the prompt, or the member fallback delivered it), not from the offer.
- P2 makes the position `offered` with `delivered_at = 0` and `window_until = 0`: the host is reserved (reserved counts as running), the window has not started.
- D1: delivery sets `delivered_at` and `window_until = delivered_at + 3m`. A user alert (T34) is not a delivery: the session never sees it.
- P15: if the notice has not been delivered within 3 minutes of the offer, **the offer passes**: the position goes back to `queued`, keeps `created_at` (so it stays first in line) and is **not** ended; the rest of the queue may be offered.
- **Eligibility of a passed position** (read before the lock, §2.5 step 7): it is a queue entry again only when (a) its session can take a notice now — `deliverable(sid)`: the agent module reports no main turn running (`MainTurnRunning`, added next to `AbortedAt` in `internal/module/agent/light_status.go:32-43`; no live stream or an unknown session reads as deliverable, the outbox then finds out) — and (b) a grant decision was made after `passed_at` (`max(lastGrantMS, lastOfferMS) > passed_at`) or no other entry is eligible. So the next waiter gets its turn first, then the passed position is offered again "when the host can grant the head next time and delivery can happen". In P2b′-3b `deliverable` is a seam that answers true; P2d-1b wires it.
- `no_answer` (P9) therefore only follows a delivered offer.
- The idle window still counts from the command end (P6): the session is not blocked then, and the final notice rides on the command result.
- Cost: the host can be reserved for up to 3 minutes before delivery plus 3 after it; both count in `offer_wait_ms` (#10), and the report shows offers delivered, passed and their delays.

**To flip to "window from the offer"**: P2 sets `window_until = offered_at + 3m`; drop P15, the eligibility rule and `MarkDelivered`'s window write (keep `delivered_at` for the report); T11's delivery-delay tests change their expected times (`…FromDelivery` → `…FromTheOffer`, `…UndeliveredOfferPasses` → removed); T32 drops the passed-position handling; P2d-1b drops `MainTurnRunning` (≈ −150 lines across P2b′-3b and P2d-1b).

### 2.9 Drain, mode changes and rollback (#4, coordinator)

- **Leaving lease drains (P16).** When the sweeper finds live positions and the mode is not lease (advise, measure, off), it ends them all `drained`, with their waiting queue and ask rows, in one transaction; held rows keep running (V8) and end as plain rows. `boot()` does the same when the daemon starts in a mode other than lease. The sweeper otherwise leaves rows alone in off and measure (`sweeper.go:58,77-79`); this one step runs in every mode. B clients see `ended`/`drained` and exit 15 (not run). Sessions with something pending get T-drained once P2d is deployed (Q7 condition). Whether a plain switch to advise should drain, or keep positions until lease returns, is user-visible (QN1); the drain is required before any downgrade either way.
- **Rollback (any downgrade of the daemon after positions may exist):**
  1. Switch both hosts to advise (`PUT /api/hostconfig/resources`). Within one sweeper tick every live position is drained.
  2. Verify on each host: `pdx lease ls` shows no position; `sqlite3 … "select count(*) from resource_positions where state!='ended'"` is 0; `sqlite3 … "select count(*) from resource_leases where state='waiting' and position_id!=''"` is 0.
  3. Check `relays_active=0`, install the older binary, restart, and `pdx setup` from that binary if the mod differs.
  4. What the older daemon does with what is left: ended positions and the new columns are never read (F5); held rows that belonged to positions are plain held rows to it (release, `holder_gone`, `expired` as today); no waiting row of a position is left to grant. If step 2 was skipped, R8 is the second line: a background ask client refuses a `held` answer without `position_id`.
- **Before positions exist on the live hosts** (until the acceptance window of §5), a rollback is the usual binary swap: advise creates no positions (§2.3).
- **Roll-forward gate** (re-deploying the newer daemon after a rollback): both hosts in advise; the boot log shows the reconcile counts (§2.4a; any repaired pair is read before going on); the post-deploy checks of §5 pass; the acceptance run is repeated before lease is switched on again.

### 2.10 Notice outbox (P2d-1b; persisted slot, #8, #9)

- **One slot per position**, persisted in its row (`notice_kind`, `notice_due_at`, `notice_sent_at`, `notice_tries`, `notice_result`). At most one notice of a position is current at a time, and a newer one replaces an older one: P2 arms `offer` (T-offer for named queue rows, T-ask for named ask rows, one message), P6 and R7 arm `idle` / `offer` for newly named rows, P9 / P10 / P16 arm `cancelled` / `drained` when Q7's condition holds. Arming is in the transition's own transaction, so a restart never loses it.
- **Sent by its own goroutine, never under `stateMu`.** A wake channel plus a 2-s tick. It reads pending slots, composes the text from the rows that are still named and waiting, and calls `SubmitNotice` with key `<position>:<notice_due_at>:<kind>:<notice_tries>`.
- **Current check before each try:** `offer` — the position is still `offered` with the same `offered_at` and a named row still waits (or, for a position with no rows left, still offered); `idle` — still `idle` with the same `window_until` and a named row waits; `cancelled` / `drained` — ended ≤ 10 min ago. A notice that is not current is cleared (`notice_result='stale'`).
- **Outcomes:**
  - `accepted` → `notice_sent_at = now`; for an `offer`, D1 (`MarkDelivered`).
  - `busy` (the session is mid-turn) and `timeout` (never handed out) → try again in 2 s with the same key (promptq lets both be re-sent, `queue.go:9-11,371-372,477-480`).
  - `ErrBusy` (16 jobs waiting for the session) → try again in 5 s.
  - `unknown` (handed out, no result) → **not** a delivery: `notice_tries += 1` and try again with the fresh key while current. A duplicate offer or question is harmless (the session re-runs or confirms once; later copies are stopped by the current check). For an offer, the 3-minute delivery limit of §2.8 still applies.
  - `dropped` with reason `session_changed` → cleared (the /clear case; liveness ends the position, Q9). Any other drop reason → try again in 5 s while current.
  - `no_mod` → the fallback (P2d-2) once per slot, and the mod channel is still tried every 5 s while current (a reload or a daemon restart is over in seconds). A member fallback that is delivered (`ResultDelivered` or `ResultDeliveryUncertain`) counts as delivered; a user alert does not.
- **Boot replay:** every pending slot that is still current is enqueued again at start (the `cancelled` ones within their 10 minutes included).

---

## 3. PRs

Estimates are diff lines (insertions + deletions) / files. Revision 2's fixes grew P2b′ from four PRs to nine and P2c, P2d from two to three each; neighbouring PRs may be combined when their real diff together stays under 800 lines and 20 files.

| PR | content | estimate |
|---|---|---|
| P2b′-0 | the amendment spec and this plan, docs only (§1) | docs |
| P2b′-1a | schema, wire, pure rules, POST stores the fields | ~650 / 11 |
| P2b′-1b | transactional transitions, retention, reconcile (store only) | ~700 / 5 |
| P2b′-2a | remove parking and re-attach; agent identity | ~450 / 11 |
| P2b′-2b | queue at once, the offer, one position per session | ~750 / 8 |
| P2b′-3a | command end, limits, notice release, re-queue, legacy rows | ~700 / 6 |
| P2b′-3b | windows (§2.8), liveness, costs | ~750 / 5 |
| P2b′-3c | drain, boot reconcile, rollback guards | ~400 / 5 |
| P2b′-4a | confirm / cancel / done routes | ~550 / 5 |
| P2b′-4b | snapshot, event, report | ~500 / 8 |
| P2c-1a | clients: modes, queue / ask, script step, final notice | ~600 / 7 |
| P2c-1b | clients: new verbs, `ls`, report | ~450 / 6 |
| P2c-2 | the mod, the skill, the probes | ~700 / 6 |
| P2d-1a | the notice job kind (promptq + mod) | ~450 / 8 |
| P2d-1b | the notice outbox, delivery, `MainTurnRunning` | ~750 / 6 |
| P2d-2 | fallback without a mod channel | ~450 / 8 |

### P2b′-1a — positions: schema, wire, pure rules (~650 / 11) — no behaviour change

Branch `feat/lease-v2-amend-p2b1a`. Cherry-pick `4f8d4f3ed` from `feat/lease-v2-p2b` first (the replay fix and its test; kept as is).

**T1 Pure rules** — new `internal/resources/position.go` (+ `position_test.go`, `position_texts.go`): `AfterCommandEnd`, the constants, `NoticeKind`, `FinalNotice(kind)`, `CutCommand(s, max)`, the position and row state names, and the new end reasons.
- `TestAfterCommandEnd_NobodyWaitingNoLimit`: k=5, span 2 h, not waiting → idle, no notice. (M: put rule 3 before rule 1.)
- `TestAfterCommandEnd_NoticeVoidWhenNobodyWaits`: warned, not waiting → idle and warned=false. (M: return `warned` unchanged.)
- `TestAfterCommandEnd_ThreeShortCommandsAnnounceTheThird`: k=1 → idle; k=2 → warn `three_commands`. (M: `k+1>=3` → `k>=3`.)
- `TestAfterCommandEnd_OverThirtyMinutes`: k=1 at 29m59.999s → idle; at 30m00s → warn `overtime`. (M: `>=` → `>`.)
- `TestAfterCommandEnd_OneTwoOverThirty`: k=1 at 10m → idle; k=2 at 31m → warn `overtime`.
- `TestAfterCommandEnd_WaiterAfterTheThirdGetsOneExtension` (V21): k=3, not warned → warn; then k=4, warned → release. (M: `k+1 >= 3` → `k+1 == 3`, so k=3 is not warned.) (#22)
- `TestAfterCommandEnd_WarnedReleases`. (M: drop rule 2.)
- `TestAfterCommandEnd_OvertimeWinsWhenBothHold`.
- `TestCutCommand_RuneBoundaryAndControls`: 200 bytes, a 3-byte rune at the edge, `\n` / `\t` → space.

**T2 Wire** — `wire_lease.go`, `wire.go` (+ `wire_lease_test.go`): the fields of §2.2, the mode constants (`CallQueue`, `CallAsk`, `CallWait`; not `Mode*`, which the settings use), the end reasons (`drained` included), `PositionView`, the error codes.
- `TestLeaseRequest_ModeCommandCwdRoundTrip`.
- `TestLeaseResponse_NoticeAndPositionOmittedWhenEmpty`.

**T3a Schema and reads** — `store.go` (schema, `decisionColumns`, `leaseCols`, `scanLease`, `Create`) and new `store_positions.go` (+ `store_positions_test.go`): every column and index of §2.1; reads `LivePositionOf(session)`, `LivePositions()`, `PositionByID`, `RowsOf(position)`, `MaxOfferedAt`; `MaxGrantedAt` leaves out `dec_path='offer'` rows (§2.5 step 2).
- `TestStore_MigratesAPreAmendmentDatabase`: build the P2a schema, open it, the new columns exist, old rows read `position_id='' mode=''`, the positions table exists. (M: drop one ALTER.)
- `TestStore_UniqueLivePositionPerSession`: two live inserts for one session conflict; after one ends, a new one inserts. (M: drop the `WHERE` of the unique index.)
- `TestStore_MaxGrantedAtSkipsOfferPath`. (M: drop the `dec_path` filter.)

**T4 POST stores the new fields** — `lease_handler.go`: validate and store `mode`, `command`, `cwd`; `mode` joins the replay identity; behaviour otherwise unchanged.
- `TestLeaseCreate_StoresModeCommandCwd`: advise; the command is cut to 200 bytes.
- `TestLeaseCreate_BadModeIs400`.
- `TestLeaseCreate_ReplayWithAnotherModeIs409`.
- `TestLeaseCreate_ParkableStillAccepted`.

Done when: merged, and nothing reads positions yet.

### P2b′-1b — positions: transactional transitions, retention, reconcile (~700 / 5) — no behaviour change

Branch `feat/lease-v2-amend-p2b1b`. Store only; nothing calls these until P2b′-2b.

**T3b Transitions** — `store_positions.go`, new `store_tx.go` (+ tests): `tx(...)`, and every combined operation of §2.4a (`CreateAttached`, `OfferPosition`, `GrantInPosition`, `EndCommand` with P7's re-queue, `EndPositionWithRows`, `EndRowMaybePosition`, `PassOffer`, `NameRows`, `ReconcilePositions`), plus single-statement `MarkDelivered`, `MarkDone`, `SetNoticeResult`, `AddWindowWait`, `AdoptQueued(old, n)`, `EndParkedAtBoot(now)`, and the position prune.
- `TestPositions_CreateAttachedReturnsTheLiveOne`: a second create for the session attaches the row to the first position; after it ends, a new one is created.
- `TestPositions_OfferCASOnce`.
- `TestPositions_StartKeepsTheFirstStart`.
- `TestPositions_EndOnlyFromTheNamedStates`.
- `TestPositions_WindowLapseLosesToAStart`: P9's `window_until=?` predicate.
- `TestPositions_MarkDeliveredOnlyForTheSameOffer`: a delivery for an older `offered_at` changes nothing.
- `TestStore_AdoptQueuedOnlySameCommandCwdAndWaiting`: a held row or another cwd is never adopted. (M: drop the `cwd` term.)
- `TestStore_EndParkedAtBoot`.
- **Crash boundaries (#3):** `TestTx_FailureBetweenStatementsLeavesNeither`, table-driven over every combined operation. A test seam `afterFirstStmt func() error` fails between the two statements; the file DB (temp dir, WAL) is closed and reopened; both tables read as before the operation. (M: run the operation's statements outside the transaction.)
- `TestReconcile_RepairsEachInconsistentPair`: one sub-test per case (a)–(e) of §2.4a, built by direct SQL on a reopened file DB. (M: skip each case in turn.)
- `TestPrune_EndedPositionsOlderThanRetention`: an ended position at 15 days goes, one at 13 days and every live one stay. (M: prune only `resource_leases`.) (#16)
- One transaction's time on the WAL file DB goes in the PR body (§1).

### P2b′-2a — remove parking and re-attach; agent identity (~450 / 11)

Branch `feat/lease-v2-amend-p2b2a`.

**T5 Remove parking and re-attach**:
- `admission_pass.go:137-158` (parking);
- `lease_handler.go:146-157, 466-501` (`reattach`) and the parked branches of the poll (`:321-342`) and of `leaseResponse` (`:445-447`);
- `store.Park`, `store.ParkedFor`; `Adopt` is replaced by `AdoptQueued` (T3b);
- the `parked` reason text in `cmd/pdx/lease_cmd.go:331,362-364` stays until P2c-1b (old daemons).
- Delete `park_test.go`, `parkable_test.go`, `parkexpiry_test.go`. Rewrite `reattach_test.go` as `adopt_queued_test.go`.
- `boot()` calls `EndParkedAtBoot` (F5). `Settings.Park()` / `Deadline()` stay but are read nowhere in admission (Q11).
- `TestBoot_ParkedRowsEndAbandoned`.
- `TestPass_NothingIsEverParked`: in lease mode, a wait-mode row with **`parkable=1`**, `deadline_at < now` and `lease_until > now` stays waiting with `parked_until = 0` after a pass. (M: put the parking loop back; with `parkable=1` it parks the row.) (#18)

**T6 Agent identity** — `session_attr.go`: `sessionOfProcess` returns `(session, agentPID, agentStart)` from the match (`:59-66`). Callers are updated. `sessionRoot` already gives them for session-new.
- `TestSessionOfProcess_ReturnsTheAgentPidAndStart`.

### P2b′-2b — queue at once, the offer, one position per session (~750 / 8)

Branch `feat/lease-v2-amend-p2b2b`.

**T7 Create path** — `lease_handler.go` `handleLeaseCreate` and new `positions.go`, per §2.3 and §2.4 P1, R1–R3:
- Lease mode with mode queue or ask and a session: under `stateMu`, `CreateAttached` (or R2's adopt). Unlock, then `admissionPass(ctx, row.ID)`. Re-read the row and the position, and answer:
  - held → held (with `position_id`);
  - queue mode, still waiting → `queued` + `position` + `queued_reason`;
  - ask mode → `waiting` + `position` + `position_id`.
- Without a session: wait mode.
- Tests:
  - `TestPosition_FreeHostRunsAtOnce` (V13): empty queue, normal → F 201 held, position `running`, `offered_at == first_started_at`, `dec_path=immediate` (§2.4 P3). (M: record `offer` for a same-pass start.) (#19)
  - `TestPosition_BusyHostQueuesAtOnce` (V11): danger → F answers `queued`, place 1, at once. No poll exists; the test asserts that the POST returned under 100 ms with the module's fake clock frozen. (M: route queue mode to the wait path.)
  - `TestPosition_OnePerSessionTwoCommands`: two F POSTs of one session while queued → both rows carry the same `position_id`, which exists in `resource_positions`; the same command and cwd again → the first row adopted (same id). (M: in `CreateAttached`, attach the row to the id of the attempted insert instead of the re-read winner.) (#18)
  - `TestPosition_WaitModeAndOldClientsMakeNoPosition`: mode `""` and `wait` → plain rows; their FIFO place is among the positions.
  - `TestPosition_QueueWithoutASessionIsWait`.
  - `TestPosition_AdviseMakesNoPositions`.
  - `TestPosition_ConcurrentCreatesOneLivePosition` (`-race`, 8 goroutines, one session).

**T8 The pass with positions** — `admission_pass.go`, `reasons.go` (keys), `boot.go` (`lastOfferMS`, `MaxGrantedAt` without the offer path), per §2.5:
- `TestPosition_OfferIsAGrantDecisionWithTheGap`: a queued head with the table saying grant → `offered`; a wait-mode row behind it waits `gap` 30 s from `offered_at`. (M: leave `lastOfferMS` out of `lastGrant`.)
- `TestPosition_FirstCommandDoesNotRestartTheGap` (#5): offer at t0, the session's re-run at t0+20 s (path `offer`); a second session's head in normal+flat is offered at t0+30 s, not t0+50 s. (M: let the `offer` path set `m.lastGrantMS`.)
- `TestBoot_GapCountsFromTheLastOfferNotItsFirstCommand`.
- `TestPosition_OfferedFirstCommandSkipsTheTable`: offered, then the zone turns danger → F POST of the session → held, `dec_path=offer`. (M: send it through `AdmitV2`.)
- `TestPosition_IdleNextCommandRunsAtOnce`: the store sets idle → F POST → held, `continued`.
- `TestPosition_AskPostedWhileOfferedOrIdleRunsAtOnce` (#1): a B POST of the session at an offered position → held, `offer`; at an idle one → held, `continued`; no question is armed. (M: grant only queue-mode fresh rows in step 3.)
- `TestPosition_UngrantedRowAtAnOfferedPositionIsNamed` (#1, R7): a guarded-kind B row cut by a running trim at an offered position → named (`offered_at > 0`) and the slot armed `offer`. (M: skip the naming step.)
- `TestPosition_ReservedCountsAsRunning`: an offered position with no held row. In normal+rising, a second session's head observes `rising`; in normal+flat it is offered (both reserved). (M: `running = len(held) > 0`.)
- `TestPosition_SecondCommandWhileRunning` (coordinator Q6): the table grants → held, **with or without someone queued**; the table refuses → F `queued`/`own_running`, B `waiting`. (M: require an empty queue, the revision-1 rule.) (#6)
- `TestPosition_SecondCommandCountsTowardTheLimit`: someone queued, the table grants; `commands_started=3` → `own_running`; `warned` → `own_running`; nobody queued and `commands_started=5` → held (V18). (M: drop the guard.)
- `TestPosition_StrictFIFOAcrossPositionsAndRows`.
- `TestPosition_QueueRowsAreNeverAbandonedByLease`: an attached queue row unpolled for 2 min is still waiting. (M: drop the exclusion in `sweepLeases`.)
- `TestPosition_AskRowIsPolledAndAbandoned`: not polled for 31 s → abandoned; the queued position with no rows left ends `abandoned` (P14).
- `TestPosition_ReasonsKeyedByPosition`: `wait_reasons` written on the position at the offer.
- `BenchmarkPassOnce_Positions` (20 positions, 20 rows): number in the PR body (§1).

**T9 Answers** — `leaseResponse`: `position` = queue place (positions and wait-mode rows by `created_at`), `position_id`, `queued_reason`. `TestLeaseResponse_PlaceCountsPositionsAndRows`.

### P2b′-3a — command end, limits, notice release, re-queue, legacy rows (~700 / 6)

Branch `feat/lease-v2-amend-p2b3a`.

**T10 `onCommandEnd`** — `positions.go`, `lease_handler.go` (`endLease`, DELETE reads `notice=1`), `sweeper.go` (held ends), per §2.6, through `EndCommand` (one transaction). A DELETE answer with `notice=1` carries `notice` (T-final-*) and stores it on the row.
- `TestLimits_ThreeShortCommandsWithAWaiter`: cmd1 ends → idle. A waiter arrives. The cmd2 release (`notice=1`) answer carries T-final-three. The cmd3 release → position `released_after_limit`; the waiter is offered in the same pass. (M: do not apply rule 3.)
- `TestLimits_NobodyWaitingNoLimit`: five commands, 2 h on the fake clock → never released, no notice. (M: apply the limits without a waiter.)
- `TestLimits_OvertimeNoticeOnTheReleaseAnswer`.
- `TestLimits_WaiterDuringTheThirdGetsOneExtension` (V21): the waiter arrives while cmd3 runs → cmd3's release warns, cmd4's release releases. (M: release at `k >= 3` without looking at `warned`.) (#22)
- `TestLimits_NoticeVoidWhenTheWaiterLeaves`.
- `TestLimits_RuleThreeNeedsANoticeRelease` (#2): a waiter, k=2, a DELETE **without** `notice=1` (the mod's throw path) → idle, not warned, no `notice`; the next DELETE with `notice=1` warns. (M: apply rule 3 to every client release.)
- `TestLimits_SweeperEndNeverWarns`: `holder_gone` with a waiter and k=2 → idle, not warned; the next notice release warns. (M: let the sweeper warn.)
- `TestLimits_LastOfTwoConcurrentCommandsDecides`.
- `TestLimits_WaitModeRowsCountAsWaiting` (Q5).
- `TestLimits_ReleaseRequeuesAskRowsAtTheBack` (coordinator Q3, #7): at release, a waiting ask row of the position moves to a new `queued` position of the same session, behind the waiter, `requeued_from` set; its long poll keeps answering `waiting` with the new place and the same row id; queue rows end `released_after_limit`. (M: end the ask rows instead.)
- `TestLimits_RequeuedAskIsAskedAtItsTurn`: the new position is offered after the waiter, and its ask row is named.
- `TestCommandEnd_LegacyHeldRowEndsAsToday` (#14): a held row with `position_id=''` (as left by the deploy) ends by DELETE by id, by DELETE by client id, and by the sweeper as `holder_gone`, `expired` and `vanished`, with today's reason and answer, and no position read. (M: make `onCommandEnd` fail when no position is found.)
- `TestCommandEnd_RowOfAMissingOrEndedPositionEndsAsToday`.
- `TestRelease_AnswerCarriesTheNotice`: DELETE by client id and by id, each with `notice=1`.

### P2b′-3b — windows, liveness, costs (~750 / 5)

Branch `feat/lease-v2-amend-p2b3b`. Implements §2.8 with `deliverable` as a seam (true) and `MarkDelivered` called by tests.

**T11 Windows** — `sweeper.go`, `positions.go`: P9, P10, P15, D1, the eligibility of passed positions, and the cost accumulators. Every boundary is asserted on both sides (`2:59.999` stays, `3:00.000` lapses), so changing `PositionWindow` either way turns them red (#11).
- `TestWindow_OfferWindowCountsFromDelivery`: offer t0, delivered t0+1:30 → still offered at t0+4:29.999, `no_answer` at t0+4:30; its queue rows `no_answer`; the next head offered. (M: start the window at `offered_at`.)
- `TestWindow_UndeliveredOfferPasses`: offer t0, never delivered → offered at t0+2:59.999; at t0+3:00 back to `queued`, first in line, not ended, `offer_passes=1`; the next head is offered in that pass. (M: end it `no_answer` instead.)
- `TestWindow_PassedPositionWaitsForItsSession`: `deliverable=false` → skipped while another entry waits; after the other's offer and `deliverable=true` → offered again; alone in the queue and deliverable → offered at the next pass. (M: ignore `deliverable`.)
- `TestWindow_IdleWindowRestartsAtTheCommandEnd`: offer t0, delivered t0+0:10, the command starts t0+2:00 and ends t0+10:00 → idle at t0+12:59.999, `idle_timeout` at t0+13:00. (M: keep `window_until` from the offer.)
- `TestWindow_IdleLapsesToIdleTimeout`: an asked ask row → `no_answer`, a queue row → `idle_timeout`.
- `TestWindow_QueuedNeverLapses` (V16): 2 h queued → still queued. (M: give queued a window.)
- `TestWindow_StartWinsOverALapseInTheSameMillisecond`.
- `TestWindow_CancelNoticeOnlyWhenSomethingIsPending` (coordinator Q7): idle with a named ask row → slot `cancelled`; idle with nothing named → slot empty; offered with a waiting queue row → slot `cancelled`. (M: always arm it.)
- `TestCost_OfferAndIdleWindowsWhileSomeoneWaits` (#10): `offer_wait_ms` covers offer → start (delivery delay included), `idle_wait_ms` the idle window; neither counts while nobody waits. (M: count only idle windows.)

**T12 Liveness** — `sweeper.go`, per F4: the view is read when `anyHeld() || anyLivePosition()`.
- `TestLiveness_AgentGoneAbandons`: queued, offered, idle.
- `TestLiveness_PidUnderAnotherSessionTwice`: once → kept; twice → `abandoned`. (M: once is enough.)
- `TestLiveness_UnreadableViewIsAlive`.
- `TestLiveness_RunningEndsAfterItsCommand`: the agent dies while running → held row `holder_gone` → position `abandoned`, not idle.

### P2b′-3c — drain, boot reconcile, rollback guards (~400 / 5)

Branch `feat/lease-v2-amend-p2b3c`. Per §2.9 and §2.4a.

**T12b Drain and boot** — `sweeper.go`, `boot.go`, `store.go` (`ExtendWaiting` skips `mode='queue'` rows).
- `TestDrain_LeavingLeaseEndsEveryLivePosition`: lease → advise; the next tick ends every live position `drained`, their queue and ask rows `drained`; a held row keeps running and its release ends it as today; a B poll answers `ended`/`drained`. (M: skip the drain outside lease.)
- `TestDrain_RunsInMeasureAndOff`.
- `TestBoot_DrainsWhenStartingOutsideLease`.
- `TestBoot_ReconcileRunsBeforeTheSampler`: a pair of each §2.4a case left in the file DB → repaired at boot, counts logged. (M: skip the reconcile call.)
- `TestBoot_GraceSkipsQueueRows`: a queue row keeps `lease_until=0` after boot. (M: drop the `mode` filter.)
- `TestRollback_TheP2aColumnListStillReads`: a frozen copy of `origin/main`'s `leaseCols` (`store.go:157-161`) selects every row of a migrated DB. (M: rename one column in the migration.)

### P2b′-4a — confirm / cancel / done routes (~550 / 5)

Branch `feat/lease-v2-amend-p2b4a`.

**T13 Routes** — new `position_routes.go`, `module.go:304-311`. The own-session check is `sessionOfProcess(holder_pid)` (T6), shared by the four routes.
- **confirm:** a waiting ask row of the caller.
  - Position offered or idle: `confirmed_at = now`, then pass(fresh=row) → step 3 grants it.
  - Queued: 409 `not_your_turn`.
  - Running: step 4.
- **cancel request:** a waiting row → `cancelled_by_session` (P14 may follow); held → 409 `running`.
- **cancel position:** queued/offered/idle → P12; running → 409 `running` (Q10).
- **done:** P13.
- Tests:
  - `TestConfirm_GrantsTheAskedCommand`: the pending long poll returns held, with `position_id`.
  - `TestConfirm_QueuedIs409`.
  - `TestCancel_RequestEndsCancelledBySession`: the B poll answers `ended`/`cancelled_by_session`.
  - `TestCancel_LastRequestEndsTheQueuedPosition`.
  - `TestCancel_RunningIs409`.
  - `TestCancel_PositionLetsTheNextIn`.
  - `TestDone_IdleEndsAtOnce`.
  - `TestDone_RunningEndsAfterTheCommandWithoutAWindow`.
  - **Own-session guard on every route (#17):** `TestOwnSession_AnotherSessionIs403` and `TestOwnSession_NoSessionIs403`, each with one sub-test per route (confirm, cancel request, cancel position, done); the target is unchanged afterwards. (M: drop the check from each route in turn; each sub-test turns red on its own route.)

### P2b′-4b — snapshot, event, report (~500 / 8)

Branch `feat/lease-v2-amend-p2b4b`.

**T14 Snapshot and event** — `handler.go` (`addLeases`), `events.go` (`hasLeaseActivity` counts live positions), `Snapshot.Positions` per §2.2.
- `TestSnapshot_ListsPositionsWithTheirCommands`: place, state, window left, k, T, warned, delivered, notice result, requests with mode, command and cwd; env is never present.
- `TestEvent_SentWhileOnlyAnIdlePositionLives`.

**T15 Report** (v2 D-7 extended; A §5) — `report_handler.go` (a positions query), `internal/resources/report.go`, `wire_report.go`:
- positions by end reason;
- commands per position (1/2/3/4+), re-queued positions;
- extensions;
- `offer_wait_ms` and `idle_wait_ms`: total / p50 / p90 / max each (#10);
- queued wait (`offered_at − created_at`) p50 / p90 / max;
- offers: delivered, passed undelivered (`offer_passes`), delivery delay (`delivered_at − offered_at`) p50 / max, lapsed after delivery (`no_answer`), and notice outcomes by `notice_result` (filled from P2d);
- grant paths `offer` and `continued` next to `immediate` / `waited` (`report.go:81-88`).
- `TestBuildReport_PositionFigures`.

### P2c-1a — clients: modes, queue / ask, script step, final notice (~600 / 7)

Branch `feat/lease-v2-amend-p2c1a`, from `origin/main` after P2b′-4b. Cherry-pick `c798af59c` and amend it (§6). Delete the stray empty file `cmd/pdx/--config` it added.

**T16 Modes** — `lease_acquire.go`, `lease_run.go`, `lease_cmd.go`. `--queue`, `--ask`, `--wait` choose the mode. Without one, `PDX_LEASE_CALLER` decides (`fg` → queue, `bg` → ask); with neither the mode is `auto` and the daemon decides (queue under an interactive session, else wait; see Q1). *(Shipped as P2c-1c.)* The env is read through `runLeaseCmd`'s `getenv` (`lease_cmd.go:68`), for both `run` and `acquire` (Q12).
- `--wait` keeps its old duration form for scripts and the old skill text. A custom flag accepts a bare `--wait`, `--wait=<dur>` and `--wait <dur>` (a token that parses as a duration is taken). The duration is accepted and ignored (Q11).
- `TestCaller_EnvPicksTheModeAndFlagsOverride`. (M: swap `fg` and `bg`.)
- `TestWaitFlag_BareAndDurationForms`.

**T17 Queue and ask** — `leaseAcquire`: queue mode POSTs once and never polls. Ask mode polls as wait mode does.
- An `ended` answer with `cancelled_by_session`, `no_answer`, `idle_timeout`, `done`, `released_after_limit` or `drained` → not run, exit 15, `T-ask-cancel` on stderr (`lease run`).
- R8: an ask request answered `waiting` with a `position_id` runs only on a `held` answer that carries a `position_id`; otherwise it releases and exits 15.
- `abandoned` and `cancelled` still fail open as today (`lease_acquire.go:247-250`).
- `run` sends `command` (argv joined, first 200 bytes; `--command` overrides, for the mod's B wrap) and `cwd` (`os.Getwd`). `acquire` takes `--command` and `--cwd`.
- Tests:
  - `TestAcquire_QueueModeNeverPolls`. (M: poll in queue mode.)
  - `TestAcquire_QueuedExits15WithoutCancelling` (kept from p2b).
  - `TestRun_QueuedDoesNotRunTheCommand` (kept).
  - `TestRun_QueuedOwnRunningText`.
  - `TestRun_AskWaitsThenRuns`.
  - `TestRun_AskCancelledExits15WithoutRunning`, including `drained`. (M: let it reach fail-open.)
  - `TestRun_AskRefusesAHeldWithoutPosition` (R8). (M: run on any held.)
  - `TestRun_AbandonedStillFailsOpen`.
  - `TestRun_SendsCommandAndCwd`.

**T18 Script step** — no env var (Q13 is void): `run` finds the agent pid from `CLAUDE_CODE_MESSAGING_SOCKET` (`/tmp/cc-socks/<pid>.sock`) and appends T-script when its parent is neither the agent nor a child of the agent (anything deeper is a step of a script), with the kind filled in. *(Shipped as P2c-1c.)*
- `TestRun_ScriptStepAddsTheWholeScriptLine`. (M: invert the comparison.)

**T19 The final notice** — `lease_run.go`, `lease_acquire.go`: `pdx lease release` gains `--notice` (adds `notice=1` to the DELETE). `run` releases by id with `notice=1` **after** the child exited, reads `notice`, and prints `pdx lease: <notice>` to stderr after the child's output (the deferred release is restructured; the path interrupted before the child started, `lease_run.go:118-123`, releases without `notice=1`).
- `TestRun_PrintsTheFinalNoticeAfterTheOutput`. (M: print it before `runChild`.)
- `TestRun_InterruptedBeforeTheChildSendsNoNoticeFlag`. (M: always send `notice=1`.)
- `TestRelease_NoticeFlagAddsTheQuery`.

### P2c-1b — clients: new verbs, `ls`, report (~450 / 6)

Branch `feat/lease-v2-amend-p2c1b`.

**T20 New verbs** — `pdx lease confirm <id>`, `pdx lease cancel <id>` (a `pos_` prefix goes to the position route), `pdx lease done`. Each sends `holder_pid = os.Getpid()`. Exit 0; 13 (`ExitRefused`) for 403 / 409; 2 for usage. The lines of §2.7.
- `TestConfirmCancelDone_SendTheirPidAndPrint`.
- `TestConfirm_RefusedExits13`.
- `TestCancel_PositionIDGoesToThePositionRoute`.

**T21 `pdx lease ls` and report** — `lease_cmd.go`, `lease_report.go`. A `POSITIONS` section, in place of the parked text:

```
POSITIONS  SESSION                       STATE    LEFT   K  T    WARNED
占用        mlab/purdex-1f-vq (_vq3x9a)   空窗      2m14s  2  41m  已預告
  queue  執行中  pnpm run build            ~/Workspace/wake/purdex/spa
#1         mlab/purdex-88-b8 (_b8k2p1)   排隊中    -
  queue  排隊中  go test ./...             ~/Workspace/wake/purdex
  ask    已詢問  npx vitest run …          ~/Workspace/wake/purdex/spa
```

- `#N` is the queue place; `占用` marks reserved positions.
- States: 排隊中 / 輪到了 / 執行中 / 空窗; an offered position not yet delivered shows 輪到了（通知中）. Request states: 執行中 / 排隊中 / 等待中 / 已詢問. A position whose notice went to a user alert shows `通知沒送到` (from `notice_result`, T34).
- `LEFT` is shown for offered (after delivery) / idle; `K`, `T`, `WARNED` for running/idle.
- The `WAITERS` section stays for wait-mode rows. Cells go through `sanitizeCell`.
- The report adds the T15 lines.
- `TestLs_PositionsSection`.
- `TestLs_OldDaemonStillPrints`.
- `TestReport_PositionLines`.

### P2c-2 — the mod, the skill, the probes (~700 / 6)

Branch `feat/lease-v2-amend-p2c2`, after P2c-1b is **merged** (the mod's tests call the new flags through fakes; the probes use the worktree's `bin/pdx`). Its deploy needs a binary that contains it (§5). Cherry-pick `eec9a8cda` and amend it (§6).

**T22 M-P2 probe (first, before any mod change).** Throwaway session, probe mod, default permission mode, a settings allowlist `Bash(git status:*)`. Record:
- (a) Prefixing `export PDX_LEASE_CALLER=fg PDX_LEASE_SHELL=$$` + newline:
  - Does `git status` still run without a prompt?
  - Does a read-only `ls` still run without a prompt?
  - Is the dialog text of a command that prompts the prefixed one?
- (b) `$$` expands to the tool shell, and `os.Getppid()` of a directly typed `pdx` equals it.
- (c) A background command rewritten to `pdx lease run --ask … -- "${SHELL:-/bin/sh}" -c '<cmd>'`: does it run, is its output in the background output, does the completion notice arrive?
- (d) The working directory `$.process.run` gives `pdx lease acquire`.
- (e) **Reload (#20):** rewrite the probe mod's folder as `pdx setup` does. Does `session.start` fire again, with `isInteractive`, before the next `tool.call`? Probe: a `session.start` hook that appends `isInteractive` and a counter to `.scratch/m-p2e.log`, and a `tool.call` hook that appends the flag it sees.
- (f) **Subagent (#20):** in a Task subagent's Bash call, is `e.agentId` set, and does `$.session.id()` equal the main conversation's id? Probe: log both from `tool.call` in the main conversation and in a subagent.
- (g) In a `claude -p` run with the probe mod: `session.start` has `isInteractive:false` (re-confirm MP7).

Results go in the PR body and into Q1 and Q14. **If (a) shows any permission change, stop and ask the lead (Q1) before T24.** Fallbacks: (e) no → lease.js asks the daemon once per mod load, through a new `GET /api/resources/caller?session=<sid>` that answers `{interactive}` from `modevents.Registry.SessionCapable(sid, CapPromptV1, CapsFresh)` (`internal/modevents/registry.go:273-285`: a live stream that announced the prompt capability, which only an interactive reporter does), +~80 lines in this PR; (f) the ids differ → lease.js passes the id it read at `session.start` as `--session`.

**T22 results (2026-10-14, Claude Code 2.1.299, a haiku probe session in default permission mode with a `--plugin-dir` probe mod, allowlist `Bash(git status:*)` and a few `Bash(<tool>:*)` rules; the table of (a) is under Q1):**
- (a) any prefix changes the prompts; see Q1 and the lead's decision (no prefix).
- (b) moot: no `$$` is sent any more (T18 reads the agent pid instead).
- (c) **The wrap `pdx lease run --ask … -- "${SHELL:-/bin/sh}" -c '<cmd>'` itself raises a prompt** ("A variable in braces in this command can't be checked before it runs"; with `"$SHELL"`: "Quoted text in this command can't be checked before it runs"), even with `pdx` allow-listed and the original allow-listed. With a **literal** shell path (`/bin/zsh -c '<cmd>'`) and `pdx` allow-listed it ran with no prompt, the output reached the background output, and the completion notice arrived. T25 therefore wraps with a literal shell (`$.env.get('SHELL')` if it is a plain path, else `/bin/sh`).
- (d) `$.session.cwd()` answers the session's directory; that is what `--cwd` carries.
- (e) after the probe mod's folder was rewritten the engine reloaded it ("reloaded (2 hooks …)") and `session.start` fired again with `isInteractive:true` (the module's variables started over). The mod no longer depends on it for the foreground (the daemon decides); it sets its `interactive` flag from `session.start` for the background wrap only.
- (f), (g) not measured: they served T24, which is dropped.

**T23 Queue answers** (kept from `eec9a8cda`): exit 15 counts only with a queued line. `{deny}` uses T-queued / T-queued-own; `next(e)` is not called and no release is sent. The `replaced` branch is removed. *(Amended, Q1: no mode flag at all.)* acquire gets `--command <e.command, first 200 bytes> --cwd <session cwd>` and **no** `--queue` / `--ask` / `--wait`: the daemon resolves `auto` (queue under an interactive session, wait otherwise, coordinator Q2).
- Kept from eec9a8cda, text updated: `queued: the command is not run, the model gets the queued text as a deny, and nothing is released`.
- `own_running: the deny says to wait for its own previous command`.
- Kept: `exit 15 without a queued line runs the command and releases`.
- `acquire passes the original command (first 200 bytes) and the cwd, and no mode`. (M: pass `--queue` regardless.)

**T24 ~~The caller export~~ — dropped (lead, Q1): the mod adds no prefix; `auto` (above) replaces it.** The original text follows for the record. (coordinator Q2) — lease.js registers `session.start` with the matcher `{ isInteractive: true }` (a matched registration; `embed_test.go`'s registration rows gain it) and keeps a module flag. In an interactive session, **every** Bash call, heavy or not, foreground or background, a subagent's included (its notices go to the main conversation, whose mod polls prompts), becomes `export PDX_LEASE_CALLER=<fg|bg> PDX_LEASE_SHELL=$$` + `\n` + the original. A non-interactive session (`claude -p`), or a load that has seen no `session.start` (T22 (e)), gets no export, so nested calls use wait mode. Not announced: it produces no output (M-R2's tampering signal was output that did not match).
- `every foreground Bash of an interactive session runs with PDX_LEASE_CALLER=fg exported`. (M: drop the export for non-heavy commands.)
- `every background Bash of an interactive session runs with PDX_LEASE_CALLER=bg exported`.
- `a subagent's Bash call is exported like the main conversation's`.
- `a non-interactive session's Bash is not prefixed`. (M: drop the interactive check.)
- The existing `non-heavy Bash passes through untouched (no process.run)` becomes `non-heavy Bash only gets the export (no process.run)`.

**T25 Background heavy → ask** (*amended: the shell is a literal path, see T22 (c); the wrap is made only when `$.tool.check` gives the same verdict for the original and the wrapped command, Q1*) — interactive sessions only. `run_in_background:true` and `classify` heavy: the command becomes `pdx lease run --ask --kind <k> --command <original, 200 B> -- <literal shell path> -c '<original, single-quote escaped>'` (no export, no variable: T22 (c)). A `context` note tells the model (Q15). A background command already wrapped in `pdx lease` is not wrapped again. A non-interactive session's background calls pass through as today (nothing can ask it).
- `a heavy background Bash is wrapped in pdx lease run --ask with its kind and command`. (M: skip the wrap.)
- `a background pdx lease run is left as it is, not wrapped twice`; a command with a `pdx lease` segment anywhere in it is not wrapped either (the inner run would find its own session's position running).
- `the wrap is announced in the context`.
- `a non-interactive heavy background Bash is not wrapped`.

**T26 The final notice in the result** — `r = await next(...)`; `rel = await releaseLease(... '--json', '--notice')`; notes += `rel.notice`; return. `finally` releases only if the release has not run yet (the throw path), **without** `--notice` (#2).
- `the final notice from the release answer is added to the tool result`. (M: release back in `finally` only.)
- `a release answer without a notice adds nothing`.
- `the throw path releases without --notice`. (M: pass `--notice` in `finally`.)
- Kept: `release runs even when the tool throws`.

**T27 Skill text** — `skills/pdx-lease/SKILL.md`:
- Queued is normal and is answered at once. 輪到時 mod 會通知你，照原樣重跑。
- 3-min continuation, `pdx lease done` optional.
- Background heavy commands are asked at their turn (`pdx lease confirm/cancel <id>`).
- Scripts: wrap the whole script with `pdx lease run --kind <k> -- ./script.sh`.
- Never `&` or `run_in_background` to bypass.
- `pdx lease ls` shows the whole queue; `cancel` acts only on your own requests.
- The two final notices and what to do.
- Remove "排太久會放行" and "預設最多等 5 分鐘".
- Update the pinned lines in `cmd/pdx/plugin/embed_test.go:541-555`.

**T28 result (M-P1): `next(e)` DOES resolve early.** A foreground command that runs past Claude Code's default 120 s timeout is moved to the background and `next(e)` resolves at that moment (a `sleep 160` call: resolved after 122 s with `backgroundTaskId` and `timedOutAfterMs: 120000`; the command finished 38 s later). A command killed by an explicit short `timeout` resolves at the kill (6 s for `timeout: 5000`, exit 143). So for a heavy command longer than 2 minutes the release (and the §3.3 end with `--notice`) fires at 2:00 while the process still runs. No behaviour change here; follow-up issue #2841.

**T28 M-P1 probe** (v2 §6, carried over): a heavy-classified foreground `sleep 150`-class command with a short Bash timeout. Does `next(e)` resolve when CC moves it to the background, and when does the release (now also the §3.3 end, with `--notice`) fire? Method and result go in the PR body. No behaviour change here; a follow-up issue if it does resolve early.

### P2d-1a — the notice job kind (~450 / 8)

Branch `feat/lease-v2-amend-p2d1a`. Touches the interface line's `promptq` and `events.js`: tell 88 before starting (Q16).

**T29 M-D1 measurement first** (A §4), on the CC version installed then, in a throwaway session with a probe mod:
- (a) `$.prompt.submit({text})` without `asUser` while idle: latency, framing, and whether `turn.start` fires (so `ev.turnId` covers it).
- (b) Called mid-turn: still blocks until idle (re-confirm `u3-plan:27`).
- (c) With a permission dialog open.
- (d) Right after `/clear`.
- (e) Fed T-offer, does the model re-run the named command? Fed T-ask, does it run `pdx lease confirm`?

Numbers go in the PR body. If (b) changed, stop and ask the lead.

**T30 The job kind** — `internal/promptq/queue.go`:
- `KindNotice`.
- `SubmitNotice(ctx, sessionID, key, text)`: at most once by `key` (the ledger); `busy` and `timeout` may be sent again under the same key. It needs an owner that announced `lease.notice.v1`; otherwise `no_mod`.
- `Owners` gains `OwnerOfCap(sid, cap)`, implemented in `internal/module/modevents/prompt.go` with `NewestCapableStream`. `CapLeaseNoticeV1` goes in `internal/modevents`.
- `TestSubmitNotice_NeedsTheCapability`. (M: use `OwnerOf`.)
- `TestSubmitNotice_BusyMaySendAgain`.
- `TestSubmitNotice_AtMostOnceByKey`.

**T31 The mod** — `workbook.js` CAPS adds `lease.notice.v1`. `parsePromptJob` accepts `notice` (≤ 4000 bytes). `events.js` `pqRun` → `pqNotice`: `busy` if `ev.turnId`; else `$.prompt.submit({ text })` **without** `asUser`, the way the relay seed goes in. `dropped` passes on the submit's `drop`.
- `notice job: idle → submitted as a plugin message (no asUser)`.
- `notice job mid-turn → busy, nothing submitted`. (M: drop the `ev.turnId` check.)
- `an unknown kind is still ignored`.

### P2d-1b — the notice outbox, delivery, `MainTurnRunning` (~750 / 6)

Branch `feat/lease-v2-amend-p2d1b`. Per §2.10 and §2.8. Touches `internal/module/agent/light_status.go` (one accessor): tell 88 with T30.

**T32 The outbox** — new `internal/module/resources/notices.go`: the slot sender of §2.10, `D1` on an accepted offer, the boot replay, and the `deliverable` seam wired to `MainTurnRunning`.
- `agent.Module.MainTurnRunning(sessionID) (running, ok bool)` beside `AbortedAt` (`light_status.go:32-43`): `TurnID != ""` of the session's stream within `lights.LiveWindow`. resources asserts the method on `c.Registry.Get("agent.module")` (`internal/module/agent/module.go:320`) at use time, before taking `stateMu`; missing → deliverable.
- Tests:
  - `TestNotices_OfferSentWhenIdle`: accepted → `notice_sent_at`, `delivered_at`, `window_until = delivered + 3m`.
  - `TestNotices_WindowCountsFromDelivery`: the fixture's mod answers `busy` for 90 s, then accepts → the window ends at delivery + 3:00, not offer + 3:00. (M: start the window at the offer.) (§2.8, #11)
  - `TestNotices_UndeliveredOfferPasses`: `busy` for 3 min → P15; the slot is cleared; the next waiter's offer goes out.
  - `TestNotices_BusyAndTimeoutRetryWithTheSameKey`. (M: give up on busy.)
  - `TestNotices_ErrBusyRetries`.
  - `TestNotices_UnknownIsNotADelivery`: `unknown` → retried with `notice_tries+1`; the window starts only at the later `accepted`. (M: treat `unknown` as delivered.) (#8)
  - `TestNotices_DroppedSessionChangedGivesUpOtherDropsRetry`.
  - `TestNotices_StaleOfferIsDropped`: the session started a command meanwhile. (M: drop the current check.)
  - `TestNotices_CancelOnlyWhenSomethingIsPending` (coordinator Q7).
  - `TestNotices_BootResendsPendingSlots` (#9): an undelivered offer, and a `cancelled` slot armed 2 min before the restart, are both sent after boot; one armed 11 min before is not. (M: replay offers only.)
  - `TestNotices_NoModGoesToTheFallbackHookAndKeepsTrying`.
  - `TestMainTurnRunning_FromTheStream` (agent package).

### P2d-2 — fallback without a mod channel (~450 / 8)

Branch `feat/lease-v2-amend-p2d2`. It pulls P3b's T24 (team seam) forward, and the event part of T26 (Q17).

**T33 Team seam** — `internal/team` gains `LeaderLookup.LeadOf(sessionID)` and a key; `internal/module/team` registers it. resources looks it up **at send time**, every time.
- Member: from its lead's inbox to the member's address (v2 D-6 member path: `OriginResolver.InboxOf` + `peers.Sender`, as `team/notify.go:80`). `ResultDelivered` and `ResultDeliveryUncertain` count as delivered (an offer's D1).
- `TestFallback_MemberGetsItFromItsLead`.
- `TestFallback_LeadLookupMissingIsRetried`.

**T34 Anything else: a user alert** naming the session (#13: the smaller option). It is the WS event `resources.alert` (rule `lease_notice`, the App contract shape of the v2 plan plus this rule), sent once per slot, and the position's `notice_result = 'user_alert'`; `pdx lease ls` marks such a position `通知沒送到` from that column (T21). **No persisted alert record**: `resource_alerts` stays in P3a (v2 plan T19), whose report may count `lease_notice` later. The push stays P3b's, behind `alert_push`.
- `TestFallback_SoloSessionIsAUserAlertNeverASelfSend`. (M: attempt the peer send.)
- `TestFallback_EventCarriesTheTextAndMarksThePosition`. (M: skip the mark.)

---

## 4. Migration and the rows that exist at the deploy

- P2b′-1a adds every column and the positions table (§2.1). No later PR of this line changes the schema, so each later deploy is code only.
- At the P2b′ deploys both hosts are in advise, and advise never makes a position (§2.3).
- Held rows from before the deploy end as today. They have no position, so no limits apply to them (T10 `TestCommandEnd_LegacyHeldRowEndsAsToday`).
- Waiting rows from before the deploy are wait-mode rows and keep today's long poll (boot grace 30 s).
- Parked rows are not expected (F5). `EndParkedAtBoot` ends any as `abandoned` at the first boot (T5).
- `deadline_s` and `park_s` remain valid settings that nothing reads. `wait_s` remains a valid request field that nothing reads (Q11).
- When the lead later switches a host to lease, positions start empty. The only queue entries are new requests.

## 5. Compatibility, deploy order, acceptance, rollback

| combination | what happens | why it is fine |
|---|---|---|
| new daemon (P2b′) + old CLI and mod (main) | no `mode` → wait; old `pdx lease acquire`/`run` behave as today | advise grants at once; in lease the old mod blocks up to its 600 s cap then fails open (`lease.js:388,497`); `pdx setup` hot-reloads every running session's mod (memory: reload notice), so old mods do not outlive the deploy |
| new CLI (P2c-1) + old daemon | POST with `mode` is 400 (`DisallowUnknownFields`) → `postMayHaveLanded` false → fail open, the command runs | the CLI and the daemon are one binary: they differ only between the install and the daemon restart |
| new mod (P2c-2) + old CLI | `--queue` is a usage error, exit 2 → `readAcquire` null → fail open; **the B wrap would not run the background command** | the mod is embedded in the binary (`cmd/pdx/plugin/embed.go:13-17`, handed to setup at `cmd/pdx/main.go:57-59`), so a `pdx setup` run from the binary that contains P2c-2 installs a mod and a CLI that match; `pdx.json` points at that installed binary |
| new daemon (P2d) + old mod between restart and `pdx setup` | no `lease.notice.v1` owner → `no_mod` → fallback, while the mod channel is retried | advise: no positions, so no notices |
| codex sessions, scripts, launchd, `claude -p` (no flag, no `PDX_LEASE_CALLER`) | mode `auto`: the daemon finds no interactive session above the caller → wait mode, today's behaviour | A §2 "S unchanged"; coordinator Q2; lead Q1 |
| older daemon after a rollback | reads named columns only; would grant leftover ask rows | §2.9: drain first; R8 as the second line |

**Order:**
1. P2b′-0 → P2b′-4b merged; the lead deploys the binary on both hosts and restarts the daemons, mode advise. Post-deploy checks (below).
2. P2c-1a, P2c-1b merged. Optional: deploy that binary on both (no `pdx setup` needed).
3. P2c-2 merged. **Build the binary from the `origin/main` that contains P2c-1a, P2c-1b and P2c-2**, install it on both hosts, restart the daemons, then `pdx setup` **from that binary** on both (the mod and the skill) (#15).
4. P2d-1a, P2d-1b, P2d-2 merged; the binary from that `origin/main` on both, restart, `pdx setup` on both.
5. The acceptance run (below).
6. The lead switches both hosts to **lease** (`PUT /api/hostconfig/resources`) and tells the user (V22). Post-switch verification (below).

The lead may batch bumps (P2b′ in one deploy, P2c in one, P2d in one). Within any batch, `pdx setup` runs from the binary that was installed. air26 is upgraded together with mlab, `pdx setup` included (memory). The `relays_active=0` gate is checked before each restart and each `pdx setup` (memory).

**Post-deploy checks (every deploy, both hosts, #14):**
- The daemon log since the restart has no `[resources] resources.db: … measuring only` line (`module.go:278-282`: a failed open is logged and leaves the store nil) and shows the boot reconcile counts (0 expected before the switch).
- `sqlite3 "<data>/resources.db" "pragma table_info(resource_leases)"` lists `position_id`; `select count(*) from resource_positions` runs.
- A smoke in advise: `pdx lease acquire --kind <test> --json` answers `held` with an `id` (a nil store answers 503 `not_ready`, `lease_handler.go:88-91`, which the CLI fails open on: that is a failed check), then `pdx lease release <id> --json` answers `ended`; `pdx lease ls` and `pdx lease report` print.

**Acceptance (after P2d is deployed on both hosts, before the switch; the lead runs it; #21).**
- **mlab, full:** the lead switches mlab to lease for the run window only, with nobody else running heavy work. Two throwaway interactive sessions on `tmux -L lease-accept` with a test kind:
  - F queued → deny, then the offer lands between turns, the re-run starts.
  - An offer while the session is mid-turn: delivered when the turn ends, the window counts from then (§2.8).
  - B → ask → confirm runs it; a second B → cancel.
  - Offer lapse after delivery → T-cancelled.
  - Three short commands with a waiter → T-final-three, then release; a waiting B of the released position is re-queued and asked at its turn.
  - `pdx lease ls` from a third session shows all of it; `pdx lease report`.
  - Switch back to advise: every live position drains (§2.9); `pdx lease ls` shows none.
- **air26, smoke:** the post-deploy checks, then lease for a short window with two throwaway sessions: one F queued → offer delivered → re-run; one B → ask → confirm; back to advise, drained.
- Anything that fails here stops the switch.

**Post-switch verification (first hour, then the next day's report):** on both hosts `pdx lease ls` and `pdx lease report` show positions moving, offers delivered (`delivered_at` set) and no `通知沒送到`, no store errors in the log.
**Rollback condition:** any of — an F session never offered while its host was free for over a minute; an ask row run without a confirm; a store error or a reconcile repair after the switch; repeated `user_alert` or passed offers for sessions that were idle. Then switch both hosts to advise (drain, §2.9) and investigate before anything else.

## 6. What becomes of `feat/lease-v2-p2b`

The branch is not merged; it is a source of commits.

| commit | kept | changed |
|---|---|---|
| `c798af59c` CLI exit 15 / parkable / client_id polls | `ExitQueued = 15` (`exitcodes.go`); `acquireOutcome.queued`; the acquire line's `state` and `position`; queued → exit 15, no DELETE; `lease run` prints the queued text, does not run, does not release; polls send `client_id`; the overrun notice removed | `Parkable: true` → `Mode` + `command` + `cwd`; `ParkedUntil` and 「位置會保留 P 分鐘」 removed; `queuedText` → T-queued / T-queued-own / T-script; `replaced` no longer expected (kept only as a defensive "do not run"); queue mode does not poll at all; **drop the stray empty `cmd/pdx/--config`** → P2c-1a |
| `4f8d4f3ed` replay compares a session only if the client sent one | entirely, with `TestLeaseCreate_ReplayWhenOnlyTheSecondLookupFindsASession` | the resolved session now also selects the position → cherry-picked into **P2b′-1a** (daemon code) |
| `eec9a8cda` mod `{deny}` on queued | `EXIT_QUEUED` and its read; `{deny}` without `next(e)`; no release for queued; the overrun note removed; the tests' shape | `queuedDeny` text → T-queued / T-queued-own; the `replaced` branch removed; acquire gains `--queue --command --cwd` (interactive only); plus T24–T26 → **P2c-2** |

---

## 7. Open technical questions (each with the plan's default)

**Settled by the coordinator (2026-10-14):** Q2 (export only in interactive sessions and their subagents: T22–T25), Q3 (ask rows re-queued at the back: P7, T10), Q6 (a second command starts whenever the table grants: §2.5 step 4), Q7 (cancel notice only when something is pending: §2.7). They stay numbered below for reference.

1. **Prefix vs permissions (F3, M-P2).**
   - If prefixing every Bash command changes allowlist or read-only prompts, the spec's mechanism costs prompts.
   - Default: implement per spec if M-P2 is clean.
   - Guarded variant (no spec change): before prefixing, the mod asks `$.tool.check` for the original and for the prefixed command (d.ts `:3032-3041`; no dialog, nothing runs). It prefixes only when both verdicts are the same. Otherwise it leaves the command alone, so a nested `pdx lease run` falls back to wait. M-P2 also measures the cost of the two checks per Bash call.
   - Fallback needing a lead decision: do not prefix foreground commands. Prefix background commands only (they are rewritten anyway). Make `pdx lease run`/`acquire` default to queue mode when the daemon finds a session for the caller, and wait only without one. That changes A §2's "unset → wait" for agents.
   - **M-P2 (a) measured (2026-10-14, Claude Code 2.1.299, default permission mode, allowlist `Bash(git status:*)`, a haiku probe session with a `--plugin-dir` probe mod rewriting `tool.call`):**

     | command sent | `git status` | `ls` |
     |---|---|---|
     | no prefix | no prompt | no prompt |
     | `export PDX_LEASE_CALLER=fg PDX_LEASE_SHELL=$$` + newline (the spec) | prompt | prompt |
     | `export PDX_LEASE_CALLER=fg` + newline (no `$$`) | prompt | prompt |
     | inline `PDX_LEASE_CALLER=fg <cmd>` | — | prompt |
     | inline `PDX_LEASE_CALLER=fg PDX_LEASE_SHELL=$$ <cmd>` | not measured | prompt |

     With `$$` the dialog reason is "'export' operand 'PDX_LEASE_SHELL=${…}' is runtime-determined and may carry an array subscript"; without it, "requires approval". The dialog shows the **prefixed** command. So a prefix of any shape costs a prompt on allow-listed and read-only commands.
   - **Decision (lead, 2026-10-14): none of the three options as written; the foreground gets no prefix at all.** (1) The user's rule is "a session never sits on a pending lock", and the guarded variant falls back to wait exactly where an allowlist hits, so a nested call would block the session; (3) extra confirmations are out. Instead:
     - (a) A foreground Bash gets nothing added. `pdx lease run` / `acquire` with no flag and no `PDX_LEASE_CALLER` send mode `auto`; the daemon resolves it at create time: an agent session above the holder pid that is interactive (`SessionCapable(sid, CapPromptV1)`) → queue, anything else → wait. A daemon without `auto` answers 400 and the CLI sends wait. *(P2c-1c.)*
     - (b) A background heavy command is wrapped in `pdx lease run --ask` (T25) only if `$.tool.check` gives the same verdict for the original and the wrapped command; otherwise it is let through as today, with a context note "這個背景指令沒有納入資源排隊". A `pdx lease run` nested in an unwrapped background command takes `auto` → queue (exit 15, the session is told when it is its turn and re-runs). *(P2c-2.)*
     - (c) T-script no longer needs `PDX_LEASE_SHELL`: the CLI walks up to the agent (T18). *(P2c-1c.)*
   - Most live sessions on this host run `--dangerously-skip-permissions`; the product must still be right in default mode, hence no foreground prefix.
2. ~~`claude -p` and subagents~~ — **coordinator:** no export in non-interactive sessions (wait mode); subagents are exported like their main conversation, whose mod gets the notices. How the mod knows: `session.start.isInteractive` (F3); T22 (e)–(g) measures the reload and subagent cases, with fallbacks.
3. ~~Ask rows when their position is released after the limit~~ — **coordinator:** re-queued at the back as a new position of the same session.
4. **Positions in advise.** Default: none (lease only), so the code runs for real only after the switch, hence the acceptance window in §5.
5. **"Someone is waiting" includes wait-mode rows** (S, old clients), not only queued positions (A §3.3 says positions). Default: include them.
6. ~~A second command of a running position while someone is queued~~ — **coordinator:** follow the spec; it starts when the table grants it, and counts toward V19–V21 (§2.5 step 4).
7. ~~The cancel notice when nothing of the session is pending~~ — **coordinator:** sent only when the session has an unanswered B question or an F request it has not re-run.
8. **Several queue rows in one offer:** one message, the T-offer lines joined; at most 10 rows.
9. **`/clear` ends a position** (the agent pid listed under another session id twice). A §3.1 says "the session's agent process", which survives a `/clear`. Default: end it, since the new conversation cannot re-attach anyway.
10. **Cancel of a running position:** default 409 `running` (`done` lets it go after the command). Alternative: treat it as `done`.
11. **`deadline_s`, `park_s`, `wait_s`, `--wait <dur>`** stay accepted and unused. The hostconfig normaliser rejects unknown keys, so removing them would invalidate stored settings. A later cleanup can drop them.
12. **`acquire` reads `PDX_LEASE_CALLER` as `run` does** (A §2 names only `run`); both send `auto` when it is unset.
13. ~~`PDX_LEASE_SHELL=$$`~~ — void (Q1): T-script reads the agent pid from `CLAUDE_CODE_MESSAGING_SOCKET` and the parent chain. A script that the shell `exec`s as its last command looks like a direct call and gets no T-script line.
14. **The cwd of a mod-managed F command** is the session's start cwd (`events.js:717`), not the Bash tool's current directory, unless M-P2 (d) shows something better. A `cd x && …` is still visible in the command text.
15. **The B wrap's `context` note** must exist: M-R2, the model reports tampering otherwise. Proposed: 「Purdex mod 把這個背景指令交給 `pdx lease run --ask` 排隊（主機資源規則）：主機有空就立刻執行；忙的話輪到時會先問你。實際執行的是：<command>」. It runs under `"${SHELL:-/bin/sh}" -c`, without CC's shell snapshot (aliases and functions).
16. **The channel touches the interface line's files** (`internal/promptq`, `events.js`, `workbook.js` CAPS, and now one accessor in `internal/module/agent/light_status.go`): a new kind, a capability and a read-only accessor, no change to App behaviour. Coordinate with 88 before P2d-1a. The alternative is a lease-only long poll from the mod, but then "no mod" cannot be told.
17. **The fallback needs P3b's T24 (team seam) and the event part of T26.** Default: move them into P2d-2, without a persisted record (#13). The push stays in P3b behind `alert_push`, and 88 gets the extra rule `lease_notice`.
18. **When the offer window starts** — see §2.8 (decided: from delivery, user 2026-10-14). Replaces revision 1's "3 min from the offer, an offer to a long turn always lapses".
19. **Inline secrets:** a command like `TOKEN=… pnpm build` is stored and shown verbatim (first 200 bytes, 14 days, every session and the App). The spec accepts showing commands; masking assignment values would be a small addition.

**New in revision 2 (user-visible, spec silent):**

- **QN1 — decided (coordinator, 2026-10-14): yes, every switch out of lease drains** (advise means nothing queues, so nothing is kept; the plan default stands, T-drained text as proposed). Original question: Does a plain switch from lease to advise drain the queue? A drain is required before a downgrade (§2.9). The plan's default also drains on every switch out of lease: every queued session loses its place, a waiting background command is cancelled (not run), and sessions with something pending get T-drained (proposed: 「主機資源改回不排隊模式，你在佇列裡的位置已經放掉；現在直接重下指令就會執行。」). Alternative: keep the positions frozen until lease returns, and drain only through an explicit `pdx lease drain` before a downgrade; then queued sessions are told nothing while the host is in advise.
- **QN2 — decided (coordinator, 2026-10-14): yes, move such `own_running` foreground rows into the re-queued position too** (the promised notice must come). Original question: A foreground command queued behind its own running command, when that command is the announced last. Example: the session got T-final-three with command 2; during command 3 it issues command 4, which is answered T-queued-own (「等你自己的上一個指令跑完。輪到你時我會通知你」); command 3 ends and the position is released. Per the coordinator's Q3 decision only background rows are re-queued, so command 4's row ends and the promised notice never comes. Proposed: also move such `own_running` foreground rows into the re-queued position (one more condition in P7's transaction), so the session is offered command 4 at its new turn.

---

## 8. Changes to the v2 plan's P3a (anomaly rules) — A5 reads positions (#12)

A §6: "A5 queue stuck reads positions". When P3a starts, its tasks change as follows (the v2 plan's text is otherwise kept):

- **T20 (`AnomalyInput`, v2 plan :262-276):** the head is the pass's head entry, not a waiter row: the oldest **eligible** queue entry of §2.5 step 5 — a `queued` position (`{ID: "pos:<id>", CreatedAt: created_at}`), skipping a passed position that is not eligible (§2.8: it waits for its session, not for the host), or a wait-mode row. `Reason` / `ReasonSince` come from the reasons state under the same key (`pos:<id>` or the row id, §2.5 step 6). An `offered` or `idle` position is never a waiting head.
- **A5 (v2 spec :127-134):** "parked time included" no longer applies (nothing is parked). Diagnosis (c)'s "no lease running" reads "nothing running or reserved" (reserved counts as running, A §3.1), so a host held by an offered or idle position is diagnosis (a)/(b) material, not (c).
- **T22 (check step, v2 plan :280-292):** builds the head from `LivePositions()` and the wait-mode rows, with the same eligibility seam as the pass; a queue-mode row's `lease_until = 0` is never read as "the head left".
- **New tests in T20/T22:** `A5 finds a queued position as the head` (queue rows have `lease_until=0`); `A5 skips a passed position that waits for its session`; `a host reserved by an offered position is not diagnosis c`.
- **T23 (status surfaces):** may count `lease_notice` user alerts in the report once `resource_alerts` exists (T34 sends the event only).
