# Profile Sync P3e — the new-tab "profile" becomes "preset" (plan)

Spec: `docs/specs/2026-09-20-profile-sync-p3-plan.md` §"P3e" (line 869) and
`docs/specs/2026-09-20-profile-sync-spec.md` §4.5 (shape: fingerprint + ordinal).
Decided 2026-09-23 by the user: do it now, not as an issue — "so a later session
never meets two meanings of *profile*". Only the **new-tab layout** concept
(3-col / 2-col / 1-col) is renamed. Profile Sync's *profile* is untouched; so is
Nexen's "Sandbox profile / max profile" (`newtab.headless.profile`,
`newtab.headless.max_profile`, `HeadlessLauncherFields.tsx`). `PRODUCT.md` is not
touched (the coordinator writes §3.9).

## Vocabulary (the whole mapping — nothing else is renamed)

| Old | New |
|---|---|
| `lib/resolve-profile.ts` (+test) | `lib/resolve-preset.ts` (+test) |
| `type ProfileKey` | `type PresetKey` |
| `interface Profile` | `interface LayoutPreset` (no bare `Preset`: too generic next to themes/quick commands) |
| `resolveProfile()` | `resolvePreset()` |
| `makeProfile` / `cloneProfile` / `healProfileState` | `makePreset` / `clonePreset` / `healPresetState` |
| store state `profiles` | `presets` (**persisted, synced**) |
| store state `activeEditingProfile` | `activeEditingPreset` (persisted, device-local) |
| `NewTabProfileSwitcher.tsx` (+test) | `NewTabPresetSwitcher.tsx` (+test) |
| prop `profileKey` (Canvas / Thumbnail / dnd `data`) | `presetKey` |
| test ids `profile-{tab,empty,toggle,hint,thumb}-<k>` | `preset-{…}-<k>` |
| i18n `settings.interface.profile_{3col,2col,1col,locked,empty,prefilled}` | `settings.interface.preset_{…}` |
| en "Fallback profile (cannot be disabled)" | "Fallback preset (cannot be disabled)" |

zh-TW copy is unchanged (它本來就是「三欄／兩欄／單欄／保底配置」). dnd id strings
(`item:<k>:<id>`, `col:<k>:<i>`) keep their format — only the variable is renamed.
Comments that say "profile" about this concept are reworded in the files touched.
Historical docs (`docs/superpowers/specs/2026-04-17-…`) are not rewritten.

Known, accepted loss: a user's *custom locale* that translated the six old
`settings.interface.profile_*` keys falls back to the builtin text for them
(alpha: no migration, per `feedback_no_alpha_migration`).

## PR split (≤ 20 files each; subagents list files before committing)

~25 files in total, so three PRs, merged in order. B and C may be one PR if
together they stay ≤ 20 files.

### PR-A — identifiers, file names, i18n, copy (no persisted / synced change)
Everything in the table **except** the two store state fields. `git mv` for the
two file renames. Store action parameter names and helpers rename here too; the
state fields `profiles` / `activeEditingProfile` keep their names until PR-B, so
this PR changes no byte in localStorage and no byte on the wire (the settings
fingerprint snapshot in `projections.test.ts` must NOT change — that is the
proof). Expected files (~15): resolve-preset(+test), NewTabPresetSwitcher(+test),
NewTabCanvas(+test), NewTabThumbnail, NewTabSubsection, NewTabPage, store(+test),
en.json, zh-TW.json, lib/profile/types.ts (comment names `resolve-profile.ts`).

Tasks
- A1 resolve-preset: rename file + identifiers; test renamed with it (behaviour identical).
- A2 store helpers / action param names / types re-export (`PresetKey`, `LayoutPreset`).
- A3 components: switcher rename, `presetKey` props, test ids, i18n keys + en copy. Tests updated in the same commit.
- Gate: `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json`, full `npx vitest run`; `rg -n "ProfileKey|resolveProfile|NewTabProfileSwitcher|profileKey|makeProfile|healProfileState|interface\.profile_" spa/src` returns nothing.

### PR-B — persisted field `profiles → presets`, migrate v2, sync projection, ordinal 4
- B1 **store migrate** (`useNewTabLayoutStore.ts`): `version: 2`, `migrate(persisted, from)`:
  for `from < 2`, `{ presets: p.profiles, knownIds: p.knownIds, activeEditingPreset: p.activeEditingProfile }`
  (old keys dropped; a missing `activeEditingProfile` stays missing and `healPresetState`
  defaults it to `'1col'`; if a `presets` is somehow already there it wins). `partialize`
  and `healPresetState` use the new names. Heal still runs after migrate (`onRehydrateStorage`).
  Tests (TDD, written first):
  - v1 blob with `profiles` + `activeEditingProfile: '3col'` → state deep-equals the same data under the new names;
  - v1 blob **without** `activeEditingProfile` → `activeEditingPreset === '1col'`, presets equal;
  - v1 blob with a corrupt `profiles` (wrong column count) → heals exactly as v1 healing did;
  - the written-back blob is `{ state: { presets, knownIds, activeEditingPreset }, version: 2 }` and contains no `profiles` key;
  - a v2 blob round-trips unchanged.
  (Use the real `purdexStorage` + `persist.rehydrate()`, like the existing heal tests.)
- B2 **consumers**: every `s.profiles` / `activeEditingProfile` read → new names
  (NewTabPage, NewTabCanvas, NewTabThumbnail, NewTabSubsection, NewTabPresetSwitcher,
  `hooks/useNewTabBootstrap.ts`) and their tests' `setState` fixtures.
- B3 **projection + ordinal** (`lib/profile/projections.ts`): `settingsPaths('purdex-newtab-layout', ['presets'])`,
  comment "NOT `activeEditingPreset`"; `SECTION_SCHEMA_ORDINAL.settings: 4` with the
  history comment extended ("4: newtab `profiles` → `presets`"). Update the guard's
  inline snapshot **in the same commit** (settings fingerprint changes — expected — and
  ordinal 4; the other three kinds' rows unchanged). Update `projections.test.ts`
  (`toEqual(['presets'])`), `collector.test.ts` (ignored-field case → `activeEditingPreset`;
  the "synced field changes" case → `presets`), `applier.test.ts` / `sections.test.ts`
  fixtures that use the newtab store as "a listed field" → `presets`.
  New test in `projections.test.ts` — **coexistence by shape**: compute the old settings
  fingerprint from the current list with `presets` swapped back to `profiles`, then
  `compareShape(new, old) === 'i-am-newer'` and `compareShape(old, new) === 'sot-is-newer'`
  (an ordinal-3 client meeting an ordinal-4 row locks `locked:schema`; this build meeting
  an ordinal-3 row does not lock).

### PR-C — an ordinal-3 settings payload is upcast on apply (no `invalid`, no wipe)
Why: after PR-B, a settings payload written by an ordinal-3 client carries
`purdex-newtab-layout.profiles`. This build's guard refuses unlisted fields
(`isWellFormedSection` → `invalid`), and even past the guard the listed-but-absent
`presets` would be patched to `undefined` → healed to defaults → a wiped layout
pushed to the SOT. Such a payload reaches `applySettingsSection` through (a) a pull
while clean (`i-am-newer` is pulled like any other — executor header), (b) the
first attach in `pull` direction, (c) a conflict answered keep-sot.
- C1 pure `upcastLegacySettings(payload)` in `lib/profile/applier.ts`: when
  `payload['purdex-newtab-layout']` is a plain object that has `profiles` and no
  `presets`, return a copy with the field renamed; otherwise return the input as is
  (same reference). No other field is touched; never mutates. Unit tests in
  `applier.test.ts` (rename, both present → untouched, absent store → same ref, frozen input).
- C2 `applySettingsSection` (`apply-to-stores.ts`) calls it **before** `isWellFormedSection`.
  The returned hash is rebuilt from the stores, so it differs from the fetched row's hash:
  the executor reports `pull-hash-mismatch` once and pushes the section back in the new
  shape — that push IS the SOT's upgrade to ordinal 4. Accepted and asserted.
- C3 `executor.direction.integration.test.ts` — **coexistence, no ping-pong**:
  the SOT's settings row is ordinal 3 / old fingerprint / payload with `profiles`
  (a real layout); this build attaches (and, second case, receives it as a remote
  event while clean) → the stores hold that layout under `presets` (deep-equal),
  exactly **one** settings PUT goes out with ordinal 4, and over the next 60 s of fake
  time no further settings write is sent and the profile is `synced`.
  The old client's side (ordinal-3 build meeting the ordinal-4 row → `locked:schema`,
  writes nothing) is the mechanism the existing "a NEWER Purdex writes a section"
  test already proves; PR-B's shape test ties it to the real fingerprints.

### Fingerprint note
The settings fingerprint changes in PR-B — expected (spec §4.5); the ordinal bump is
what keeps clients from `shape-changed-without-ordinal`.

## Verification

- Every PR: lint, `tsc -p tsconfig.app.json`, full vitest, build.
- Mutation checks (delivered, per `feedback_tests_that_verify_nothing`) — run with **no
  browser / dev server open on this worktree**: remove the migrate → B1 tests red;
  revert ordinal to 3 → guard red; drop the upcast call → C3 red.
- **Real machine** (PR-B/C, `:5176` from this worktree): the `:5176` origin has its own
  localStorage. First run `main`'s code on `:5176`, set a non-default layout (enable
  3-col, move blocks, select 2-col for editing), screenshot Settings › Interface › New Tab
  and a new tab at each width; then switch the worktree to the branch, reload, and
  compare: same presets, same editing tab, same new-tab page; localStorage
  `purdex-newtab-layout` is `version: 2` with `presets`. **Not attached to any sync
  profile** — pushing an ordinal-4 settings row to the shared mlab daemon would lock
  every other session's older client; any sync-attached check is cleared with the
  coordinator first. `playwright cli -s=p3e-newtab-preset`, closed afterwards.

## Out of scope / risks
- A still-open *older build* window on the same origin as a new one (e.g. an old
  bundled Electron renderer): it has no migrate for v2 and zustand refuses the blob —
  alpha, dev-only, accepted.
- Nexen "Sandbox profile" wording — coordinator opens an issue.

## Flow
plan → codex (plan + spec, one round) → subagent TDD per task → PR-A → R1 + attacker
(parallel) → critic → merge; PR-B, PR-C likewise → **one** bump PR after PR-C (coordinator
notified first). Pure SPA, no deploy.
