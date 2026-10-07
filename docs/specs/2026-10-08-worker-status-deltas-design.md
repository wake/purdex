# #1866: Worker status deltas on the host stream (design v2)

Status: v2, rewritten after the coordinator's six decisions and codex design review `task-muyhy7u8-pw7x2s` (19 findings, all accepted). For the second codex round. Author: purdex-6d. Base: main (alpha.587). Nexen pinned v0.19.0.

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
| 3 | Safety reconcile about every 120 s, only while subscribed, that **counts and logs** mismatches (to detect missed pushes, not to hide them). **Implemented daemon-side; see §3.7 for why. Please confirm.** | §3.7 |
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
- **Cost:** reads are serialized. A single-row GET takes about a millisecond. A 500-row page takes tens of ms, and pages are only read on reconcile, not per frame.

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

### 3.5 Broadcast order, hello and gaps (fixes R2, R6, R14)
- **`bseq`** is a contiguous per-epoch counter. It is taken **only** when a delta is broadcast, inside the slot, and `BroadcastEvent` is called while still holding the slot. So broadcast (enqueue) order equals `bseq` order, and a failed read never makes a gap.
- **Delta frame:** `HostEvent{type: "nex.execution", epoch: E, seq: bseq, value: {"id", "ver", "cause": [kinds], "row": {…} | null}}`. `row: null` means remove.
- **hello:** `OnSubscribe` acquires the slot and sends `HostEvent{type: "nex.executions.hello", epoch: E, seq: bseq}` to that subscriber only. Because the slot is held:
  - every delta enqueued to that subscriber *before* the hello has `seq ≤ hello.seq`;
  - every delta after it has `seq > hello.seq`.
- **Client rule:** ignore any delta received before a hello; after hello(S), expect `S+1`; anything else is a gap and triggers a reconcile.
- If the hello itself is dropped (full buffer on a brand-new subscriber), the client stays in legacy mode for that connection. That is slower but correct.
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
- **Safety reconcile.** Every 120 s, but only while `core.Events.HasSubscribers()`:
  - Under the slot, `Store.List` pages over all non-archived rows (taking a `ver` R).
  - It compares each row's status digest (`state`, `pending_permission.request_id`, `archived`, `turn_count`, `last_turn_reason`, `terminal_reason`) with `lastPushed[id]`.
  - Executions never pushed get a baseline only, with no count.
  - A difference (including a pushed row that is now missing) is a **suspect**. After a 1 s grace:
    - if a newer read (`lastPushed.ver > R`) has happened, it was in flight and is benign;
    - otherwise `nex_delta_mismatch_total++`, log `nex-delta: missed push exec=<id> field=<f> pushed=<v> actual=<v> total=<n>`, and flush that execution.
- **Why daemon-side rather than each SPA (decision 3, please confirm):**
  - Only the daemon knows which executions are mid-coalescing or mid-flush, so its suspects are exact and need only a 1 s grace. The SPA cannot tell an in-flight push from a missed one.
  - It is one list read per host per 120 s instead of one per client window.
  - iOS gets the same coverage for free.
  - The counter lives in one place.
- Missed **delivery** (a dropped WS message) is detected by the client's `bseq` gap check, so between them both kinds of loss are detected.

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
- **Walk in flight.** Deltas go into an **overlay** keyed by id, holding `{ver, row}` or a tombstone `{ver, null}`; a later higher `ver` replaces an entry. They also apply to the visible cache as usual, so the UI is not frozen.
- **Each page records** `{ver, upTo}`, where `upTo` is the page's last id, or `∞` for the final page. Nexen pages by `id >` cursor in `ORDER BY id` (`N/store/execution.go:934-1016`), so every id has exactly one *covering page*.
- **Commit.** Start from the walk's rows, each keyed with its page's `ver`. Then apply each overlay entry if `entry.ver > coveringPage(id).ver`:
  - an upsert adds or replaces the row;
  - a tombstone removes it.
  - An id beyond a truncated walk has no covering page, and the overlay entry wins.
- **Why this cannot resurrect a removed row.** Deltas of one execution are broadcast in `ver` order (§3.3) and arrive in that order over one WS. A stale upsert in the overlay is therefore always followed by its later tombstone, which outranks it.
- **A page without a valid `pdx`** (missing or malformed) makes the walk *unversioned*: rows get `ver = 0` (any delta wins), and a warning is logged.
- A page whose `pdx.epoch` differs from the current baseline does not mix epochs: the walk is discarded and a fresh walk starts.
- **API change:** `listExecutions` returns `{page, pdx?}`; `listAllExecutions` returns `pages: {ver, upTo}[]` alongside `items`.

### 4.4 Delta handling
- `useMultiHostEventWs` routes `nex.executions.hello` and `nex.execution` to the list effects.
- Checks in order:
  - not in `delta` mode or no baseline → ignore;
  - epoch differs from the baseline → ignore (a hello for the new epoch follows);
  - `seq ≠ last+1` → set `last = seq` and reconcile;
  - otherwise apply the row: `archived: true` or `row: null` removes it (the store holds non-archived rows only), anything else upserts it.

### 4.5 Reconcile triggers (SPA)
1. the first list subscribe;
2. every hello;
3. a `bseq` gap;
4. an explicit `refetch` (exit, rebuild, worker-exited, host lifecycle).

There is no SPA timer: the periodic check is daemon-side (§3.7).

### 4.6 Connection lane (R9)
- **In `delta` mode the lane is never reserved.**
- **A late hello** (the list subscribed before the host-events hello arrived, as can happen at app start) unreserves. Any pane evicted meanwhile follows the existing rule: it resumes when it is re-activated, not spontaneously when a slot frees (`useExecutionSubscription.ts:45-51`, a deliberate spec §4.3.2 choice). So no slot is lost permanently; the slot is free, and that pane waits for its next activation.
- **Proposal, open to veto:** keep that rule rather than add a capacity-freed notification.

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
- hello vs `Add`: a delta broadcast between `Add` and `OnSubscribe` has `seq ≤ hello.seq`, and the next has `hello.seq+1`.
- two clients, one with a full buffer: only the slow one sees a gap.
- list wrapper:
  - `pdx` injected on 200 with a page `ver` ordered against neighbouring deltas;
  - non-200 passes through unchanged;
  - query parameters are preserved.
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
  - unseen execution → baseline only;
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
- unversioned page → `ver = 0` rows.
- late hello: the SSE closes and the lane is unreserved.
- `delta` host: an explicit refetch does not reopen the SSE, and a subscribe reserves nothing.
- fingerprint change resets the capability.
- `archivedRevision`: bumped only by archive-membership deltas and reconciles, and the table re-queries only while the toggle is on.

## 6. Phasing (each ≤ 800 lines / ≤ 20 files)

| PR | Content | Depends on |
|---|---|---|
| PR1a | Read slot, `ver`, in-process row reader, list wrapper with `pdx` | – |
| PR1b | Bus consumer, coalescing, flush worker, `bseq`, hello, deltas, terminal recheck | PR1a |
| PR1c | Bus lifecycle (resubscribe/backoff/shutdown), 120 s safety reconcile + mismatch counter | PR1b |
| PR2a | SPA: `listExecutions` `pdx`, versioned rows, overlay/tombstones, fetch/stream split, capability state machine, `archivedRevision` | PR1a; aa's PR merged |
| PR2b | SPA: host-events routing (hello/delta), gap/epoch handling, `NexExecutionsTable` on `archivedRevision` | PR1b, PR2a |

PR1a–c are inert until PR2 lands: an old SPA ignores both the unknown event types and the extra `pdx` field. Each PR gets R1 + attacker + critic.

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
| 18 | 120 s reconcile + counter missing | §3.7 (daemon-side; please confirm) |
| 19 | Test gaps | §5 |
