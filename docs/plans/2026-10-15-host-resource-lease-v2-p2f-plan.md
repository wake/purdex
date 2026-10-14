# Host resource lease v2 — P2f: nested runs, and an offer that cannot be told lets go (#2759)

Found by the 735 acceptance run on mlab (2026-10-15, lease for 23 minutes, then back to advise). Parent plan: `2026-10-13-host-resource-lease-v2-amend-a-plan.md`.

## Findings

1. **Nested `pdx lease run`.** The Aigora lead ran `pdx lease run --kind test -- pnpm check --fast`; `check.mjs` spawns `pdx lease run --kind build -- pnpm run typecheck`. The inner run is a new request of the same session whose position is running: the pass answers `queued` with `own_running` (exit 15, T-script text) and the whole check fails. The outer lease's descendants are not recognised as the same job.
2. **A notice that cannot be told holds the position.** Position a0cda21a stayed `offered` with `notice_result = user_alert`, `sent_at = 0` until the drain. The offer only passes after the full 3 minutes (`positions_sweep.go`, `PositionWindow`), and the lead's `pnpm run typecheck` behind it waited the whole time.
3. **Why the notice could not be told (cause, measured).** `GET /api/mod/streams` lists that session (stream `KzwKnpYc38Z7osmEWN6092`, sid a0cda21a, cc 2.1.296) with `mod_version 1.0.0-alpha.734`, first seen 00:24:25 (the daemon restart re-registered it). The sessions started after the deploy (cc 2.1.299, e.g. the three acceptance sessions) show `1.0.0-alpha.735`. So the running Claude Code kept the old mod through `pdx setup`'s "hooks changed, reloaded": every session that was already running at the deploy has no `lease.notice.v1` owner, and `promptq.NoMod` → fallback → `user_alert` is the designed answer. The streams endpoint does not list capabilities; the version is the evidence. All twelve long-running interactive sessions on mlab (including this one) showed 734; the five started after the deploy showed 735.
   - **Why it blocks the switch (lead, 2026-10-15):** a 734 mod in lease mode runs a foreground heavy command with the old `--wait` behaviour and can stall for up to 600 s (`ACQUIRE_TIMEOUT_MS`), which is the stuck session the amendment set out to remove. So **every interactive session must run the new mod before the switch**; how (restart the sessions, or fix #2871 first) is the lead's talk with the user. Mod-lease nesting is a separate issue.
   - Not a P2f fix: noted as an issue (the reload does not reach running sessions' mods), and a deploy check is added: before the switch, `GET /api/mod/streams` must show no interactive stream below the deployed mod version, or those sessions are restarted / told.

## Decisions (lead, 2026-10-15)

- **N1.** The daemon's process ancestry is the authority. An environment variable (`PDX_LEASE_HELD`) is **not** added: the mod's lease cannot carry one, `nohup` / `setsid` / `env -i` cut it, and a stale copy in a long-lived child (a tmux server started from inside a run) would let it skip every later lease. One extra POST per inner run is the price.
- **N2.** A probe that fails or times out is fail-open for this question only: it is the existing `failed` branch (503 `not_ready`; the CLI gives up after its grace and lets the command run).
- **N3.** The mod's foreground lease is **not** covered. The mod calls `pdx lease acquire` through `$.process.run` with no `--holder-pid` (`lease.js:609`), so the holder is the parent of that `pdx`, which is the agent process; "descendant of the holder" would then be the whole session and would wave through a separate background heavy command. The Bash tool's shell pid is not available to the mod (only `tool_use_id`). Covered: the outer is a `pdx lease run`. The mod already skips a command that has a `pdx lease` segment of its own (`hasLeaseSegment`), so the common nest (an outer `pdx lease run` written by a person or a script, inner runs in its tree) is the case that matters. A mod-lease nest is a separate issue.
- **N4.** A delivery that falls back to `user_alert` lets the offer go after **60 s** (the offer passes, the position keeps its place), not 3 minutes.

## Design

### P2f-1. Nested answer (daemon, CLI)

In `postLease` (`lease_handler.go`), after the session and the agent pid are resolved and before the replay lookup's create path, for `scope process` only:

```
nestedUnder(ctx, holderPID, agentPID, session) → (outer leaseRow, ok, failed)
```

- Walk from `req.HolderPID` upward with `ancestry` (same table and hop bound `maxAncestryHops` as `sessionOfProcessChecked`), stopping **before** `agentPID` (the agent itself is never an outer holder: that is N3). For each ancestor pid `a` (the caller itself is skipped: a repeat of one's own request is the replay path), look for a **held**, scope-process row of the same session whose `holder_pid = a` and whose `holder_start` equals the ancestor's start at second precision (a reused pid is another process).
- Found: answer `201`/`200` with `State held`, `Granted true`, `ID ""` (the answer already means "no row was recorded" for mode off / measure, so `lease run` and `lease acquire` skip the release), a new field `Nested string` carrying the outer id, `Mode` as is. No row, no position, no admission pass, nothing in the report except a counter (`nested`, below). The command runs under the outer lease's weight.
- Not found: unchanged. The position/queue path still queues a second request of a session whose position is running (the `own_running` case stays for a request that is **not** below the outer one, e.g. a sibling background run).
- Applies in every mode (advise answers held at once anyway; the only visible difference is "no second row").
- The walk's table read failing returns `failed` → the existing 503 `not_ready` (N2).

`cmd/pdx`: `lease run` and `lease acquire` treat `Nested != ""` as held with nothing to release; `acquire --json` prints it. T-script's `own_running` text is untouched.

### P2f-2. The offer passes at 60 s after `user_alert` (daemon)

`positions_sweep.go`, the undelivered-offer case: the age limit is `resources.PositionWindow` (3 min) as today, or `resources.NoticeFallbackPass` (60 s) when `p.NoticeResult == noticeUserAlert`. `positionRow` carries the slot's result already (the drain log shows it); if not, the sweeper's SELECT gets the column. `PassOffer` and the log line are unchanged (the line says "not delivered in time and passes").

### P2f-3. Report and `ls`

- `pdx lease report`: `nested n` in the positions block (a count of nested answers, from a column `nested_runs` on the outer row; the outer row is the only place a nested answer can be attributed, so the count is `UPDATE resource_leases SET nested_runs = nested_runs + 1`, a best-effort write that never fails the answer). No schema change beyond that one column; `ADD COLUMN` through the store's existing migrations.
- `pdx lease ls`: unchanged.

## Tests (TDD)

- `TestNested_RunUnderRunIsHeldWithoutARow`: outer `run` row (holder = pdx pid A), a request whose holder pid has A as an ancestor, same session → `held`, `ID ""`, `Nested = outer id`, row count unchanged, no queue entry.
- `TestNested_SiblingIsStillQueued`: a request that is not below A (same session) → `queued` `own_running` as before.
- `TestNested_AgentIsNotAnOuter`: a mod-style held row whose holder is the agent pid never makes a descendant nested.
- `TestNested_ReusedPidIsNotAnOuter`: ancestor pid equal but start time differs.
- `TestNested_ProbeFailureIsNotReady`: ancestry error → 503 `not_ready`.
- CLI: `TestLeaseRun_NestedAnswerRunsWithoutRelease`, `TestLeaseAcquire_NestedAnswerPrintsNested`.
- `TestSweep_UserAlertOfferPassesAt60s`, `TestSweep_UndeliveredOfferStillWaits3Min` (the other result), boundary at 59 s / 60 s.
- Mutation: the agent exclusion removed (N3); the start-time check removed; 60 s applied to every undelivered offer; nested rows created anyway.

## Acceptance (after the deploy, with the rest of the 735 acceptance)

Re-run the items the first run did not reach (B cancel, an offer while the session is mid-turn, offer lapse → T-cancelled, a waiting B re-queued after a release, air26 smoke) **plus** the nested case: an outer `pdx lease run --kind test -- sh -c 'pdx lease run --kind build -- sleep 5'` completes, `pdx lease ls` shows one row, the report counts one nested. Before the switch: `GET /api/mod/streams` shows no interactive stream with a mod older than the deployed one (finding 3).

## Out of scope

- A nested answer for the mod's foreground lease (N3).
- Making a running session reload its mod (finding 3): an issue.
- "Heavy processes that are not under a lease are visible" and the "-run classification" are the next two PRs.
