# Spec — Profile Sync: one wire identity per daemon (boundary translation)

Status: approved direction 2026-09-23 (user chose "B"). Part 2 of the cross-device host-id fix; part 1 (daemon
identity on every host) shipped in #1349 / #1351 (alpha.434) — `docs/specs/2026-09-23-host-daemon-id-spec.md`.
Supersedes the first part-2 draft (runtime rekey of local ids), abandoned after its plan review found five
critical problems, all from rewriting host ids in live, multi-window stores. Architecture comparison: codex task
`task-mudfzn9g-8rqv3u`.

## 1. The defect

A host id is random per device (the built-in `mlab` included). The synced sections key by it — `hosts` (record keys,
`hostOrder`), `tabs.*` (`tmux-session.hostId`, `source.hostId` of editor / image-preview / pdf-preview panes,
`execution.host`), `settings` (`purdex-host-settings.hosts[id]`, new-tab preset columns `sessions:<id>`). Two
devices that each added the same daemon disagree on its id, so the second device's pull ends `locked:invalid`
(`removes-master-host`) and every other shared daemon is cascaded as removed.

## 2. The model

Three identities, each with one job:
- **local id** — this device's surrogate key; every store, API route, connection, URL and composite key keeps
  using it. **It never changes.**
- **daemon identity** — `HostConfig.daemonId` (part 1): the claimed identity, synced; runtime verification
  (`selectDaemonIdVerified` / `selectDaemonIdMismatch`) says whether this device confirmed it.
- **sync id** — the host's key ON THE WIRE only: `syncIdOf(daemonId)`, identical on every device.

Profile Sync translates at its boundary: **build** (collector → section payload) maps local → sync, **apply**
(section payload → stores) maps sync → local. Nothing else in the app learns of sync ids. Section hashes and the
conflict stash are of wire payloads, so they agree across devices.

## 3. `syncIdOf(daemonId)`

`"d1_" + base36(first 80 bits of SHA-256(UTF-8(daemonId)))`, lower-case, zero-padded to 16 characters. Fixed forever;
a new algorithm gets a new prefix (`d2_`). Uses `lib/crypto-hash.ts` (both paths byte-identical — P2b-2).
The `d1_` prefix cannot collide with a local id (6-char base36, no underscore) or a profile/client id.

## 4. The identity codec — `lib/profile/host-identity.ts` (pure)

`identityOf(hosts): Identity` from ONE snapshot of the host store:
- a host whose `daemonId` is a valid claim (`isValidDaemonId`) → wire id `syncIdOf(daemonId)`;
- a host without one → wire id = its **local id** (legacy fallback: it still syncs, as today — two devices then
  show two entries for that daemon until it learns its identity; nothing is destroyed);
- two local hosts claiming the same daemon, or two claims hashing to one sync id → `conflict` (list of ids);
  the collector then builds NOTHING that names hosts (see §6) and reports problem `host-identity-conflict`.

Runtime verification never changes a wire id (a hash must not depend on reachability). A host with a runtime
**mismatch** (this device reached a different daemon at that address) → the profile's sync is paused with
`blocked: 'host-identity-mismatch'` naming the host, until the user fixes its address or removes it. No payload is
built or applied meanwhile.

`toWire*/fromWire*` translators for: hosts payload (keys, `.id`, `hostOrder`), a pane layout (the three fields, the
whole tree), the host-settings record, preset column strings `sessions:<id>`.

## 5. Build (local → wire)

`buildHostsSection`, `buildTabsSection`, the settings builder translate through ONE `Identity` taken per collector
pass, so hosts / tabs / settings of one pass agree. A pane naming a local id the snapshot lacks is left as is (it
already means "removed host" locally).

## 6. Apply (wire → local)

- **hosts**: match each incoming row to a local host: by wire id = `syncIdOf(local.daemonId)`, else (legacy row
  whose wire id is a local-id-shaped string) by that local id, else — a legacy row from ANOTHER device, carrying
  a `daemonId` — by `daemonId`. Matched → update that local host in place (local id kept). Unmatched → create a
  local host with a NEW random local id. Local hosts matched by no row → removed (`deleteHostCascade`, as today).
  `hostsRefusal` compares the master by identity: the row that matches the master's local host must exist
  (else `removes-master-host`) and its ip / port / token are compared as today.
- **tabs.* / settings**: translate wire → local with the Identity AFTER the hosts apply (the executor already pulls
  `tabs.*` only once `hosts` is settled; **`settings` gains the same gate** — today it is not gated on `hosts`, and a
  `sessions:<sync id>` preset would be pruned as unknown). A wire id nobody maps → left as is → the existing
  "unknown host" handling (`host-removed` mark).
- After apply, the hash reported back is of the wire form (the builder does the translation — one path).

## 7. Transition from ordinal-2 data

Ordinals `hosts` 2→3, `tabs` +1, `settings` +1 (the wire id changes meaning; a client that does not translate must
be kept out — `locked:schema` until it upgrades, user decision 6). An SOT still at the old ordinals holds local
ids of whichever device pushed: apply matches them as in §6 (local id, then `daemonId`) and translates tabs /
settings through the resulting map; the next build is canonical and is pushed. All of hosts / tabs / settings move
within the attach's first reconciliation, so no mix of old and new keys is left on the SOT.

## 8. Attach (wizard)

Pull requires the attach host to be **verified** with no mismatch (checked in `prepareRun`; refusal sentences
`master-unverified` / `master-mismatch`). Push unchanged. `listProfiles` in `wizard-run.ts` gets `expectEndpoint`
(finding from #1340). No store is rewritten by the wizard.

## 9. Not in scope

- A daemon reached by different addresses on different devices: the `hosts` row carries one ip/port (user decision
  7); the master's `changes-master-host` check still refuses it. Say it in the wizard when detected.
- Deterministic local ids for new hosts (an optimisation; not needed for correctness).

## 10. Acceptance (real machine)

Two clients with **independent** host ids (each adds mlab itself). A: new → push. B: existing → pull (save first).
Expect: no lock; B's mlab keeps its local id; the SOT `hosts` row key is `d1_…`; edits both ways (a tab on mlab
opened on A appears on B bound to B's mlab and attaches); a conflict resolved; B's saved slave untouched. Also:
a third host only B has (no daemonId: unreachable) — B's pull removes it and the wizard listed it first; an
ordinal-2 SOT profile written by an alpha.434 client pulls cleanly and is rewritten canonical.

## 11. Plan review (codex `task-mudm3zu7-gw99zj`, 11 findings, all taken) — these override the text above

1. **Every host-bearing preset column**, not only `sessions:<id>`: `headless:<hostId>` too (execution module,
   `lib/headless-new-tab-providers.tsx`). The translator takes the list of host-bearing prefixes from ONE constant;
   PR 1's task 0 greps for any other `<prefix>:<hostId>` producer and adds it.
2. **Legacy ids stay resolvable after the hosts row went canonical.** A canonical `hosts` row carries
   `aliases: string[]` — the legacy wire keys (foreign local ids) it was matched from, deduplicated, ≤ 16, oldest
   dropped. `fromWire` for tabs / settings resolves: sync id → local; else an alias of some row → that row's daemon →
   local; else unknown (as today). This covers an interrupted transition (hosts canonical, tabs still legacy) on
   any device, not only the one that did the matching. `aliases` is in the hosts projection (ordinal 3 already).
3. **No "collector pass".** The identity is computed SYNCHRONOUSLY (`syncIdOfSync`) from the host store at every
   build of a host-bearing section. Consistency across sections comes from invalidation instead: the collector
   watches an identity signature (sorted `[localId, wireId]` pairs + conflict) and, when it changes, schedules
   EVERY host-bearing section (`hosts`, `settings`, all `tabs.*`) — a daemonId learned later re-keys all of them.
4. **Identity conflict pauses the profile**, like a mismatch: `blocked: 'host-identity-conflict'` (PR 3's
   mechanism in start.ts; the executor is disposed, nothing built, pushed or applied), not just "build nothing".
5. **One-to-one on apply.** An incoming payload with two rows for one daemon (two rows with the same `daemonId`, or a
   canonical row and a legacy row resolving to one daemon) is `locked:invalid`, new code `duplicate-host-identity`.
6. **A row carrying `daemonId` matches by `daemonId` ONLY.** The legacy local-id match applies only to a row with no
   `daemonId`, and only to a local host that has no `daemonId` either (or the same). A foreign key that happens to
   equal another local host's id can no longer capture it.
7. **Interrupted transition** is tested at every write boundary (hosts pushed, tabs not; tabs partly; settings
   not), across a restart, from both devices — with finding 2's aliases.
8. **Settings gate = hosts settled AND workspaces settled** (conjunction), each gate's release pumps; tested both
   orders and a gate re-closing after the pull was fetched.
9. **Mismatch pause** (PR 3) is decided on entering master mode, on every host-store change incl. runtime-only
   updates (`selectDaemonIdMismatch` over ALL hosts), disposes the leader at once, and rebuilds it when cleared;
   tested for each.
10. PR 3 needs `lib/profile/api.ts` (`getSection` with `expectEndpoint`) and a `WizardPlan` field for the removal
    list — its file list says so.
11. **PR 3 starts after PR 1 is merged** (it imports `matchIncomingHosts`); PR 2 and PR 3 run in parallel after it.

**PR 1 task 0 (purdex-38, 2026-09-23)** — inventory confirmed: 10 places, all listed above (preset prefixes are
exactly `sessions:` and `headless:`; `source.hostId` only when `source.type === 'daemon'`). Two notes for PR 2:
(a) `execution.host` may be `''` (the no-host fallback) — passed through untranslated; (b) `useNewTabBootstrap`
prunes `sessions:X` / `headless:X` whose X is not in `hostOrder` once the host store hydrates
(`useNewTabBootstrap.ts:27-33`): a preset column apply could not translate would be deleted locally and pushed back —
settings must be translated before any prune can see it, and an UNKNOWN wire id must not reach the store in a form
the prune deletes (keep it out of the applied presets, or keep the local value; decide in PR 2 and test it).
