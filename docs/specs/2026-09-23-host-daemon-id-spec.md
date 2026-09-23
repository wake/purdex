# Host daemon identity (`daemonId`) — spec & plan (host-id fix, part 1)

Status: final (2026-09-23; plan review task-muddsq1i-rxkdeg applied; D2–D5 agreed by coordinator) · Owner: purdex-cleanup · Coordinator: purdex-fb (does part 2: rekey before
wizard pull, `hostsRefusal`, `wizard-run.ts`, `start.ts`).

## 1. Problem

A host's SPA id is random per device (`useHostStore.ts` `generateId()`, even for the built-in `mlab`
default), so two devices never agree on the id of the same daemon. The `hosts` section is keyed by
that id → the second device's wizard pull always hits `locked:invalid` (`removes-master-host`), and
ids that don't match go through `deleteHostCascade`. The daemon already has a stable identity:
authenticated `GET /api/info` → `host_id` (`internal/config/hostid.go`: `<shorthost>:<6 base36>`,
persisted in `config.toml`; `""` only if persisting failed at boot). The SPA never stores it.

This part makes every device record that identity per host and carry it in the `hosts` section, so
part 2 can match hosts across devices by daemon, not by SPA id.

## 2. Facts that shape the design (inventory, 2026-09-23)

- `/api/info` is behind `TokenAuth` (only `/api/health` is open). `HostInfo.host_id` is already typed.
- **No connection-flow call to `/api/info` exists**: the state machine uses `/api/health` +
  `POST /api/ws-ticket`. Existing `/api/info` callers: `nex-host-effects.load()` (only hosts the Nex
  store tracks), `OverviewSection` (host page), `peer-pairing-load` (Peers page).
- `AddHostDialog` never calls `/api/info`; its confirm uses raw base-URL helpers (`fetchTokenAuth` /
  `fetchPairSetup`, `{ok}` only). Its duplicate check today: same ip+port → silently `updateHost(token)`.
- `registerLocalHost` makes no network call (Electron hands `{url, token, hostname}`); the built-in
  default host has no token and makes no call.
- `addHost` spreads extra fields; `sanitizeHostConfig` keeps unknown fields; persist `version: 1`, no
  migrate — adding an optional field needs no migration.
- `hosts` projection (`projections.ts`) lists explicit fields; `SECTION_SCHEMA_ORDINAL.hosts = 1`;
  `isHostsPayload` allows exactly the projection's keys; `applyHosts` replaces host objects whole.

## 3. Decisions

### D1. Field
`HostConfig.daemonId?: string` — present only when non-empty. Never set to `""`. Only
`observeDaemonId` writes it locally (sync applies through its own setState path): `addHost` and
`updateHost` do not accept it and strip it if passed (PR review #4).

### D2. Stored value vs local verification — the model (revised after plan review `task-muddsq1i-rxkdeg`)
Two things, deliberately separate:
- **`HostConfig.daemonId` (persisted, synced)** — the *claimed* identity of the daemon behind this
  host entry. It converges like every other synced field: on pull, the SOT value wins.
- **`daemonIdMismatch` (runtime only)** — this device's *verification*: `/api/info` at this host's
  endpoint answered a different `host_id` than the stored claim. Not persisted, not synced, not in
  the projection. Part 2 (rekey) must exclude a host that carries it (or that was never verified).
Why not "local wins": a synced address that reaches a different daemon per device (e.g.
`127.0.0.1:7860`) would then make devices push their own values at each other forever. With "SOT
wins + local flag" the stored value converges and each device knows whether it holds for itself.

### D3. Learning and verification
`observeDaemonId(hostId, observed, atRequest)` — one entry point for every `/api/info` answer;
`atRequest = requestAtOf(host) = { endpoint: ip:port, token }` captured before the request:
- drop the answer if the host is gone, or its endpoint or token is no longer the one in `atRequest`
  (PR review #2), or `observed === ""` (daemon has no stable id — nothing learned, nothing flagged);
- stored absent → **write** `daemonId = observed` (a local write → synced like any edit);
- stored === observed → clear any `daemonIdMismatch`;
- stored ≠ observed → **do not write**; set `daemonIdMismatch = { stored, observed, endpoint }` and
  `console.warn` once per (host, observed). Covers a reinstalled daemon (`config.toml` lost → new
  `host_id`) and a synced address that reaches another daemon here.
- A mismatch flag whose `endpoint` or `stored` no longer matches the host is ignored/cleared (so a
  re-point — local `updateHost` or one arriving by sync — never inherits an old flag).
Local re-point (`updateHost` changing `ip` or `port`) clears `daemonId` in the same write,
unconditionally (it is then learned for the new endpoint). A re-point arriving by sync carries its own `daemonId` (learned by the
device that re-pointed).

### D4. When `/api/info` is asked (verification triggers)
One request per trigger, per host, only while connected:
1. **Add host (dialog)**: after the confirm succeeds, `fetchInfoAt(base, token, signal)` (raw, Bearer,
   bounded by a 5 s timeout and aborted when the dialog unmounts — PR review #3) — the host does not
   exist in the store yet. Failure never blocks the add. **Pairing route**: once `fetchPairSetup`
   succeeded the daemon's token is rotated, so the host (with the new token, no `daemonId`) is
   persisted however the dialog ends — probe timeout/failure, or dismissal mid-probe (saved exactly
   once; the subscription learns the id later). **Token route** (nothing rotated): dismissal mid-probe
   simply cancels.
2. **Verification subscription** (`lib/host-daemon-id.ts`, modelled on `host-config-loader.ts`):
   a host's transition to `connected`, a change of its endpoint or token, or a change of its stored
   `daemonId` (e.g. by sync) while connected → one `fetchHostInfo(hostId)`; endpoint + token are
   captured before the request and passed to `observeDaemonId`. **Freshness** (PR review #2): each
   request takes the host's next generation; a newer trigger or the host's deletion invalidates older
   ones, so only the newest answer applies, and never to a host deleted and re-added under the same
   id. The stored-`daemonId` change caused by the subscription applying its own answer is recognised
   synchronously (not by remembering past answers), so a stale answer can never make a later sync
   change skip re-verification. A learn by another path (Nex, Overview, dialog) costs one extra
   verification request. A failed request (network, 401, 5xx) is not
   retried until the next trigger. Cost: one `/api/info` per (re)connect or change — not per render,
   not polled. (No connect-time `/api/info` exists today to piggyback on — inventory §2.)
3. **Opportunistic**: `nex-host-effects.load()` and `OverviewSection` already fetch `/api/info`; each
   captures the endpoint before its request and feeds `observeDaemonId` (Nex: inside its existing
   `stillCurrent` guard).
`registerLocalHost` and the built-in default host are covered by (2).

### D5. Duplicate detection on add (by daemon)
In `AddHostDialog`, after learning the new daemon's `host_id` `X`:
- same ip+port as an existing host → today's behaviour (update that host's token). Unchanged.
- different endpoint, and an existing host `H` with `daemonId === X` **and no `daemonIdMismatch`**
  → do not add a second host; the dialog shows "This daemon is already added as “{name}”."
  (`hosts.duplicate_daemon`) with Close.
  - **Pairing route only** (the daemon's token was just replaced, so `H`'s saved token is now stale):
    the message adds an explicit action "Use this address for “{name}”" → re-points `H` to the
    endpoint just paired, with the new token (an `updateHost` re-point: `daemonId` cleared and
    re-verified on connect). Never silently rewrite `H`'s token — `H`'s own endpoint may reach a
    different daemon (plan review #5).
- `H` flagged with a mismatch, `/api/info` failed, or `host_id` empty → cannot tell → added as today.

### D6. Sync (`hosts` section)
- `PROJECTIONS.hosts` gains `hosts.*.daemonId`; `SECTION_SCHEMA_ORDINAL.hosts` 1 → 2 (projection
  guard test updated).
- `isHostsPayload`: `isOptional(h.daemonId, isNonEmptyString)` — an ordinal-1 payload is well-formed.
- **Upcast on pull**: an incoming host without `daemonId` keeps the local one (in `applier.applyHosts`);
  an incoming host with one wins (D2). The upgraded state then pushes once.
- Two new clients upcasting different local values at the same time (possible only for a
  device-relative address) conflict like any concurrent edit; after it resolves, the stored value is
  one of them and the other device carries `daemonIdMismatch` — no loop (pinned by a test).
- Coexistence: a new client never locks on an ordinal-1 hosts row. An old client that meets an
  ordinal-2 row goes `locked:schema` for the **whole profile** and stops syncing entirely until it is
  updated (P3e's accepted behaviour; the coordinator confirms this is acceptable for this bump).
- `apply-to-stores.ts` does not change (the mismatch flag is keyed by endpoint + stored value, so a
  host re-pointed by sync never inherits one — D3).

### Not in scope (part 2, coordinator)
Rekey by `daemonId` before wizard pull, `hostsRefusal`, `wizard-run.ts`, `start.ts`. Peer pairing
reuse of stored `daemonId` (possible later).

## 4. Plan (TDD, one commit per task, PR ≤ 20 files)

1. **Store** (`useHostStore.ts`): `HostConfig.daemonId`; `updateHost` Pick gains it and a re-point
   (ip/port change) clears it; runtime slice `daemonIdMismatch` (not in `partialize`);
   `observeDaemonId(hostId, observed, endpointAtRequest)` per D3. Tests: never `""`; absent → write;
   equal → clear flag; different → no write + flag + one warn; stale endpoint / deleted host → dropped;
   flag ignored after re-point (local and by a store replace simulating sync).
2. **API helper**: `fetchInfoAt(base, token)` in `host-api.ts` + tests.
3. **Verification subscription** `lib/host-daemon-id.ts` (D4.2), started where `host-config-loader`
   starts. Tests: request on connected transition; on endpoint/token change; on stored `daemonId`
   change while connected; none while disconnected; failure → no retry until next trigger; answer for
   an old endpoint dropped; deleted-before-answer dropped.
4. **Opportunistic** (D4.3): `nex-host-effects.load()` (inside `stillCurrent`) and `OverviewSection`
   feed `observeDaemonId` with the endpoint captured before the request. Tests: stale answer after a
   re-point is dropped.
5. **AddHostDialog** (D4.1 + D5): learn after confirm; duplicate-by-daemon; pairing-route explicit
   "use this address" re-point; `hosts.duplicate_daemon` / `hosts.duplicate_daemon_use_address` (en +
   zh-TW). Tests per branch, incl. a mismatch-flagged `H` is not treated as a duplicate.
6. **Sync** (D6): projection + ordinal 2 + validator + applier upcast. Tests copied from P3e:
   `projections.test.ts` (shape, guard, coexistence by shape), `applier.test.ts` (well-formed without
   `daemonId`, upcast keeps local, incoming wins), `executor.direction.integration.test.ts` (NEW side:
   ordinal-1 row → one upgrade PUT then quiet; OLD side: ordinal-2 row → `locked:schema`, writes
   nothing; two new clients with different local values → converge, no loop).
7. Verify: tsc / vitest / lint / build; mutations for D3 (overwrite, endpoint guard, flag), D4
   (trigger on daemonId change), D5 (dup check, mismatch exclusion), D6 (upcast, validator).
   Real machine (single client, :5177): add mlab → `daemonId` stored (`mini-lab:…`); add the same
   daemon again via `<any>.mlab.host:7860` (public wildcard → 100.64.0.2) → blocked with the message.
   If the file count exceeds 20, split at the task-5/6 boundary (UI+store PR, then sync PR).
