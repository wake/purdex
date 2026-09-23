// spa/src/lib/profile/applier.ts — the applier's pure half: `(local, incoming) →
// next`, one function per section kind (Profile Sync spec §4.7; P2a plan Task 6).
//
// These are the inverse of the builders in sections.ts, and the property that
// ties the two together is stated on hashes:
//
//     hash(build(apply(local, p))) === hash(p)     for every well-formed p
//
// Without it a section could never be "converged": every apply would leave the
// client dirty again. So an apply REPLACES the synced fields with the incoming
// ones (never merges them), keeps what is device-local (focus, split ratios,
// unlisted settings fields), and `isWellFormedSection` refuses any payload the
// builders could not have produced — unknown fields included, since a field
// that does not survive the next build can never hash back to `p`.
//
// No fetching, no clocks, no stores, no id generation. Nothing is mutated;
// results may share structure with `local` and `incoming`.
import type { HostConfig } from '../../stores/useHostStore'
import { isValidDaemonId } from '../daemon-id'
import type { PaneLayout, SplitLayout, Tab, Workspace } from '../../types/tab'
import { structuralKey } from './hash'
import {
  HOST_BEARING_COLUMN_PREFIXES,
  MAX_HOST_ALIASES,
  NEW_HOST,
  SYNC_ID_PREFIX,
  hostSettingsFromWire,
  hostsFromWire,
  isSyncId,
  layoutFromWire,
  matchIncomingHosts,
  mergeAliases,
  presetColumnsFromWire,
  syncIdOfSync,
  type HostMatchError,
  type WireHostsPayload,
  type WireResolver,
} from './host-identity'
import { PROJECTIONS, workspaceIdOf } from './projections'
import { WORKSPACE_SCOPED_SETTINGS, isSyncableWorkspaceId } from './sections'
import type { SettingsBuildInput } from './sections'
import type {
  HostsPayload,
  HostsSlice,
  SectionKind,
  SettingsPayload,
  SettingsStorageKey,
  StrippedLayout,
  TabsPayload,
  TabsSlice,
  WorkspaceEntry,
  WorkspacesPayload,
  WorkspacesSlice,
} from './types'

// === Results ===

export interface ApplyHostsResult {
  next: HostsSlice
  /** Hosts that existed locally and are not in the payload — P2b marks their panes (`markHostRemovedPanes`). */
  removedHostIds: string[]
}

export interface ApplyWorkspacesResult {
  next: WorkspacesSlice
  /** Workspaces gained: their `tabs.<id>` section is to be created (§4.6.3). */
  addedWorkspaceIds: string[]
  /** Workspaces lost: their `tabs.<id>` section is to be deleted (§4.6.3). */
  removedWorkspaceIds: string[]
}

export interface ApplyTabsResult {
  next: TabsSlice
  /** The workspace is not known locally: nothing was applied, the section is kept and not rendered (§4.6.3). */
  unrendered: boolean
  /** Tabs of this workspace that the payload no longer holds, and that were removed from the record. */
  removedTabIds: string[]
}

/** Per store, the fields to merge into its state (`store.setState(patch)`). A store with nothing to change is absent. */
export type SettingsPatches = Partial<Record<SettingsStorageKey, Record<string, unknown>>>

export interface ApplySettingsResult {
  /** Empty whenever `rejected` is not: a settings payload is applied whole or not at all. */
  patches: SettingsPatches
  /** `'<storageKey>.<field>'` of every incoming value whose shape differs from the local one; sorted, no duplicates. Non-empty → the caller locks the section. */
  rejected: string[]
}

// === Helpers ===

type Rec = Record<string, unknown>

// Never written as a record key: on a plain object it would set the prototype.
const PROTO_KEY = '__proto__'

function unique(ids: readonly string[]): string[] {
  return [...new Set(ids)]
}

// === hosts ===

/**
 * Replaces `hosts` + `hostOrder`; the device's `activeHostId` / `devHostId` survive while their host does.
 *
 * Hosts ordinal 1 → 2 added `daemonId` (host-daemon-id D6). An incoming host
 * WITHOUT one (an ordinal-1 writer, or a host nobody has verified yet) keeps the
 * local host's `daemonId` — the only local field that does — but ONLY for the
 * same incarnation as far as the address can tell: same id AND same ip + port.
 * Another address is a re-point (an ordinal-2 client clears `daemonId` exactly
 * by re-pointing; an ordinal-1 client re-points without knowing the field) or a
 * host deleted and recreated under the same id: the local claim belongs to
 * another daemon and is dropped. (The row's ordinal is not an input here — the
 * address rule covers both writers.) One that carries `daemonId` wins (SOT
 * wins, D2). The upcast state then builds a
 * payload that differs from the one pulled, and that difference is the one
 * upgrade push. The runtime `daemonIdMismatch` lives outside `hosts` and is
 * never touched here.
 */
export function applyHosts(local: HostsSlice, incoming: HostsPayload): ApplyHostsResult {
  const hosts: Record<string, HostConfig> = {}
  for (const id of Object.keys(incoming.hosts)) {
    if (id === PROTO_KEY) continue
    const h = incoming.hosts[id]
    const mine = Object.hasOwn(local.hosts, id) ? local.hosts[id] : undefined
    const sameIncarnation = mine !== undefined && mine.ip === h.ip && mine.port === h.port
    const kept = h.daemonId === undefined && sameIncarnation ? mine.daemonId : undefined
    hosts[id] = kept ? { ...h, daemonId: kept } : h
  }
  const survives = (id: string | null): string | null => (id !== null && Object.hasOwn(hosts, id) ? id : null)
  return {
    next: {
      hosts,
      hostOrder: [...incoming.hostOrder],
      activeHostId: survives(local.activeHostId),
      devHostId: survives(local.devHostId),
    },
    removedHostIds: Object.keys(local.hosts).filter((id) => !Object.hasOwn(hosts, id)),
  }
}

// === hosts: wire → local (host-sync-identity spec §6, §11.5/§11.6) ===

export interface HostsPlan {
  /** The incoming payload in LOCAL ids — what `applyHosts` takes. */
  payload: HostsPayload
  /** Per local id, the `syncAliases` that host must hold after the apply; a host absent here holds none. */
  aliases: Record<string, string[]>
  /** Incoming row key → the local id it lands on (created ones included; never `NEW_HOST`). */
  byRow: Map<string, string>
  /** Local ids this apply creates, in row order. */
  created: string[]
}

/** `record[key] = value` as an own data property (a wire key may be anything the guard let through). */
function setOwn<T>(record: Record<string, T>, key: string, value: T): void {
  Object.defineProperty(record, key, { value, enumerable: true, writable: true, configurable: true })
}

/**
 * How an incoming `hosts` payload (WIRE ids) lands on this device's hosts.
 *   - D6 FIRST: a row under a legacy key with no `daemonId` whose local host of
 *     that id holds a claim at the SAME ip + port is that host — the claim is put
 *     on the row before matching (`applyHosts`' ordinal-1 upcast, moved in front
 *     of the matcher, whose §11.6 rule would otherwise refuse the local-id match
 *     and recreate the host — cascading its panes to `host-removed`).
 *   - `matchIncomingHosts` (codec): daemonId, then sync id, then legacy local id.
 *   - A row nobody matches is CREATED: under its own key when that key is a legacy
 *     local id not taken here (a host without a claim has no other wire identity
 *     — under a random id its next build would name another host, and the two
 *     devices would recreate it from each other for ever), else under `newId()`.
 *   - `aliases`: a canonical row's own (the SOT wins); a LEGACY row carrying a
 *     `daemonId` adds its key to the matched host's aliases (the next build is
 *     canonical, and other devices resolve that legacy key through it).
 * Pure: `newId` is the only source of new ids; nothing is mutated.
 */
export function planHostsApply(
  localHosts: Record<string, HostConfig>,
  incoming: HostsPayload,
  newId: () => string,
): { plan: HostsPlan } | { error: HostMatchError } {
  const rows: Record<string, HostConfig> = {}
  for (const [key, row] of Object.entries(incoming.hosts)) {
    const mine = !isSyncId(key) && Object.hasOwn(localHosts, key) ? localHosts[key] : undefined
    const upcast = row.daemonId === undefined && mine !== undefined && isValidDaemonId(mine.daemonId) && mine.ip === row.ip && mine.port === row.port
    setOwn(rows, key, upcast ? { ...row, daemonId: mine.daemonId } : row)
  }

  const match = matchIncomingHosts(localHosts, rows)
  if (match.error !== undefined) return { error: match.error }

  const taken = new Set(Object.keys(localHosts))
  const byRow = new Map<string, string>()
  const created: string[] = []
  for (const [key, local] of match.byRow) {
    if (local !== NEW_HOST) {
      byRow.set(key, local)
      continue
    }
    let id = !isSyncId(key) && key !== PROTO_KEY && key !== NEW_HOST && !taken.has(key) ? key : newId()
    while (taken.has(id) || id === NEW_HOST || isSyncId(id)) id = newId()
    taken.add(id)
    created.push(id)
    byRow.set(key, id)
  }

  const resolve: WireResolver = (key) => byRow.get(key) ?? key
  const { hosts: payload, aliasesByLocal } = hostsFromWire({ hosts: rows, hostOrder: incoming.hostOrder } as WireHostsPayload, resolve)
  const aliases: Record<string, string[]> = { ...aliasesByLocal }
  for (const [key, row] of Object.entries(rows)) {
    if (isSyncId(key) || !isValidDaemonId(row.daemonId)) continue
    const local = byRow.get(key) as string
    const before = created.includes(local) ? [] : localHosts[local]?.syncAliases
    const merged = mergeAliases(aliases[local] ?? before, [key])
    if (merged.length > 0) aliases[local] = merged
  }
  return { plan: { payload, aliases, byRow, created } }
}

/** wire → local over every tab of a `tabs.<ws>` payload (host-identity `layoutFromWire`). Input untouched. */
export function tabsFromWire(payload: TabsPayload, resolve: WireResolver): TabsPayload {
  const tabs: Record<string, TabsPayload['tabs'][string]> = {}
  for (const [id, entry] of Object.entries(payload.tabs)) {
    setOwn(tabs, id, { ...entry, layout: layoutFromWire(entry.layout, resolve) })
  }
  return { ...payload, tabs }
}

/** The host id of a host-bearing New Tab column (`sessions:<id>` / `headless:<id>`), or null. */
function columnHost(id: unknown): string | null {
  if (typeof id !== 'string') return null
  const colon = id.indexOf(':')
  if (colon < 0 || colon === id.length - 1) return null
  return (HOST_BEARING_COLUMN_PREFIXES as readonly string[]).includes(id.slice(0, colon)) ? id.slice(colon + 1) : null
}

/**
 * wire → local for `settings`: the `purdex-host-settings.hosts` keys and the
 * host-bearing New Tab columns. A host-bearing column whose host is not LIVE here
 * after that (`liveHostIds`: in `hostOrder` and in `hosts` — exactly the per-host
 * providers useNewTabBootstrap keeps) is LEFT OUT of the applied presets
 * (host-sync-identity PR-1 note (b)): in the store it would be pruned by the
 * bootstrap at some later render, which then pushes the settings back without it
 * — the same end, but racing the apply's own hash. Left out here, the apply's
 * hash says so at once (`pull-hash-mismatch`) and ONE push drops it from the SOT,
 * as this device's prune always did for a host it does not have. An unknown
 * host-settings key is kept as is (nothing prunes it; it round-trips). Input untouched.
 */
export function settingsFromWire(payload: SettingsPayload, resolve: WireResolver, liveHostIds: ReadonlySet<string>): SettingsPayload {
  const out: SettingsPayload = { ...payload }
  const hostSettings = payload['purdex-host-settings']
  if (isPlainObject(hostSettings) && isPlainObject(hostSettings.hosts)) {
    out['purdex-host-settings'] = { ...hostSettings, hosts: hostSettingsFromWire(hostSettings.hosts, resolve) }
  }
  const newtab = payload['purdex-newtab-layout']
  if (isPlainObject(newtab) && isPlainObject(newtab.presets)) {
    const resolved = presetColumnsFromWire(newtab.presets, resolve) as Rec
    const presets: Rec = {}
    for (const [key, preset] of Object.entries(resolved)) {
      if (!isPlainObject(preset) || !Array.isArray(preset.columns)) {
        setOwn(presets, key, preset)
        continue
      }
      const columns = preset.columns.map((col: unknown) =>
        Array.isArray(col) ? col.filter((id) => { const h = columnHost(id); return h === null || liveHostIds.has(h) }) : col,
      )
      setOwn<unknown>(presets, key, { ...preset, columns })
    }
    out['purdex-newtab-layout'] = { ...newtab, presets }
  }
  return out
}

// === workspaces ===

/** A `Workspace` made of the entry's synced fields and nothing else synced — an optional field the entry lacks gets NO key. */
function workspaceFrom(id: string, entry: WorkspaceEntry, local: Workspace | undefined): Workspace {
  const out: Workspace = { id, name: entry.name, tabs: local ? local.tabs : [], activeTabId: local ? local.activeTabId : null }
  if (entry.icon !== undefined) out.icon = entry.icon
  if (entry.iconWeight !== undefined) out.iconWeight = entry.iconWeight
  if (entry.moduleConfig !== undefined) out.moduleConfig = entry.moduleConfig
  return out
}

/**
 * `{order, record}` → `Workspace[]` in `order`. `tabs` and `activeTabId` of a
 * workspace that already exists come from local (they belong to `tabs.<ws>` and
 * to the device); a new workspace arrives empty — the defined partial state of
 * §4.6.3.
 *
 * A local workspace whose id cannot form a `tabs.<id>` key is device-local
 * (`buildWorkspacesSection` never sends it), so no payload can mean "delete it":
 * those are kept, after the incoming order, in their own relative order, and are
 * never reported as removed. The round trip holds because the builder filters
 * them out again.
 */
export function applyWorkspaces(local: WorkspacesSlice, incoming: WorkspacesPayload): ApplyWorkspacesResult {
  const localById = new Map<string, Workspace>()
  for (const w of local.workspaces) {
    if (!localById.has(w.id)) localById.set(w.id, w)
  }
  const order = unique(incoming.order).filter((id) => id !== PROTO_KEY && Object.hasOwn(incoming.workspaces, id))
  const deviceLocal = [...localById.values()].filter((w) => !isSyncableWorkspaceId(w.id))
  const workspaces = [...order.map((id) => workspaceFrom(id, incoming.workspaces[id], localById.get(id))), ...deviceLocal]
  const kept = new Set(workspaces.map((w) => w.id))
  const active = local.activeWorkspaceId !== null && kept.has(local.activeWorkspaceId) ? local.activeWorkspaceId : (workspaces[0]?.id ?? null)
  return {
    next: { workspaces, activeWorkspaceId: active },
    addedWorkspaceIds: order.filter((id) => !localById.has(id)),
    removedWorkspaceIds: [...localById.keys()].filter((id) => !kept.has(id)),
  }
}

// === tabs.<ws> ===

function isValidSizes(sizes: unknown, arity: number): sizes is number[] {
  return Array.isArray(sizes) && sizes.length === arity && sizes.every((s) => typeof s === 'number' && Number.isFinite(s) && s > 0)
}

/** Every split of a tree by id; the first occurrence of a repeated id wins. */
function indexSplits(layout: PaneLayout | undefined, into: Map<string, SplitLayout>): Map<string, SplitLayout> {
  if (layout === undefined || layout.type !== 'split') return into
  if (!into.has(layout.id)) into.set(layout.id, layout)
  for (const child of layout.children) indexSplits(child, into)
  return into
}

function withSizes(incoming: StrippedLayout, localSplits: Map<string, SplitLayout>): PaneLayout {
  if (incoming.type === 'leaf') return { type: 'leaf', pane: incoming.pane }
  const n = incoming.children.length
  const match = localSplits.get(incoming.id)
  // Same id AND same arity: a split that gained or lost a child has no ratios worth keeping.
  const sizes = match && match.children.length === n && isValidSizes(match.sizes, n) ? [...match.sizes] : Array.from({ length: n }, () => 100 / n)
  return { type: 'split', id: incoming.id, direction: incoming.direction, children: incoming.children.map((c) => withSizes(c, localSplits)), sizes }
}

/**
 * Puts split ratios back on a stripped layout (decision 8): a split keeps the
 * LOCAL `sizes` of the split with the same id and child count, wherever that
 * sits in the local tree; any other split is distributed evenly. Every split of
 * the result has `sizes.length === children.length`.
 */
export function restoreSizes(incoming: StrippedLayout, local: PaneLayout | undefined): PaneLayout {
  return withSizes(incoming, indexSplits(local, new Map()))
}

/** `ws` without the tabs in `taken`; the same object when it holds none of them. */
function withoutTabs(ws: Workspace, taken: ReadonlySet<string>): Workspace {
  if (!ws.tabs.some((id) => taken.has(id))) return ws
  const tabs = ws.tabs.filter((id) => !taken.has(id))
  const activeTabId = ws.activeTabId !== null && tabs.includes(ws.activeTabId) ? ws.activeTabId : (tabs[0] ?? null)
  return { ...ws, tabs, activeTabId }
}

/**
 * Replaces one workspace's tabs and order. A tab the payload brings that
 * currently sits in ANOTHER workspace is taken out of it — a tab belongs to
 * exactly one workspace (§4.3), and that workspace's own section may not have
 * been applied yet. Everything else of the other workspaces is returned by
 * reference.
 */
export function applyTabs(local: TabsSlice, workspaceId: string, incoming: TabsPayload): ApplyTabsResult {
  const target = local.workspaces.find((w) => w.id === workspaceId)
  if (target === undefined) return { next: local, unrendered: true, removedTabIds: [] }

  const order = unique(incoming.order).filter((id) => id !== PROTO_KEY && Object.hasOwn(incoming.tabs, id))
  const arriving = new Set(order)
  const removedTabIds = unique(target.tabs).filter((id) => !arriving.has(id) && Object.hasOwn(local.tabs, id))
  const removed = new Set(removedTabIds)

  const tabs: Record<string, Tab> = {}
  for (const id of Object.keys(local.tabs)) {
    if (!removed.has(id) && id !== PROTO_KEY) tabs[id] = local.tabs[id]
  }
  for (const id of order) {
    const entry = incoming.tabs[id]
    const localTab = Object.hasOwn(local.tabs, id) ? local.tabs[id] : undefined
    tabs[id] = {
      id: entry.id,
      pinned: entry.pinned,
      locked: entry.locked,
      createdAt: entry.createdAt,
      layout: restoreSizes(entry.layout, localTab?.layout),
    }
  }

  const activeTabId = target.activeTabId !== null && arriving.has(target.activeTabId) ? target.activeTabId : (order[0] ?? null)
  const workspaces = local.workspaces.map((w) => (w === target ? { ...w, tabs: order, activeTabId } : withoutTabs(w, arriving)))
  return { next: { tabs, workspaces }, unrendered: false, removedTabIds }
}

/**
 * `tabOrder`, rebuilt from the workspaces (§4.3): their orders concatenated
 * (existing tabs only, each once), then the remaining — standalone — tabs in
 * their `previous` relative order, then any tab `previous` never mentioned, in
 * record order. No reachable tab is dropped.
 */
export function deriveTabOrder(workspaces: readonly Workspace[], tabs: Record<string, Tab>, previous: readonly string[]): string[] {
  const candidates = [...workspaces.flatMap((w) => w.tabs), ...previous, ...Object.keys(tabs)]
  return unique(candidates).filter((id) => Object.hasOwn(tabs, id))
}

// === settings ===

/** `{<storageKey>: [<listed fields>]}`, read off `PROJECTIONS.settings` so there is no second allowlist. */
function listedSettingsFields(): Map<string, string[]> {
  const out = new Map<string, string[]>()
  for (const path of PROJECTIONS.settings) {
    if (path.startsWith('!')) continue
    const dot = path.indexOf('.')
    if (dot <= 0) continue
    const key = path.slice(0, dot)
    const field = path.slice(dot + 1).split('.')[0]
    out.set(key, [...(out.get(key) ?? []), field])
  }
  return out
}

function sameValue(a: unknown, b: unknown): boolean {
  if (a === undefined || b === undefined) return a === b
  try {
    return structuralKey(a) === structuralKey(b)
  } catch {
    return false // a local value that is not JSON data cannot equal a payload value
  }
}

/** The coarse shape of a value. `null` and arrays are classes of their own; anything that is not JSON data is `'other'`. */
function shapeOf(value: unknown): string {
  if (value === null) return 'null'
  if (Array.isArray(value)) return 'array'
  const t = typeof value
  return t === 'object' || t === 'string' || t === 'number' || t === 'boolean' ? t : 'other'
}

/**
 * The workspace-scoped record after an apply: the payload decides the MASTER's
 * workspace ids and nothing else.
 *   - a master id takes the incoming entry; one the payload lacks loses its local
 *     entry (the replace semantics every other field has);
 *   - a local entry under any other id stays, BY REFERENCE — it is not the
 *     profile's (another local profile's workspace, an unsyncable id, an orphan),
 *     the builder never sends it, so no payload can mean "delete it";
 *   - an incoming entry under any other id is not written: no filtering client
 *     builds one, so it is an orphan an older client pushed. The section then
 *     reads dirty and is pushed back without it — once; the older client takes that.
 * An incoming value that is not a record is returned as it is: the shape check
 * rejects it (or, `undefined`, the clear rule applies — which a well-formed
 * payload cannot trigger, this being the store's only listed field).
 */
function scopeToMaster(current: unknown, sent: unknown, masterWorkspaceIds: ReadonlySet<string>): unknown {
  if (!isPlainObject(sent)) return sent
  const out: Rec = {}
  if (isPlainObject(current)) {
    for (const id of Object.keys(current)) if (id !== PROTO_KEY && !masterWorkspaceIds.has(id)) out[id] = current[id]
  }
  for (const id of Object.keys(sent)) if (id !== PROTO_KEY && masterWorkspaceIds.has(id)) out[id] = sent[id]
  return out
}

/**
 * Per store, the listed fields whose incoming value differs from the local one.
 * Unlisted fields and unknown stores are left alone (the guard refuses both;
 * ignoring them here as well is defence in depth).
 *
 * Absence means two different things. A STORE the payload lacks was simply not
 * sent (it contributed no listed field over there, or the sender does not know
 * it) — nothing is cleared, the local store is left alone. A listed FIELD the
 * payload lacks, inside a store it does carry, is `undefined` on the sending
 * side: the builder and the hash both drop `undefined` members, so absence is
 * the only way that value can travel. It is therefore patched as
 * `{[field]: undefined}` (key present — zustand's `setState(patch)` then clears
 * it) whenever the local value is not already `undefined`. Leaving it alone
 * would make the local build carry one field more than the payload, the hashes
 * would differ, this side would look dirty and push the old value back —
 * resurrecting what the other side cleared.
 *
 * Value shapes. The pure core has no schema of what each store field may hold,
 * so the check is fail-safe rather than exact: a field about to be patched whose
 * local value is not `undefined` must arrive in the same shape class (`shapeOf`)
 * — `{"purdex-layout":{"tabPosition":{}}}` must not land in a store that expects
 * a string union. `null` is a class of its own: none of the listed fields of the
 * eight stores is nullable today (checked against the store types), so a `null`
 * where a value lives is a mismatch. Only the TOP-LEVEL value of a field is
 * compared. Not compared: a local `undefined` (nothing to compare with) and an
 * incoming `undefined` (a clear — see above). ONE mismatch rejects the payload:
 * `patches` comes back empty, never partial, and `rejected` names the fields.
 *
 * The workspace-scoped record (`WORKSPACE_SCOPED_SETTINGS`) is the exception to
 * "a field is replaced whole": see `scopeToMaster`. The builder filters by the
 * same set, so the property this file is about becomes
 * `hash(build(apply(local, p))) === hash(build-filtered(p))` — equal to
 * `hash(p)` for every `p` a filtering client built.
 *
 * Does not heal layout invariants — that is the store's business (P2b).
 */
export function applySettings(local: SettingsBuildInput, incoming: SettingsPayload, masterWorkspaceIds: ReadonlySet<string>): ApplySettingsResult {
  const patches: SettingsPatches = {}
  const rejected = new Set<string>()
  for (const [key, fields] of listedSettingsFields()) {
    const storageKey = key as SettingsStorageKey
    if (!Object.hasOwn(incoming, storageKey)) continue
    const theirs = incoming[storageKey]
    if (!isPlainObject(theirs)) continue
    const mine = (Object.hasOwn(local, storageKey) ? local[storageKey] : undefined) as Rec | undefined
    const patch: Rec = {}
    for (const field of fields) {
      // An absent field and an `undefined` one are the same thing: cleared on the sending side.
      const sent = Object.hasOwn(theirs, field) ? theirs[field] : undefined
      const current = mine !== undefined && Object.hasOwn(mine, field) ? mine[field] : undefined
      const scoped = storageKey === WORKSPACE_SCOPED_SETTINGS.storageKey && field === WORKSPACE_SCOPED_SETTINGS.field
      const next = scoped ? scopeToMaster(current, sent, masterWorkspaceIds) : sent
      if (sameValue(current, next)) continue
      if (current !== undefined && next !== undefined && shapeOf(current) !== shapeOf(next)) rejected.add(`${storageKey}.${field}`)
      patch[field] = next
    }
    if (Object.keys(patch).length > 0) patches[storageKey] = patch
  }
  if (rejected.size > 0) return { patches: {}, rejected: [...rejected].sort() }
  return { patches, rejected: [] }
}

// === Well-formedness ===

const MAX_JSON_DEPTH = 256
const MAX_LAYOUT_DEPTH = 64
const POLLUTING_KEYS: ReadonlySet<string> = new Set(['__proto__', 'constructor', 'prototype'])

function isPlainObject(value: unknown): value is Rec {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false
  const proto: unknown = Object.getPrototypeOf(value)
  return proto === Object.prototype || proto === null
}

/**
 * Plain JSON data all the way down: no cycle, no polluting key, no non-finite
 * number, no function / class instance, bounded depth. `done` makes a shared
 * (non-cyclic) subtree cost one visit, so a hostile graph cannot blow up.
 */
function isSafeJson(value: unknown, depth: number, ancestors: Set<object>, done: Set<object>): boolean {
  if (value === null) return true
  switch (typeof value) {
    case 'string':
    case 'boolean':
      return true
    case 'number':
      return Number.isFinite(value)
    case 'object':
      break
    default:
      return false
  }
  if (depth > MAX_JSON_DEPTH || ancestors.has(value)) return false
  if (done.has(value)) return true
  const isArray = Array.isArray(value)
  if (!isArray && !isPlainObject(value)) return false
  ancestors.add(value)
  let ok = true
  if (isArray) {
    for (let i = 0; ok && i < value.length; i++) ok = isSafeJson(value[i], depth + 1, ancestors, done)
  } else {
    for (const key of Object.keys(value)) {
      const member = (value as Rec)[key]
      // An `undefined` member is dropped by the hash and by `project` alike; tolerated.
      ok = !POLLUTING_KEYS.has(key) && (member === undefined || isSafeJson(member, depth + 1, ancestors, done))
      if (!ok) break
    }
  }
  ancestors.delete(value)
  if (ok) done.add(value)
  return ok
}

function definedKeys(node: Rec): string[] {
  return Object.keys(node).filter((k) => node[k] !== undefined)
}

function hasOnlyKeys(node: Rec, allowed: readonly string[]): boolean {
  return definedKeys(node).every((k) => allowed.includes(k))
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((v) => typeof v === 'string')
}

function isOptional(value: unknown, test: (v: unknown) => boolean): boolean {
  return value === undefined || test(value)
}

const isString = (v: unknown): boolean => typeof v === 'string'
const isFiniteNumber = (v: unknown): boolean => typeof v === 'number' && Number.isFinite(v)

/** `order` is a duplicate-free string array and exactly the key set of `record`. */
function orderMatchesRecord(order: unknown, record: Rec): boolean {
  if (!isStringArray(order)) return false
  const keys = definedKeys(record)
  return new Set(order).size === order.length && order.length === keys.length && order.every((id) => Object.hasOwn(record, id) && record[id] !== undefined)
}

/** The fields a record entry may carry: `<record>.*.<field>` in the kind's projection. */
function entryFields(kind: SectionKind, record: string): string[] {
  const prefix = `${record}.*.`
  return PROJECTIONS[kind].filter((p) => p.startsWith(prefix)).map((p) => p.slice(prefix.length).split('.')[0])
}

/** Key `name` anywhere at or below `node`. Only called on data `isSafeJson` has accepted. */
function hasKeyDeep(node: unknown, name: string): boolean {
  if (Array.isArray(node)) return node.some((item) => hasKeyDeep(item, name))
  if (!isPlainObject(node)) return false
  return Object.hasOwn(node, name) || Object.keys(node).some((k) => hasKeyDeep(node[k], name))
}

function isLayout(node: unknown, depth: number): boolean {
  if (depth > MAX_LAYOUT_DEPTH || !isPlainObject(node)) return false
  if (node.type === 'leaf') {
    const pane = node.pane
    return hasOnlyKeys(node, ['type', 'pane']) && isPlainObject(pane) && typeof pane.id === 'string' && isPlainObject(pane.content) && typeof pane.content.kind === 'string'
  }
  if (node.type !== 'split') return false
  const children = node.children
  return (
    hasOnlyKeys(node, ['type', 'id', 'direction', 'children']) && // `sizes` included: ratios never travel
    typeof node.id === 'string' &&
    (node.direction === 'h' || node.direction === 'v') &&
    Array.isArray(children) &&
    children.length > 0 &&
    children.every((child) => isLayout(child, depth + 1))
  )
}

function isTabsPayload(p: Rec): boolean {
  const record = p.tabs
  if (!hasOnlyKeys(p, ['order', 'tabs']) || !isPlainObject(record) || !orderMatchesRecord(p.order, record)) return false
  const fields = entryFields('tabs', 'tabs')
  return definedKeys(record).every((id) => {
    const t = record[id]
    return (
      isPlainObject(t) &&
      hasOnlyKeys(t, fields) &&
      t.id === id &&
      typeof t.pinned === 'boolean' &&
      typeof t.locked === 'boolean' &&
      isFiniteNumber(t.createdAt) &&
      isLayout(t.layout, 0) &&
      // The projection strips `sizes` at ANY depth under `layout`, pane contents
      // included — a payload carrying one could never hash back to itself.
      !hasKeyDeep(t.layout, 'sizes')
    )
  })
}

function isWorkspacesPayload(p: Rec): boolean {
  const record = p.workspaces
  if (!hasOnlyKeys(p, ['order', 'workspaces']) || !isPlainObject(record) || !orderMatchesRecord(p.order, record)) return false
  const fields = entryFields('workspaces', 'workspaces')
  return definedKeys(record).every((id) => {
    const w = record[id]
    return (
      workspaceIdOf(`tabs.${id}`) !== null && // the id must be able to name its `tabs.<id>` section
      isPlainObject(w) &&
      hasOnlyKeys(w, fields) &&
      typeof w.name === 'string' &&
      isOptional(w.icon, isString) &&
      isOptional(w.iconWeight, isString) &&
      isOptional(w.moduleConfig, isPlainObject)
    )
  })
}

/**
 * hosts ordinal 3 (host-sync-identity): a key shaped like a sync id is THIS
 * version's (`d1_`) and the sync id of the row's own `daemonId` — nothing else
 * is a builder's output (a no-claim host travels under its local id, which is
 * never sync-id shaped). `aliases` rides on such a canonical row only, in
 * `mergeAliases`' form. A legacy key with a `daemonId` is an ordinal-2 row: allowed.
 */
function isWireKeyOf(id: string, h: Rec): boolean {
  if (isSyncId(id)) {
    if (!id.startsWith(SYNC_ID_PREFIX) || !isValidDaemonId(h.daemonId) || syncIdOfSync(h.daemonId) !== id) return false
  }
  if (h.aliases === undefined) return true
  const a = h.aliases
  if (!isSyncId(id) || !Array.isArray(a) || a.length === 0 || a.length > MAX_HOST_ALIASES) return false
  const canonical = mergeAliases(a, [])
  return canonical.length === a.length && canonical.every((alias, i) => alias === a[i])
}

function isHostsPayload(p: Rec): boolean {
  const record = p.hosts
  const order = p.hostOrder
  if (!hasOnlyKeys(p, ['hosts', 'hostOrder']) || !isPlainObject(record) || !isStringArray(order)) return false
  // `order ⊆ keys` only: useHostStore.reorderHosts stores whatever list it is
  // given, so a host missing from hostOrder is a state the store can hold today.
  if (new Set(order).size !== order.length || !order.every((id) => Object.hasOwn(record, id) && record[id] !== undefined)) return false
  const fields = entryFields('hosts', 'hosts')
  return definedKeys(record).every((id) => {
    const h = record[id]
    return (
      isPlainObject(h) &&
      hasOnlyKeys(h, fields) &&
      h.id === id &&
      typeof h.name === 'string' &&
      typeof h.ip === 'string' &&
      Number.isInteger(h.port) &&
      isFiniteNumber(h.order) &&
      (h.token === undefined || h.token === null || typeof h.token === 'string') &&
      isOptional(h.color, isString) &&
      isOptional(h.colors, isPlainObject) &&
      isOptional(h.icon, isString) &&
      isOptional(h.iconWeight, isString) &&
      // hosts ordinal 2 (host-daemon-id D6); absent in an ordinal-1 payload. The shared validator: never "", never hostile text.
      isOptional(h.daemonId, isValidDaemonId) &&
      isWireKeyOf(id, h)
    )
  })
}

/**
 * Every top-level key must be one of the storage keys `PROJECTIONS.settings`
 * covers, and its store may carry only the fields listed for it, and at least
 * one of them:
 *   - an UNKNOWN storage key stays in the payload's hash, but applySettings
 *     ignores it and no builder here can rebuild it, so
 *     `hash(build(apply(p))) !== hash(p)` — the section could never converge.
 *     This is not where forward compatibility lives: a newer client with one
 *     more store has a different `PROJECTIONS.settings`, hence another
 *     fingerprint, and the schema lock (profile-state.ts) stops it long before;
 *   - an unlisted field (`terminalSettingsVersion`, …) is never projected by the
 *     builder, so the payload could not hash back to itself — and applySettings,
 *     seeing the store present, would read every listed field it lacks as
 *     "cleared over there" and wipe the user's settings;
 *   - an empty store is something the builder never emits (it omits a store that
 *     contributes no listed field), and it would clear ALL of that store's fields.
 * The allowlist is `listedSettingsFields()` — read off PROJECTIONS, no second copy.
 * Value SHAPES are not checked here (there is no local value to compare with):
 * applySettings reports them as `rejected`.
 */
function isSettingsPayload(p: Rec): boolean {
  const listed = listedSettingsFields()
  return definedKeys(p).every((key) => {
    const store = p[key]
    const fields = listed.get(key)
    return fields !== undefined && isPlainObject(store) && definedKeys(store).length > 0 && hasOnlyKeys(store, fields)
  })
}

/**
 * Settings ordinal 3 → 4 (Profile Sync P3e) renamed one synced field:
 * `purdex-newtab-layout.profiles` → `.presets`. A payload an ordinal-3 client
 * wrote (a row on the SOT, or its local side in a persisted conflict stash)
 * still says `profiles`; the guard would refuse it (unlisted field) and, past
 * the guard, the listed-but-absent `presets` would read as "cleared over
 * there". So `apply-to-stores` runs this first: when the newtab store is a
 * plain object that has `profiles` and no `presets`, the result is a copy with
 * that one field renamed — the other stores are the same objects, and the
 * input is never mutated. Anything else (current shape, both names, no newtab
 * store, not a payload) comes back as the same reference. Never throws: the
 * guard that follows is what refuses a hostile input.
 */
export function upcastLegacySettings(payload: unknown): unknown {
  try {
    if (!isPlainObject(payload)) return payload
    const store = payload['purdex-newtab-layout']
    if (!isPlainObject(store) || !Object.hasOwn(store, 'profiles') || Object.hasOwn(store, 'presets')) return payload
    const { profiles, ...rest } = store
    return { ...payload, 'purdex-newtab-layout': { ...rest, presets: profiles } }
  } catch {
    return payload
  }
}

/**
 * The structural guard run on a payload from the daemon BEFORE any apply. It
 * accepts what the builders can produce and nothing else, and never throws —
 * whatever it is handed. A payload that fails is not applied at all; the caller
 * locks the section.
 */
export function isWellFormedSection(kind: SectionKind, payload: unknown): boolean {
  try {
    if (!isPlainObject(payload) || !isSafeJson(payload, 0, new Set(), new Set())) return false
    switch (kind) {
      case 'hosts':
        return isHostsPayload(payload)
      case 'workspaces':
        return isWorkspacesPayload(payload)
      case 'tabs':
        return isTabsPayload(payload)
      case 'settings':
        return isSettingsPayload(payload)
      default:
        return false
    }
  } catch {
    return false // a hostile getter or Proxy trap: not data
  }
}
