# Spec — Profile Sync: one id per daemon across devices (host rekey on pull)

Status: draft, 2026-09-23. Part 2 of the cross-device host-id fix. Part 1 (daemon identity on every host) shipped
in #1349 / #1351 — see `docs/specs/2026-09-23-host-daemon-id-spec.md`.

## 1. The defect

A host id is random per device (`generateId()`, the built-in `mlab` included — `useHostStore.ts`). The `hosts`
section is keyed by that id, and so are pane fields in `tabs.*` (`tmux-session.hostId`, `source.hostId`,
`execution.host`) and `settings` (`purdex-host-settings.hosts[id]`, new-tab preset columns `sessions:<id>`).
Two real devices that each added the same daemon therefore disagree on its id, and today:

- the second device's wizard **pull** ends `locked:invalid` (`hostsRefusal` → `removes-master-host`), and
  *Keep this device's* pushes its own ids, which locks the first device the same way;
- every other daemon both devices added is, on pull, an id the payload lacks → `deleteHostCascade` marks every
  pane on it `host-removed` in every world and clears its host settings — and those marks go back to the SOT.

Every earlier acceptance loaded one host config into both browsers (same ids) and missed it.

## 2. The fix, in one sentence

**Before a pull attaches, the device renames its own host ids to the SOT's ids for the same daemon**, everywhere a
host id is stored; afterwards ids agree, and a host added later on either device travels with its id (the `hosts`
section carries it) — so the rename is needed once per attach, never during sync.

Push needs none of it: the wizard offers push only while the SOT profile is EMPTY (P3d-3 as built).

## 3. Matching: which local host is which SOT host

Input: the SOT's `hosts` payload (one `getSection(hostId, profileId, 'hosts', {expectEndpoint})` in `prepareRun`,
after the re-list) and this device's hosts.

A local host L **matches** SOT host S when:
1. **Identity**: `S.daemonId` is set, L is **verified** (`selectDaemonIdVerified`) with `L.daemonId === S.daemonId`,
   and L has **no mismatch** (`selectDaemonIdMismatch` undefined). A host this device never verified is never
   matched by identity.
2. **Fallback, only when `S.daemonId` is absent** (a payload written before part 1): same `token` (non-empty), same
   `port`, and L has no mismatch. (One daemon has one token; the address is not compared — the same daemon is
   often reached by different addresses.)

Rules:
- A match must be one-to-one. Two local hosts matching one S, or one L matching two S → **ambiguous**, refused.
- `L.id === S.id` → nothing to rename.
- `S.id` already used locally by a DIFFERENT host (not L) → **id-collision**, refused (6-char ids; rare).
- **The attach host H** (the one the wizard talks to) must match some S — else the pull would remove it:
  **master-unmatched**, refused. When H is unverified, the refusal says so ("connect once so this device can check
  which daemon it is"), since verification happens on connect.
- **H's address vs S's address**: after the pull H takes S's `ip`/`port` (the `hosts` section travels whole — user
  decision 7). If they differ, the first pull is refused by `changes-master-host` today. The wizard checks it up
  front and refuses with **master-address-differs**, naming both addresses (fix: edit one side's address so both
  devices use the same one). Not solved here: a device that must reach the daemon by a different address.
- Local hosts that match nothing are **removed by the pull** (existing semantics). The direction step already
  says what a pull replaces; it now also lists these hosts by name ("removed from this device: A, B").

All refusals happen in `prepareRun` — before the promote, before anything is written. Each has a sentence.

## 4. Renaming: `rekeyHosts(map)` — `lib/profile/host-rekey.ts`

`map: Record<oldId, newId>` (only entries with `oldId !== newId`). One synchronous block (no `await` between the
first write and the last — same rule as `applyHostsSection`), inside the world lock and while no master is attached
(the wizard's step 1 guarantees it; `rekeyHosts` re-checks and refuses `attached`).

Persisted, renamed in place (every row of the measured table, "part 2 measurement", 2026-09-23):
- `useHostStore`: `hosts` keys and `.id`, `hostOrder`, `activeHostId`, `devHostId`; `runtime` keys moved (so the
  connection and the `daemonIdVerified` mark follow).
- Pane fields in the on-screen tab store, `parkedMaster` and every slave world (`updateParkedWorlds`):
  `tmux-session.hostId`, `source.hostId` (`editor`, `image-preview`, `pdf-preview`), `execution.host`.
- `useHostSettingsStore.hosts` keys; new-tab presets via `migrateId('sessions:<old>', ['sessions:<new>'])`.
- `useSessionStore.activeHostId`; `useHistoryStore` closed tabs / browse history pane fields;
  `useRecentFilesStore` (`source.hostId` and its key); `useHeadlessLauncherMemoryStore.byHost` keys.
- `useProfileStore.pendingDetaches[].hostId` (and their keys).

In memory, cleared for the old ids (refilled on the next fetch/connect): agent, execution, execution-list, nex-host,
peer, session-cwd, upload, rebuild, backup, host-config caches; `useSessionStore.sessions[old]` moved to `[new]`.
Editor buffers keyed `daemon:<old>:<path>`: renamed if clean; a dirty buffer blocks the rekey (**dirty-editor**,
refused with the file names) — never lose an unsaved edit.

Rollback: snapshot every persisted store touched before the first write; any throw → write the snapshots back and
answer `write-failed`. Cross-window: every touched persisted store is shared through storage events; the world
fence epoch is bumped like `switchActiveProfile` does, so other windows rehydrate before writing.

## 5. Where it runs

`runPlan` becomes promote → save → **rekey** → attach. The rekey uses the plan frozen by `prepareRun` (the map and
the SOT hosts fingerprint). Right before writing, it re-reads the SOT `hosts` rev (one GET with `expectEndpoint`);
changed → nothing written, back to the direction step (`profile-changed`, as today). `attachMaster` is then called
with H's NEW id. A failed rekey stops the run like any sub-step (P3d-3: nothing after it runs, nothing before it is
undone; *Try again* re-runs from it). The run's sub-step list gains `rekey` (shown only when the map is non-empty).

Also here: `listProfiles` in `wizard-run.ts` gets `expectEndpoint` (38's #1340 finding).

## 6. Not in scope

- Renaming during sync (after attach). Ids agree from the attach on; a host added later travels with its id; add-host
  refuses a second host for a daemon already present (part 1).
- A daemon reached by different addresses on different devices (see §3; needs addresses to become device-local —
  a change to user decision 7).
- Loopback hosts (`127.0.0.1`) meaning different daemons per device: part 1's mismatch flag keeps them out of
  matching; they are removed by the pull like any unmatched host.

## 7. Acceptance (real machine)

Two clients with **independent host ids** (each adds mlab itself — see memory `feedback_acceptance_distinct_host_ids`):
A: wizard new → push. B: wizard existing → pull (save first). Expect: no lock; B's mlab id becomes A's; B's saved
slave's panes follow the new id (open one: its session is still there); B's other hosts that A lacks are removed
and were listed first; then edits both ways and one conflict resolved, as in the P3 acceptance. Refusal cases by
hand: B's mlab unverified, B using a different address for mlab.
