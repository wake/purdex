# Plan — host ownership H3 (the `hosts` section leaves Profile Sync)

Spec: `docs/specs/2026-09-23-host-ownership-spec.md` (§1 decisions 1, 7, 9; §3.1.3; §5; §7 H3a / H3b; §8 H3).
Measured on the worktree at `cffbcb2e` (origin/main: H1a, H1b, H1c, H2a, H2b-1, H2b-2, H2c-1, H4a, H4b merged;
H2c-2, H2c-3, H2d-* NOT merged). **Rev 2 (2026-09-24):** main is now alpha.444 (`342fb656`: H2c-2 and H2c-3
merged) — of every file this plan lists, only `host-lifecycle.hash.integration.test.ts` and
`host-reresolve.integration.test.ts` changed since `cffbcb2e` (their `hosts` lines re-checked: `:34-35` and `:36`).
Rev 2 records the coordinator's decisions (D1–D4 **DECIDED**) and the codex plan review `task-mufgyw2p-92pcln`
(§Review). Every task is TDD (failing test first) and its own commit. File lists are the files each PR touches, counted from
the code (grep + reading), not estimates. Line counts are estimates and say so.

Test / lint / build: `cd spa && npx vitest run <files>`, `pnpm run lint`, `pnpm run build`, and
`npx tsc --noEmit -p tsconfig.app.json` (a bare `tsc --noEmit` checks nothing in `spa/`).

The spec's two PRs become five (§0.12): **H3a-1** (the pull guard leaves the executor), **H3a-2** (`hosts` retired from
the sync loop), **H3a-3** (the hosts apply path goes), **H3a-4** (the pure hosts-apply helpers go), **H3b** (wizard,
the guard's store half, the notice, the delete copy).

## Dependencies and order

```
H2c-2 ─► H2c-3  (merged, alpha.444)
                 │
H3a-1 ─────────► H3a-2 ─► H3b ─► bump "H3" (H3a-1 + H3a-2 + H3b: the behaviour — one release)
                   │  ▲
                   │  H2d-1 (merges first, takes the next `settings` ordinal; H3a-2 takes the one after)
                   └─► H3a-3 ─► H3a-4 ─► any later bump (cleanup of code H3a-2 made unreachable; no release
                                         invariant ties it to the H3 bump — review item 3)
H2d-2 … H2d-6: no file overlap with H3 except the two locale files — run in parallel.
```

- **H3a-2 needs H2c-2 and H2c-3 merged — satisfied since alpha.444.** Before H2c-2 the ONLY thing carrying a rename
  or a colour to another device was the `hosts` section (the selector read `HostConfig`, writers wrote it, the
  transfer spread looks into it, New Tab labels did not follow the look store). Retiring `hosts` before that would
  have silently stopped looks from syncing — spec decision 3 broken.
- **H3a-1, H3a-2 and H3b must be in the SAME release** (one bump, after H3b): H3a-1 alone reopens #1366 (a `hosts`
  written between the wizard's check and the first pull could remove a host the user was not told about); H3a-2
  without H3b ships a wizard that still reads the SOT `hosts` row (`wizard-run.ts:331`) and shows a "this pull
  removes these hosts" list while nothing is removed; H3b's store half needs H3a-1 (the executor no longer reads
  the guard). On `main`, H3a-1 may be merged right before H3a-2 to keep the #1366 window short.
- **H3a-3 and H3a-4 are cleanup** of the apply path and pure helpers H3a-2 made unreachable (the executor never
  pulls `hosts`, so `applySectionToStores('hosts')` has no caller — `executor.ts:1360` is its only way in). They
  follow H3a-2 in any order relative to H3b and ride any later bump; they are not needed for the H3 release.
- **H2d**: H2d-1 touches `projections.ts` / `.test.ts`, `collector.ts` / `.test.ts`, `apply-to-stores.ts` / `.test.ts`,
  `host-reresolve.integration.test.ts` — also in H3a-2 / H3a-3. **Coordinator (2026-09-24): H2d-1 merges first** and
  takes the next `settings` ordinal (`settings` is 6 on alpha.444); **H3a-2 takes the one after it** (D1) and rebases on H2d-1. No
  ordinal number is written into this plan: "H2d-1's + 1" whatever it is at merge time. H2d-2 … H2d-6 share only
  `locales/en.json` / `zh-TW.json` with H3 (merge conflicts, no semantic overlap).
- The H3 release notes carry spec §5.4's known limitation (§H3-note).

## 0. Where the spec and the code disagree (found while measuring)

Status per item: plain = measurement / plan choice; D1–D4 were NEEDS DECISION and are now **DECIDED** (§Decisions).

1. **How `hosts` is "retired" (spec §5.1 "a retired kind: skipped by `profileLock`, not in the managed section set,
   its persisted section-store record discarded, never touched by any orphan sweep").** There is no "managed section
   set" in the code: the executor's `sections` map is it (`executor.ts:1606-1611` restores it from the section store,
   `:1143-1146` adds every live index entry whose kind is known, `:1626` every remote event, `receive` every
   collector report). `sectionKind('hosts')` is `'hosts'` (`projections.ts:133-135`) and `SectionKind` includes it
   (`types.ts:16`). Two ways to retire it:
   - (a) make `sectionKind('hosts')` return `null` and drop `'hosts'` from `SectionKind`: every "unknown kind is
     carried, never rewritten" path then covers it for free — but `PROJECTIONS.hosts` / the ordinal / the shape table
     are keyed by `SectionKind` and `buildHostsSection` (kept, §5.2) projects with `PROJECTIONS.hosts`
     (`sections.ts:238-248`); `section-store.ts:232-234` refuses non-kind keys, so its pure storage tests that use
     `'hosts'` as a sample key (17 refs) break; `reconcileSectionSet` would report it as `sections-unknown-kind`
     (`profile-state.ts:134-137`) on every index; sync-view / resolve-counts lose their label.
   - (b) **plan choice**: keep `'hosts'` as a KNOWN kind (it mirrors the daemon's grammar, `validate.go:29`, and its
     shape stays known) and add `RETIRED_SECTIONS = ['hosts']` + `isRetiredSection(key)` in `projections.ts`, enforced
     at the executor's ONE choke point (`dispatch`: a retired key is refused before `sections.set`) plus the startup
     restore (drop, below), `profileLock` (skip), and the collector (never built). `reconcileSectionSet` already
     ignores non-`tabs` keys (`profile-state.ts:139` `id === null → continue`), and the orphan sweep only ever
     considers `tabs.*` (`executor.ts:821-822` `isUnrendered`). So "never touched by any orphan sweep" is ALREADY true
     — H3a-2 pins it with a test, it changes no sweep code.
2. **The persisted record carries tokens in the stash (spec §5.1 "record discarded").** `loadSectionStore` keeps a
   `hosts` record (`section-store.ts:323-336`), and a `hosts` conflict keeps its payloads in the section store's
   stash — a `hosts` payload holds every host's token. Discarding the record without `pruneStash` would leave those
   tokens in localStorage until the next attach clears the store (`start.ts:1127` `clearSectionStore()`). Plan: the
   executor's startup drops a retired record (`dropSection`) and then prunes the stash against what is left
   (`pruneStash(profileId, keepSet())`). Other profiles' stores are already cleared on attach / leave
   (`start.ts:752`, `:1127`, `:1182`).
3. **The #1370 / #1366 pull guard cannot outlive the section.** With `hosts` refused by `dispatch`, a guard row
   (`pendingPullHosts`) would keep the barrier up for ever: `checkConfirmedHosts` (`executor.ts:765-770`) sees the
   index list `hosts`, `judgeAgreedHosts` waits for `upToDate('hosts')` (`:774-781`), which never comes, and every
   other section waits behind the barrier (`:1024`). The guard's executor half therefore goes BEFORE (or with) the
   retirement: H3a-1. Its store / wizard / notice half is inert once the executor ignores it and goes in H3b.
4. **Legacy-reference tests that "stay" go through the hosts apply (spec §5.2 "every legacy-reference test").**
   `host-identity.wire.integration.test.ts` (the transition tests, `:194-334`) LEARN aliases by applying a `hosts`
   payload (`planHostsApply` → `withSyncAliases`, `apply-to-stores.ts:263-270`) and then resolve legacy ids through
   them. After H3 no alias is learned (spec §5.2 says so itself), so those tests cannot stay as written. Plan (H3a-3
   T3): keep every RESOLUTION assertion by seeding the persisted `HostConfig.syncAliases` directly (what a pre-H3 apply
   left behind, and what survives a restart — `:314`), delete the alias-LEARNING halves, and add one test that a
   `hosts` payload arriving now teaches nothing (`syncAliases` unchanged). The resolver tests in
   `host-identity.hosts.test.ts` / `.edge.test.ts` / `.translate.test.ts` / `sections.test.ts` (`buildHostsSection`,
   `wireResolverOf`) are untouched.
5. **`matchIncomingHosts` / `hostsFromWire` lose their last production caller** (`applier.ts:201`, `:220` go in H3a-4;
   `wizard-run.ts:354` goes in H3b). Spec §5.2 keeps `makeWireResolver`, `wireResolverOf`, `syncAliases`,
   `mergeAliases` and the hosts wire builder — not these two. Plan choice: leave `host-identity.ts` and its
   `.match` / `.hosts` / `.edge` tests untouched in H3; they go with the alias exit issue (spec §5.2), which also
   retires the resolver's alias half. Deleting them now would be the ambiguous "is this a legacy-reference test"
   call for no user-visible gain.
6. **Spec §6.4 step 6 `replace-all` does not exist in the H4b code.** `ReceiveMode = 'add-only' | 'overwrite'`
   (`host-transfer-plan.ts:124`). So after H3a-3 the hosts apply was the ONLY caller of `deleteHostCascade`'s `grant`
   and `afterCommit` parameters (`host-lifecycle.ts:50`; the Hosts page calls `deleteHostCascade(hostId)`,
   `:341`). → **D2 (DECIDED: keep, #1395)**.
7. **"The attach host must exist on this device" (spec §5.1 wizard) is already enforced**: `brokenLocalPremise` →
   `host-gone` (`wizard-run.ts:293-295`) before and after the host is asked, and `attachMaster` → `unknown-host`
   (`start.ts:971`). H3b adds no code for it, only a test that pins it with the hosts code gone.
8. **The pull premise's reason is gone, its check is not (spec silent).** `brokenPullPremise` (`wizard-run.ts:313-318`)
   requires the attach host's daemonId to be verified, "so its host list cannot be matched safely"
   (`en.json:1697`). After H3 no host list is matched. → **D3 (DECIDED: keep, reword)**.
9. **The wizard's SOT fingerprint and "empty" count the `hosts` row** (`wizard-run.ts:282-287`). After H3 a profile
   whose SOT has ONLY a `hosts` row (spec §5.4's case) reads as non-empty — the direction step warns "replaces what is
   there" although a pull would bring nothing — and a straggler's `hosts` write between choosing and Start bounces the
   wizard with `profile-changed`. Plan (H3b T2): both ignore retired sections. Spec gap, not a contradiction.
10. **An H2-era client that is not yet H3 is locked by nothing (spec decision 7 / §5.4).** Decision 7 says "H2 locks
    old clients out through the `settings` marker"; that locks clients BELOW H2c (`settings` ordinal 6,
    `projections.ts:109`). A client on H2c … H2d but not H3 has the same shapes as H3 (H3 changes no projection), so
    it keeps pulling / pushing `hosts` — including tokens into `profiles.db` — on any profile, for as long as it is
    not upgraded. Nothing it does harms an H3 client (it never reads what the H3 client writes about hosts; H1 made
    unknown ids harmless), so this is not a correctness bug, but it is exactly the "old client keeps syncing hosts"
    the known limitation limits to hosts-only profiles. → **D1 (DECIDED: marker bump)**.
11. **Spec §7's H3a file estimate ("~15 files: executor, start, collector, sections, applier, apply-to-stores,
    profile-state, sync-status / sync-view, tests") is far below the measured size.** `hosts` is the generic sample
    section of the executor's unit tests (118 of 143 tests in `executor.test.ts` use it, 258 references), of
    `ResolveBlock.integration.test.tsx` (6 of 6, through a real executor), `executor.direction.integration` (13 of 28),
    `executor.integration` (4 of 4), `start.integration` (5 of 10); the hosts apply is ~500 lines of code and ~1000
    lines of tests. Split per §0.12.
12. **Split.** Five PRs, each ≤ 20 files. Three of them exceed 800 diff lines ONLY by deleting code and the tests of
    that code (a PR cannot keep a test for what it deletes): H3a-1 ≈ −960 (−604 of them the whole
    `executor.pull-guard.integration.test.ts`), H3a-3 ≈ −1050, H3a-4 ≈ −520. Added lines stay well under 800 in every
    PR. → **D4 (DECIDED: accepted)**.
13. **What does NOT go, though it mentions hosts** (to keep the implementer from over-deleting): `HOSTS_MOVED` in the
    settings apply (`apply-to-stores.ts:504`, `:565`) — it guards against THIS device's hosts moving mid-apply, still
    real; `IDENTITY_CONFLICT` / `host-identity-conflict` (used by the tabs and settings applies, `:539`, `:659`);
    `start.ts` `hostIdentityBlockOf` / `blocked: host-identity-*` (spec §5.1 keep); `ApplyContext.masterHostId`
    (its only reader goes, the field stays — removing it touches every `applySectionToStores` call in ~10 test files;
    follow-up issue); `sectionKind('hosts')`, `PROJECTIONS.hosts`, `SECTION_SCHEMA_ORDINAL.hosts`,
    `WIRE_MARKERS.hosts` (the builder and the shape stay, §0.1); `sync-view.ts:96` `FIXED_ORDER` and
    `resolve-counts.ts:50` (label / count of a kind that is still known — dead only in practice).
14. **The daemon needs no change (spec §5.3 verified).** `validate.go:29` accepts `hosts`; `store.go:229` deletes a
    profile's section rows in one transaction with the profile. No H3 file is in `internal/`.

## Decisions (coordinator, 2026-09-24 — all DECIDED)

- **D1 — DECIDED (b): a marker bump locks H2-era pre-H3 clients (§0.10).** H3a-2 adds `@wire:hosts-retired=1` to
  `WIRE_MARKERS.settings` and bumps `SECTION_SCHEMA_ORDINAL.settings` to **H2d-1's ordinal + 1** (H2d-1 merges first
  and takes the next one; no number is fixed here) — no projection change; an H2-era client then sees `settings` as
  newer and locks the whole profile (all writes stop, its `hosts` pushes included). Cost: 0 extra files
  (`projections.ts` / `.test.ts` are in H3a-2), one lock-regression test in the style of H2c-1 T2. Limit, said in the
  release notes: like every marker it bites only once an H3 client has WRITTEN `settings` to that SOT — a marker
  changes the fingerprint, not the payload hash, so an unchanged `settings` is not re-pushed; the first settings edit
  after the upgrade writes it. (Rejected: (a) no bump — an H2-era client would keep syncing `hosts`, tokens included.)
- **D2 — DECIDED (a): keep `deleteHostCascade(hostId, grant, afterCommit)` (§0.6).** Both parameters and their tests
  stay; H3a-3 rewrites the doc comment (`host-lifecycle.ts:33-48`): they exist for a caller that deletes hosts inside
  its own transaction — spec §6.4 step 6 replace-all, not built, tracked by **#1395**. (Rejected: (b) remove as dead
  code.)
- **D3 — DECIDED (a): keep the wizard's pull premise (§0.8).** `brokenPullPremise` (`wizard-run.ts:313-318`) stays (a
  pull resolves the SOT's `d1_…` references through the attach host's CLAIMED daemonId too; an unverified /
  mismatched claim can map them onto the wrong host, and the start layer would block the sync anyway —
  `host-identity-mismatch`). H3b rewrites `settings.profile.wizard.pull.master_unverified` / `.master_mismatch`
  (en + zh-TW): "its host list cannot be matched" → "the tabs and settings it names cannot be matched to this
  device's hosts safely". (Rejected: (b) drop the premise.)
- **D4 — DECIDED (a): deletion-dominated PRs over 800 lines are accepted (§0.12).** H3a-1 (≈ −960 / +30), H3a-3
  (≈ −1050 / +150) and H3a-4 (≈ −520 / +40) pass the 20-file limit; they exceed 800 lines only by deleting code and
  the tests of that code (a test of deleted code cannot stay behind in a later PR — it would not compile).

## What stays on the SOT, and what an old client then does

- **New clients never read, write or delete the `hosts` row** — the sync loop from H3a-2 on (executor, collector,
  start layer), the wizard from H3b on (until then it still GETs the row for its removal preview — review item 1). It stays on the daemon, unchanged, with every
  token it holds, until the profile is deleted on the sync host (`store.go:229`: rows go with the profile) — or until
  the later, user-consented cleanup of spec §5.3's own issue.
- **An old client (pre-H2c)** is locked by `settings` / `tabs.*` markers once an H3 client has written them to that SOT
  (`locked:schema`, no writes). On a profile whose SOT has only `hosts` (or whose `tabs.*` / `settings` an old client
  wrote last) it is not locked, keeps pulling / pushing `hosts` there, and its pulls may cascade host removals on
  ITSELF (the pre-H1c behaviour of that build) — never on an H3 client. That is spec §5.4's known limitation.
- **An H2-era, pre-H3 client**: see D1.
- **The tokens already in `profiles.db`**: not touched by H3. The delete-a-workbench dialog on the sync host says
  they go with it (H3b T5, both dialogs that use `settings.profile.sot.delete_body`: `SotProfilesBlock.tsx:220`,
  `ProfileWizard.tsx:602`). New copy, unconditional (the index is not asked whether a `hosts` row exists — the
  sentence is true either way): en "Every device loses this profile on the host. What each device holds locally
  stays. Host addresses and access tokens that older versions of Purdex stored in it are deleted with it. This cannot
  be undone." / zh-TW 「所有裝置都會失去主機上的這份工作台，各裝置本機的內容不受影響。舊版 Purdex 存在裡面的主機位址與存取 token 也會一併刪除。這個動作無法復原。」
- **This device's section store**: the `hosts` record and its stashed payloads (tokens) are dropped at the next
  executor start (§0.2).

## H3a-1 — the pull guard leaves the executor (5 files)

Files:
1. `spa/src/lib/profile/executor.ts` (THE PULL GUARD header `:172-213`; `confirmedPullHosts` / `onPullUnconfirmed`
   deps `:273-277`; `pullGuard` / `guardReleased` / `halted` state `:476-480`; `readGuard` … `halt` `:733-807`; the
   barrier in `answerFor` `:716`, `checkSettled` `:908-910`, `pump` `:1028`, `send` `:1280`, `pull` `:1375`,
   `:1424-1431`, `:1492-1494`, `restoreLocal` `:1561`; `checkConfirmedHosts` at the end of `reindex` `:1153`; the
   startup snapshot `:1615-1617`)
2. `spa/src/lib/profile/executor.pull-guard.integration.test.ts` (deleted, 604 lines)
3. `spa/src/lib/profile/start.ts` (`confirmedPullHosts` / `onPullUnconfirmed` deps `:345-351`; `stopUnconfirmedPull`
   `:1190-1215` and its `writePullUnconfirmed` import; header `:71-76`)
4. `spa/src/lib/profile/start.test.ts` (the executor-deps fake `:18-19`; the `confirmedPullHosts` cases `:1700-1765`;
   the `stopUnconfirmedPull` cases)
5. `spa/src/lib/profile/start.integration.test.ts` (the two guard describes `:161-211`)

Left for H3b (inert after this PR): `useProfileStore.pendingPullHosts` (written by `setMaster`, read by nobody),
`attachMaster`'s `confirmedHosts` option, `pull-unconfirmed.ts` (read by `CurrentBlock`, written by nobody),
`clearPullUnconfirmed()` at attach (`start.ts:1132`), the wizard's `hostsRow`.

Tasks:
- **T1 — a pull attach starts pulling at once.** Test (`start.integration.test.ts`, fake daemon): attach `pull` with
  a `confirmedHosts` row, then another client rewrites the SOT `hosts` (new rev, new hash) before the first index →
  the executor does NOT halt, no `pull-hosts-unconfirmed` problem, no notice written, the sync is not stopped;
  `workspaces` / `settings` / `tabs.*` are pulled. (Pre-H3a-2 the `hosts` row is also pulled — asserted as such, H3a-2
  flips it.) Implement: remove the guard from the executor. Commit.
- **T2 — the start layer stops wiring it.** Tests (`start.test.ts`): the executor deps carry no `confirmedPullHosts`
  / `onPullUnconfirmed` (key absent); `writePullUnconfirmed` is never called. Implement: remove the two deps and
  `stopUnconfirmedPull`. Delete `executor.pull-guard.integration.test.ts` and the guard cases of
  `start.test.ts` / `start.integration.test.ts` in the same commit. Commit.

Mutations: M1 the barrier kept in `pump` (T1 red: nothing pulled); M2 `halt()` kept on a `hosts` hash mismatch (T1
red: sync stopped); M3 start still passes `confirmedPullHosts` (T2 red).

## H3a-2 — `hosts` is retired from the sync loop (18 files)

Files:
1. `spa/src/lib/profile/projections.ts` (`RETIRED_SECTIONS`, `isRetiredSection`; D1 marker + ordinal = H2d-1's + 1)
2. `spa/src/lib/profile/projections.test.ts` (retired set pinned; D1 guard snapshot + lock regression)
3. `spa/src/lib/profile/profile-state.ts` (`profileLock` skips a retired entry)
4. `spa/src/lib/profile/profile-state.test.ts` (`:79-124`, `:138` — the hosts-offender cases flip)
5. `spa/src/lib/profile/executor.ts` (`dispatch` refuses retired keys; startup drops a retired record + prunes the
   stash; `GATES` / `SETTINGS_GATES` = `['workspaces']` `:357-360`; header `:34-66` ORDER rules rewritten)
6. `spa/src/lib/profile/executor.test.ts` (see T3)
7. `spa/src/lib/profile/collector.ts` (no `hosts` slot: `buildSection` `:178`, `scheduleHostBearing` `:324`,
   `isWorldSlot` `:334`, `scheduleWholeWorld` `:341`, host-store subscriber `:424-433`, `primeAll` `:453`;
   `buildSectionPayload('hosts')` → `{ payload: null }`; header `:24`, `:29-35`)
8. `spa/src/lib/profile/collector.test.ts` (`:155-199`, `:345`, `:478-500`, `:625-672`: no `hosts` key)
9. `spa/src/lib/profile/collector.world.test.ts` (`:179`, `:228`, `:243`, `:306`)
10. `spa/src/lib/profile/executor.direction.integration.test.ts` (live lists `:163-373` lose `hosts`; the hosts-shape
    upgrade cases `:740-770` and the two-new-clients `removes-master-host` case `:872-905` deleted)
11. `spa/src/lib/profile/executor.integration.test.ts` (its 4 cases resolve / push `hosts`: re-cut on `settings`)
12. `spa/src/lib/profile/start.integration.test.ts` (`:109` live list; `:232`, `:291` hosts writes)
13. `spa/src/lib/host-lifecycle.hash.integration.test.ts` (`:34-35`: `hosts` is not built; deletion and undo leave
    EVERY built section identical — the pre-H3 exception of H1 plan §0.13 flips)
14. `spa/src/lib/host-lifecycle.worlds.test.ts` (`:119` "pre-H3: only `hosts` reported" → nothing reported)
15. `spa/src/lib/host-reresolve.integration.test.ts` (`:36` on alpha.444: KEYS without `hosts`)
16. `spa/src/components/settings/profile/ResolveBlock.integration.test.tsx` (all 6 cases lock `hosts` through a real
    executor: re-cut on `workspaces` / `settings` — `locked:invalid` via a malformed `workspaces`)
17. `spa/src/components/settings/profile/wizard/ProfileWizard.integration.test.tsx` (`:140` live list)
18. `spa/src/lib/profile/hosts-retired.integration.test.ts` (new — spec §5.3 guarantees, fake daemon)

Checked, NOT changed: `section-store.ts` (storage stays kind-agnostic; the executor drops the record),
`sync-view.ts`, `resolve-counts.ts`, `ResolveBlock.test.tsx`, `sync-status.test.ts` (`'hosts'` there is a label /
status key, never through an executor), `start.ironrule.test.ts:97` (a `requestResolve('hosts', …)` against a
mocked executor — run it; if it asserts a resolve reaches the executor it stays valid: `resolve` of an unknown
section is a no-op `:1646`).

Tasks:
- **T1 — `isRetiredSection` and the lock.** Tests: `isRetiredSection('hosts')` true, `'settings'` / `'workspaces'` /
  `'tabs.a'` / `'plugins'` false; `sectionKind('hosts')` still `'hosts'`; `profileLock` with a `hosts` entry of a
  NEWER ordinal / unorderable fingerprint → `null` (was `sot-is-newer`), with a newer `settings` → still locks.
  **[D1]** marker array pinned, guard snapshot updated, lock regression: an H2-era shape table (current
  `settings` minus the marker, previous ordinal) against a SOT `settings` row of this build → `sot-is-newer`.
  Commit.
- **T2 — the collector never builds `hosts`.** Tests: `primeAll` reports no `hosts`; a host add / remove / reorder /
  rename / daemonId learned reports no `hosts` (identity changes still reschedule `settings` / `tabs.*`);
  `buildSectionPayload('hosts')` → `{ payload: null }`; an identity conflict still yields `null` for `settings`.
  Flip the existing key lists. Commit.
- **T3 — the executor refuses retired sections; gates are `workspaces` only.** Test harness: `executor.test.ts`
  already mocks `./projections` (`:49-55`, `shapeTable`); it adds `isRetiredSection: vi.fn(() => false)` so that the
  118 generic tests keep `'hosts'` as an opaque, ungated sample key (rewriting them onto `workspaces` is not
  mechanical: 33 of them also use `workspaces`, which is a gate with its own side effects — `previousWorkspaceIds`,
  `pumpTabs`, `sweepOrphans`). The file header says so. A new `describe('a retired section')` restores the real
  `isRetiredSection` (`vi.mocked(...).mockImplementation(actual.isRetiredSection)`) and tests: a persisted `hosts`
  record (plain, conflict, pending restore-local) is dropped at startup (`dropSection` called, `status().sections`
  has no `hosts`) and `pruneStash` runs with a keep-set that excludes its payload hashes; an index listing `hosts`
  creates no state and pulls nothing; a remote event on `hosts` is ignored; a collector report `hosts` is ignored;
  `resolve('hosts', …)` is a no-op; the period ends (`onInitialSettled`) with `hosts` on the SOT under both
  directions. Gate tests: the describe "settings waits for hosts AND workspaces" (`:1087-1175`) becomes "settings
  waits for workspaces" (hosts cases deleted, the workspaces cases kept); `:1177` a `tabs.*` pull waits for
  `workspaces` and its workspace only. Implement. Commit.
- **T4 — spec §5.3's never-PUT / never-DELETE list for the SYNC LOOP, end to end** (`hosts-retired.integration.test.ts`:
  real executor, collector, start layer, `applySectionToStores`, fake daemon; SCOPE: every path that goes through the
  executor or `attachMaster` / `detachMaster` — NOT the wizard's own reads (`previewPull` / `prepareRun` /
  `recheckBeforeAttach` still GET `hosts` directly, `wizard-run.ts:331`, until H3b T1, which completes the guarantee;
  review item 1); the SOT seeded with a `hosts` row holding a token, written by
  "another client"). With a spy on `api.putSection` / `deleteSection` / `getSection`, for each: (1) push attach (`attachMaster`, no wizard);
  (2) pull attach (`attachMaster`, no wizard); (3) a `settings` conflict resolved keep-local (restore-local path); (4) resolved keep-SOT; (5)
  `push` first reconciliation with an unrendered `tabs.*` (orphan sweep runs — deletes `tabs.ghost`, not `hosts`);
  (6) a pull of `settings` followed by its push; (7) `detachMaster()` ("stop sync, keep local"); (8) a device-local
  host add, host delete, host rename — the calls never name `hosts` (no PUT, no DELETE, and no GET either), the
  row's rev / hash / payload are byte-identical at the end, and `useHostStore` is the same object where no local edit
  happened. Plus: (9) a local host added on this client is in no PUT payload of any section except as a wire id in
  `tabs.*` / `settings` references. Commit.
- **T5 — the hash invariants flip.** `host-lifecycle.hash.integration.test.ts`: after a deletion and after its undo,
  EVERY section the collector builds hashes identically (no `hosts` exception; the §0.11 column-duplicate case
  unchanged). `host-lifecycle.worlds.test.ts:119`: nothing reported. `host-reresolve.integration.test.ts`: KEYS
  without `hosts`. Integration tests' live lists updated (`executor.direction`, `executor.integration`,
  `start.integration`, `ProfileWizard.integration`, `ResolveBlock.integration` re-cut). Commit.

Invariants (H3a-2 — the sync loop only): no request the executor, the collector or the start layer makes names
`hosts`; `profileLock` never answers for `hosts`; `tabs.*` / `settings` gate
on `workspaces` only; a host-list change on this device changes no section hash except through references it renames
(H1b / H2c rules); the stash holds no `hosts` payload after the executor starts. NOT yet: the wizard (H3b).

Mutations: M1 `dispatch` admits a retired key (T4: a GET / PUT of `hosts` appears); M2 startup keeps the persisted
record (T3 restore-local case red); M3 startup drops the record but skips `pruneStash` (T3 keep-set case red); M4
`profileLock` does not skip retired (T1 red); M5 `'hosts'` back in `GATES` (T3 tabs gate red — the pull never
starts); M6 `'hosts'` back in `SETTINGS_GATES` (T3 settings gate red); M7 the collector reports `hosts` (T2 and T4
red); M8 the real `isRetiredSection` returns false (T1 and the real-retired describe red — proves the mock does not
hide production); M9 **[D1]** marker without the ordinal bump (guard snapshot red).

## H3a-3 — the hosts apply path goes (10 files)

Files:
1. `spa/src/lib/profile/apply-to-stores.ts` (`applyHostsSection` `:353-468`, `planOrRefuse` / `hostsRefusal` /
   `isAliasWriteBackOnly` / `withSyncAliases` / host caches `:186-325`; `InvalidReason` loses `no-host`,
   `removes-master-host`, `changes-master-host`, `duplicate-host-identity`, `duplicate-host-alias` `:91-116`;
   `ApplyOutcome.rewrite` loses `'aliases'` `:68-75`; the `'hosts'` case `:719-720` → `invalid('unknown-section',
   'hosts is not synced')`; header `:25-30`, `:694-712`)
2. `spa/src/lib/profile/apply-to-stores.test.ts` (`:308-736`, `:1906-2026`, the `hosts` cases of `:1493-1662`; the
   reasons pin `:300-304`)
3. `spa/src/lib/profile/executor.ts` (comment `:1465-1468` on the own-alias rewrite)
4. `spa/src/lib/profile/executor.test.ts` (`(a) rewrite: aliases` `:1258-1279` deleted; the sample invalid code
   `removes-master-host` `:972-987`, `:2099` → `rejected-settings`)
5. `spa/src/lib/profile/sync-status.ts` (`KNOWN_INVALID_REASONS` `:375-388`)
6. `spa/src/lib/profile/sync-status.test.ts` (`:673-694` sample reason)
7. `spa/src/lib/profile/host-identity.wire.integration.test.ts` (§0.4)
8. `spa/src/lib/host-lifecycle.ts` (doc comment `:33-48` per D2: kept for a transactional caller, replace-all #1395)
9. `spa/src/locales/en.json` (`settings.profile.resolve.why.invalid.{no_host,removes_master_host,changes_master_host,
   duplicate_host_identity,duplicate_host_alias}` `:1489-1495` removed)
10. `spa/src/locales/zh-TW.json` (same keys)


Tasks:
- **T1 — a `hosts` payload is never applied.** Tests: `applySectionToStores('hosts', <valid payload removing a host,
  renaming one, adding one>)` → `{ ok: false, reason: 'invalid', code: 'unknown-section' }`, `useHostStore`,
  `useTabStore`, every parked world, `useHostSettingsStore` and the look store are the SAME objects afterwards, no
  cascade ran (spy on `deleteHostCascade`), no operation lock was requested; `INVALID_REASONS` pinned to the six that
  remain. Implement: delete the hosts path. Commit.
- **T2 — the reason list.** Tests (`sync-status.test.ts`): a published detail carrying a removed code parses as
  `invalidReason: null` (lenient, cross-window); the kept codes parse. Locale keys removed (completeness test stays
  green). `executor.test.ts` sample codes switched; the `'aliases'` rewrite case deleted (the `device-local-tabs`
  case `(a2)` covers the rewrite path). Commit.
- **T3 — legacy references still resolve with no hosts apply** (§0.4). Rewrite the transition describes: device B's
  `HostConfig` carries `syncAliases: ['aaaaaa']` (as a pre-H3 apply left it); A's ordinal-2 `tabs.*` / `settings` with
  local-id keys applied on B → every reference lands on B's host; across a restart (rehydrate) likewise; B's next
  build is canonical (`d1_…`), one push. New: a `hosts` payload whose canonical row lists a NEW alias, "arriving"
  now, leaves `syncAliases` unchanged (it is refused, T1). The "two devices, independent ids" describe keeps its
  tabs / settings halves (no `hosts` step; ids resolve by daemonId). Commit.

Mutations: M1 the `'hosts'` case still routes to an apply (T1 red: host store changed); M2 `KNOWN_INVALID_REASONS`
keeps a removed code (T2 red); M3 `wireResolverOf` stops reading `syncAliases` (T3 red); M4 `buildHostsSection` drops
`aliases` from its rows (T3 red — the resolver's alias source).

## H3a-4 — the pure hosts-apply helpers go (4 files)

Files:
1. `spa/src/lib/profile/applier.ts` (`applyHosts` `:132-154`, `HostsPlan` / `planHostsApply` `:156-238`,
   `duplicateHostAlias` `:240-252`, `ApplyHostsResult` `:70-75`; `isHostsPayload` `:707-768` and the `'hosts'` case of
   `isWellFormedSection` `:848` — a `hosts` payload is no longer checked by anybody)
2. `spa/src/lib/profile/applier.test.ts` (`:113-256`, `:1237-1289`, `:1449-1560`, `:1615-1624`)
3. `spa/src/lib/profile/sections.ts` (`buildProfileDocument` `:407-424` without `hosts` — "what a profile syncs";
   `wireResolverOf` doc `:202-211` "after the hosts apply" → "this device's own hosts")
4. `spa/src/lib/profile/sections.test.ts` (`buildProfileDocument` cases `:490-605`; `buildHostsSection` cases
   `:374-418` and `:799+` STAY — resolver alias source)

Tasks:
- **T1 — delete the helpers.** Tests: `isWellFormedSection('hosts', …)` → `false` for any payload (documented: the
  kind is known, not accepted); the remaining `applier.test.ts` green. Commit.
- **T2 — the document.** Tests: `buildProfileDocument` has `settings`, `workspaces`, one `tabs.<id>` per workspace
  and no `hosts`; `buildHostsSection` output unchanged (resolver rows, aliases). Commit.

Mutations: M1 `buildProfileDocument` keeps `hosts` (T2 red); M2 `isWellFormedSection('hosts')` accepts a well-formed
payload (T1 red).

## H3b — wizard, the guard's store half, the notice, the delete copy (20 files)

Files:
1. `spa/src/components/settings/profile/wizard/wizard-run.ts` (`WizardPlan.removesHosts` / `hostsRow`,
   `HostsRead` / `readSotHosts` / `removalsOf` / `previewPull` `:320-376`, `PullMatchReason` /
   `PullUnmatchedReason`, the hosts half of `prepareRun` `:396-446`, `retargetPlan`'s removes check, `WizardDraft.
   removesSeen`, `recheckBeforeAttach`'s `removesSeen`, `runSubStep`'s `confirmedHosts` `:189-192`; `sotFingerprint`
   / `sotNow` ignore retired sections `:282-287`; header `:9-12`, `:44-46`, `:461-462`)
2. `spa/src/components/settings/profile/wizard/wizard-run.test.ts`
3. `spa/src/components/settings/profile/wizard/ProfileWizard.tsx` (`previewPull` read `:319-343`: Next no longer
   waits for a removal list; `removes-changed` notice `:74`, `:372`, `:419-427`; header `:13-14`)
4. `spa/src/components/settings/profile/wizard/ProfileWizard.test.tsx`
5. `spa/src/components/settings/profile/wizard/WizardChoiceSteps.tsx` (removal list `:249-259`, `:276`, `:332-337`;
   `PULL_REFUSED` keeps `master-unverified` / `master-mismatch` per D3)
6. `spa/src/lib/profile/start.ts` (`attachMaster` `opts.confirmedHosts` `:957`, `:967`, `:993`, `attachHeld` `:1078`,
   `:1130`; `clearPullUnconfirmed()` `:1132`; header `:71-76`)
7. `spa/src/lib/profile/start.test.ts`
8. `spa/src/stores/useProfileStore.ts` (`ConfirmedHosts`, `pendingPullHosts`, `sanitiseConfirmedHosts`,
   `setMaster`'s 6th parameter, header `:126-137`, `:143`; `partialize` `:387`)
9. `spa/src/stores/useProfileStore.test.ts`
10. `spa/src/lib/profile/pull-unconfirmed.ts` (deleted)
11. `spa/src/lib/profile/pull-unconfirmed.test.ts` (deleted)
12. `spa/src/components/settings/profile/CurrentBlock.tsx` (notice `:24-41`, `:122-124`)
13. `spa/src/components/settings/profile/CurrentBlock.test.tsx`
14. `spa/src/lib/storage/keys.ts` (`PROFILE_PULL_UNCONFIRMED` `:58-62` → documented as legacy)
15. `spa/src/lib/legacy-residue-cleanup.ts` (`LEGACY_LOCAL_STORAGE_KEYS` += `'purdex-profile-pull-unconfirmed'`)
16. `spa/src/lib/legacy-residue-cleanup.test.ts`
17. `spa/src/locales/en.json` (remove `settings.profile.wizard.pull.{checking,removes,check_failed,master_unmatched,
    duplicate_host_identity,host_identity_conflict}`, the `removes_changed` notice, `settings.profile.current.
    pull_unconfirmed*` `:1444-1446`; reword `pull.master_unverified` / `.master_mismatch` per D3; new
    `settings.profile.sot.delete_body` `:1437`)
18. `spa/src/locales/zh-TW.json` (same keys)
19. `spa/src/components/settings/profile/SotProfilesBlock.test.tsx` (the delete dialog shows the token sentence)

20. `spa/src/components/settings/profile/wizard/ProfileWizard.integration.test.tsx` (T1: the wizard run never names
    `hosts` — review item 1)

(`ProfileWizard.delete.test.tsx` renders the same key; not changed — one assertion is enough.)

Tasks:
- **T1 — a pull reads no `hosts` and removes nothing; spec §5.3 complete.** Tests (`wizard-run.test.ts`, real stores
  + mocked api): for a pull AND a push, `previewPull` is gone and `prepareRun` / `recheckBeforeAttach` never call
  `getSection` (spy: zero calls, any section); a plan has no
  `removesHosts` / `hostsRow`; `attachMaster` is called with three arguments; the attach host missing →
  `host-gone`, still (§0.7). Integration (`ProfileWizard.test.tsx`): the direction step shows no removal list and
  Next is enabled without a hosts read; after a pull run the host store holds exactly the hosts it held before
  (a SOT whose `hosts` row lacks one of them). And the wizard's attach end to end (`ProfileWizard.integration.test.tsx`,
  fake daemon seeded with a `hosts` row, +1 file): a full wizard run, push and pull, makes no GET / PUT / DELETE that
  names `hosts` — with H3a-2 T4 this closes spec §5.3's "wizard's attach" case. Invariant after H3b: no request of
  any kind, from any part of the app, names `hosts`. Commit.
- **T2 — the SOT fingerprint and "empty" ignore retired sections** (§0.9). Tests: an index with only a `hosts` row →
  `empty: true` (push offered without the "replaces" warning — `ProfileWizard.test.tsx`); a `hosts` rev change
  between choosing and Start → no `profile-changed`; a `settings` rev change → `profile-changed` as before. Commit.
- **T3 — the guard's store half goes.** Tests (`useProfileStore.test.ts`): `setMaster` takes five arguments; a
  persisted state holding `pendingPullHosts` rehydrates without it (and the next write does not carry it);
  `start.test.ts`: `attachMaster(h, p, 'pull')` stores direction `pull` and nothing else of hosts. Commit.
- **T4 — the stopped-pull notice goes.** Tests: `CurrentBlock` renders no `profile-pull-unconfirmed` even with the
  old key present in localStorage; `cleanupLegacyResidue()` removes `purdex-profile-pull-unconfirmed`;
  `pull-unconfirmed.ts` and its test deleted; locale keys removed (completeness test green). Commit.
- **T5 — the delete copy.** Tests (`SotProfilesBlock.test.tsx`): the confirmation shows the new sentence in en and
  zh-TW ("access tokens" / 「存取 token」). D3 reword of the two pull premises (`ProfileWizard.test.tsx` asserts the
  new en text for `master_unverified`). Commit.

Mutations: M1 `prepareRun` still reads `getSection(…, 'hosts')` (T1 zero-calls red); M2 `sotNow.empty` counts `hosts`
(T2 red); M3 `sotFingerprint` includes `hosts` (T2 bounce red); M4 `sanitiseControl` keeps `pendingPullHosts`
(T3 red); M5 `CurrentBlock` still reads the old key (T4 red); M6 the residue list without the key (T4 red); M7 old
`delete_body` text (T5 red).

## Real-device acceptance (after H3b; spec §8 H3, plus what H1 plan §0.9 and H2 plan "Not reachable before H3" moved here)

Setup (never print tokens — read them into variables, print only lengths):
- Two clients, each with ITS OWN host list and its own local ids (feedback: distinct host ids per client), from the
  worktree root: `playwright cli -s=host-ownership-a` / `-s=host-ownership-b`, worktree dev server on :5175 (the main
  checkout's :5174 is not touched). Section revs: `GET /api/profiles/{id}` on mlab (auth header from a variable).
- **A SOT with a legacy `hosts` row.** First run :5175 on the base commit (the last main before H3a-2): both clients
  add mlab (`100.64.0.2:7860`) through their UI; create test workbench W on mlab, A master, B attached; confirm the
  SOT lists `hosts` (rev ≥ 1). Record `hosts` rev / hash and a hash of its payload (a scratchpad script: fetch, hash,
  print only the hash). Then stop :5175, check out the H3 branch, start :5175 again, reload both clients.
- Close both sessions and stop :5175 before any mutation run (feedback: no mutation test with a live page open).

Steps:
1. **Nothing reads or writes the row.** `playwright cli -s=host-ownership-a requests` and `-b`: over steps 2–7, no
   request path contains `/sections/hosts` (GET, PUT or DELETE). At the end the `hosts` rev, hash and payload hash
   equal the recorded ones.
2. **A new host stays on its device.** On A add air26 (`100.64.0.4:7860`) through the UI. B's Hosts list: still mlab
   only (wait 10 s, reload B, still mlab only). The legacy `hosts` row is unchanged (step 1). `settings` MAY move —
   expected, not a leak (review item 2): the add dialog seeds a look entry (`AddHostDialog.tsx:195` `seedHostLook`,
   H2c) under the host's wire id at that moment — its LOCAL id, the daemonId is not known yet — and the H1b pass
   re-keys it to `d1_…` once the daemonId is verified (a second push). Check the end state instead: `settings.looks`
   holds exactly one entry for air26, under its `d1_…` key, and no key equal to A's local id; `workspaces` and every
   `tabs.*` rev unchanged; no section payload contains air26's ip or token (scratchpad script over every section: print
   only "absent" / the section key).
3. **A tab on it reaches B as "no host here".** On A open an air26 session tab in W → B shows the tab, its pane "This
   device has no host ‹air26 look name›" (the look name — H2c), nothing marked; A's pane live.
4. **A pull never removes a local host.** B adds air26 itself, through its own UI (its own local id). B's pane from
   step 3 goes live (H1b pass). `tabs.<ws>` rev unchanged; `settings` may move (B's add seeds a look under B's local
   id, then the re-key drops it because the workbench's `d1_…` entry wins — step 2's rule); at the end
   `settings.looks.<d1 air26>` is byte-for-byte what step 2 left. Then on B: Settings › 工作台 → stop
   sync → attach W again with **pull**: the wizard shows no removal list; after the run B still has mlab AND air26,
   although the legacy SOT `hosts` row lists mlab only.
5. **Deleting is local.** On A delete air26 (dialog: tabs stay). A: its air26 tab shows "no host here". B: host list
   unchanged, its air26 pane stays live, no toast, nothing marked. `tabs.<ws>` / `settings` revs unchanged.
6. **Names per device, looks per workbench.** A re-adds air26 through the add dialog under the name "air-A". The
   workbench already holds a look for air26's `d1_…`; A's seed lands under A's local id and the re-key drops it once
   the daemonId is verified (the existing `d1_…` entry wins), so after that A shows the workbench name, not "air-A",
   `settings.looks.<d1 air26>` is unchanged, and B's host list is unchanged. Rename air26 on B → A's badge and New Tab label
   follow (through `settings`); neither host list moves.
7. **Undo is local too.** Delete air26 on B, Undo in the toast → B's pane live again; A unaffected throughout.
8. **Delete copy.** Settings › 工作台 on A, sync host list: open the delete dialog of a throwaway workbench W2 created
   in setup on the base commit (it has a `hosts` row) → the sentence about host addresses and access tokens is shown
   (en and zh-TW); confirm → `GET /api/profiles/{W2}` is 404 (its rows went with it — `store.go:229`).
9. **[D1]** A settings edit on A (e.g. tab position) → the SOT `settings` row's ordinal is H2d-1's + 1 and its fingerprint is this build's.

Not reproducible (stated, not tested): the known limitation of spec §5.4 needs an old client.

Cleanup: `playwright cli -s=host-ownership-a close`, `-s=host-ownership-b close` (same cwd), delete W, remove air26
from both clients if added, stop :5175.

## H3-note — for the H3 bump PR's (H3a-1 + H3a-2 + H3b) CHANGELOG / release notes

- The host list is per device: adding, renaming the device fallback of, or deleting a host no longer reaches other
  devices; move hosts with the transfer code (H4).
- Known limitation (spec §5.4): upgrade every device together. A profile whose sync-host copy holds only a host list
  (or whose tabs / settings an older version wrote last) does not lock an older Purdex, which keeps syncing hosts
  there. An H2-era build (host looks, pre-H3) is locked once this version has written the profile's settings (D1).
- Host addresses and tokens older versions stored in a profile stay on the sync host until that profile is deleted
  there.

## Review 2026-09-24 (codex plan review task-mufgyw2p-92pcln)

All three checked against the code and adopted.

1. [important 0.99] H3a-2 claimed the full "never reads the `hosts` row" guarantee while the wizard still GETs it (`wizard-run.ts:331`, called from `previewPull` / `prepareRun` / `recheckBeforeAttach`) until H3b — confirmed; H3a-2 T4 and its invariants are scoped to the sync loop (executor, collector, start layer), H3b T1 completes spec §5.3 with a wizard end-to-end test (`ProfileWizard.integration.test.tsx`, H3b 19 → 20 files), "What stays" says which PR guarantees what.
2. [important 0.98] Acceptance step 2 forbade a `settings` move that H2c makes on every add — confirmed (`AddHostDialog.tsx:195` `seedHostLook`, keyed by the local id until the daemonId is verified, then re-keyed by the pass); steps 2, 4 and 6 now check the legacy row, host membership, one `d1_…` look entry and no host secrets in any section, and allow `settings` to move.
3. [minor 0.97] One bump for all five PRs was not a real dependency — confirmed: H3a-3 / H3a-4 delete code H3a-2 made unreachable (`applySectionToStores('hosts')` has no caller once the executor never pulls it, `executor.ts:1360`); the H3 bump is H3a-1 + H3a-2 + H3b, the two cleanups ride any later bump (Dependencies and order).
