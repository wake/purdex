# #1866: Worker status deltas on the host stream (design v3)

Status: v3, for an incremental codex round 3.
- v2 answered the coordinator's six decisions and codex round 1 (`task-muyhy7u8-pw7x2s`, 19 findings).
- v3 answers codex round 2 (`task-muyime61-up5dy4`, 10 findings, all accepted, with the coordinator's rulings) and one self-found fix (the safety reconcile now takes the slot per page, §3.7).
- §8 maps round 2; §7 keeps round 1.

Author: purdex-6d. Base: main (alpha.587). Nexen pinned v0.19.0.

`N/` = `~/Library/Caches/go/pkg/mod/lab.protype.tw/wake/nexen@v0.19.0/`. Other paths are relative to the Purdex repo.

**Already shipped:** PR0 (#1870, alpha.587). The site-wide SSE now opens with `?kind=` (Nexen's declared durable kinds + `result`), and a client-side noise denylist stops token frames from postponing the list refetch. This stays as the legacy path (§4.6).

**Nexen issues** (coordinator decision 4):
- [nexen#162](https://lab.protype.tw/wake/nexen/issues/162): silent state transitions.
- [nexen#163](https://lab.protype.tw/wake/nexen/issues/163): no row revision; `updated_at` is not bumped by permission, rollup, archive or title writes.
- Use case added as a comment on the existing [nexen#33](https://lab.protype.tw/wake/nexen/issues/33): envelope with execution attribution.

## 1. Findings

**F1. Frames on the site-wide SSE carry no execution attribution.**
- A frame is `id: <seq>` / `event: <kind>` / `data: <stored payload>`, with no envelope (`N/api/sse.go:293-315`).
- Raw provider payloads pass through verbatim (`sse.go:337-338`), and they are an open set, so nothing *guarantees* where an `execution_id` key might appear. What matters is that there is no reliable attribution for every frame. nexen#33, the opt-in envelope, has not shipped.
- So **the SPA cannot fold site-wide frames into list rows.**

**F2. Several state changes emit no event, and emits are best-effort.**
- `SettleIdle` (running → idle) runs after `execution.terminal` is emitted and is silent: `N/execution/conclude.go:166` vs `:185-191`, `N/store/turn.go:861-880`. The reconcile path settles to idle silently (`reconcile.go:207`).
- `ClaimTurn` is silent (`turn.go:385-469`); `execution.running` follows only after spawn (`launch.go:228`). `MarkTurnStarted` is silent too.
- A failed append is only logged (`N/execution/events.go:106-113`). See nexen#162.
- So **a delta must be the row re-read after a trigger, never a fold of event payloads.**

**F3. Fixed in PR0:** token deltas and raw frames on the site stream used to starve the trailing 500 ms debounce.

**F4. `updated_at` is not a version.**
- Writes that leave it untouched:
  - permission requested/resolved write only `permission_requests` (`N/store/permission.go:46-58,145-170`);
  - tool/activity rollups (`rollup.go:69-97`);
  - archive/unarchive (`execution.go:817,832`);
  - titles (`title.go:127-145`).
- The **cost** rollup *does* bump it (`N/store/cost.go:90-100`), as do create, claim, start, finish, settle, terminate and lease writes.
- So reads of the same row cannot be ordered by `updated_at`. Ordering needs its own version (§3). See nexen#163.

**F5. Purdex mounts Nexen in-process.**
- `internal/module/nex/module.go:49-61,388`. `System.Bus` is exported (`N/assemble.go:177`), and `bus.Frame` carries `ExecutionID` (`N/bus/bus.go:42-48`).

**F6. `/ws/host-events`** (`internal/core/events.go`):
- A subscriber is registered (`Add`) **before** the `OnSubscribe` callbacks run (`:240-250`).
- `BroadcastEvent` holds only `RLock`, so concurrent callers are not ordered (`:172-184`).
- A full 64-slot buffer drops the message (`:48-66`).
- Versioned `sessions` frames already solve read ordering with a single read slot (`internal/module/session/versioned.go:46-76`, spec §3.3 rules). This design reuses that pattern.

**F7. Response headers.**
- The SPA calls the daemon cross-origin, and CORS sets no `Access-Control-Expose-Headers` (`rg` finds none in `internal/`, `cmd/`). A custom response header would be unreadable, so versions travel in the **body** (§3.4).
- `listExecutions` returns only the parsed page today (`spa/src/lib/nex/nex-api.ts:59-62,95-107`).

## 2. Decisions (coordinator, 2026-10-08)

| # | Decision | Where |
|---|---|---|
| 1 | Option B: a daemon projector, carried on `/ws/host-events`; no new SSE | §3–§4 |
| 2 | A host that sent hello opens no site-wide SSE at all. Checked: the only consumer of the stream is `execution-list-effects`. `NexExecutionsTable`'s archived view depended on its refresh cadence; handled by decision 5. | §4.6, §4.7 |
| 3 | Safety reconcile about every 120 s, only while subscribed, that **counts and logs** mismatches (to detect missed pushes, not to hide them). Round-2 ruling: on **both** sides. The daemon side detects missed pushes, including executions it never pushed. The SPA side detects apply-side defects, only while the window is visible. Two separate counters. | §3.7, §4.5 |
| 4 | Nexen issues: nexen#162 and nexen#163 opened, comment on nexen#33 | header |
| 5 | Separate `archivedRevision`, bumped only by deltas that change archive membership and by reconciles. `NexExecutionsTable` re-queries on it only while "show archived" is on. | §4.7 |

## 3. Daemon: `internal/module/nex` projector

### 3.1 One read slot orders every read (fixes R1, R2, R4, R14)
- The module owns one `readSlot`, a context-aware mutex like the session module's `snapSlot`. **Every read whose result reaches a client goes through it:**
  - the projector's single-row reads (§3.3);
  - each list page served to a client (§3.4);
  - the safety reconcile's list read (§3.7).
- Inside the slot, after a **successful** read, the module increments `ver`, a uint64 that never resets within the process. A failed, timed-out or abandoned read consumes nothing.
- **Rule V:** any two reads that produce data are totally ordered by `ver`. A larger `ver` means a read that started after the smaller one's read finished. No read can straddle another, so there is no "about one row read" window.
- Why one global slot rather than a per-execution version (the coordinator's suggestion):
  - A per-execution version is ordered against a list read only if the list read of that row cannot interleave with the row's re-read.
  - Guaranteeing that means holding every execution's lock across the list read, which *is* a global slot.
  - Given the slot, one counter is enough. It also orders rows **absent** from a list page (tombstones, §4.3) with a single number per page.
- **Cost:** reads are serialized, so how long a page holds the slot is how long the projector can be delayed. Nexen's page handler is not just a SELECT. It also checks title freshness per row (`N/api/session_title.go:42-63`), may commit `title_changed` and re-read the page, and counts events (`N/api/query.go:448-487`). So the hold time is measured, not assumed; see §3.8 for the numbers and the bounds.

### 3.2 Bus consumer (fixes R10, R11)
- One goroutine runs `bus.Subscribe("", 1024)`. It **never blocks on the slot**: it filters by kind and marks the execution dirty.
- **Trigger kinds:**
  - `execution.{delegated, rejected, running, terminal, interrupted, error, message_accepted, interrupt_requested, turn_stalled, turn_orphaned, terminated, archived, unarchived, title_changed, observer_attached, observer_detached, credential_repaired}`
  - `permission.{requested, resolved}`, `tool_use`, `tool_result`, `task_start`, `task_end`, `result` (cost)
  - `lease.{acquired, released}` (durable) and `lease.renewed` (transient). Lease writes bump `updated_at`, which rows show.
- **Never:** `stream_event`, `stream_snapshot`, or the other raw provider frames.
- **Coalescing per execution:** trailing 75 ms, capped at 250 ms after the first mark. The trigger kinds are accumulated into a set for that flush (`cause`).

### 3.3 Flush worker and the in-process read (fixes R15)
- **One flush worker** pops ready executions in order. For each one:
  1. acquire the slot;
  2. read the row;
  3. on success, `ver++` and `bseq++` (§3.5) and broadcast;
  4. release the slot.
- One worker means the deltas of a given execution are read and broadcast in order.
- **The in-process read contract.** The request is built only by `rowReader.read(ctx, id)`:
  - method `GET`, path `"/v1/executions/" + url.PathEscape(id)`;
  - `id` comes from `bus.Frame.ExecutionID` and must match `^[0-9A-Za-z_-]{1,64}$`;
  - no headers, so the principal is the bare `pdx:<hostID>` (`build_config.go:120-135`). A GET needs no lease.
  - Nothing from a user request ever reaches this path.
  - The response goes into a capped buffer (1 MiB).
  - 200 → upsert; 404 (`execution_not_found`) → remove; anything else is a read failure (logged, no `ver`/`bseq` consumed; the execution is retried once after 1 s).
- **Normalization:** `lease` and `live_turn_id` are deleted from the GET body so a delta row has the same shape as a list row. The SPA store holds list-shaped rows only.
- **Side effect:** Nexen's GET may commit `execution.title_changed` (`N/api/query.go:513-521`). That frame only marks the execution dirty again; the next read finds no change, so it does not loop.

### 3.4 List wrapper (fixes R5)
- `GET /api/nex/v1/executions` gets a Purdex handler registered as a more specific mux pattern. Nexen's handler is unchanged.
- It runs Nexen's list handler **inside the slot** into a buffer.
- On 200 it injects a top-level `"pdx": {"epoch": E, "ver": V}` into the JSON object, using the `ver` taken for that page. `items` and `next_cursor` are untouched. Any non-200 response passes through unchanged, without `pdx`.
- Each page is its own read with its own `ver`. The SPA does not need a multi-page snapshot (§4.3).

### 3.5 Broadcast order, hello and gaps (fixes R2, R6, R14; round 2: 3, 4)
- **`bseq`** is a contiguous per-epoch counter. It is taken **only** when a delta is broadcast, inside the slot, and the broadcast happens while still holding the slot. So broadcast (enqueue) order equals `bseq` order, and a failed read never makes a gap.
- **Versions live in `value`, never in `HostEvent.Epoch` / `Seq`.** Those are `omitempty` (`internal/core/events.go:19-20`), so a hello with `seq 0` would lose its baseline (round 2 #3). Every nex frame carries its own explicit fields:
  - **Delta:** `HostEvent{type: "nex.execution", value: {"epoch": E, "bseq": n, "id", "ver", "cause": [kinds], "row": {…} | null}}`. `row: null` means remove.
  - **hello:** `HostEvent{type: "nex.executions.hello", value: {"epoch": E, "bseq": n}}`, where `bseq` 0 is spelled out.
  - Tests assert the marshalled JSON text (`"bseq":0` present).
- **hello timing:** `OnSubscribe` acquires the slot and sends the hello to that subscriber only. Because the slot is held:
  - every delta enqueued to that subscriber *before* the hello has `bseq ≤ hello.bseq`;
  - every delta after it has `bseq > hello.bseq`.
- **Client rule:** ignore any delta received before a hello; after hello(S), expect `S+1`; anything else is a gap and triggers a reconcile.
- **A drop closes the connection (round 2 #4, coordinator ruling).**
  - `TrySend` drops silently on a full buffer (`events.go:47-66`). A dropped *last* delta would never show up as a gap.
  - So nex frames go through a new `EventsBroadcaster.BroadcastStrict(ev)` / `sub.SendStrict`. Any failed enqueue `Remove`s that subscriber, which closes the WS. The client reconnects, gets a fresh hello, and reconciles. Nothing is repaired on the same connection.
  - `Remove` takes `eb.mu.Lock`, so failed subscribers are collected under `RLock` and removed after it is released.
  - A dropped hello is the same case: the connection closes and the next connection gets a new hello. So "legacy for that connection" no longer happens.
  - Other event types keep today's best-effort `Broadcast`. Making all frames strict is a separate decision, not taken here.
- `bseq` reaching 2^53−1 rotates the epoch (same rule as sessions).

### 3.6 Bus lifecycle (fixes R12, R13)
- The projector has its own `ctx`. Module `Stop` cancels it **before** the engine shuts down.
- When the consumer's channel closes while `ctx` is live (slow-subscriber kick, or the bus closing):
  - back off (100 ms, doubling to 5 s);
  - `Subscribe` again;
  - **after** the new subscription is registered, under the slot: new epoch, `bseq = 0`, broadcast a hello to everyone, and mark every execution in `lastPushed` dirty.
  - Every client then reconciles with list pages read after the new subscription existed, so nothing between the old channel's close and the re-registration is lost.
- A closed bus returns an already-closed channel. The backoff keeps that from busy-looping, and `ctx` ends it.
- An engine rebuild starts a new projector bound to the new bus.

### 3.7 Silent transitions: recheck plus a 120 s safety reconcile (F2, R18)
- **Recheck.** If a flush whose `cause` contains `execution.terminal` reads a row that is still `running`, the projector re-marks that execution at +150 ms, +600 ms and +2 s, stopping once the state is no longer `running`. This covers `SettleIdle`.
- **Daemon safety reconcile** (detects missed *pushes*). Every 120 s, only while `core.Events.HasSubscribers()`.
  - **The slot is taken per page,** like the list wrapper (§3.4), so the projector waits at most one page (§3.8). Each page `p` is read with `Store.List` (limit 100) inside the slot and gets its own `ver` R_p. The slot is released between pages.
  - Each row's status digest (`state`, `pending_permission.request_id`, `archived`, `turn_count`, `last_turn_reason`, `terminal_reason`) is compared with `lastPushed[id]`, for the rows in that page and for every `lastPushed` id that page's cursor range covers but that is missing (a missing row is a difference).
  - **No `lastPushed` entry** (never pushed since this process started, e.g. a change that predates the projector or a first change that was never seen) → push it now and count `nex_delta_reconcile_unseen_total` (round 2 #5 ruling). A baseline alone would hide a first miss forever.
  - **A difference** is a suspect. After a 1 s grace:
    - if a newer read happened meanwhile (`lastPushed[id].ver > R_p`), the change was in flight and is benign;
    - otherwise count `nex_delta_mismatch_total`, log `nex-delta: missed push exec=<id> field=<f> pushed=<v> actual=<v> total=<n>`, and flush that execution.
  - **What it cannot see:** defects on the SPA's apply side (a delta delivered but applied wrongly, or a bad overlay merge). Those are covered by the SPA reconcile (§4.5), the `bseq` gap check and tests.
- Missed **delivery** is no longer possible silently: a full buffer closes the connection (§3.5).

### 3.8 How long the slot is held, and the bounds (round 2 #6)
Measured on this machine (Apple M4, internal SSD).

**Live mlab daemon** (`curl` over tailscale; 23 executions, 6,761 events):

| request | time |
|---|---|
| `/v1/executions?limit=500` | 2.0–4.9 ms (1 non-archived row) |
| `limit=500&include_archived=true` | 2.5–2.9 ms (23 rows) |
| `/v1/executions/{id}` | 2.1 ms |
| `/api/health` (baseline) | 1.3 ms |

**Synthetic benchmark.**
- **Setup:**
  - the real `nexen.Assemble` with Purdex's `buildOptions`, called through `System.Handler.ServeHTTP` (no Purdex middleware and no TCP);
  - 500 or 100 idle executions, each with 3 turns and a 207 KB JSONL transcript;
  - 10 % with a pending permission;
  - 200 or 1,000 events per execution, written straight to the store;
  - each scenario ≥ 30 runs (single GET: 100);
  - the benchmark file was deleted afterwards.

Median ms (min–p95 where it matters):

| request | 500 × 200 (100k events) | 500 × 1,000 (500k events) | 100 × 200 (20k events) |
|---|---|---|---|
| `limit=500` steady | **15.5** (14.0–19.1) | **41.4** (40.3–46.8) | 2.4 |
| `limit=100` steady | **9.1** (8.7–10.1) | **36.6** (35.5–38.6) | 2.5 |
| `limit=500`, every title changed (500 `title_changed` commits + re-list) | 155 (p95 175) | 190 (p95 250) | 31 |
| `limit=500`, every transcript grew, titles unchanged | 83 | 111 | 16 |
| `limit=500`, 10 titles changed | 20.7 | 50.2 | 5.8 |
| first list in process (500 first-time title reads) | 454 | 472 | 69 |
| `GET /v1/executions/{id}` | **0.12** | **0.17** | 0.12 |
| single GET right after its title changed | 0.38 | 0.44 | 0.40 |
| breakdown: `store.List(500)` | 4.3 | 4.0 | 0.9 |
| breakdown: `store.EventCounts()` | **7.4** | **34.6** | 1.2 |

**What the numbers say**
- **A row read is about 0.1–0.4 ms.** The projector side of the slot is negligible.
- **A page's floor is `EventCounts`.** It is a `GROUP BY` over the *whole* `events` table, archived executions included (`N/store/event.go:159-180`). It grows with total events (about 70 ns per event) and does **not** shrink with `limit`, which is why `limit=100` is barely faster at 500k events. Filed as [nexen#164](https://lab.protype.tw/wake/nexen/issues/164) (count only the page's ids).
- **Title work is the spike.** A page where every transcript changed costs 0.14–0.3 ms per row on top. So a 100-row page bounds that part to about 30 ms, versus about 150 ms for 500 rows. The first list after a daemon start pays the first-time title reads (about 0.9 ms per row).

**Expected worst-case projector wait with 100-row pages:**
- 100k events: about 10 ms steady; 40–60 ms when every title on the page changed; about 100 ms for the first list after start.
- 500k events: about 40 / 70 / 130 ms respectively.

All of these are below today's 500 ms debounce. mlab today: 23 executions, 6,761 events.

**Bounds**
- **Page size.** Walks in `delta` mode use `limit=100`. The legacy walk keeps 500.
- **Slot wait timeout.**
  - The list wrapper waits for the slot at most 2 s, then answers 503 `nex_busy`, which the walk retries with backoff.
  - The projector waits without a timeout, but in its own goroutine. Bus consumption never waits on the slot.
  - The reconcile skips its turn if it waited more than 2 s.
- **Observability.**
  - Every slot hold longer than 250 ms logs `nex-delta: slot held <ms> by <list|row|reconcile>`.
  - Every wait longer than 250 ms logs `nex-delta: slot wait <ms> for <who>`.
  - `nex_delta_slot_max_hold_ms` and `nex_delta_slot_max_wait_ms` (since start) are printed with the mismatch counters every reconcile.
- **Cancellation.** The wrapper passes the request context into the slot wait and into Nexen's handler. The slot is released in a `defer` whether the page finished, failed, timed out or the client went away. A test cancels mid-page and asserts the slot is free.

## 4. SPA

### 4.1 Per-host capability state machine (fixes R7)
- **`deltaCap: 'unknown' | 'delta'`** per host, kept with the list runtime.
  - Set to `delta` by the first hello.
  - **Sticky across host-events reconnects.**
  - Reset to `unknown` when the host's fingerprint changes (another daemon) or the host is removed.
- **A list subscribe while `unknown`** behaves as today: reserve the lane, open the legacy SSE (with PR0's `?kind=`), fetch.
- **A list subscribe while `delta`:** no lane, no SSE, a versioned fetch (§4.3).
- **On a hello:**
  - record the baseline `{epoch, seq}`;
  - if the legacy SSE is open, close it and unreserve the lane;
  - if anything is subscribed, reconcile.
  - Every hello reconciles, since deltas may have been missed while disconnected.
  - A hello with no subscriber only records capability and baseline.
- **While the host-events WS is down,** `delta` stays: there is no SSE fallback for a single outage. The next hello reconciles.

### 4.2 Splitting "fetch" from "open stream" (fixes R8)
- `refetch` today calls `open` when `rt.sse` is null (`execution-list-effects.ts:277-282`), and exit, rebuild and worker-exited all go through it.
- After the split:
  - `fetchAll(host)` is a one-shot walk.
  - `openLegacyStream(host)` is reserve + SSE.
  - `refetch` is `fetchAll` in `delta` mode and today's behaviour otherwise.

### 4.3 Versioned rows, overlay and tombstones (fixes R3, R4)
- **Keys.** Each cached row stores `ver`. A delta is applied only if `delta.ver > row.ver`. Rows from a legacy (unversioned) fetch have `ver = 0`.
- **One normalization, used everywhere (round 2 #2).** The store holds non-archived rows only, so a delta is a **tombstone** `{ver, null}` when its `row` is `null` **or** has `archived: true`. The same function normalizes deltas for the visible cache (§4.4) and for the overlay. An archived upsert can therefore never reach the overlay as a row, and cannot be resurrected by the commit.
- **Walk in flight.** Deltas go into an **overlay** keyed by id, holding `{ver, row}` or a tombstone `{ver, null}`; a later higher `ver` replaces an entry. They also apply to the visible cache as usual, so the UI is not frozen.
- **Each page records** `{ver, upTo}`, where `upTo` is the page's last id, or `∞` for the final page, including an empty final page. Nexen pages by `id >` cursor in `ORDER BY id` (`N/store/execution.go:934-1016`), so every id has exactly one *covering page*: the first page with `id ≤ upTo` (an id equal to `upTo` belongs to that page). Ids compare as plain strings, which matches SQLite's BINARY order for Nexen's ASCII ids.
- **A row that moves into an already-walked range during the walk** (unarchive of an old id, which sorts early) is covered by an earlier page whose `ver` is older than the delta. So the overlay upsert wins and the row is present after the commit.
- **Commit.** Start from the walk's rows, each keyed with its page's `ver`. Then apply each overlay entry if `entry.ver > coveringPage(id).ver`:
  - an upsert adds or replaces the row;
  - a tombstone removes it.
  - An id beyond a truncated walk has no covering page, and the overlay entry wins.
- **Why this cannot resurrect a removed row.** Deltas of one execution are broadcast in `ver` order (§3.3) and arrive in that order over one WS. A stale upsert in the overlay is therefore always followed by its later tombstone, which outranks it.
- **A page without a valid `pdx`** (missing or malformed) makes the walk *unversioned*: rows get `ver = 0` (any delta wins), and a warning is logged.
- A page whose `pdx.epoch` differs from the current baseline does not mix epochs: the walk is discarded and a fresh walk starts.
- **API change:** `listExecutions` returns `{page, pdx?}`; `listAllExecutions` returns `pages: {ver, upTo}[]` alongside `items`. In `delta` mode it walks with `limit=100` (§3.8); the legacy walk keeps 500.

### 4.4 Delta handling
- `useMultiHostEventWs` routes `nex.executions.hello` and `nex.execution` to the list effects.
- Checks in order:
  - not in `delta` mode or no baseline → ignore;
  - epoch differs from the baseline → ignore (a hello for the new epoch follows);
  - `bseq ≠ last+1` → set `last = bseq` and reconcile;
  - otherwise normalize (§4.3: `row: null` or `archived: true` → tombstone) and apply: a tombstone removes the row, anything else upserts it.

### 4.5 Reconcile triggers (SPA)
1. the first list subscribe;
2. every hello;
3. a `bseq` gap;
4. an explicit `refetch` (exit, rebuild, worker-exited, host lifecycle);
5. **the SPA safety reconcile** (round 2 #5 ruling): every 120 s, only while the host is in `delta` mode, the list is subscribed and `document.visibilityState === 'visible'`.

The safety reconcile detects *apply-side* defects:
- Before committing, it compares each fetched row (page `ver` V) with the cached row. A **suspect** is either:
  - `V > cached.ver` with a different status digest (same fields as §3.7);
  - or a row present on one side only.
- It commits as usual, which repairs the cache.
- After a 1.5 s grace, any suspect for which no delta with `ver > V` arrived is a **mismatch**: `spaMismatchTotal++` on the host's list cache, plus `console.warn('nex-delta: spa mismatch', {hostId, id, field, cached, fetched, total})`. A delta that did arrive means the change was in flight, so it is benign.
- This counter is kept separate from the daemon's counters (§3.7), so we can see later which side is losing updates.

### 4.6 Connection lane (R9; round 2 #1)
- **In `delta` mode the lane is never reserved.**
- **A late hello** (the list subscribed before the host-events hello arrived, as can happen at app start) unreserves.
- My v2 rebuttal was wrong (round 2 #1). A pane evicted by `reserve` re-touches only when its `active` prop changes (`useExecutionSubscription.ts:45-61`), and `unreserve` only raises the cap (`subscription-slots.ts:90-98`). A pane that stays active would therefore stay paused.
- **Fix:**
  - `subscription-slots` records the keys evicted by a `reserve` (per host).
  - `unreserve` notifies those keys through a new `onCapacity(key, cb)` listener and then forgets them.
  - In `useExecutionSubscription`, a paused pane whose `active` is still true handles the notice by running the activation path (`touch` → `isLive` → `openStream`). An inactive pane ignores it and keeps today's resume-on-activation rule.
  - This is limited to reserve-evicted keys, so an ordinary LRU eviction between panes still never resumes spontaneously.

### 4.7 `archivedRevision` (decision 5)
- `HostListCache.archivedRevision` is bumped by:
  - a delta whose `cause` contains `execution.archived` or `execution.unarchived`;
  - a delta with `row: null`;
  - a delta whose row has `archived: true`;
  - every committed reconcile.
- `NexExecutionsTable` keys its archived query on it (`NexExecutionsTable.tsx:108-133`), only while "show archived" is on. Ordinary deltas never re-run it.

### 4.8 Untouched
- `useWorkerAgentProjection`, `useTabDisplay`, `readWorkerSummary`, the notification dispatcher. They keep reading rows; the rows just change sooner.
- Pane live never feeds status (aa's single-source model).

## 5. Tests (R19)

**Daemon**
- read slot and `ver`:
  - a read blocked inside the slot while a list page waits, then the reverse;
  - a failed read consumes neither `ver` nor `bseq`.
- broadcast order:
  - two executions flushed back-to-back broadcast in `bseq` order;
  - concurrent flush attempts are serialized.
- hello vs `Add`: a delta broadcast between `Add` and `OnSubscribe` has `bseq ≤ hello.bseq`, and the next has `hello.bseq+1`.
- **wire format (round 2 #3, #10):**
  - the marshalled hello at epoch start contains `"bseq":0`;
  - deltas carry `epoch`/`bseq` in `value`;
  - `HostEvent.Epoch`/`Seq` stay empty for nex frames.
- **strict send (round 2 #4, #10):**
  - two clients, one with a full buffer: only the slow one is removed (its WS closed); the fast one keeps receiving with contiguous `bseq`;
  - a dropped *last* delta still closes the connection;
  - a dropped hello closes the connection;
  - removal happens after `RLock` is released (no deadlock under `-race`).
- **reconnect (round 2 #10):**
  - same-epoch reconnect with no deltas in between → hello with the same `bseq` → client reconciles once, no gap;
  - new-epoch hello is the first frame on the new connection.
- list wrapper:
  - `pdx` injected on 200 with a page `ver` ordered against neighbouring deltas;
  - non-200 passes through unchanged;
  - query parameters are preserved.
- **slot under the real Nexen handler (round 2 #9):**
  - a page whose title refresh commits `title_changed` inside the slot → the bus consumer only marks dirty (no lock-order deadlock), and the flush happens after the page releases the slot;
  - the measured hold is logged above the threshold;
  - a page cancelled or timed out mid-read releases the slot (the next acquire succeeds immediately);
  - a wrapper waiting longer than 2 s answers 503 `nex_busy`.
- triggers: `lease.*`, `observer_*` and `result` mark dirty; `stream_event` and `assistant` do not.
- recheck: terminal → still running → rechecked until idle.
- bus overflow: channel closed with `ctx` live → resubscribe → new epoch hello → all executions dirty, with no busy loop on a closed bus.
- shutdown: `ctx` cancelled → the consumer exits; a closed bus → backoff, not a spin.
- in-process read:
  - fixed method, path and headers;
  - an id failing the pattern is never requested;
  - 404 → remove; 500 → retry with no counters consumed.
- safety reconcile:
  - silent drift → mismatch counted, logged and repaired;
  - in-flight → benign;
  - unseen execution → **pushed** and counted as unseen;
  - a pushed id missing from its covering page → counted and pushed as remove;
  - the slot is released between pages;
  - runs only while subscribed.

**SPA**
- delta before hello is ignored; gap → reconcile; old-epoch delta is ignored.
- delta `ver ≤ row.ver` is dropped.
- overlay:
  - deltas before the first page, between pages and after the last page;
  - an upsert overwritten by the commit is restored;
  - a tombstone beats an older list row;
  - no resurrection;
  - truncated walk.
- **overlay edges (round 2 #8):**
  - an `archived: true` delta in the overlay becomes a tombstone, and the commit removes that id;
  - a delta whose `ver` is between its covering page's `ver` and the next page's `ver` wins only against its covering page;
  - an id equal to a page's `upTo` is covered by that page;
  - an empty final page still covers ids beyond the previous `upTo`;
  - unarchive and create land in an already-walked range and are present after the commit.
- unversioned page → `ver = 0` rows.
- late hello (round 2 #1):
  - the SSE closes and the lane is unreserved;
  - a pane evicted by the reservation that is still active resumes via `onCapacity`;
  - an inactive evicted pane stays paused until activated;
  - an LRU eviction between panes never resumes spontaneously.
- **SPA safety reconcile (round 2 #5):**
  - runs only when visible, subscribed and in `delta` mode;
  - a seeded apply-side defect (a delta dropped by the client after hello) → mismatch counted after the grace and repaired;
  - an in-flight delta inside the grace → benign.
- `delta` host: an explicit refetch does not reopen the SSE, and a subscribe reserves nothing.
- fingerprint change resets the capability.
- `archivedRevision`: bumped only by archive-membership deltas and reconciles, and the table re-queries only while the toggle is on.

## 6. Phasing (each ≤ 800 lines / ≤ 20 files)

| PR | Content | Depends on |
|---|---|---|
| PR1a | Read slot (timeouts, hold/wait logging), `ver`, in-process row reader, list wrapper with `pdx` | – |
| PR1b | Bus consumer, coalescing, flush worker, `bseq`, strict broadcast, hello, deltas, terminal recheck | PR1a |
| PR1c | Bus lifecycle (resubscribe/backoff/shutdown), 120 s daemon safety reconcile + counters | PR1b |
| PR2a | SPA: `listExecutions` `pdx`, versioned rows, overlay/tombstones, fetch/stream split, capability state machine, `archivedRevision`, `onCapacity`. Built but **inert**: the capability never leaves `unknown` without PR2b. | PR1a; aa's PR merged |
| PR2b | SPA: host-events routing (hello/delta), gap/epoch handling, SPA safety reconcile, `NexExecutionsTable` on `archivedRevision`. This is the PR that turns `delta` mode on and the legacy SSE off. | **PR1c** (round 2 #7), PR2a |

PR1a–c are inert until PR2b lands: an old SPA ignores both the unknown event types and the extra `pdx` field. `delta` mode is never enabled against a daemon without resubscribe/backoff and the safety reconcile. Each PR gets R1 + attacker + critic.

## 7. Review response (`task-muyhy7u8-pw7x2s`)

| R | Finding | Resolution |
|---|---|---|
| 1 | `seq ≤ S` cannot prove the delta read preceded the list read | §3.1: one read slot; `ver` taken after a successful read inside it (rule V) |
| 2 | Global seq vs broadcast order | §3.3/§3.5: one flush worker; `bseq` taken and broadcast inside the slot |
| 3 | No fetch-in-flight overlay | §4.3: overlay + tombstones, applied by covering page |
| 4 | The first page is not a snapshot | §4.3: each page has its own `ver`; covering-page rule |
| 5 | `listExecutions` drops headers | F7/§3.4/§4.3: `pdx` in the body; API change listed |
| 6 | hello vs `Add` | §3.5: hello sent under the slot; pre-hello deltas ignored |
| 7 | No start-up state machine | §4.1 |
| 8 | Explicit refetch reopens the SSE | §4.2 |
| 9 | Late hello loses a slot | §4.6: slot freed; the evicted pane follows the existing resume-on-activation rule (evidence cited) |
| 10 | `lease.*` missing from triggers | §3.2 |
| 11 | `observer_*` missing | §3.2 |
| 12 | Closed channel ≠ fell behind | §3.6: ctx + backoff |
| 13 | Resubscribe boundary | §3.6: register first, then new epoch + hello + all dirty |
| 14 | Read failure consumes seq | §3.1/§3.5: counters taken only on success |
| 15 | In-process auth contract | §3.3 |
| 16 | Cost bumps `updated_at` | F4 corrected |
| 17 | F1 too absolute | F1 reworded |
| 18 | 120 s reconcile + counter missing | §3.7, §4.5 (both sides after the round-2 ruling) |
| 19 | Test gaps | §5 |

## 8. Review response, round 2 (`task-muyime61-up5dy4`)

| # | Sev | Finding | Resolution |
|---|---|---|---|
| 1 | med | Late-hello rebuttal wrong: an active evicted pane stays paused | Accepted; rebuttal withdrawn. §4.6: `onCapacity` for reserve-evicted keys; an active pane re-runs activation. Tests in §5 |
| 2 | high | `archived: true` in the overlay resurrects archived rows | §4.3: one normalization (null or archived → tombstone) for cache and overlay; §4.4 uses it |
| 3 | high | `HostEvent.Seq` is `omitempty`, so a hello with seq 0 loses its baseline | §3.5: `epoch`/`bseq` move into `value` and are always present; the wire JSON is tested |
| 4 | high | `TrySend` drops silently, so a dropped last frame is never a gap | §3.5: strict send for nex frames; any drop removes the subscriber (closes the WS) → reconnect → hello → reconcile |
| 5 | high | Daemon reconcile misses SPA apply defects; a baseline-only entry hides a first miss | §3.7: unseen rows are pushed and counted. §4.5: SPA 120 s reconcile (visible + subscribed + delta) with its own counter |
| 6 | med | Slot hold per page is unbounded; "tens of ms" unproven | §3.1/§3.8: measured (live + synthetic); 100-row pages in delta mode; 2 s wait timeout → 503 `nex_busy`; hold/wait logging and maxima; cancellation releases the slot |
| 7 | high | PR2b must depend on PR1c | §6: PR2b depends on PR1c; PR2a is inert until PR2b |
| 8 | med | Overlay archive/unarchive test gaps at page boundaries | §4.3 (covering-page edges spelled out), §5 overlay edges |
| 9 | med | Real-handler `title_changed` inside the slot; timeout/cancel releases the slot | §5 "slot under the real Nexen handler" |
| 10 | high | hello `seq 0` JSON, dropped last frame, same-epoch reconnect, dropped new-epoch hello | §5 wire format / strict send / reconnect |
| self | – | §3.7 held the slot across the whole reconcile walk | §3.7: slot per page, each page its own `ver` |

### Round 3 (`task-muyjfxdr-el1o15`): 8 of 10 resolved, #5/#6 partly, 3 new Important (coordinator: PR1a released; these fixes land before the PRs named below)

**R3-1. SPA reconcile counts a delayed-but-legitimate delta as a mismatch** (blocks PR2b).
- A delta read before the page (`D ≤ V`) can be enqueued before the page read yet written to the socket after the page response, because the write pump is asynchronous (`internal/core/events.go:117-130`). "Benign only if `ver > V` arrives" misjudges this case.
- **Fix:**
  - the list wrapper's `pdx` gains `"bseq": H`, the broadcast high-water mark taken inside the slot together with the page's `ver` (`"pdx": {"epoch", "ver", "bseq"}`);
  - at commit, a suspect is recorded with `{id, V, H, listDigest}`;
  - it is **not evaluated until the client has processed every delta with `bseq ≤ H`** (`lastBseq ≥ H`), and then the grace (1.5 s) runs;
  - it is benign if, meanwhile, any delta for that id arrived with `bseq ≤ H` and a digest equal to `listDigest` (in flight, carrying the same state; such a delta is not applied because `D ≤ V`, but it is observed), or with `ver > V` (a newer change);
  - otherwise it is a mismatch.
  - A gap or epoch change cancels pending suspects, because the following reconcile re-evaluates.
- **Tests:**
  - a delta with `D ≤ V` delivered after the page → benign;
  - a delta withheld (seeded apply defect) → mismatch;
  - a suspect stays pending while `lastBseq < H`.

**R3-2. First reconcile after start pushes every execution as unseen → disconnect storm** (blocks PR1c).
- With `lastPushed` empty, the first tick would push every row. The 64-slot buffer plus strict send would disconnect everyone.
- **Fix (coordinator ruling):**
  - when an epoch starts (daemon start, or bus resubscribe §3.6), the projector **seeds `lastPushed`** from a full read (per page, inside the slot) **without pushing**. The new epoch's hello makes every client do a full reconcile, so clients hold exactly that baseline and nothing is hidden;
  - after seeding, unseen and mismatch pushes per tick are capped at 32 (oldest first; the rest wait for the next tick; the counters count every detection, pushed or deferred).
- **Tests:**
  - start with N > 64 existing executions → no pushes on the first tick, `lastPushed` seeded;
  - 100 unseen in one tick → 32 pushed, the rest next tick;
  - no subscriber disconnected.

**R3-3. No SPA retry contract for 503 `nex_busy`** (blocks PR2a).
- Today `listExecutions` turns any non-2xx into `NexApiError` and the walk goes straight to `error`.
- The wrapper answers `503 {"code": "nex_busy"}` (Nexen's error shape, so `NexApiError.code === 'nex_busy'`).
- **Contract (`listAllExecutions`):**
  - retry the **same page** (same cursor);
  - back off 250 ms, doubling, capped at 2 s;
  - at most 5 retries per page (about 5.75 s);
  - every wait and retry is guarded by the walk's existing `stillCurrent` (generation, fingerprint, readiness, subscribers), so a superseded walk stops quietly;
  - the overlay stays open through the retries;
  - only exhaustion (or any other error) puts the cache in `error`, keeping the previous rows.
  - Other 5xx codes keep today's behaviour.
- **Tests:**
  - busy twice then 200 → the walk completes, and deltas that arrived during the retries are applied by the overlay;
  - busy six times → `error`, rows kept;
  - a walk superseded during backoff → no further request and no commit.
