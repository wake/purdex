# Host daemon identity (`daemonId`) — spec & plan (host-id fix, part 1)

Status: draft 1 (2026-09-23) · Owner: purdex-cleanup · Coordinator: purdex-fb (does part 2: rekey before
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
`HostConfig.daemonId?: string` — present only when non-empty. Never set to `""`. `updateHost`'s
`Pick` gains `daemonId`.

### D2. Where it is learned (one request per host, only while it is missing)
1. **Add host (dialog)**: after the confirm call succeeds (daemon now in normal state), one
   authenticated `GET /api/info` with the base URL + token just used (new helper
   `fetchInfoAt(base, token)`, raw fetch like `fetchTokenAuth`). Failure → the host is added
   without `daemonId` (backfill will learn it); the add is never blocked by `/api/info`.
2. **Backfill on connect**: a subscription modelled on `host-config-loader.ts` — on a host's
   transition to `connected` (or an endpoint change) **and only if it has no `daemonId`**, call
   `fetchHostInfo(hostId)` once. So it costs one request per host per install, not per connect.
   (The coordinator's "reuse an existing call" is not possible for every host — no such call exists
   on connect; this is the cheapest full-coverage option. Opportunistic reuse: `nex-host-effects`
   and `OverviewSection` also report `host_id` through the same setter.)
3. `registerLocalHost` and the built-in default host: learned by (2) on first connect.

### D3. Write rule — never overwrite, never ping-pong
`learnDaemonId(hostId, id, endpoint)` writes only when: `id !== ""`, the host still exists, its
endpoint (`ip:port`) is still the one the answer came from, and it has **no** `daemonId` yet.
A different value than the stored one is **not** written (logged once; part 2 may surface it).
Reason: a synced host whose address reaches a different daemon on each device (e.g.
`127.0.0.1:7860`) would otherwise make devices overwrite each other forever.
Re-pointing a host (`updateHost` with a changed `ip` or `port`) clears `daemonId` in the same
write, so it is learned again for the new endpoint.

### D4. Duplicate detection on add (by daemon)
In `AddHostDialog`, after learning the new daemon's `daemonId`:
- same ip+port as an existing host → today's behaviour (update that host's token; now also learn
  its `daemonId` if missing). Unchanged.
- different endpoint, **same `daemonId`** as an existing host `H` → do **not** add a second host.
  Show: "This daemon is already added as “{name}”." (`hosts.duplicate_daemon`). If the confirm was
  the pairing route (the daemon's token just changed), also update `H`'s token so `H` keeps working.
  The dialog stays open on the message with a Close.
- `/api/info` failed or `host_id` empty → cannot tell → added as today.

### D5. Sync (`hosts` section)
- `PROJECTIONS.hosts` gains `hosts.*.daemonId`; `SECTION_SCHEMA_ORDINAL.hosts` 1 → 2 (projection
  guard test updated with the new fingerprint).
- `isHostsPayload`: `isOptional(h.daemonId, isNonEmptyString)` — an ordinal-1 payload (field absent)
  is well-formed.
- **Upcast on pull** (P3e precedent): when an incoming host has no `daemonId` and the local host with
  the same id has one, keep the local value (in `applier.applyHosts`, not `apply-to-stores.ts`). The
  applied state then differs from the pulled payload → one push upgrades the SOT. If the incoming
  host HAS a `daemonId`, it wins (SOT is truth on pull; D3 only governs local learning).
- Coexistence (as P3e): a new client never locks on an ordinal-1 hosts row; an old client seeing
  ordinal 2 goes `locked:schema` (accepted P3e behaviour) and writes nothing — no ping-pong.
- `apply-to-stores.ts` is not expected to change (validator + applier cover it). If it must, the
  coordinator is told first.

### Not in scope (part 2, coordinator)
Rekey by `daemonId` before wizard pull, `hostsRefusal`, `wizard-run.ts`, `start.ts`. Peer pairing
reuse of stored `daemonId` (possible later).

## 4. Plan (TDD, one commit per task, PR ≤ 20 files)

1. **Store**: `HostConfig.daemonId`, `updateHost` Pick + re-point clears it, `learnDaemonId` action
   (D3). Tests: never `""`, never overwrite, endpoint-changed answer dropped, re-point clears.
2. **API helper**: `fetchInfoAt(base, token)` (raw, Bearer) in `host-api.ts` + tests.
3. **Backfill subscription**: `lib/host-daemon-id.ts` `startDaemonIdBackfill()` (connected &
   missing → one `fetchHostInfo`; endpoint change re-arms; wired where `host-config-loader` is
   started). Opportunistic: `nex-host-effects.load()` and `OverviewSection` call `learnDaemonId`.
   Tests: one request per missing host, none when present, none when not connected, answer for an
   old endpoint dropped.
4. **AddHostDialog**: learn after confirm; duplicate-by-daemon (D4) incl. pairing-route token update;
   i18n `hosts.duplicate_daemon` (en + zh-TW). Tests per D4 branch.
5. **Sync**: projection + ordinal 2 + validator + applier upcast; tests copied from P3e
   (`projections.test.ts` shape/guard, `applier.test.ts` hosts well-formedness + upcast,
   `executor.direction.integration.test.ts` NEW/OLD-side coexistence for hosts).
6. Verify: tsc / vitest / lint / build; mutation for D3 (overwrite, endpoint guard), D4 (dup check),
   D5 (upcast, validator). Real machine (single client, :5177): add mlab → `daemonId` stored
   (`mini-lab:…`); add the same daemon again via a second address — `<any>.mlab.host:7860` resolves
   to 100.64.0.2 (public wildcard) → blocked with the message.
