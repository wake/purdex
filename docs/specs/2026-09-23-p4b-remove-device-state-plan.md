# Profile Sync P4b — remove device-state and workspace snapshot — spec & plan

Spec: `docs/specs/2026-09-20-profile-sync-spec.md` §3 (inventory table, coupling note), §6 (P4b
row), kickoff 定案 14 ("Host › Snapshots 只留 rebuild 紀錄"). P4a (old Sync) is merged
(#1298–#1302, #1305); P3e (#1297) is merged. Same method as P4a: pure deletion, reverse
verification instead of mutation, codex R1 + attacker.

## 1. What goes, what stays (inventory 2026-09-23)

**Deleted — SPA (≈42 files, ≈9.1 K lines incl. tests)**
- `lib/device-state/**` (uploader, payload, api, restore, merge, identity, reattach, prev,
  `device-name.ts` re-export shim; all tests).
- `components/settings/device-state/**` except `DeviceNameField` (moved, §2).
- `stores/useDeviceStateStore.ts` (+test) — uploader status + re-exports only; not persisted.
- `lib/snapshot/**` (capture, restore, storage, filter, types; all tests).
- Host › Snapshots blocks other than rebuild records: `components/settings/snapshot/{TabsBlock,
  TmuxBlock,ClientSnapshotBlock}.tsx`, `components/hosts/SnapshotsSection.test.tsx` (every case
  covers the deleted behaviour; the rebuild-records tests are in `SnapshotsSection.records.test.tsx`).
- i18n (en + zh-TW, same keys): `settings.device_state.*` (34, of which the 3 device-name keys
  move — §2), 25 of 37 `settings.snapshot.*` (keep `col.{name,cwd,command,health}`, `health.*`
  (built dynamically by `HealthBadge`), `toast.{locked,restoreFailed,restoreError}`),
  `hosts.snapshots.{client_title,client_desc,client_no_dev}`, `rebuild.legacy_shell_only`.
  `hosts.snapshots.host_desc` is reworded (it names captured tmux sessions).

**Deleted — daemon (8 files, ≈1 K lines)**: `internal/module/devicestate/**` (4 routes:
`GET /api/device-state`, `PUT|GET|DELETE /api/device-state/{clientId}`), its registration in
`cmd/pdx/main.go`. `profiles/validate.go` has its own copy of the rules (no import) — only its
comment naming devicestate is reworded.

**Stays**
- `lib/device-name.ts`, `stores/useDeviceNameStore.ts` (Profile Sync reads the name:
  `lib/profile/start.ts`, `wizard-run.ts`, `LocalProfilesBlock.tsx`).
- `STORAGE_KEYS.DEVICE_STATE` = `purdex-device-state`: it is where `useDeviceNameStore`
  persists. The name is misleading but renaming a persisted key is a migration, not a deletion.
- `components/settings/snapshot/{RebuildRecordsBlock,shared}.tsx` (shared trimmed),
  `EditableCwdCell` (also used by `RebuildActionSet`, `ResumeTemplateSettings`),
  `SnapshotsSection.tsx` (trimmed to the rebuild-records block), `lib/profile/**`, `lib/rebuild/**`.
- The lock-owner label strings `'snapshot:restoreAll'` / `'snapshot:undo'` in rebuild tests.

## 2. Decisions

1. **The device-name editor moves, it is not deleted.** `DeviceNameField` is the only caller of
   `setDeviceName`; deleting it would leave the name Profile Sync shows un-editable. It moves
   to `components/settings/profile/` and mounts in `ProfileSection` (top of the section). Its
   3 keys move with it as `settings.profile.device_name{,_aria,_reset}` (same strings).
   This is the one user-visible change besides the deletions: the field used to live in the
   device-state block on the dev host's Host › Snapshots page.
2. **The boot-time default name is KEPT.** `startDeviceStateUploader` (called from `main.tsx`)
   was the only boot caller of `ensureDefaultDeviceName()` — a side effect of the uploader.
   `DeviceNameField` and the profile wizard read the store and rely on it (without it they show
   the "Browser" fallback, and the wizard can persist it), so `main.tsx` calls
   `void ensureDefaultDeviceName()` itself where the uploader used to start (SPA-6, codex R1;
   `src/main.test.tsx`). Nothing else changes at boot.
3. **Residue (same policy as P4a, #1303):**

| residue | decision |
|---|---|
| localStorage `purdex-workspace-snapshot`, `purdex-workspace-snapshot-prev` | Left, unused (nothing reads them after this). Cleanup = follow-up (#1303 gets these two added). |
| localStorage `purdex-device-state` | **Live** — `useDeviceNameStore`. Not residue. |
| daemon `<DataDir>/device_state.db` (table `device_state`) | **Left on disk, not dropped.** Dropping needs code that opens a DB nothing else uses; nothing opens it after this. Added to #1303. |

4. **SnapshotsSection live-session lookup** is keyed on `snap` today (effect deps,
   `LiveResult.snap`, `hasHostData` reads `hostSnap?.sessionMeta`). After the trim it is keyed
   on `hostId` (+ a `refreshSeq` bumped after a rebuild action, replacing `refresh()`), and
   `hasHostData` = `rows.length > 0`. This is the only rewritten logic in the phase; it gets a
   test (lookup re-runs after a rebuild; a stale host's result reads as loading).

## 3. PR split (≤ 20 files each; one branch, each PR a prefix; merged in order)

1. **SPA-1 unwire**: `main.tsx` (uploader), `SnapshotsSection.tsx` trim, `shared.tsx` trim
   (drop `CAPTURE_OWNER`, `computeHealth`, `leafLabel`, `formatRelativeTime`,
   `statusForRestore`, `restoreAction`, `SnapshotActions`, `Status.unattached`), 
   `SnapshotsSection.records.test.tsx` (drop snapshot mocks + the legacy shell-only case),
   delete `SnapshotsSection.test.tsx`, `TabsBlock`, `TmuxBlock`, `ClientSnapshotBlock`;
   move `DeviceNameField(+test)` → profile, mount in `ProfileSection(+test)`; locale edits for
   the moved keys and `host_desc`. After it, device-state UI and `lib/snapshot` are reachable
   only from tests.
2. **SPA-2 device-state UI + uploader + store**: remaining `components/settings/device-state/*`,
   `lib/device-state/{uploader,payload,api,device-name}(+tests)`, `useDeviceStateStore(+test)`,
   edits in `lib/device-name.test.ts`, `useDeviceNameStore.test.ts`; `settings.device_state.*`
   keys.
3. **SPA-3 rest of device-state**: `lib/device-state/{restore,merge,identity,prev,reattach}
   (+tests)`; `adopt-standalone.test.ts` device-state describe; `lib/device-state/` is gone.
4. **SPA-4 snapshot lib**: `lib/snapshot/**`; `adopt-standalone.test.ts` rehydrate case
   rewritten with direct store `setState` (tab store first, as the old helper did);
   `master-world.test.ts` (`replaceTabSnapshot` describe); `batch.test.ts` (second lock
   contender via `withOperationLock('other', …)`); remaining `settings.snapshot.*` /
   `hosts.snapshots.client_*` / `rebuild.legacy_shell_only` keys; comment clean-ups naming
   deleted files.
5. **Daemon**: `internal/module/devicestate/**`, `cmd/pdx/main.go`, `removed_routes_test.go`
   (+4 routes, P4b bullet), `profiles/validate.go` comment. Merged after all SPA PRs; deployed
   to mlab + air26 together with P4a's #1305 (coordinator OK first).

## 4. Verification (every PR tip)

- SPA: `npx tsc --noEmit -p tsconfig.app.json && npx vitest run && pnpm run lint && pnpm run build`.
  Daemon: `go build ./... && go vet ./... && go test ./...`.
- Reverse verification (saved per PR): every deleted import specifier (`lib/device-state`,
  `device-state/`, `lib/snapshot`, `snapshot/storage|restore|capture|filter|types`,
  `TabsBlock`, `TmuxBlock`, `ClientSnapshotBlock`, `useDeviceStateStore`,
  `startDeviceStateUploader`) and exported symbol → zero hits; every removed i18n key →
  zero hits (`HealthBadge`'s dynamic `settings.snapshot.health.*` checked by hand); daemon:
  `internal/module/devicestate"`, `devicestatemod`, `/api/device-state` → zero hits in source.
- Edits are subtraction-only except: the `DeviceNameField` move (byte-identical apart from
  import paths and i18n key names — shown by a diff of old vs new file), the §2.4 lookup
  re-keying (tested), the test rewrites in SPA-4 (listed), comments.
- Mutation only where logic changed: §2.4 (re-keying) — reverting to a never-refreshing key
  must turn the new test red.
- `TestRemovedRoutesAre404` gains the 4 device-state routes; restoring the module must turn it red.

## 5. Review

Codex R1 + attacker once over the SPA stack (base origin/main) and once over the daemon PR.
Attacker focus: anything live still reaching deleted code; Profile Sync's device name path
(ensureDefaultDeviceName on demand, `purdex-device-state` persistence untouched); the
SnapshotsSection re-keying; i18n keys still used (HealthBadge dynamic keys).
