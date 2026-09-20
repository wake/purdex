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
import type { PaneLayout, SplitLayout, Tab, Workspace } from '../../types/tab'
import { structuralKey } from './hash'
import { PROJECTIONS, workspaceIdOf } from './projections'
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
  /** Hosts that existed locally and are not in the payload — P2b runs `markMissingHosts` over them. */
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

/** Replaces `hosts` + `hostOrder`; the device's `activeHostId` / `devHostId` survive while their host does. */
export function applyHosts(local: HostsSlice, incoming: HostsPayload): ApplyHostsResult {
  const hosts: Record<string, HostConfig> = {}
  for (const id of Object.keys(incoming.hosts)) {
    if (id !== PROTO_KEY) hosts[id] = incoming.hosts[id]
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
 */
export function applyWorkspaces(local: WorkspacesSlice, incoming: WorkspacesPayload): ApplyWorkspacesResult {
  const localById = new Map<string, Workspace>()
  for (const w of local.workspaces) {
    if (!localById.has(w.id)) localById.set(w.id, w)
  }
  const order = unique(incoming.order).filter((id) => id !== PROTO_KEY && Object.hasOwn(incoming.workspaces, id))
  const workspaces = order.map((id) => workspaceFrom(id, incoming.workspaces[id], localById.get(id)))
  const kept = new Set(order)
  const active = local.activeWorkspaceId !== null && kept.has(local.activeWorkspaceId) ? local.activeWorkspaceId : (order[0] ?? null)
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
 * Does not heal layout invariants — that is the store's business (P2b).
 */
export function applySettings(local: SettingsBuildInput, incoming: SettingsPayload): ApplySettingsResult {
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
      const next = Object.hasOwn(theirs, field) ? theirs[field] : undefined
      const current = mine !== undefined && Object.hasOwn(mine, field) ? mine[field] : undefined
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
      isOptional(h.iconWeight, isString)
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
