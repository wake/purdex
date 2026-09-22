# Fresh, versioned session list (#1255, daemon half) — spec

Status: draft 1 (2026-09-23) · Owner: purdex-cleanup · Consumer: the SPA half of #1255 (purdex-3b)

## 1. Problem

After a profile switch the SPA must re-reconcile a world against the host's
session list, but nothing it can obtain today is usable as *negative* evidence
("session X is gone"):

- `GET /api/sessions` is served from a 1 s TTL cache (`handler.go`
  `cachedListSessions`, `module.go` `listCacheTTL`) that no mutation
  invalidates — a list fetched right after a kill can still show the session.
- WS `sessions` pushes carry no ordering, arrive through a 500 ms debounce
  (a dropped push is only caught up by the 5 s ticker), and cannot be ordered
  against a fetch.

The SPA therefore cannot tell which of two lists is newer. The kickoff record
("切換 profile 後不重播 payload、也不重新 fetch") is explicit that an
unversioned payload is never evidence.

## 2. Goals / non-goals

Goals
- G1. A way to obtain a session list that is **never** served from a cache.
- G2. Every list the daemon hands out on the new paths carries a version that
  totally orders it against every other versioned list from the **same daemon
  process**, and says unambiguously when two lists are not comparable.
- G3. WS `sessions` frames carry the same version.
- G4. **No existing client breaks.** Old SPA ↔ new daemon and new SPA ↔ old
  daemon both keep working; the new SPA can detect an old daemon structurally.

Non-goals
- Removing the 500 ms debounce or the 5 s ticker (latency of pushes is
  unchanged; the fresh fetch is the answer to lateness).
- Making versions comparable across daemon restarts (see §4.3 — the rule is
  "different epoch ⇒ not comparable", and the SPA has a clean way to act on it).
- Any SPA change. `spa/` is not touched by this PR.

## 3. Wire contract

### 3.1 `GET /api/sessions?fresh=1`

- Same route, auth and error behaviour as `GET /api/sessions`
  (tmux read error → `500`, text body).
- Response `200`, `Content-Type: application/json`, body is an **object**:

  ```json
  { "epoch": "9f3c1a0b7d2e4c61", "seq": 42, "sessions": [ /* SessionInfo[], same shape as today */ ] }
  ```

  - `sessions` is never `null` (empty list → `[]`).
  - Never served from the list cache; always a new tmux read.
- Only the exact value `fresh=1` selects this form. Absent / any other value
  → today's behaviour unchanged (bare array, may be up to 1 s old).
- **Old-daemon detection**: an old daemon ignores the query and answers a bare
  JSON array. The SPA MUST treat "body is an array" as *unversioned* — usable
  for display, never as evidence.

### 3.2 WS `/ws/host-events` frame `type: "sessions"`

Two new top-level fields, next to the existing `type` / `session` / `value`:

```json
{ "type": "sessions", "session": "", "value": "[...]", "epoch": "9f3c1a0b7d2e4c61", "seq": 43 }
```

- Present on **every** `sessions` frame from a new daemon: the on-subscribe
  snapshot, the wait-for push, and the ticker push.
- Absent (`omitempty`) on every other frame type — no other frame changes
  byte-for-byte.
- `value` is unchanged (JSON-encoded `SessionInfo[]`). Old SPAs ignore unknown
  top-level keys (`JSON.parse` + field access), so they are unaffected.
- A frame without `epoch`/`seq` comes from an old daemon ⇒ unversioned.

### 3.3 Field semantics

| field | type | meaning |
|---|---|---|
| `epoch` | string, 16 lowercase hex chars | Identity of the daemon **process**. Random (crypto/rand, 64 bit), fixed for the life of the process, different after every restart. Opaque: compare for equality only. |
| `seq` | integer ≥ 1 (JSON number, < 2^53) | Position of this list in the process's read order. Starts at 1; 0 is never sent. |

Ordering guarantee (the part the SPA relies on):

1. **Total order within an epoch.** Versioned reads are serialized: `seq` is
   assigned and the tmux read performed under one daemon-wide lock. So for two
   lists with the same `epoch`, the larger `seq` was read *after* the smaller
   one's read completed. Larger `seq` ⇒ newer, always; equal `seq` ⇒ same read.
2. **Read-your-writes.** A versioned list reflects every tmux mutation that
   finished before its read started. In particular: if a client receives the
   HTTP response of create / rename / delete (`POST`/`PATCH`/`DELETE
   /api/sessions…`) and *then* sends `GET /api/sessions?fresh=1`, that list
   reflects the mutation. External tmux commands are covered the same way (the
   read is a real `tmux list-sessions`).
3. **Delivery order is not read order.** A push with `seq` 41 may arrive after
   a fetch response with `seq` 42. The SPA orders by `seq`, not by arrival,
   and discards anything older than what it already holds for that host.
4. **One counter for all channels.** `?fresh=1` responses and every WS
   `sessions` frame draw from the same per-process counter, so within an epoch
   a fetch result and a push are directly comparable by `seq`.
5. **A `seq` belongs to exactly one read.** Every `seq` sent is the number
   taken by the very tmux read that produced that list. A cached or re-used
   list is never paired with a newer `seq`; a path that re-uses a list must
   carry the `seq` it was read with, or send no `seq`. (Rules 1–2 depend on
   this.) In this design no versioned path re-uses a list: all of them call
   `versionedList()`.
6. `seq` gaps are normal (other readers — the ticker, other clients' fresh
   fetches, subscribe snapshots — consume numbers). Only order is meaningful.

### 3.4 Across daemon restarts (different `epoch`)

`seq` values from different epochs are **not comparable** (it restarts at 1).
There is only ever one daemon process per host, and a WS connection or HTTP
response always comes from the process that is running at that moment, so:

- A list whose `epoch` differs from the one the SPA holds for that host is
  from a newer process **if it arrived on the current connection or from a
  fetch started after that connection opened**; the SPA replaces what it holds
  (new epoch, its `seq`).
- A late frame from a *closed* connection must be dropped by the SPA (the
  SPA half must verify its `connectHostEvents` reconnect path guarantees this;
  not assumed here).
- Recommended SPA pattern after a switch: `GET ?fresh=1`, then apply only if
  `(epoch == held.epoch && seq > held.seq) || epoch != held.epoch`; the first
  frame of every new WS connection (the subscribe snapshot) re-establishes the
  held epoch.

`tmuxInstance` (per entry, unchanged) remains the signal for a **tmux server**
restart; `epoch` is only about the **daemon** process. They are independent.

## 4. Daemon design

### 4.1 Versioned read primitive

In `internal/module/session`:

- `SessionModule` gets `epoch string` (set in `NewSessionModule`), and
  `snapMu sync.Mutex` + `snapSeq uint64`.
- `func (m *SessionModule) versionedList() (VersionedSessions, error)`:
  lock `snapMu`; `seq := snapSeq+1`; `ListSessions()`; on success
  `snapSeq = seq`, return `{Epoch, Seq, Sessions(non-nil)}`; unlock.
  On error `snapSeq` is not advanced.
- `ListSessions()` stays public and unversioned (monitor / agent / peers keep
  calling it; they neither need nor take the lock).

### 4.2 Call sites that switch to `versionedList`

- `handleList` when `r.URL.Query().Get("fresh") == "1"` → envelope (§3.1).
- `broadcastSessions` (wait-for path), `tickNormal` (ticker path), and the
  `OnSubscribe` snapshot → frames carry `epoch`/`seq`.
  - `tickNormal`'s change hash stays computed over `(instance, sessions)` only,
    so a new `seq` alone never triggers a broadcast.
- `core.HostEvent` gains `Epoch string \`json:"epoch,omitempty"\`` and
  `Seq uint64 \`json:"seq,omitempty"\``; `EventsBroadcaster` gains
  `BroadcastEvent(HostEvent)`; `Broadcast(session, type, value)` becomes a thin
  wrapper (existing callers and frames byte-identical).

### 4.3 List-cache invalidation (benefits old clients too)

`invalidateListCache()` (zero `listCacheAt`) is called wherever
`invalidateNameCache()` is called today: `CreateSession` success, rename
success, delete success, and `broadcastSessions` (tmux wait-for ⇒ any tmux
change, including external ones). The plain `GET /api/sessions` then stays
cached only between changes. This is an improvement, not a guarantee — the
guarantee is `?fresh=1`.

### 4.4 Cost

The lock serializes versioned reads (one `tmux list-sessions` + per-session
metadata at a time). Unversioned `ListSessions` callers are not serialized.
A fresh fetch is on-demand (after a switch), not polled.

## 5. Compatibility matrix

| SPA \ daemon | old daemon | new daemon |
|---|---|---|
| old SPA | today | plain GET unchanged (fresher); WS frames have 2 extra keys, ignored |
| new SPA | `?fresh=1` → array ⇒ unversioned; frames without epoch ⇒ unversioned ⇒ SPA keeps today's "no evidence" behaviour | full contract |

## 6. Tests (TDD)

- versionedList: seq strictly increases across calls; epoch stable within a
  module, differs between two modules; error does not consume a seq; empty ⇒
  `[]` not null; concurrent callers get distinct seqs whose order matches read
  order (fake executor records read order).
- `GET ?fresh=1`: envelope shape; bypasses a warm cache (mutate fake tmux
  inside TTL → fresh sees it, plain may not); `fresh=0` / absent ⇒ bare array
  byte-compatible with today.
- WS: subscribe snapshot, wait-for broadcast and ticker broadcast frames carry
  epoch+seq; a non-sessions broadcast frame has no `epoch`/`seq` keys.
- Cache invalidation: plain GET after create / rename / delete within the TTL
  reflects the mutation.
- Read-your-writes: DELETE then `?fresh=1` never lists the killed session.
- Shared counter (§3.3 rule 4): interleave `?fresh=1` fetches with triggered
  WS pushes (`broadcastSessions`, `tickNormal`, subscribe snapshot); all seqs
  obtained, ordered by the fake executor's recorded read order, are strictly
  increasing.
- Seq-belongs-to-read (§3.3 rule 5): the fake executor stamps each read with
  its own ordinal and injects it into the list (e.g. a session name carrying
  the ordinal); every versioned payload's list must be the one read under the
  same seq (seq ↔ read ordinal is a bijection, monotone). A warm plain-GET
  cache never leaks into a versioned payload.

## 7. Rollout

Daemon-only PR → merge → bump → deploy mlab and air26 (ask the coordinator
first; `bin/pdx` swap is rm→cp→mv). The SPA half (purdex-3b) can land before or
after: against an old daemon it degrades to "unversioned" (§5).
