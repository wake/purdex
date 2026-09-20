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
- PR size: `types.ts` + 6 modules + 6 test files + this plan + the spec = 15 files (rule: ≤ 800
  lines **or** ≤ 20 files).

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

**Task 0b** (after the plan review): spec §4.6.1's table restated with "absent" as `hash === null`
on either side, the wire-`baseRev`-0 rule for creating over an absent SOT, and the epoch / flight
token / retained-payload rules of §4.6.2. Logged in §9.4.

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
  **exclusion of that key at any depth under the prefix — depth zero included** (a tab whose
  `layout` *is* a split has `layout.sizes`, and it goes), descending through **objects and arrays**
  (`children[]`). Exclusions are applied after all includes, so the order of entries in the list
  never changes the result. `project()` implements includes and
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

Tests: `project` — includes, `*`, missing optional path, exclusion at **depth 0 (root split)**, depth
1, and depth 3 **through `children[]`**, the same result with the exclusion listed first or last, the
exclusion does not remove a same-named key outside its prefix, input never mutated. `sectionKind`
for the four kinds + garbage. **The guard test** (spec §4.5): 

```ts
expect(await shapeTable()).toMatchInlineSnapshot(`{ hosts: [<fp>, 1], … }`)
```

with a failure-message comment above it: *a projection changed — bump
`SECTION_SCHEMA_ORDINAL.<kind>` and update this snapshot in the same commit.* Plus: reordering a
projection list leaves the fingerprint unchanged; adding/removing a path changes it.

## Task 3 — `sections.ts`: builders (store state in → section payloads out)

Pure functions over **plain data** shaped like the stores' state. `HostState` and `WorkspaceState`
are **not exported** by their stores (plan review #11), and this PR does not touch store files, so
the lib declares its own structural input types in `types.ts` (`HostsSource`, `WorkspacesSource`,
`TabsSource`, `SettingsSources`) built from the types that *are* exported (`HostConfig`,
`Workspace`, `Tab`, `PaneLayout`, `TabPosition`). Compatibility is pinned where importing a store
is allowed — **in the test file**: `const _h: HostsSource = useHostStore.getState()` (and one per
store), so a store refactor fails `tsc -p tsconfig.app.json`, which includes tests.

```ts
export function buildHostsSection(s: HostsSource): HostsPayload
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
  Every tab belongs to exactly one workspace (spec §4.3), so the document has no place for a
  standalone tab. The pure half of §4.3 lives here (plan review #12):

  ```ts
  export function adoptStandaloneTabs(world: { workspaces: readonly Workspace[]; tabs: Record<string, Tab>; tabOrder: readonly string[] },
    opts: { unsortedName: string; newWorkspaceId: string }): { workspaces: Workspace[]; adopted: string[]; createdWorkspaceId: string | null }
  ```

  Standalone tabs (in `tabOrder` order) are appended to an existing workspace **named**
  `unsortedName`, else to a new one with `newWorkspaceId` — created only if there is something to
  adopt. `buildProfileDocument` still returns `standaloneTabIds` so a caller that skipped the
  adoption finds out; it never silently drops them into a section. When the wizard calls it and how
  the user is told is P2b/P3.
- Device-local fields must be **provably absent**: a test feeds a fully-populated input with
  sentinel values in every device-local field (`activeWorkspaceId`, `activeTabId`, `activeHostId`,
  `devHostId`, `runtime`, `visitHistory`, `terminalSettingsVersion`, `activeEditingProfile`,
  `knownIds`, `regions`, `activityBarWidth`, `sizes`, …) and asserts the sentinel string appears
  nowhere in `structuralKey(document)`.

Tests, additionally: `adoptStandaloneTabs` — none to adopt → input returned, no workspace created;
adopts into an existing `Unsorted`; creates one; preserves `tabOrder` order; never mutates.
Resizing a split changes no hash; splitting a tab does; reordering workspaces
changes `workspaces` only; moving a tab between two workspaces changes exactly those two `tabs.*`
sections; a host colour change changes `hosts` only; `PaneRebuildRecord` on a pane content is
preserved verbatim (decision 13); inputs are never mutated (deep-freeze the input).

## Task 4 — `sync-state.ts`: the per-section state machine (§4.4, §4.6.1, §4.6.2)

A reducer, in the style of `lib/nex/event-reducer.ts`. No `enum` (`erasableSyntaxOnly`) — string
unions. **Rewritten after the plan review (spec §9.4)**: the first draft could not represent a
tombstone, let a late index response move the SOT backwards, and had no way to tell a stale
decision from a current one across the effect boundary.

```ts
/** What one side holds. hash === null ⇔ the section does not exist there
 *  (never created, or a P1 tombstone — the client cannot and need not tell them apart). */
export interface Held { rev: number; hash: string | null }

export interface SectionSyncState {
  base: Held                 // what this client last agreed with the SOT on. {0,null} = nothing yet
  currentHash: string | null // hash of the live local payload; null = does not exist locally
  sot: Held                  // newest SOT state this client has observed. rev never decreases
  epoch: number              // bumped whenever base, sot, inFlight or lock changes
  status: 'synced' | 'pending' | 'locked:conflict' | 'locked:reset'
  inFlight: FlightToken | null
  sotMovedWhileInFlight: boolean
  conflict: { localHash: string | null; sot: Held } | null
  forcePull: boolean         // set by resolved keep:'sot'
  restoreLocal: string | null// set by resolved keep:'local' — hash of the sent snapshot to put back
  indexStale: boolean        // an index response was discarded; ask again
}
export interface FlightToken { kind: 'put' | 'delete'; hash: string | null; baseRev: number; epoch: number }

export function initialSectionState(currentHash: string | null): SectionSyncState

export type SectionEvent =
  | { type: 'local-changed'; hash: string | null }
  | { type: 'sot-index'; epoch: number; entry: { rev: number; hash: string } | null } // null = not listed
  | { type: 'remote-event'; rev: number; hash: string | null; own: boolean }          // hash null = deleted
  | { type: 'push-started'; token: FlightToken }
  | { type: 'push-applied'; rev: number }                       // 200 applied:true / DELETE 200
  | { type: 'push-converged'; rev: number }                     // 200 applied:false
  | { type: 'push-conflict'; rev: number; hash: string | null } // 409 conflict; rev 0 + null = absent
  | { type: 'push-failed' }                                     // network, 5xx, 503, timeout, malformed
  | { type: 'pull-applied'; rev: number; hash: string | null; localHash: string | null }
      // hash = what the SOT held (null = a deletion); localHash = what the stores hold after the apply.
      // Two hashes since P2b-2 (spec §9.9): with one, a sanitiser's correction could never read as dirty.
  | { type: 'local-restored'; hash: string | null }             // the driver put the snapshot back
  | { type: 'resolved'; keep: 'local' | 'sot' }

export type SectionAction =
  | { do: 'nothing' } | { do: 'reindex' } | { do: 'pull' }
  | { do: 'push'; token: FlightToken } | { do: 'delete'; token: FlightToken }
  | { do: 'restore-local'; hash: string | null }
  | { do: 'lock-conflict' } | { do: 'lock-reset' }

export function reduceSection(s: SectionSyncState, e: SectionEvent): SectionSyncState
export function decideSection(s: SectionSyncState, ctx: { reachable: boolean; autoSync: boolean }): SectionAction
export function isDirty(s: SectionSyncState): boolean           // currentHash !== base.hash
export function sotMoved(s: SectionSyncState): boolean          // sot.hash !== base.hash || sot.rev > base.rev
export function retainedHashes(s: SectionSyncState): (string)[] // payloads the driver must keep
```

**Wire `baseRev`.** P1 accepts a create — over nothing *or* over a tombstone — only with
`baseRev 0` (`sections.go`, decision step 1). So the token's `baseRev` is
`s.sot.hash === null ? 0 : s.base.rev`, never `base.rev` blindly. A `delete` is only ever decided
when the SOT side is live.

`decideSection`, evaluated top to bottom; first match wins. One test per row.

| # | Condition | Action |
|---|---|---|
| 0a | `status` is `locked:*` | nothing |
| 0b | `inFlight !== null` | nothing |
| 0c | `indexStale` | reindex (if reachable) |
| 0d | `restoreLocal !== null && currentHash !== restoreLocal` | restore-local |
| 0e | `forcePull` | pull (if reachable) |
| 1 | `sot.rev < base.rev` | lock-reset |
| 2 | clean, SOT not moved | nothing |
| 3 | clean, SOT moved | pull (a pull of an absent SOT applies a deletion) |
| 4 | dirty, SOT not moved, `currentHash !== null` | push |
| 5 | dirty, SOT not moved, `currentHash === null`, `sot.hash !== null` | delete |
| 6 | dirty, SOT not moved, both null | — unreachable: both-null is clean (row 2); test asserts it |
| 7 | dirty, SOT moved, `sot.hash === currentHash` | nothing here — the *reducer* already folded this into `synced` (converged, below) |
| 8 | dirty, SOT moved, otherwise | lock-conflict |

`!reachable || !autoSync` turns rows 0c/0e/3/4/5 into `nothing`, and a dirty section then reads
`pending`. "Sync now" is the caller passing `autoSync: true` once — not another code path.
This table **is** spec §4.6.1 with "absent" folded into `hash === null`: its six rows map to
2, 3, 4/5, 8, 1, and (absent) 3/4/8 respectively; the spec's table is updated to match in Task 0b.

`reduceSection` — every transition that carries a promise is a named test:

1. **Convergence is folded in the reducer**: after any event, if `isDirty && sotMoved &&
   sot.hash === currentHash` → `base = sot`, status `synced`. (The client-side mirror of the
   daemon's `applied:false`; it is how two machines making the same edit never see a lock.)
2. `remote-event`, `own: true` → ignored entirely. `own: false` → `sot = {rev, hash}` **only if
   `rev > sot.rev`**; on a dirty section nothing is applied, so the next decision is row 8
   (§4.6.2 rule 1). While `inFlight`, it additionally sets `sotMovedWhileInFlight` (rule 2). While
   `locked:conflict`, it still advances `sot` **and `conflict.sot`** — the lock refuses to *apply*,
   not to *know* — so a later keep-local targets the newest revision (review #6).
3. `sot-index` with `epoch !== s.epoch` → discarded, `indexStale = true` (review #3: a response
   that was in the air while we pushed or heard an event must not rewind what we know). With a
   matching epoch it is authoritative: `entry` → `sot = entry` **even if `entry.rev < sot.rev`**
   (that is the only way `lock-reset` is ever detected); `null` → `sot = {rev: max(sot.rev,
   base.rev), hash: null}`. Clears `indexStale`.
4. `push-started` → accepted only if `token.epoch === s.epoch` **and** no flight is open; then
   `inFlight = token` and the epoch bumps. Otherwise the state is returned unchanged.
   **Driver contract (P2b), stated here because the reducer is what makes it safe:** dispatch
   `push-started`, then send the request *only if* the resulting `inFlight === token`. A decision
   made before an event arrived thereby dies at the boundary instead of reaching the wire
   (review #4).
5. **Terminal events** — `push-applied`, `push-converged`, `push-conflict`, `push-failed` — each
   clear `inFlight` and `sotMovedWhileInFlight` and bump the epoch; arriving with no flight open
   they are ignored. One shared table-driven test asserts this for all four from every reachable
   pre-state (review #8: a section that keeps a flight open never syncs again).
   - `push-applied` → `base = {rev, inFlight.hash}`, `sot = base` if `rev > sot.rev`. If
     `currentHash` moved during the flight the section is simply dirty against the new base.
   - `push-converged` → **`base = {rev, inFlight.hash}`** — the hash that was *sent*, never
     `currentHash` (review #9: the SOT holds what we sent, not what we have typed since).
   - `push-conflict` → `sot = {rev, hash}` (rev 0 → `{max(sot.rev, base.rev), null}`), then rule 1
     may converge it; otherwise `status = 'locked:conflict'`,
     `conflict = {localHash: inFlight.hash, sot}` — **the sent snapshot**, not the live stores.
   - `push-failed` → back to dirty/pending; nothing else changes. The driver maps timeouts and
     malformed responses to it, so there is no path that leaves a flight open.
6. `pull-applied` → `base = sot = {rev, hash}` (if `rev >= sot.rev`), `currentHash = localHash`
   *(P2b-2: was `hash`)*,
   `forcePull = false`.
7. `resolved keep:'sot'` → unlock, `forcePull = true` (row 0e pulls even though the section is
   dirty — the user said so). `keep:'local'` → unlock, **`base = conflict.sot`** (the newest known,
   rule 2), `restoreLocal = conflict.localHash`. Row 0d then has the driver put the sent snapshot
   back; its `local-restored` sets `currentHash` and clears `restoreLocal`; the section is dirty
   against the SOT's current rev and row 4/5 pushes. If the SOT moves yet again before that push
   lands, the CAS answers 409 and the section locks again with the new pair — correct, and each
   round needs a human, so it cannot spin.
8. `retainedHashes` = `inFlight.hash`, `conflict.localHash`, `conflict.sot.hash`, `restoreLocal`
   (non-null ones). The core stores **no payloads**; this is how it tells the driver which ones
   must survive until the user has chosen (review #5). P2b's payload stash is keyed by hash and
   pruned to this set.

Property tests (table-driven over generated event sequences, seeded, no library needed):
`sot.rev` never decreases except through an epoch-matching `sot-index`; `inFlight` is never set in
a `locked:*` state; after any terminal event `inFlight === null`; `decideSection` never returns
`push`/`delete` when `sotMoved`; reducer never mutates (deep-frozen inputs).
Review sequences #1, #3, #4, #6 and #9 from spec §9.4 are each a regression test, verbatim.

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
  well-formedness step, per section): right top-level keys, **`order` has no duplicates and is exactly the key set of the record**
  (plan review #13 — a record entry missing from `order` would be applied and then dropped by the
  next build, so `build(apply(p))` would not hash to `p` and the section could never converge), every
  `Tab` has a layout whose leaves have `pane.id` and `content.kind`. A payload that fails is **not
  applied** and the caller locks the section — never a partial apply.

Tests, the ones that matter most:
- **Round trip, stated on hashes** (plan review #16 — `restoreSizes` puts `sizes` back, so the
  applied tree is never deep-equal to a stripped payload):
  `hash(build*(apply*(local, p))) === hash(p)` for every well-formed `p` and **any** `local`, for
  all four kinds; and `hash(build*(apply*(empty, build*(world)))) === hash(build*(world))`. This is
  the property that makes "converged" reachable at all. Device-local survival is asserted
  separately (below), not by deep equality.
- `restoreSizes`: same id + same arity → local ratios; same id, different arity → even; new id →
  even; nested splits; local `undefined`.
- focus is not mirrored: `activeWorkspaceId`, `activeTabId`, `activeHostId`, `devHostId` survive
  every apply when their target survives.
- `applySettings` leaves sentinel values in every unlisted field untouched.
- inputs never mutated (deep-freeze `local` and `incoming`).

## Task order

`0`/`0b` (main session) → `1` → `2` → **`3` ∥ `4`** → **`5` ∥ `6`** (plan review #14: 5 takes
`SectionSyncState` from 4; 6 needs 3 for the round trip). `types.ts` is created in Task 2 with every
shared type this plan names — payloads, sources, slices — so no later task edits it; a task that
needs another type declares it in its own module. Parallel tasks share one worktree, so each
**runs only its own test file** (`npx vitest run src/lib/profile/<name>`) and commits with
`git commit --only`; the main session runs the whole suite, lint and `tsc -p tsconfig.app.json`
between waves.

## As built — where the implementation deliberately departs from the text above

Recorded while the tasks ran; each was either an instruction from the main session or a
contradiction a subagent reported instead of resolving silently. Spec §9.5 has the same list.

- **`hash.ts`** builds the canonical string directly rather than `JSON.stringify` of a sorted copy:
  the device-state `sortKeysDeep` idiom is wrong twice — JS engines re-order integer-like keys
  (`"9"` before `"10"` whatever you insert), and writing `out['__proto__']` sets a prototype instead
  of a key. Also rejects cycles, sparse arrays, `undefined` in arrays, and non-plain objects, naming
  the path.
- **Settings types.** A store-state `interface` has no index signature, so it is not assignable to
  `Record<string, unknown>`. Builders and the applier take `SettingsBuildInput`
  (`Partial<Record<SettingsStorageKey, object>>`, declared in `sections.ts`); `types.ts`'s
  `SettingsSources` is unused by them. **`applySettings` returns `{patches}`** — per store, only
  listed fields whose value differs — so P2b is a plain `store.setState(patch)` and unlisted fields
  are untouched by construction. A listed field missing from a store that *is* present in the
  payload is patched to `undefined` (that is how "cleared" travels; builder and hash both drop
  `undefined`); a store missing altogether is ignored.
- **Builders pad empty records** (`{order: [], tabs: {}}` …) because `project()` contributes nothing
  for a `*` over an empty record; the applier produces the same shapes. `buildProfileDocument`
  returns `{document, standaloneTabIds}` and emits a `tabs.<id>` for **every** workspace, empty ones
  included. `hostOrder` and `Workspace.tabs` are filtered to ids that exist. An id equal to
  `__proto__` is dropped everywhere.
- **`sync-state.ts` decision order is 0a locked → 0b in flight → 0c restore-local → 0d reindex → 0e
  forcePull → rows 1–8** (the table above lists reindex before restore-local; restoring is local
  and must not wait for the network). Added to the state machine beyond the table:
  - events `locked` (accepted only while `decideSection` still returns that lock), `reconnected`
    (sets `indexStale`; this *is* reconcile-on-connect), and `restoreSectionState(persisted)`;
    a fresh or restored section is `indexStale`, so **nothing is decided before an index is seen**;
  - `restoreLocal` is `{hash: string | null} | null` — a sent *delete* that met a 409 must be
    restorable to "absent";
  - a 409 that arrives on a section that is **clean** (the user typed back to base mid-flight) does
    not lock — there is nothing local to lose, row 3 pulls;
  - `push-started` is validated against the token `decideSection` would produce *now*, which
    subsumes the epoch check and also `token.hash === currentHash`; `local-changed` bumps the epoch;
  - a 409 never lowers the known `sot.rev` (the Go side documents that its `Current` may be one
    revision stale); `pull-applied` always sets base and `currentHash` (the payload *is* in the
    stores) and only guards the `sot` update; `canApplyPull(s)` is what the driver asks first; a
    pull of an absent SOT reports `rev: state.sot.rev`;
  - the convergence fold wins over `lock-reset` (identical content leaves nothing to choose).
- **`profile-state.ts`**: `profileStatus` takes `{hasMaster, sections, lock}` and ranks
  `locked:reset` above `locked:conflict`. `reconcileSectionSet` takes **`previousWorkspaceIds`** and
  returns **`unknown`**: spec §4.6.3 said both "a `tabs.<id>` whose workspace is unknown is kept" and
  "after applying `workspaces`, delete the `tabs.*` of workspaces that are gone" — `remove` is only a
  workspace this client *knew* and the apply just took away; never-seen ones are `keepUnrendered`.
- **`applier.ts`**: every apply returns a result object (`next` plus what vanished / was added /
  `unrendered`). `isWellFormedSection` is stricter than planned — unknown keys at the top and entry
  level are rejected (they would not survive the next build, so the section could never converge),
  with the allowlist derived from `PROJECTIONS`, not written twice; `sizes` is rejected at any depth
  under `layout` (so no `PaneContent` may ever grow a field called `sizes`); `__proto__` /
  `constructor` / `prototype` as data keys are rejected; layout depth ≤ 64; the guard never throws.
  **Applying `tabs.<A>` can change `tabs.<B>`**: a tab that arrives in A while still listed in B is
  removed from B (a tab has one workspace, §4.3), so B turns dirty. Correct — the tab did move — and
  it converges once B's own section arrives; P2b must expect it.

### After the PR review (spec §9.6)

- `SectionSyncState.indexEpoch` — bumped by base / sot / flight changes and by every `reconnected`,
  never by a local edit; `reindex` carries it and `sot-index.epoch` is compared against it. `epoch`
  (the flight-token guard) is unchanged and still moves on every change.
- A `local-changed` cancels a pending `restoreLocal`; `local-restored` is accepted only for the
  pending hash; `canRestoreLocal(s, hash)` is exported for the driver.
- A `409 {rev: 0}` that arrives after a live SOT was learnt mid-flight is **ambiguous** (events and
  responses have no causal order) and is not resolved by guessing: close the flight, leave `sot`,
  do not lock, mark the index stale; the authoritative index decides. While the index is stale the
  convergence fold does not run. `sotMoved` is `sot.hash !== base.hash || (sot.hash !== null &&
  sot.rev > base.rev)` — an absent side has no revision.
- `isWellFormedSection('settings', …)`: only the ten known stores, only listed fields, at least one
  per store. `applySettings` returns `{patches, rejected}`; `rejected` non-empty ⇒ `patches` is `{}`.
- **For P2b**: the collector always passes all ten stores to `buildSettingsSection` — a store
  missing from a payload is "not sent", not "cleared", so a sender that omitted one would leave the
  receiver permanently dirty for it. Value *domains* (`tabPosition: 'garbage'`) are not the core's to
  judge; the shape check is a fail-safe, each store's own sanitiser is the real gate.

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
