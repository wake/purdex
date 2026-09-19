# Plan — Profile Sync P2a: `spa/src/lib/profile/`, the pure core

- Spec: `2026-09-20-profile-sync-spec.md`. §4.2 projections, §4.4 state, §4.5 shape, §4.6.1–4.6.3 the
  decision table, §4.7 collector/applier. P1 (the daemon) shipped as alpha.411; its wire contract
  is spec §4.6/§4.8 and is **not** touched here.
- Worktree `.claude/worktrees/profile-sync`, branch `worktree-profile-sync-p2a`, based on
  `origin/main` alpha.411 (`07f292b3`). **P2a only** — one PR.
- **Scope rule: nothing in this PR imports a store, `fetch`, `hostFetch`, a timer, `Date.now`, or
  React.** Every input is a parameter. The template is `spa/src/lib/nex/nex-host-reducer.ts`
  (header: "No fetching, no clocks, no stores — every input is a parameter") with its effects in a
  sibling file — that sibling is P2b. The single environment-dependent call is `sha256Hex`
  (`crypto.subtle`, async), isolated in `hash.ts`.
- Nothing is wired: no store subscribes, no UI reads it, the app behaves exactly as alpha.411.
- Every task: subagent, **TDD — failing test first**, one commit per task,
  `git commit --only <files>`. Every Bash call prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/profile-sync/spa &&`.
- Verify per task: `npx vitest run src/lib/profile`. Before the PR:
  `npx vitest run && pnpm run lint && npx tsc -p tsconfig.app.json --noEmit && pnpm run build`
  (bare `tsc --noEmit` is a no-op here — the root tsconfig is references-only).
- PR size: 11 source/test files + this plan + spec edits = 13 files (rule: ≤ 800 lines **or** ≤ 20
  files).

## Measured baseline (2026-09-20, alpha.411) — and where the spec was wrong

Measured by reading the code, not inferred. Items marked ⚠ contradict the spec and are fixed in
the spec in Task 0.

- `structuralKey` (`lib/device-state/payload.ts:83`) is typed `(snap: WorkspaceSnapshot)` and strips
  a top-level `capturedAt`; `hashPayload` (`:89`) is **async** (`sha256Hex`, `lib/crypto-hash.ts:10`,
  `crypto.subtle.digest`) and returns 64 lowercase hex. It works under vitest's jsdom with no
  polyfill. `sortKeysDeep` (`:67`) is module-private. → P2a writes a generic copy in
  `lib/profile/hash.ts`; the device-state original stays where it is until P4b deletes it (moving
  it now would drag `snapshot/types` into the new lib).
- ⚠ `HostConfig` (`stores/useHostStore.ts:23-51`) has five persisted fields the spec's `hosts`
  projection omits: **`order` (required `number`)**, `color`, `colors`, `icon`, `iconWeight`.
  Decision 7 is "the hosts section travels whole"; without them a pull loses every host's colour
  and icon and yields a type-invalid `HostConfig`.
- ⚠ `useWorkspaceStore.workspaces` is a **`Workspace[]`** (`features/workspace/store.ts:10`), order =
  array order. The `{order, workspaces: Record}` of spec §4.2 is a conversion P2a writes on both
  sides, not something that exists.
- ⚠ `useUISettingsStore`, `useWorkspaceSettingsStore`, `useHostSettingsStore` have **no
  `partialize`** — "the partialized state" is not a callable thing for them. And three persisted
  fields are not preferences at all: `useUISettingsStore.terminalSettingsVersion` (a bump counter
  that forces terminal reconnects), `useNewTabLayoutStore.activeEditingProfile` (which tab of the
  layout editor is open) and `.knownIds` (a derived registry). → the `settings` projection lists
  **fields per store**, and those three are not listed (device-local by construction, §4.2).
- ⚠ `visitHistory` is not persisted at all; `activeTabId` is. Both stay device-local — no change in
  behaviour, the spec's table was just imprecise.
- ⚠ `replaceTabSnapshot` (`lib/snapshot/restore.ts:272`) replaces the **whole world** and
  `validateSnapshotConsistency` demands global self-consistency; a single `tabs.<ws>` section
  cannot be fed to it. → the applier is new per-section pure functions. What P2b reuses from the
  device-state pipeline is the pure helpers (`remapLayoutSessions`, `markMissingHosts`) and the
  operation lock — not the commit step.
- Split nodes get `id: generateId()` at three sites, all `lib/pane-tree.ts` (`:133`, `:193`, `:194`),
  always `sizes: [50, 50]`. The id **travels inside `layout`**, so after one apply both sides share
  it — §4.7's "match split nodes by id" holds. `sizes` are percentages summing to 100 by convention
  (`pane-tree.ts:157-160` renormalises to 100; the renderer uses them as flex-grow, so the sum is
  not enforced).
- `useLayoutStore.healLayoutInvariant` (`:23-27`) widens the device-local `activityBarWidth` when
  `tabPosition === 'left'`. Syncing `tabPosition` can therefore move one device-local field. That
  is a layout invariant, not a sync leak; accepted, recorded in the spec, wired in P2b.
- tsconfig: `strict`, `noUnusedLocals/Parameters`, `erasableSyntaxOnly` (**no `enum`**),
  `verbatimModuleSyntax` (**`import type`**), `noUncheckedIndexedAccess` off. ESLint has no
  import-boundary or naming rules for `lib/`.
- No `lib/` test uses `toMatchSnapshot`/`toMatchInlineSnapshot`; §4.5's guard test is the first.
  Use **`toMatchInlineSnapshot`** so the expected values live in the test file and a change shows
  up in the PR diff.
- `lib/profile/` does not exist. `Profile`/`ProfileKey` are already exported by
  `lib/resolve-profile.ts` (new-tab layout presets). → this lib exports **no bare `Profile` type**;
  its names are `ProfileSectionKey`, `SectionPayload`, `SectionSyncState`, … (the rename of the
  new-tab concept is P3, spec §7).

## Task 0 — spec corrections (docs only, main session, before the subagents start)

§3.2–§3.4 line references and the `useNewTabLayoutStore` line count; §3.3 the `visitHistory` note
and the three non-preference fields; §4.2 the `hosts` and per-store `settings` projections and the
exclusion entry; §4.7 that the applier is per-section pure functions and what is reused; the
`healLayoutInvariant` note. Logged in §9.3.

## Task 1 — `hash.ts`: canonical form and hash

```ts
export function structuralKey(value: unknown): string          // sync
export async function hashSection(payload: unknown): Promise<string>   // 64 lowercase hex
```

- `structuralKey` = `JSON.stringify` of the value with object keys sorted recursively, arrays in
  order. **No `capturedAt` special case** (section payloads have none).
- `undefined` object members are dropped (as `JSON.stringify` does) so `{icon: undefined}` and `{}`
  hash the same — optional fields come and go across clients.
- Non-finite numbers, functions, symbols, `bigint` → **throw**. A payload that cannot round-trip
  JSON must not produce a hash that the other side can never reproduce.

Tests: key order does not matter, array order does; nested; `undefined` member ≡ absent;
`null` ≠ absent; throws on `NaN`/`Infinity`/function/bigint; known vector for `{}`; a payload
survives `JSON.parse(JSON.stringify(x))` with an unchanged hash (the daemon stores and returns it
as JSON, P1 stores it byte-for-byte but the client re-serialises).

## Task 2 — `projections.ts`: what is synced, and the shape signal

```ts
export type SectionKind = 'hosts' | 'settings' | 'workspaces' | 'tabs'
export type ProfileSectionKey = 'hosts' | 'settings' | 'workspaces' | `tabs.${string}`

export const PROJECTIONS: Record<SectionKind, readonly string[]>
export const SECTION_SCHEMA_ORDINAL: Record<SectionKind, number>   // all 1 at first ship

export function sectionKind(key: string): SectionKind | null      // 'tabs.ws1' → 'tabs'; unknown → null
export function tabsSectionKey(workspaceId: string): ProfileSectionKey
export function workspaceIdOf(key: ProfileSectionKey): string | null
export async function sectionFingerprint(kind: SectionKind): Promise<string>
export function project(source: unknown, paths: readonly string[]): unknown
```

`PROJECTIONS` (the corrected §4.2):

```ts
hosts:      ['hostOrder', 'hosts.*.id', 'hosts.*.name', 'hosts.*.ip', 'hosts.*.port', 'hosts.*.token',
             'hosts.*.order', 'hosts.*.color', 'hosts.*.colors', 'hosts.*.icon', 'hosts.*.iconWeight'],
workspaces: ['order', 'workspaces.*.name', 'workspaces.*.icon', 'workspaces.*.iconWeight',
             'workspaces.*.moduleConfig'],
tabs:       ['order', 'tabs.*.id', 'tabs.*.pinned', 'tabs.*.locked', 'tabs.*.createdAt',
             'tabs.*.layout', '!tabs.*.layout..sizes'],
settings:   [ /* '<storeKey>.<field>' for every listed field, see below */ ],
```

- Path grammar: dot-separated keys; `*` = every key of a record. A leading `!` with `..name` is an
  **exclusion of that key at any depth** under the prefix. `project()` implements includes and
  exclusions; nothing else. A path that matches nothing contributes nothing (optional fields).
- `settings` paths, per store (value fields only, measured from each store's persisted shape):
  - `purdex-ui-settings.*` for the 25 preference fields — **every persisted field except
    `terminalSettingsVersion`**. List them explicitly; do not use a wildcard, the point is that a
    new field is unsynced until someone lists it (and bumps the ordinal).
  - `purdex-editor-settings`: `tabSize insertSpaces wordWrap lineNumbers minimap fontSize
    popupOnMissingFile autoSearchLayer1 contentWidth`
  - `purdex-themes`: `activeThemeId customThemes` · `purdex-i18n`: `activeLocaleId customLocales`
  - `purdex-notification-settings`: `agents` · `purdex-module-enabled`: `enabled`
  - `purdex-workspace-settings`: `workspaces` · `purdex-host-settings`: `hosts`
  - `purdex-newtab-layout`: `profiles` only (**not** `knownIds`, **not** `activeEditingProfile`)
  - `purdex-layout`: `tabPosition` only
  The subagent reads each store file to get the exact field names; if a name in this list does not
  exist in the store, stop and report rather than guess.
- `sectionFingerprint(kind)` = `sha256Hex` of the kind's path list, **sorted**, joined with `\n`.
  Sorted so that reordering the literal is not a shape change; the exclusion entry is part of the
  list, so dropping it *is* one.
- `tabs.<id>`: `workspaceIdOf` must accept exactly what the daemon accepts
  (`^tabs\.[A-Za-z0-9_-]{1,64}$`, P1 `validate.go`); `tabsSectionKey` **throws** on an id outside
  that alphabet rather than producing a key the daemon will 400. (Workspace ids come from
  `generateId()`; the test pins that its alphabet is inside the daemon's.)

Tests: `project` — includes, `*`, missing optional path, exclusion at depth 1 and depth 3, the
exclusion does not remove a same-named key outside its prefix, input never mutated. `sectionKind`
for the four kinds + garbage. **The guard test** (spec §4.5): 

```ts
expect(await shapeTable()).toMatchInlineSnapshot(`{ hosts: [<fp>, 1], … }`)
```

with a failure-message comment above it: *a projection changed — bump
`SECTION_SCHEMA_ORDINAL.<kind>` and update this snapshot in the same commit.* Plus: reordering a
projection list leaves the fingerprint unchanged; adding/removing a path changes it.

## Task 3 — `sections.ts`: builders (store state in → section payloads out)

Pure functions over **plain data** shaped like the stores' state — typed with `Pick<…>` of the
real state types via `import type`, so a store refactor breaks the build here, but no store module
is imported at runtime.

```ts
export function buildHostsSection(s: Pick<HostState, 'hosts' | 'hostOrder'>): HostsPayload
export function buildWorkspacesSection(workspaces: readonly Workspace[]): WorkspacesPayload
export function buildTabsSection(ws: Workspace, tabs: Record<string, Tab>): TabsPayload
export function buildSettingsSection(stores: SettingsSources): SettingsPayload
export function buildProfileDocument(input: CollectInput): Record<ProfileSectionKey, SectionPayload>
export function stripSizes(layout: PaneLayout): PaneLayout
```

- Every builder is `project(shaped, PROJECTIONS[kind])` over an intermediate it shapes first
  (array → `{order, workspaces}`; `Workspace.tabs` → `{order, tabs}`), so **the projection is the
  only allowlist** (§4.2).
- `buildTabsSection`: `order` = `ws.tabs` filtered to ids present in `tabs`; a tab id in `ws.tabs`
  with no `Tab` is dropped (not invented). Layout goes through `stripSizes` — split **structure**
  in, **ratios** out (decision 8). `Workspace.activeTabId` is not in the payload.
- `buildProfileDocument`: `hosts`, `settings`, `workspaces`, and one `tabs.<id>` per workspace.
  **Tabs that belong to no workspace are not collected** — §4.3 removes standalone tabs in P3; until
  then they are simply device-local. The function reports them
  (`{document, standaloneTabIds}`) so P2b/P3 can surface it.
- Device-local fields must be **provably absent**: a test feeds a fully-populated input with
  sentinel values in every device-local field (`activeWorkspaceId`, `activeTabId`, `activeHostId`,
  `devHostId`, `runtime`, `visitHistory`, `terminalSettingsVersion`, `activeEditingProfile`,
  `knownIds`, `regions`, `activityBarWidth`, `sizes`, …) and asserts the sentinel string appears
  nowhere in `structuralKey(document)`.

Tests, additionally: resizing a split changes no hash; splitting a tab does; reordering workspaces
changes `workspaces` only; moving a tab between two workspaces changes exactly those two `tabs.*`
sections; a host colour change changes `hosts` only; `PaneRebuildRecord` on a pane content is
preserved verbatim (decision 13); inputs are never mutated (deep-freeze the input).

## Task 4 — `sync-state.ts`: the per-section state machine (§4.4, §4.6.1, §4.6.2)

A reducer, in the style of `lib/nex/event-reducer.ts`. No `enum` (`erasableSyntaxOnly`) — string
unions.

```ts
export interface SectionSyncState {
  baseRev: number            // 0 = never agreed
  baseHash: string | null
  currentHash: string | null // hash of the live payload; null = section does not exist locally
  status: 'synced' | 'pending' | 'locked:conflict'
  inFlight: { hash: string; baseRev: number; kind: 'put' | 'delete' } | null
  sotMovedWhileInFlight: boolean
  sot: SotIndexEntry | null  // last known SOT index row; null = absent on the SOT
  conflict: { localHash: string; sotRev: number; sotHash: string | null } | null
}

export type SectionEvent =
  | { type: 'local-changed'; hash: string | null }
  | { type: 'sot-index'; entry: SotIndexEntry | null }         // reconcile-on-connect, or a fetched row
  | { type: 'remote-event'; rev: number; hash: string; deleted: boolean; own: boolean }
  | { type: 'push-started'; hash: string | null }
  | { type: 'push-applied'; rev: number }                      // 200 applied:true
  | { type: 'push-converged'; rev: number }                    // 200 applied:false
  | { type: 'push-conflict'; rev: number; hash: string | null }// 409 conflict (rev 0 = deleted under us)
  | { type: 'push-failed' }                                    // network / 5xx / 503 contended
  | { type: 'pull-applied'; rev: number; hash: string | null }
  | { type: 'resolved'; keep: 'local' | 'sot' }

export type SectionAction =
  | { do: 'nothing' } | { do: 'pull' } | { do: 'push'; baseRev: number } | { do: 'delete'; baseRev: number }
  | { do: 'lock-conflict' } | { do: 'lock-reset' }             // rev < baseRev: the profile was recreated

export function reduceSection(s: SectionSyncState, e: SectionEvent): SectionSyncState
export function decideSection(s: SectionSyncState, ctx: { reachable: boolean; autoSync: boolean }): SectionAction
export function isDirty(s: SectionSyncState): boolean          // currentHash !== baseHash
```

`decideSection` **is** the table of §4.6.1, row for row, and the tests are one per row:

| dirty | SOT vs base | Action |
|---|---|---|
| no | `rev == baseRev` | nothing |
| no | `rev > baseRev` | pull |
| yes | `rev == baseRev` | push (or `delete` when `currentHash === null`) |
| yes | `rev > baseRev` | lock-conflict |
| — | `rev < baseRev` | lock-reset |
| — | absent on SOT, `baseRev == 0`, exists locally | push with `baseRev 0` (create) |
| — | absent on SOT, `baseRev > 0`, clean | pull-as-delete (the section was deleted elsewhere) |
| — | absent on SOT, `baseRev > 0`, dirty | lock-conflict |

Plus the three rules that are not rows:

- **Converged is not a conflict**: dirty + `rev > baseRev` but `sot.hash === currentHash` → no lock;
  reduce to `synced` at the SOT's rev (the client-side mirror of the daemon's `applied:false`).
- `status === 'locked:conflict'` → always `nothing` until `resolved` (§4.4: stops writing *and*
  refuses inbound). `inFlight !== null` → always `nothing`.
- `!reachable || !autoSync` → a push/delete/pull decision becomes `nothing` and a dirty section
  reads `pending`. A manual "Sync now" is the caller passing `autoSync: true` once — not a separate
  code path.

`reduceSection`, the parts that carry the §4.6.2 promises (each is a named test):

1. `remote-event` with `own: true` → ignored. With `own: false` on a **dirty** section → not
   applied; `sot` advances, so the next `decideSection` yields `lock-conflict` (rule 1).
2. `remote-event` while `inFlight` → only sets `sotMovedWhileInFlight` and records `sot`; the
   push's own outcome then re-runs the table (rule 2).
3. `push-conflict` → `conflict.localHash` is **`inFlight.hash`, the payload that was sent**, not
   `currentHash` — the live stores may have moved on; the user chooses between two known
   snapshots.
4. `push-applied` → `baseRev = rev`, `baseHash = inFlight.hash`. If `currentHash` moved during the
   flight the section is simply dirty again against the new base — no lost edit.
5. `resolved keep:'local'` → `baseRev = conflict.sotRev`, `baseHash = conflict.sotHash`, unlock; the
   section is dirty against the SOT's rev, so the next decision is a plain `push` that the CAS will
   accept. `keep:'sot'` → unlock and `pull`.
6. Out-of-order / duplicate events never move `sot.rev` backwards (guard as in
   `event-reducer.ts:167`), except `sot-index`, which is authoritative and is how `lock-reset` is
   detected.

Reducer purity test: every `reduceSection` call on a deep-frozen state returns without throwing.

## Task 5 — `profile-state.ts`: profile level — schema lock and section lifecycle

```ts
export type ShapeVerdict = 'ok' | 'i-am-newer' | 'sot-is-newer' | 'shape-changed-without-ordinal'
export function compareShape(mine: Shape, sot: Shape): ShapeVerdict            // §4.5's table
export function profileLock(index: SotIndexEntry[], mine: Record<SectionKind, Shape>): SchemaLock | null
export function profileStatus(sections: Record<string, SectionSyncState>, lock: SchemaLock | null): ProfileStatus
export function reconcileSectionSet(args: {
  workspaceIds: readonly string[]      // from the applied `workspaces` section — the authority
  localKeys: readonly string[]; sotKeys: readonly string[]
}): { create: ProfileSectionKey[]; remove: ProfileSectionKey[]; keepUnrendered: string[] }
```

- `compareShape` — four rows of §4.5, one test each. `profileLock` returns the **first offending
  section and the verdict** (the panel must name it, acceptance 7); any `sot-is-newer` or
  `shape-changed-without-ordinal` locks the **whole profile** (§4.4).
- `profileStatus`: `idle` (no master) | `locked:schema` | worst-of-sections
  (`locked:conflict` > `pending` > `synced`).
- `reconcileSectionSet` (§4.6.3): `tabs.*` for a workspace that is gone → `remove`; a workspace with
  no `tabs.*` → `create`; a `tabs.<id>` whose workspace is unknown → **`keepUnrendered`, never
  removed**; a key whose kind is unknown (`sectionKind() === null`) → carried, never in `remove`
  (forward compatibility).

## Task 6 — `applier.ts`: `(local, incoming) → next`, per section (§4.7)

```ts
export function applyHosts(local: HostsSlice, incoming: HostsPayload): HostsSlice
export function applyWorkspaces(local: WorkspacesSlice, incoming: WorkspacesPayload): WorkspacesSlice
export function applyTabs(local: TabsSlice, workspaceId: string, incoming: TabsPayload): TabsSlice
export function applySettings(local: SettingsSources, incoming: SettingsPayload): SettingsSources
export function restoreSizes(incoming: PaneLayout, local: PaneLayout | undefined): PaneLayout
export function deriveTabOrder(workspaces: readonly Workspace[], tabs: Record<string, Tab>, previous: readonly string[]): string[]
export function isWellFormedSection(kind: SectionKind, payload: unknown): boolean
```

- `applyHosts`: replaces `hosts` + `hostOrder`; **keeps** `activeHostId` / `devHostId` when the host
  still exists, else `null`. Returns the ids that vanished so P2b can run `markMissingHosts`.
- `applyWorkspaces`: `{order, Record}` → `Workspace[]` in `order`. For a workspace that already
  exists locally, **`tabs` and `activeTabId` are kept from local** (they belong to `tabs.<ws>` and to
  the device); a new workspace arrives with `tabs: []`, `activeTabId: null` — the defined "empty
  workspace" partial state of §4.6.3. `activeWorkspaceId` kept if it survives, else the first.
- `applyTabs`: replaces that workspace's tabs and its `Workspace.tabs` order; tabs of other
  workspaces untouched; a local tab of this workspace absent from `incoming` is removed; keeps
  `activeTabId` if it survives, else the first of the new order, else `null`. Each incoming layout
  goes through `restoreSizes(incomingLayout, localTab?.layout)`. Applying `tabs.<id>` for a
  workspace that does not exist locally is a **no-op that reports `unrendered`** (§4.6.3), not an
  error.
- `restoreSizes`: incoming has no `sizes` (stripped). For each split node, if a local split with
  the same `id` **and the same child count** exists → copy its `sizes`; otherwise distribute evenly
  (`100 / n` each). Result always has `sizes.length === children.length`.
- `applySettings`: per store, overwrite exactly the projected fields, leave every other field of
  that store's slice as it was (`terminalSettingsVersion`, `activeEditingProfile`, `knownIds`, the
  whole of `useLayoutStore` but `tabPosition`). Unknown store keys in the payload are ignored
  (a newer client's store). It does **not** call `healLayoutInvariant` — that is a store concern,
  P2b.
- `deriveTabOrder` (§4.3): concatenation of the workspaces' orders, then any remaining (standalone)
  tab ids in their `previous` relative order — so `tabOrder` stops being a second source of truth
  without dropping a tab the UI can still reach.
- `isWellFormedSection`: structural guard run before any apply (the device-state pipeline's
  well-formedness step, per section): right top-level keys, `order` ⊆ keys of the record, every
  `Tab` has a layout whose leaves have `pane.id` and `content.kind`. A payload that fails is **not
  applied** and the caller locks the section — never a partial apply.

Tests, the ones that matter most:
- **Round trip**: for a populated world, `apply*(empty, build*(world))` reproduces the synced part
  of `world` exactly, and `build*(apply*(local, payload))` has the same hash as `payload` — for all
  four kinds. This is the property that makes "converged" reachable at all.
- `restoreSizes`: same id + same arity → local ratios; same id, different arity → even; new id →
  even; nested splits; local `undefined`.
- focus is not mirrored: `activeWorkspaceId`, `activeTabId`, `activeHostId`, `devHostId` survive
  every apply when their target survives.
- `applySettings` leaves sentinel values in every unlisted field untouched.
- inputs never mutated (deep-freeze `local` and `incoming`).

## Task order

`0` (main session) → `1` → `2` → then **`3`, `4`, `5` in parallel** (3 needs `project`; 4 and 5 need
only the types from 2) → `6` (needs 2 and 3, for the round-trip tests). A shared `types.ts` is
created in Task 2 and only **appended to** afterwards; parallel tasks add their types in their own
file instead, to keep `git commit --only` clean.

## PR

Title: `feat(spa): lib/profile — the pure core of Profile Sync (P2a)`

Body: spec path; that nothing is wired and the app is unchanged; the file count; the spec
corrections of Task 0 and why (measured facts); what a reviewer should check hardest —
(1) `decideSection` against §4.6.1 row by row, (2) `conflict.localHash` is the sent payload,
(3) the device-local sentinel test, (4) the build/apply round trip, (5) `restoreSizes`.

Attacker focus: a sequence of `SectionEvent`s that ends in a push the client should have known was
stale, or in an apply over a dirty section; an event order that leaves `inFlight` set forever; a
payload that passes `isWellFormedSection` and still corrupts a slice; a field that leaks through
`project()`; hash instability (`undefined` vs absent, key order, number formatting).

## After P2a

P2b: transport and wiring — the CAS client against P1's routes, `'profile'` in the `HostEvent`
union and its dispatch, the collector's store subscriptions with a 500 ms per-section debounce,
reconcile-on-connect, `useProfileStore` (device-local: `clientId`, master, active, `autoSync`, the
persisted `SectionSyncState` map), and moving `clientId` off `useSyncStore`.
