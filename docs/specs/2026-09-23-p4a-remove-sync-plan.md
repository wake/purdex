# Profile Sync P4a — remove the old Sync module — plan

Spec: `docs/specs/2026-09-20-profile-sync-spec.md` §6 (P4a row, :622; risk note :697).
Background: `docs/specs/2026-09-20-profile-sync-p3-plan.md` › "P3d-3 — As built" (why
`Settings › Sync` was kept behind `visible()`). Precondition met 2026-09-23: the user
reports that on alpha.422 the Sync entry is gone from the Settings sidebar ⇒ provider
off, no pending conflicts / bundle (confirmed via the coordinator).

Pure deletion. No behaviour is added. Verification is **reverse verification**
(every deleted module / symbol / i18n key has zero references left) instead of
mutation testing, as in P3c-2.

## Scope (inventory 2026-09-23)

- SPA, delete (64 files, ≈9.9 K lines): `spa/src/lib/sync/**` (40),
  `spa/src/features/settings/**` (16 — only `sections/sync-history/` lives there),
  `components/settings/SyncSection{,.test}.tsx`, `SyncConflictBanner{,.test}.tsx`,
  `lib/__tests__/sync-as-module.test.ts`, `locales/history-keys.test.ts`,
  `lib/object-depth{,.test}.ts` (only caller is the old manual provider).
- SPA, edit: `App.tsx` (`ensureSessionPristine` effect), `TitleBar.tsx` +
  `TitleBar.test.tsx` (conflict icon), `lib/register-modules/index.tsx`
  (`registerSyncContributors`, the `sync` module registration), `lib/settings-order.ts`
  (`MODULE_SYNC`), `lib/client-identity.test.ts` (the `useSyncStore.getClientId`
  block), `locales/en.json` + `zh-TW.json` (97 keys each; **keep
  `settings.sync.time.*`** — used by BackupHistoryList, BackupStatusSidebar,
  NexExecutionRow), comment-only touch-ups that name the deleted files.
- Daemon: delete `internal/module/sync/` (4 files); edit `cmd/pdx/main.go`
  (import + `AddModule`), `AGENTS.md:10`.
- **Not touched**: `lib/profile/**`, `lib/device-state/**`, `lib/snapshot/**`,
  `lib/device-name.ts`, `useDeviceNameStore`, `lib/storage/sync.ts` (`syncManager` /
  BroadcastChannel — shared infra, unrelated despite the name),
  `settings-contribution` `visible()`/`subscribeVisibility` (harmless; separate
  follow-up if wanted).

## What is left behind, and why (decided)

| residue | decision |
|---|---|
| localStorage `purdex-sync-state` (`STORAGE_KEYS.SYNC_STATE`) | **Kept, not read by anything but `client-identity.ts`'s legacy adoption** (`readLegacySyncClientId`), which is how an install that never wrote `purdex-client-identity` keeps its client id. Removing it would silently change a device's identity. Nothing writes it any more. Cleanup = follow-up issue, after a release in which every install has adopted. |
| IndexedDB `purdex-sync` (store `snapshots`) | **Left on disk, unused.** Deleting it is new behaviour (a boot-time `deleteDatabase`), not deletion; nothing opens it after this change. Follow-up issue. |
| daemon `<DataDir>/sync.db` | **Left on disk, unused.** Same reasoning; the daemon never opens it again. Follow-up issue (same one). |
| `.purdex-sync` export files in users' Downloads | User files; not ours. |

## PR split (≤ 20 files each, cut at commit boundaries, merged in order)

One branch, one commit per step; each PR is a prefix of the branch.

1. **PR-1 unwire** — App.tsx, TitleBar(.test), register-modules, settings-order;
   delete SyncSection(.test), SyncConflictBanner(.test), sync-as-module.test.
   After it: nothing in the running SPA reaches `lib/sync` or `/api/sync/*`.
2. **PR-2 history UI + i18n** — delete `features/settings/**`,
   `locales/history-keys.test.ts`; remove the 97 keys from both locale files.
3. **PR-3 top of lib/sync** — `register-sync`, `use-sync-store`(+test),
   `__tests__/session-pristine.test`, `sync-actions`(+test), `contributors/**`;
   edit `client-identity.test.ts`.
4. **PR-4 providers + snapshot store + core** — `providers/**`, `object-depth`,
   `snapshot-*`, `__tests__/*`, `engine`, `types`, `three-way-merge`,
   `sync-flow.test` → `lib/sync/` is gone. If > 20 files, split in two at the
   `providers+snapshot` / `core` boundary.
5. **PR-5 daemon** — `internal/module/sync/**`, `cmd/pdx/main.go`, `AGENTS.md`.
   Needs PR-1 merged (nothing calls `/api/sync/*`). Deploy mlab + air26 after bump
   (ask the coordinator first).

## Verification (every PR tip, before opening it)

- `cd spa && pnpm install --frozen-lockfile` once; then at each tip:
  `npx tsc --noEmit -p tsconfig.app.json`, `npx vitest run`, `pnpm run lint`,
  `pnpm run build`. Daemon PR: `go build ./... && go vet ./... && go test ./...`.
- **Reverse verification** (deliverable, pasted into each PR body):
  - For each deleted file: `rg -n "<module path without extension>"` over `spa/src`
    (import specifiers: `lib/sync`, `sync/`, `SyncSection`, `SyncConflictBanner`,
    `sync-history`, `object-depth`, …) → zero hits outside files deleted in the
    same or an earlier PR.
  - For each exported symbol of a deleted file (`useSyncStore`,
    `registerSyncContributors`, `ensureSessionPristine`, `SnapshotHistoryPage`,
    `MODULE_SYNC`, …) → `rg -w` zero hits.
  - For each removed i18n key → `rg -F '<key>'` in `spa/src` zero hits
    (and dynamic prefixes `settings.sync.` checked by hand: only `time.*` left).
  - Daemon: `rg 'internal/module/sync"'` and `rg '/api/sync'` → zero hits in Go
    and SPA source.
- **Edits are subtraction only**: `git diff --numstat` per edited file; every
  added line (import re-ordering, a collapsed comment) is listed and justified in
  the PR body. No behaviour lines are added.

## Review

Codex once over the whole stack at the tip of PR-4 (base origin/main): R1 +
attacker (focus: something still reachable that referenced the deleted code;
i18n keys still used; client-identity adoption untouched; settings order /
sidebar regressions). Daemon PR: R1 + attacker as well (small). No critic unless
a critical comes back.
