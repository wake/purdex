# Profile Sync — spurious conflicts & keep-local undo (2026-09-28)

## Symptoms (field report, a19 + a26 attached to the same profile)

1. A `tabs.<ws>` section locks as `locked:conflict` with nobody editing anything — the moment an agent
   starts / exits / gets probed on a host both clients watch. Recurs (Infra workspace reached rev 92).
2. After the user rebuilt a terminated pane **while its section was locked** and then chose
   「保留這台裝置的」(keep local), the pane came back terminated ("a26 已不存在") on its OLD session
   code, with the OLD run's `agentExited` — the rebuild was undone. The live session (`$4`/`hakmez`)
   was fine on the host.

## Root causes

### RC1 — automatic record writes carry the writer's local clock

Every attached client reacts to the same host event and writes the same pane of the same synced
section. `applyRebuildPatch` (`spa/src/stores/useTabStore.ts`) stamps `capturedAt: Date.now()` for
every patch kind, and the SessionStart / provenance writers stamp `agent.updatedAt: Date.now()`
(`useAgentStore.ts writeProvenanceRecord`, `lib/rebuild/provenance-probe.ts`). Two clients produce the
same content with timestamps a few ms apart → different hashes → the daemon's per-section CAS can not
fold them ("rev differs, hash equal → just the rev" never applies) → 409 / row-8 → lock.

Evidence: SOT `tabs.h3wy6i` rev 37 has `agentExited.at = …494251` and `capturedAt = …494313`.

### RC2 — keep-local restores the snapshot SENT at 409 time, not what the stores hold

`sync-state.ts` `resolved keep:'local'` sets `restoreLocal = { hash: conflict.localHash }`; row 0c
then writes that snapshot back into the stores. An edit made while locked (here: Rebuild re-pointing
the pane) only moved `currentHash`, so it is overwritten. `executor.ts answerFor` already guards the
automatic path against exactly this ("keep-local restores the SENT snapshot, which would undo the edit
made since"); the user's `resolve()` has no guard. `SectionLock.currentHash` is documented as "what
keep local would push", contradicting the reducer.

## Decisions

### D1 — automatic writes are deterministic (fixes RC1)

A write driven by a host event must produce byte-identical content on every client that sees the same
event. Timestamps in such writes come from the event / the daemon, never from the client clock:

| Patch | `capturedAt` | `agent.updatedAt` |
|---|---|---|
| `agent-exit` | `exited.at` (daemon, ms) | untouched |
| `agent-group` (SessionStart envelope) | event `broadcast_ts` ns → ms (ignored whole when older than the recorded agent) | same value |
| `agent-backfill` fill / replace (provenance answer) | max(prev, answer `started_at` (the frame's start; new daemon field) ns → ms) | answer value |
| `agent-backfill` confirm | unchanged (as today) | unchanged |
| `probe-cwd` | **not re-stamped** (keeps `prev.capturedAt`) | — |
| `unverified` | **not re-stamped** | — |
| `field` (user edit) | `Date.now()` — unchanged: two humans editing IS a conflict | — |

A daemon value of 0 / missing falls back to `Date.now()` (old daemon; no worse than today). The ns → ms
conversion with that fallback lives in one helper, `daemonNsToMs` (`lib/rebuild/provenance.ts`).
The converted value must also be a safe integer inside a **fixed** window, 2020-01-01 ≤ ms ≤
2100-01-01 (constants, never relative to `Date.now()`, so every client judges the same value the same
way); anything outside takes the same `Date.now()` fallback — a garbage far-future stamp would
otherwise win every `groupForBatch` election for good.

Why `started_at`, not `last_seen_at`: `last_seen_at` moves on every hook event of the run, so two
clients' probes of the same run usually see different values. The frame's start is fixed for the
life of the run. The daemon's provenance answer (`internal/module/agent/provenance_handler.go`) gains
`started_at` (the frame's `StartedAt`, same unit as `last_seen_at`) for this.

**Ordering rules for `capturedAt` (review round 2).** Both compare only daemon-clock values against
synced content, so every client decides identically:

- `agent-group` whose `record.agent.updatedAt` is **older** than `prev.agent.updatedAt` is ignored
  entirely (identity, cwd, `capturedAt` untouched) — a late SessionStart (reconnect replay, slow
  client) must not roll the pane back to a previous run. Only `agent.updatedAt` is compared, never
  `capturedAt`, which may be a user edit's client clock. Equal or newer applies; a prev record with
  no agent / no `updatedAt` always accepts.
- `agent-backfill` fill / replace stamp `capturedAt = max(prev.capturedAt, t)` (t = the answer's
  daemon-derived stamp), so a backfill never moves the election stamp backwards past a newer write.

`capturedAt` elects the group's newest record (`groupForBatch`). A probe filling a missing cwd and an
unverified flag learn nothing that should win that election, so not re-stamping them is correct.
The exit's daemon time and SessionStart's broadcast time are the real moments the content changed.

Residual (accepted): a client on an old daemon (no `started_at`) still stamps its own clock, as
before. Two clients whose probes are answered by DIFFERENT root frames (the owner changed between the
two probes) write different content anyway — that is a real difference, not a timestamp one. The
backfill's `agent.updatedAt` is shown by the Rebuild panel as "running when last seen"; it now shows
the run's start rather than the moment this client probed.

### D2 — keep-local keeps what this device shows NOW (fixes RC2; confirmed by the user 2026-09-28)

「保留這台裝置的」keeps the current local content, including edits made after the lock opened.
`resolved keep:'local'` sets `restoreLocal: null` **unconditionally** (when the stores still hold the
sent snapshot there is nothing to restore anyway — `finish()` used to drop such a restore at once).
The section is then dirty against `base = sot` and the ordinary table pushes (or deletes, when the
current local side is absent). The restore machinery (row 0c, `local-restored`, the executor's
`restoreLocal()`, the parked restore) is no longer reached from any event; it is left in place in this
PR and removed by a follow-up.

After a restart the same rule holds: stores are persisted, `currentHash` is their hash.

`answerFor` drops its `conflict.localHash !== currentHash` guard: under the `push` direction a 409
whose sent snapshot the stores no longer hold is answered keep-local like any other — which now pushes
the live stores, i.e. exactly what `push` means.

The conflict UI follows: the "keep local also undoes what you changed here since" lines (row and
dialog) are removed, and the local-side count is read from what the stores hold now (`currentHash`),
not from the stash of `conflict.localHash`.

## Out of scope

- `repointPane` carrying the previous run's `agentExited` into a rebuilt pane (it drives the Rebuild
  panel's "resume" default; changing it is a product decision of its own).
- Auto-resolving conflicts whose payloads differ only in timestamps.

## Tests

- Repro `spa/src/lib/profile/executor.keep-local-live.integration.test.ts` (rebuild while locked → keep local →
  pane stays on the new session, not terminated) goes green.
- Reducer: `resolved keep:'local'` with `currentHash !== conflict.localHash` → `restoreLocal === null`,
  `base = conflict.sot`, next decision push; with equal hashes → behaviour as before.
- Store: each automatic patch kind produces identical content when applied at two different wall
  clocks (fake timers), `field` still stamps the clock.
- Writers: `writeProvenanceRecord` uses `broadcast_ts`, the provenance probe uses `lastSeenAt`, with the
  0-fallback.
