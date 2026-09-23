# Plan — host rekey on pull (spec: `2026-09-23-host-rekey-spec.md`)

Two PRs, each ≤ 20 files, merged in order; bump once after the second, together with part 1 (#1349, #1351).

## PR 1 — `rekeyHosts` (no caller yet)

Task 1 — measure first (a subagent, read-only): confirm every row of spec §4 against the code at HEAD, and list any
host-id holder the table misses (grep `hostId`, `host:`, `sessions:`, `daemon:`, composite keys). Report before
writing code; a miss is added to the spec, not skipped.

Task 2 — `lib/profile/host-rekey.ts`: `rekeyHosts(map): {ok: true} | {ok: false, reason: 'attached' | 'unsettled' |
'busy' | 'dirty-editor' | 'id-collision' | 'write-failed', detail?}`. Pure helpers per store (`renameHostIdsInPane`,
`renameHostIdsInWorld`, …) tested alone; the store writes in one synchronous block under `withWorldLock`; snapshot +
rollback; world fence epoch bumped.
Tests: a table-driven test with a world holding every pane kind on host X (on screen, parked master, two slaves),
host settings for X, a preset column `sessions:X`, history / recent-files / launcher memory / pendingDetaches rows
for X → after `rekeyHosts({X: Y})` no `X` anywhere in localStorage (scan the serialized stores), every row now
names Y; a second host Z untouched. Rollback: a store write throws mid-way → every store byte-equal to before.
Cross-window: a second store instance rehydrates to the same state. Dirty editor buffer → refused, nothing written.
Mutation: drop each store's rename in turn → the scan test fails.

## PR 2 — matching, the wizard, the run

Task 3 — `lib/profile/host-match.ts`: `matchHosts(local, sot, attachHostId) → {ok: true, map, removed: string[]} |
{ok: false, reason: 'master-unmatched' | 'master-unverified' | 'master-address-differs' | 'ambiguous' |
'id-collision', detail}` — pure, spec §3's rules one by one, each with its own test (identity match, fallback only
without `S.daemonId`, mismatch excluded, unverified excluded, one-to-one, collision, address check on H only).

Task 4 — `wizard-run.ts`: `prepareRun` fetches the SOT `hosts` section (`getSection(…, {expectEndpoint})`) after
the re-list, runs `matchHosts`, freezes `{map, removed, hostsRev}` into the plan; `listProfiles` gets
`expectEndpoint`; `runPlan` = promote → save → rekey (re-check `hostsRev` first) → `attachMaster(newHostId, …)`.
New sub-step `rekey`; its failure reasons join the closed lists (`reasonKey`).

Task 5 — wizard UI + i18n: each refusal as a sentence at the direction step (`profile-wizard-hosts-refused`,
`data-reason`); the removed-hosts list under the pull warning (`profile-wizard-hosts-removed`); the `rekey` row in
the run list. en + zh-TW.

Task 6 — end-to-end test (`ProfileWizard.integration.test.tsx`, real stores, fake api): device B with its own ids
pulls a profile written with A's ids → no `locked:invalid`, B's master id is A's, the saved slave's panes name A's id.

## Review and acceptance

Codex: this plan + spec one round; each PR R1 + attack + critic (PR 1 rewrites persisted user data).
Real machine: spec §7, two clients with independent host ids.
