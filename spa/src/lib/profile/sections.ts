// spa/src/lib/profile/sections.ts — the collector's pure half: store state in,
// section payloads out (Profile Sync spec §4.2, §4.3; P2a plan Task 3).
//
// Every builder does two things and nothing else: it SHAPES its input into the
// section's layout (array → `{order, record}`, `Workspace.tabs` → `{order, tabs}`)
// and then hands that to `project(shaped, PROJECTIONS[kind])`. The projection is
// the only allowlist — no builder decides what is synced, so a device-local field
// cannot leak by being carried along on a source object. The shaping step only
// decides WHICH records a section holds and in what order.
//
// No fetching, no clocks, no stores, no id generation — every input is a
// parameter (the new workspace's id included), and no input is ever mutated.
//
// HOST IDS TRAVEL AS WIRE IDS (host-sync-identity spec §2, §5, §11.3). `hosts`,
// `tabs.*` and `settings` name hosts; each of their builders translates local →
// wire through a `HostIdentity` (host-identity.ts) — for `hosts` computed from
// the very snapshot it builds, for the other two handed in by the caller, who
// takes it from the host store AT BUILD TIME (collector, apply-to-stores). An
// identity with a `conflict` builds nothing: the builders throw. The apply side
// resolves back through `wireResolverOf`.
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import {
  hostSettingsToWire,
  hostsToWire,
  identityOfSync,
  layoutToWire,
  makeWireResolver,
  mergeAliases,
  MAX_HOST_ALIASES,
  presetColumnsToWire,
  type HostIdentity,
  type WireResolver,
} from './host-identity'
import { PROJECTIONS, project, tabsSectionKey, workspaceIdOf } from './projections'
import type {
  HostsPayload,
  HostsSource,
  ProfileSectionKey,
  SectionPayload,
  SettingsPayload,
  SettingsStorageKey,
  StrippedLayout,
  TabsPayload,
  TabsSource,
  WorkspacesPayload,
  WorkspacesSource,
} from './types'

// === Inputs ===

/**
 * The settings stores' states, by storage key. `object`, not
 * `Record<string, unknown>`: a store's state is an interface, and an interface
 * has no index signature — so `useUISettingsStore.getState()` is assignable to
 * this without a cast (pinned in sections.test.ts). A store may be absent.
 */
export type SettingsBuildInput = Partial<Record<SettingsStorageKey, object>>

/** Everything a profile document is built from: the four store states, structurally. */
export interface CollectInput {
  hosts: HostsSource
  workspaces: WorkspacesSource
  tabs: TabsSource
  settings: SettingsBuildInput
}

/** A profile document. A tab that belongs to no workspace belongs to no section (§4.3); the app adopts it. */
export interface ProfileDocumentResult {
  document: Record<ProfileSectionKey, SectionPayload>
}

// A record key that cannot travel: `project` never copies it, and assigning it
// on a plain object would rewrite the prototype instead of adding a key.
const FORBIDDEN_KEY = '__proto__'

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** `ids` without duplicates (first occurrence wins), restricted to ids `has` accepts. */
function uniqueKnown(ids: readonly string[], has: (id: string) => boolean): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const id of ids) {
    if (id === FORBIDDEN_KEY || seen.has(id) || !has(id)) continue
    seen.add(id)
    out.push(id)
  }
  return out
}

// === Layout ===

/**
 * The same tree without any split's `sizes`: structure travels, ratios stay
 * device-local (decision 8). Split nodes are new objects; leaves' panes are
 * shared with the input, which is never mutated.
 */
export function stripSizes(layout: PaneLayout): StrippedLayout {
  if (layout.type === 'leaf') return { type: 'leaf', pane: layout.pane }
  return { type: 'split', id: layout.id, direction: layout.direction, children: layout.children.map(stripSizes) }
}

// === Host identity ===

/** Thrown by a builder handed an identity with a conflict: a payload that names hosts would be ambiguous. */
function refuseConflict(identity: HostIdentity | undefined): void {
  if (identity !== undefined && identity.conflict !== null) {
    throw new Error(`profile: host identity conflict (${identity.conflict.join(', ')}): nothing that names hosts is built`)
  }
}

/**
 * wire → local for `tabs.*` / `settings`, from the host store as it is NOW —
 * after the `hosts` apply (spec §6, §11.2): a sync id → the local host of that
 * daemon; a legacy id → the host whose canonical row lists it in `aliases`
 * (persisted as `HostConfig.syncAliases`, so this survives a restart and an
 * interrupted transition); anything else unchanged (the existing unknown-host
 * handling). `null` under an identity conflict: nothing may be applied.
 * The rows are this device's own wire build of its hosts, which after a
 * `hosts` apply are the applied rows — `matched` is not needed.
 */
export function wireResolverOf(s: HostsSource, identity: HostIdentity = identityOfSync(s.hosts)): WireResolver | null {
  if (identity.conflict !== null) return null
  return makeWireResolver({ identity, rows: buildHostsSection(s, identity).hosts })
}

// === Section builders ===

/**
 * `hosts`: each host's projected fields (`token: null` survives — it means
 * "cleared"), and the order. `hostOrder` is restricted to hosts that exist (an id
 * with no host is dropped — `reorderHosts` does not check ids — and a repeat is
 * kept once), because the applier's well-formedness guard rejects a section whose
 * order names an unknown host, and this client's hosts would then never sync. A
 * host the order never mentions is NOT added: the guard allows that state, and
 * the builder reflects the store rather than repairing it.
 */
export function buildHostsSection(s: HostsSource, identity: HostIdentity = identityOfSync(s.hosts)): HostsPayload {
  refuseConflict(identity)
  const shaped = { hosts: s.hosts, hostOrder: uniqueKnown(s.hostOrder, (id) => Object.hasOwn(s.hosts, id)) }
  // An empty record matches no `hosts.*` path; the key must exist all the same.
  const local = { hosts: {}, ...(project(shaped, PROJECTIONS.hosts) as object) } as HostsPayload
  // `aliases` is a WIRE field (the projection lists it for the guard and the fingerprint): only `syncAliases`,
  // through `hostsToWire`, may put it on a row. A local host carrying a field of that name does not send it.
  for (const row of Object.values(local.hosts)) delete (row as { aliases?: unknown }).aliases
  const aliasesOf = (id: string): readonly unknown[] => withOwnAlias(Object.hasOwn(s.hosts, id) ? s.hosts[id].syncAliases : undefined, id)
  return hostsToWire(local, identity, aliasesOf)
}

/**
 * A canonical row's `aliases`: the SORTED unique union of the remembered ones (`syncAliases`) and the host's OWN
 * local id — its wire key in the ordinal-2 era, so a legacy `tabs.*` / `settings` this device wrote resolves on
 * every other device even when this device went canonical by PUSHING (spec §11.7; it never matched a legacy row) —
 * the first MAX_HOST_ALIASES kept.
 *
 * Sorted, not "most recent": a rule every client computes identically has a fixed point however many devices
 * share the daemon (A1, PR #1365 — with "own id in, oldest out" 17 devices would displace each other for ever).
 * The price: a device whose own id sorts past the 16th is simply not listed — its build then equals the row it
 * received and nothing is pushed — and during a transition its legacy keys stay unresolved on the others
 * (their panes read as a removed host there), which is accepted. A stale id (a device gone) holds its place
 * until 16 smaller ids exist. `hostsToWire` only adds this on a canonical row (a no-claim host's own id IS its key).
 */
function withOwnAlias(syncAliases: unknown, own: string): string[] {
  const union = new Set([...mergeAliases(syncAliases, []), ...mergeAliases([], [own])])
  return [...union].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0)).slice(0, MAX_HOST_ALIASES)
}

/**
 * Can this workspace id name a `tabs.<id>` section? (The daemon's key charset;
 * `tabsSectionKey` throws on the same ids — this asks without throwing.)
 * `importWorkspace` accepts any id, so the answer can be no.
 */
export function isSyncableWorkspaceId(id: string): boolean {
  return workspaceIdOf(`tabs.${id}`) !== null
}

/** The ids `buildWorkspacesSection` leaves out, each once, in list order — for the collector to report. */
export function unsyncableWorkspaceIds(workspaces: readonly Workspace[]): string[] {
  return [...new Set(workspaces.map((ws) => ws.id))].filter((id) => !isSyncableWorkspaceId(id))
}

/**
 * `workspaces`: `Workspace[]` → order + record. A repeated id is kept once, first
 * occurrence. A workspace whose id cannot form a `tabs.<id>` key is LEFT OUT —
 * device-local as a whole, like its tabs, which can never have a section. Sending
 * it would poison the profile: the daemon validates section keys, not payloads,
 * so it would be stored, and every other client's well-formedness guard refuses
 * a `workspaces` payload listing such an id (`locked:invalid`), for good.
 * `applyWorkspaces` is the other half: it never deletes such a workspace.
 */
export function buildWorkspacesSection(workspaces: readonly Workspace[]): WorkspacesPayload {
  const byId = new Map<string, Workspace>()
  for (const ws of workspaces) {
    if (!byId.has(ws.id)) byId.set(ws.id, ws)
  }
  const order = uniqueKnown([...byId.keys()], isSyncableWorkspaceId)
  const record: Record<string, Workspace> = {}
  for (const id of order) record[id] = byId.get(id) as Workspace
  // `{order: []}` alone would project to `{order: []}`; the record key must exist even when empty.
  return { workspaces: {}, ...(project({ order, workspaces: record }, PROJECTIONS.workspaces) as object) } as WorkspacesPayload
}

/**
 * `tabs.<ws.id>`: `order` is `ws.tabs` restricted to tabs that exist (an id with
 * no `Tab` is dropped, never invented; a repeat is kept once), and the record
 * holds exactly those — so `order` has no duplicates and equals the record's key
 * set, which is what the applier's well-formedness guard demands.
 */
export function buildTabsSection(ws: Workspace, tabs: Record<string, Tab>, identity?: HostIdentity): TabsPayload {
  refuseConflict(identity)
  const order = uniqueKnown(ws.tabs, (id) => Object.hasOwn(tabs, id))
  const record: Record<string, unknown> = {}
  for (const id of order) {
    const stripped = stripSizes(tabs[id].layout)
    record[id] = { ...tabs[id], layout: identity === undefined ? stripped : layoutToWire(stripped, identity) }
  }
  return { tabs: {}, ...(project({ order, tabs: record }, PROJECTIONS.tabs) as object) } as TabsPayload
}

/**
 * The one synced settings field whose record is keyed by WORKSPACE id
 * (`wsId → moduleId → payload`, useWorkspaceSettingsStore). Named once, here:
 * the builder filters it and `applySettings` scopes it, by the same constant.
 */
export const WORKSPACE_SCOPED_SETTINGS: { storageKey: SettingsStorageKey; field: string } = {
  storageKey: 'purdex-workspace-settings',
  field: 'workspaces',
}

/**
 * `settings`: `{<storageKey>: {<listed fields>}}`. A store that is absent, or contributes no listed field, has no key.
 *
 * The workspace-scoped record carries the entries of `masterWorkspaceIds` ONLY —
 * the workspaces the `workspaces` section of this profile holds. Anything else
 * in the store is not the profile's: an orphan (its workspace is gone and nothing
 * cleared the entry), a workspace whose id cannot sync, or a workspace of a
 * local profile that must never reach the SOT. The set is a PARAMETER, not
 * "whatever the workspace store holds": what is on screen need not be the
 * master's world. Filtered down to nothing the field is `{}`, exactly what a
 * store with no entry builds. `applySettings` is the other half.
 */
export function buildSettingsSection(stores: SettingsBuildInput, masterWorkspaceIds: ReadonlySet<string>, identity?: HostIdentity): SettingsPayload {
  refuseConflict(identity)
  const payload = project(stores, PROJECTIONS.settings) as SettingsPayload
  if (identity !== undefined) {
    // `project` returns fresh structure: replacing its members touches no store.
    const hostSettings = payload['purdex-host-settings']
    if (hostSettings !== undefined && isRecord(hostSettings.hosts)) hostSettings.hosts = hostSettingsToWire(hostSettings.hosts, identity)
    const newtab = payload['purdex-newtab-layout']
    if (newtab !== undefined && isRecord(newtab.presets)) newtab.presets = presetColumnsToWire(newtab.presets, identity)
  }
  const scoped = payload[WORKSPACE_SCOPED_SETTINGS.storageKey]?.[WORKSPACE_SCOPED_SETTINGS.field]
  // `project` returns fresh structure, so deleting from it touches no store.
  if (typeof scoped === 'object' && scoped !== null && !Array.isArray(scoped)) {
    for (const id of Object.keys(scoped)) if (!masterWorkspaceIds.has(id)) delete (scoped as Record<string, unknown>)[id]
  }
  return payload
}

// === Document ===

/** Tab ids that are in no workspace: `tabOrder` order first, then any tab `tabOrder` does not mention. */
function standaloneIds(workspaces: readonly Workspace[], tabs: Record<string, Tab>, tabOrder: readonly string[]): string[] {
  const owned = new Set(workspaces.flatMap((ws) => ws.tabs))
  return uniqueKnown([...tabOrder, ...Object.keys(tabs)], (id) => Object.hasOwn(tabs, id) && !owned.has(id))
}

/**
 * The whole document: `hosts`, `settings`, `workspaces`, and one `tabs.<id>` for
 * EVERY workspace in the `workspaces` section — an empty workspace still gets
 * `{order: [], tabs: {}}`, because `workspaces` is the authority on which
 * `tabs.*` exist (§4.6.3). A workspace whose id the daemon would reject is in
 * neither (see `buildWorkspacesSection`). A tab in no workspace enters no section.
 */
export function buildProfileDocument(input: CollectInput): ProfileDocumentResult {
  const { workspaces } = input.workspaces
  const workspacesPayload = buildWorkspacesSection(workspaces)
  const identity = identityOfSync(input.hosts.hosts) // ONE identity for every section of the document
  const document: Record<ProfileSectionKey, SectionPayload> = {
    hosts: buildHostsSection(input.hosts, identity),
    // The master's workspaces ARE the ones this document's `workspaces` section lists.
    settings: buildSettingsSection(input.settings, new Set(workspacesPayload.order), identity),
    workspaces: workspacesPayload,
  }
  for (const id of workspacesPayload.order) {
    const ws = workspaces.find((w) => w.id === id) as Workspace
    document[tabsSectionKey(id)] = buildTabsSection(ws, input.tabs.tabs, identity)
  }
  return { document }
}

// === Standalone tabs (§4.3) ===

/**
 * Moves every standalone tab into the workspace whose ID is `newWorkspaceId`
 * (whatever it is called: it may have been made on a device that speaks another
 * language, and an existing one is never renamed), else into the first workspace
 * NAMED `unsortedName`, else into a new one with that id and that name (shaped
 * like `createWorkspace`'s result) — created only when there is something to
 * adopt. `activeTabId` is left alone: focus is the device's business.
 */
export function adoptStandaloneTabs(
  world: { workspaces: readonly Workspace[]; tabs: Record<string, Tab>; tabOrder: readonly string[] },
  opts: { unsortedName: string; newWorkspaceId: string; only?: ReadonlySet<string> },
): { workspaces: Workspace[]; adopted: string[]; createdWorkspaceId: string | null } {
  const only = opts.only
  const adopted = standaloneIds(world.workspaces, world.tabs, world.tabOrder).filter((id) => only === undefined || only.has(id))
  if (adopted.length === 0) return { workspaces: [...world.workspaces], adopted, createdWorkspaceId: null }

  const byId = world.workspaces.findIndex((ws) => ws.id === opts.newWorkspaceId)
  const target = byId !== -1 ? byId : world.workspaces.findIndex((ws) => ws.name === opts.unsortedName)
  if (target === -1) {
    const created: Workspace = { id: opts.newWorkspaceId, name: opts.unsortedName, tabs: adopted, activeTabId: null, moduleConfig: {} }
    return { workspaces: [...world.workspaces, created], adopted: [...adopted], createdWorkspaceId: created.id }
  }
  const workspaces = world.workspaces.map((ws, i) => (i === target ? { ...ws, tabs: [...ws.tabs, ...adopted] } : ws))
  return { workspaces, adopted, createdWorkspaceId: null }
}

/** Every tab id once: the first workspace (in workspace order) that lists it keeps it, at its first position. */
function dropExtraOwners(workspaces: readonly Workspace[], only?: ReadonlySet<string>): { workspaces: readonly Workspace[]; dropped: number } {
  const seen = new Set<string>()
  let dropped = 0
  const next = workspaces.map((ws) => {
    const tabs: string[] = []
    for (const id of ws.tabs) {
      if (seen.has(id) && (only === undefined || only.has(id))) continue
      seen.add(id)
      tabs.push(id)
    }
    if (tabs.length === ws.tabs.length) return ws
    dropped += ws.tabs.length - tabs.length
    return { ...ws, tabs, activeTabId: ws.activeTabId !== null && tabs.includes(ws.activeTabId) ? ws.activeTabId : null }
  })
  return { workspaces: dropped === 0 ? workspaces : next, dropped }
}

/** One tab world, as much of it as ownership is about. `tabOrder` only orders what is adopted; `[]` will do. */
export interface OwnershipWorld {
  workspaces: readonly Workspace[]
  tabs: Record<string, Tab>
  tabOrder: readonly string[]
  activeTabId: string | null
  activeWorkspaceId: string | null
}

/**
 * EVERY TAB BELONGS TO EXACTLY ONE WORKSPACE, as a pure function of one tab world: several owners → the first
 * workspace in workspace order keeps the tab (a workspace that loses its active tab gets `activeTabId: null`);
 * zero owners → `adoptStandaloneTabs`. A rule with no choice in it, so whoever applies it to the same world gets
 * the same world. ONE copy, two callers: the standing invariant on the world on screen
 * (features/workspace/lib/adopt-standalone.ts, after its wait) and switch-active.ts, on a world it is about to
 * park or to copy — a parked world is one nobody repairs.
 *
 * THE POINTER. `null` with workspaces ("Home", which is gone) → the workspace of the tab on screen, else the
 * first. Otherwise it FOLLOWS THE TAB ON SCREEN ONLY WHEN THIS REPAIR MOVED THAT TAB: it was adopted (a click
 * can focus a tab before it has a workspace), or the pointed-at workspace is the one that lost its listing of
 * it — or the tab on screen would be in no bar, for good. Never "align the pointer with the active tab's
 * owner": a user who looks at B's bar while a tab of A is on screen chose that.
 *
 * `opts.only` — repair THESE tab ids and leave every other ownerless or twice-listed tab as it is (the
 * invariant's bounded way out: only what has been broken long enough). Absent = all of them.
 */
export function repairTabOwnership(
  world: OwnershipWorld,
  opts: { unsortedName: string; newWorkspaceId: string; only?: ReadonlySet<string> },
): { workspaces: Workspace[]; activeWorkspaceId: string | null; adopted: string[]; dropped: number; membershipChanged: boolean } {
  const deduped = dropExtraOwners(world.workspaces, opts.only)
  const { workspaces, adopted } = adoptStandaloneTabs({ ...world, workspaces: deduped.workspaces }, opts)
  const { activeTabId } = world
  const owner = activeTabId === null ? undefined : workspaces.find((ws) => ws.tabs.includes(activeTabId))

  let activeWorkspaceId = world.activeWorkspaceId ?? (owner ?? workspaces[0])?.id ?? null
  if (activeTabId !== null && owner !== undefined) {
    const pointed = world.workspaces.find((ws) => ws.id === world.activeWorkspaceId)
    const lostByPointed = pointed !== undefined && pointed.tabs.includes(activeTabId) && owner.id !== pointed.id
    if (adopted.includes(activeTabId) || lostByPointed) activeWorkspaceId = owner.id
  }
  return { workspaces, activeWorkspaceId, adopted, dropped: deduped.dropped, membershipChanged: adopted.length > 0 || deduped.dropped > 0 }
}
