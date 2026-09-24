// spa/src/lib/profile/host-identity.ts — the host identity codec of Profile
// Sync (host-sync-identity spec §2–§4, §6, §11). PURE: no store, no I/O.
//
// Three identities (spec §2):
//   - local id    — this device's key for a host; never changes, never travels
//                   when the host knows its daemon.
//   - daemon id   — `HostConfig.daemonId`, the claimed identity (synced).
//   - sync id     — the host's key ON THE WIRE: `syncIdOf(daemonId)`, identical
//                   on every device.
// A host with no valid claim travels under its local id (legacy fallback).
//
// Profile Sync translates at its boundary only: build maps local → wire,
// apply maps wire → local. Every translator here is TOTAL: it never throws,
// and a value it cannot map passes through unchanged.
import { sha256Hex, sha256HexSync } from '../crypto-hash'
import { isValidDaemonId } from '../daemon-id'
import type { HostConfig } from '../../stores/useHostStore'
import type { HostsPayload } from './types'

/** The prefix of THIS version's sync ids. A new algorithm gets a new prefix (`d2_`), never a changed `d1_`. */
export const SYNC_ID_PREFIX = 'd1_'

/** Any version's sync id: `d<digits>_…`. Only `d1_` ids are ever produced or matched by this version. */
const ANY_SYNC_ID_RE = /^d[0-9]+_/

/**
 * Is `value` a sync id of ANY version? A `d2_…` from a newer client is a sync
 * id this version cannot map — unknown, never mistaken for a legacy local id.
 * Anything that is not a sync id is a legacy local id.
 */
export function isSyncId(value: unknown): value is string {
  return typeof value === 'string' && ANY_SYNC_ID_RE.test(value)
}

const SYNC_ID_BODY_LENGTH = 16

/** `d1_` + base36 of the first 80 bits (10 bytes) of the digest, left-padded with '0' to 16. */
function syncIdFromDigestHex(hex: string): string {
  const n = BigInt('0x' + hex.slice(0, 20))
  return SYNC_ID_PREFIX + n.toString(36).padStart(SYNC_ID_BODY_LENGTH, '0')
}

/**
 * The wire id of a daemon (spec §3): `"d1_" + base36(first 80 bits of
 * SHA-256(UTF-8(daemonId))))`, lower-case, zero-padded to 16. Fixed forever.
 * Byte-identical to `syncIdOfSync` on both hash paths (crypto-hash invariant).
 */
export async function syncIdOf(daemonId: string): Promise<string> {
  return syncIdFromDigestHex(await sha256Hex(new TextEncoder().encode(daemonId)))
}

/** `syncIdOf`, synchronously (the pure-JS SHA-256). Same output for every input. */
export function syncIdOfSync(daemonId: string): string {
  return syncIdFromDigestHex(sha256HexSync(new TextEncoder().encode(daemonId)))
}

// === Identity (spec §4, §11.3) ===

/** The part of a `HostConfig` the identity reads. */
export interface IdentityHost {
  id: string
  daemonId?: string
}

/**
 * One snapshot's mapping between local ids and wire ids.
 *
 * `conflict` (sorted local ids, or null) names every host whose wire id is
 * ambiguous: two hosts claiming one daemon, two claims hashing to one sync id,
 * a claim's sync id equal to another host's legacy (local-id) wire id, or a
 * no-claim local id that itself looks like a sync id. Conflicting hosts are
 * LEFT OUT of both maps; the caller must not build or apply anything that
 * names hosts while `conflict` is set (spec §11.4 pauses the profile).
 *
 * `signature` changes exactly when a pair or the conflict changes — the
 * collector watches it and re-schedules every host-bearing section (§11.3).
 */
export interface HostIdentity {
  toWire: Map<string, string>
  toLocal: Map<string, string>
  conflict: string[] | null
  signature: string
}

function compareStrings(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0
}

function buildIdentity(hosts: Record<string, IdentityHost>, claimWire: ReadonlyMap<string, string>): HostIdentity {
  const wireOf = new Map<string, string>()
  const conflict = new Set<string>()
  for (const local of Object.keys(hosts)) {
    const claimed = claimWire.get(local)
    if (claimed !== undefined) {
      wireOf.set(local, claimed)
    } else {
      wireOf.set(local, local)
      // A no-claim id shaped like a sync id would be read as one on the wire.
      if (isSyncId(local)) conflict.add(local)
    }
  }
  const byWire = new Map<string, string[]>()
  for (const [local, wire] of wireOf) {
    const group = byWire.get(wire)
    if (group) group.push(local)
    else byWire.set(wire, [local])
  }
  for (const group of byWire.values()) {
    if (group.length > 1) for (const local of group) conflict.add(local)
  }

  const toWire = new Map<string, string>()
  const toLocal = new Map<string, string>()
  for (const [local, wire] of wireOf) {
    if (conflict.has(local)) continue
    toWire.set(local, wire)
    toLocal.set(wire, local)
  }
  const conflictList = conflict.size > 0 ? [...conflict].sort(compareStrings) : null
  const pairs = [...toWire].sort((a, b) => compareStrings(a[0], b[0]))
  return { toWire, toLocal, conflict: conflictList, signature: JSON.stringify({ pairs, conflict: conflictList }) }
}

export interface IdentityOptions {
  /** Test seam: replaces `syncIdOfSync` (e.g. to force two claims onto one sync id). */
  hash?: (daemonId: string) => string
}

export interface AsyncIdentityOptions {
  /** Test seam: replaces `syncIdOf`. */
  hash?: (daemonId: string) => string | Promise<string>
}

/** The identity of one host-store snapshot, synchronously (spec §11.3 — the collector's path). */
export function identityOfSync(hosts: Record<string, IdentityHost>, opts: IdentityOptions = {}): HostIdentity {
  const hash = opts.hash ?? syncIdOfSync
  const claimWire = new Map<string, string>()
  for (const [local, host] of Object.entries(hosts)) {
    const daemonId = host?.daemonId
    if (isValidDaemonId(daemonId)) claimWire.set(local, hash(daemonId))
  }
  return buildIdentity(hosts, claimWire)
}

/** `identityOfSync` through the async hash (WebCrypto when present). Same result for every input. */
export async function identityOf(hosts: Record<string, IdentityHost>, opts: AsyncIdentityOptions = {}): Promise<HostIdentity> {
  const hash = opts.hash ?? syncIdOf
  const claimWire = new Map<string, string>()
  for (const [local, host] of Object.entries(hosts)) {
    const daemonId = host?.daemonId
    if (isValidDaemonId(daemonId)) claimWire.set(local, await hash(daemonId))
  }
  return buildIdentity(hosts, claimWire)
}

// === Per-host wire id (H2 plan §0.4) ===

/** daemonId → sync id, one memo per hash function (the default `syncIdOfSync`, or a test seam). */
const wireIdMemo = new WeakMap<(daemonId: string) => string, Map<string, string>>()

/**
 * ONE host's wire id, without looking at the other hosts: `syncIdOfSync(daemonId)`
 * when the host has a valid claim, else its local id. Equal to
 * `identityOfSync(hosts).toWire.get(host.id)` whenever the snapshot has no
 * conflict; under a conflict (two rows claiming one daemon) both rows get the
 * same `d1_…` — which `toWire` omits — so a look keyed by wire id is not lost
 * while the user resolves the duplicate. Memoised per daemonId.
 */
export function wireIdOfHost(host: IdentityHost, opts: IdentityOptions = {}): string {
  const daemonId = host.daemonId
  if (!isValidDaemonId(daemonId)) return host.id
  const hash = opts.hash ?? syncIdOfSync
  let memo = wireIdMemo.get(hash)
  if (memo === undefined) {
    memo = new Map()
    wireIdMemo.set(hash, memo)
  }
  let wire = memo.get(daemonId)
  if (wire === undefined) {
    wire = hash(daemonId)
    memo.set(daemonId, wire)
  }
  return wire
}

// === Apply-side matching (spec §6, §11.5, §11.6) ===

/**
 * An incoming `hosts` row as far as matching is concerned. Wire data: every
 * field is untrusted, so it is typed loosely and read defensively.
 */
export interface IncomingHostRow {
  id?: unknown
  daemonId?: unknown
  aliases?: unknown
}

export type HostMatchError =
  /** Two incoming rows name one daemon (same daemonId, or a canonical key and a claim hashing to it). */
  | 'duplicate-host-identity'
  /** A row's daemon is claimed by more than one LOCAL host: which one it updates is ambiguous. */
  | 'host-identity-conflict'

export interface HostMatch {
  /** Per incoming row key: the local host it updates in place, or `'new'` (create with a new local id). */
  byRow: Map<string, string>
  /** Local hosts no row matched (the caller cascades their removal). */
  removed: string[]
  /** Set → nothing may be applied; `byRow` and `removed` are then empty. */
  error?: HostMatchError
}

/** The one sentinel `byRow` uses for "create a new local host". Never a local id (those are 6-char base36). */
export const NEW_HOST = 'new'

function rowDaemonId(row: unknown): string | undefined {
  if (row === null || typeof row !== 'object') return undefined
  const daemonId = (row as IncomingHostRow).daemonId
  return isValidDaemonId(daemonId) ? daemonId : undefined
}

/**
 * Match each incoming row to a local host (spec §6 as amended by §11.5/§11.6):
 *   - a row carrying a valid `daemonId` matches the local host with that
 *     `daemonId` — and ONLY by it; its key never captures anything;
 *   - else a `d1_` key matches the local host whose `syncIdOf(daemonId)` is it;
 *     a sync id of another version matches nothing;
 *   - else (a legacy local-id key) it matches the local host of that id, but
 *     only if that host has no `daemonId` either;
 *   - otherwise the row is new.
 * One-to-one: two rows resolving to one daemon → `duplicate-host-identity`.
 */
export function matchIncomingHosts(
  localHosts: Record<string, IdentityHost>,
  incomingRows: Record<string, unknown>,
  opts: IdentityOptions = {},
): HostMatch {
  const hash = opts.hash ?? syncIdOfSync
  const failed = (error: HostMatchError): HostMatch => ({ byRow: new Map(), removed: [], error })

  const localsByDaemon = new Map<string, string[]>()
  const localsBySyncId = new Map<string, string[]>()
  for (const [local, host] of Object.entries(localHosts)) {
    const daemonId = host?.daemonId
    if (!isValidDaemonId(daemonId)) continue
    const push = (m: Map<string, string[]>, k: string) => {
      const list = m.get(k)
      if (list) list.push(local)
      else m.set(k, [local])
    }
    push(localsByDaemon, daemonId)
    push(localsBySyncId, hash(daemonId))
  }

  const byRow = new Map<string, string>()
  const seenDaemons = new Set<string>()
  const matched = new Set<string>()
  for (const [key, row] of Object.entries(incomingRows)) {
    const daemonId = rowDaemonId(row)
    let candidates: string[] = []
    let daemonKey: string | null = null
    if (daemonId !== undefined) {
      daemonKey = hash(daemonId)
      candidates = localsByDaemon.get(daemonId) ?? []
    } else if (isSyncId(key)) {
      daemonKey = key
      if (key.startsWith(SYNC_ID_PREFIX)) candidates = localsBySyncId.get(key) ?? []
    } else {
      const host = Object.hasOwn(localHosts, key) ? localHosts[key] : undefined
      if (host && !isValidDaemonId(host.daemonId)) candidates = [key]
    }
    if (daemonKey !== null) {
      if (seenDaemons.has(daemonKey)) return failed('duplicate-host-identity')
      seenDaemons.add(daemonKey)
    }
    if (candidates.length > 1) return failed('host-identity-conflict')
    byRow.set(key, candidates[0] ?? NEW_HOST)
    if (candidates[0] !== undefined) matched.add(candidates[0])
  }

  const removed = Object.keys(localHosts).filter((local) => !matched.has(local))
  return { byRow, removed }
}

// === Aliases (spec §11.2) ===

/** How many legacy keys a canonical `hosts` row remembers. */
export const MAX_HOST_ALIASES = 16

function isAlias(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && !isSyncId(value)
}

/**
 * The legacy wire keys (foreign local ids) a canonical row was matched from:
 * `existing` in its order, then each unseen `added` entry; only non-empty
 * non-sync-id strings, each once; at most MAX_HOST_ALIASES, the oldest dropped.
 * Re-adding a known alias does not move it. Total on wire data.
 */
export function mergeAliases(existing: unknown, added: readonly unknown[]): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  const take = (list: readonly unknown[]) => {
    for (const a of list) {
      if (isAlias(a) && !seen.has(a)) {
        seen.add(a)
        out.push(a)
      }
    }
  }
  take(Array.isArray(existing) ? existing : [])
  take(added)
  return out.slice(-MAX_HOST_ALIASES)
}

// === wire → local resolution (spec §11.2) ===

/** Maps one wire host id to this device's local id; an id it cannot map comes back unchanged. */
export type WireResolver = (wireId: string) => string

export interface WireResolverInput {
  /** The identity of the host store AFTER the hosts apply. */
  identity: HostIdentity
  /** The incoming `hosts` rows (by wire key) — their `aliases` resolve legacy ids. */
  rows?: Record<string, unknown>
  /**
   * The hosts apply's result: row key → local id. Callers SHOULD pass it with
   * every `NEW_HOST` replaced by the local id the apply created for that row;
   * a `NEW_HOST` left in is ignored (the row's `daemonId` still resolves it
   * through `identity` when it has one — a legacy row without one cannot be).
   */
  matched?: ReadonlyMap<string, string>
  /** Test seam: replaces `syncIdOfSync`. */
  hash?: (daemonId: string) => string
}

/**
 * The resolver tabs / settings apply through (spec §11.2):
 *   - a sync id → the identity's local host (or the row this apply matched);
 *     one nobody maps — any version — stays unchanged;
 *   - a legacy id → the local host its exact row was matched to (`matched`);
 *     else, when that exact row carries a valid `daemonId`, the post-apply
 *     identity's host of `syncIdOf(daemonId)` (a row created as NEW); else the
 *     local host of the canonical row listing it in `aliases` (by row key, then
 *     by that row's `daemonId`); an alias claimed by rows resolving to
 *     different hosts is ambiguous; else unchanged.
 * "Unchanged" hands the id to the existing unknown-host handling.
 */
export function makeWireResolver(input: WireResolverInput): WireResolver {
  const { identity, rows = {}, matched = new Map<string, string>() } = input
  const hash = input.hash ?? syncIdOfSync
  const matchedLocal = (key: string): string | undefined => {
    const local = matched.get(key)
    return local === undefined || local === NEW_HOST ? undefined : local
  }
  const daemonLocal = (row: unknown): string | undefined => {
    const daemonId = rowDaemonId(row)
    return daemonId === undefined ? undefined : identity.toLocal.get(hash(daemonId))
  }
  const exactRowLocal = (key: string): string | undefined =>
    Object.hasOwn(rows, key) ? daemonLocal(rows[key]) : undefined
  const rowLocal = (key: string, row: unknown): string | undefined => {
    const byKey = identity.toLocal.get(key) ?? matchedLocal(key)
    return byKey !== undefined ? byKey : daemonLocal(row)
  }

  // alias → the one local host it resolves to (null = ambiguous)
  const aliasLocal = new Map<string, string | null>()
  for (const [key, row] of Object.entries(rows)) {
    if (row === null || typeof row !== 'object') continue
    const aliases = (row as IncomingHostRow).aliases
    if (!Array.isArray(aliases)) continue
    const local = rowLocal(key, row)
    for (const alias of aliases) {
      if (!isAlias(alias)) continue
      const prev = aliasLocal.get(alias)
      if (prev === undefined) aliasLocal.set(alias, local ?? null)
      else if (prev !== local) aliasLocal.set(alias, null)
    }
  }

  return (wireId) => {
    if (isSyncId(wireId)) return identity.toLocal.get(wireId) ?? matchedLocal(wireId) ?? wireId
    return matchedLocal(wireId) ?? exactRowLocal(wireId) ?? aliasLocal.get(wireId) ?? wireId
  }
}

// === hosts payload (spec §4) ===

/** A `hosts` row on the wire: a host config under its wire id, plus the canonical row's `aliases`. */
export type WireHostRow = HostConfig & { aliases?: string[] }

export interface WireHostsPayload {
  hosts: Record<string, WireHostRow>
  hostOrder: string[]
}

/**
 * Write `record[key] = value` as an OWN data property. Plain assignment with a
 * wire key `__proto__` would call the prototype setter instead (the entry is
 * lost and the output's prototype replaced). The output keeps the ordinary
 * `Object.prototype`, so spread / JSON / structural hashing behave as usual.
 */
function setOwn<T>(record: Record<string, T>, key: string, value: T): void {
  Object.defineProperty(record, key, { value, enumerable: true, writable: true, configurable: true })
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

/**
 * Re-key a record through `map`. When two keys land on one target, the entry
 * whose SOURCE key is a sync id wins (canonical over legacy), else the first.
 */
function rekeyEntries<T>(record: Record<string, T>, map: (key: string) => string): Array<[source: string, target: string, value: T]> {
  const kept = new Map<string, [string, string, T]>()
  for (const [key, value] of Object.entries(record)) {
    const target = map(key)
    const had = kept.get(target)
    if (had === undefined || (isSyncId(key) && !isSyncId(had[0]))) kept.set(target, [key, target, value])
  }
  return [...kept.values()]
}

function rekey<T>(record: Record<string, T>, map: (key: string) => string): Record<string, T> {
  const out: Record<string, T> = {}
  for (const [, target, value] of rekeyEntries(record, map)) setOwn(out, target, value)
  return out
}

function mapOrder(order: unknown, map: (id: string) => string): unknown {
  return Array.isArray(order) ? order.map((id) => (typeof id === 'string' ? map(id) : id)) : order
}

/**
 * local → wire for the `hosts` payload: record keys, each row's `.id`, and
 * `hostOrder`. `aliasesOf` (optional) supplies the legacy keys a CANONICAL
 * row carries (merged and capped by `mergeAliases`; an empty list is omitted).
 * An id the identity does not map passes through. Input is never mutated.
 */
export function hostsToWire(
  payload: HostsPayload,
  identity: HostIdentity,
  aliasesOf?: (localId: string) => readonly unknown[] | undefined,
): WireHostsPayload {
  const toWire = (id: string) => identity.toWire.get(id) ?? id
  const hosts: Record<string, WireHostRow> = {}
  for (const [local, wire, value] of rekeyEntries(payload.hosts as Record<string, unknown>, toWire)) {
    if (!isRecord(value)) {
      setOwn(hosts, wire, value as WireHostRow)
      continue
    }
    const next = { ...value, id: wire } as WireHostRow
    if (isSyncId(wire) && aliasesOf) {
      const given: unknown = aliasesOf(local)
      const aliases = mergeAliases([], Array.isArray(given) ? given : [])
      if (aliases.length > 0) next.aliases = aliases
    }
    setOwn(hosts, wire, next)
  }
  return { hosts, hostOrder: mapOrder(payload.hostOrder, toWire) as string[] }
}

/** `hostsFromWire`'s result: the local payload (never carrying `aliases`) and each row's aliases. */
export interface HostsFromWire {
  hosts: HostsPayload
  /**
   * Per local id, the row's `aliases` (sanitised like `mergeAliases`: strings,
   * no sync ids, each once, ≤ MAX_HOST_ALIASES); a row with none is absent.
   * The caller persists them (`HostConfig.syncAliases`) and feeds them back to
   * `hostsToWire` as `aliasesOf` — `(id) => aliasesByLocal[id]` reproduces the rows.
   */
  aliasesByLocal: Record<string, string[]>
}

/**
 * wire → local for the `hosts` payload: record keys, `.id`, `hostOrder`
 * through `resolve`. A row's `aliases` leave the payload and come back in
 * `aliasesByLocal`. Total.
 */
export function hostsFromWire(payload: WireHostsPayload, resolve: WireResolver): HostsFromWire {
  const hosts: Record<string, HostConfig> = {}
  const aliasesByLocal: Record<string, string[]> = {}
  const source = isRecord(payload?.hosts) ? payload.hosts : {}
  for (const [, local, value] of rekeyEntries(source as Record<string, unknown>, resolve)) {
    if (!isRecord(value)) {
      setOwn(hosts, local, value as unknown as HostConfig)
      continue
    }
    const { aliases, ...rest } = value as unknown as WireHostRow
    setOwn(hosts, local, { ...rest, id: local })
    const kept = mergeAliases(aliases, [])
    if (kept.length > 0) setOwn(aliasesByLocal, local, kept)
  }
  return { hosts: { hosts, hostOrder: mapOrder(payload?.hostOrder, resolve) as string[] }, aliasesByLocal }
}

// === Pane layouts (tabs.*) ===

type IdMap = (id: string) => string

const FILE_SOURCE_KINDS: ReadonlySet<string> = new Set(['editor', 'image-preview', 'pdf-preview'])

/** One pane's content with its host-bearing field mapped; the same object when nothing applies. */
function mapContent(content: unknown, map: IdMap): unknown {
  if (!isRecord(content)) return content
  const kind = content.kind
  if (kind === 'tmux-session' && typeof content.hostId === 'string') {
    return { ...content, hostId: map(content.hostId) }
  }
  if (typeof kind === 'string' && FILE_SOURCE_KINDS.has(kind)) {
    const source = content.source
    if (isRecord(source) && source.type === 'daemon' && typeof source.hostId === 'string') {
      return { ...content, source: { ...source, hostId: map(source.hostId) } }
    }
    return content
  }
  // '' is "no hint", not a host.
  if (kind === 'execution' && typeof content.host === 'string' && content.host !== '') {
    return { ...content, host: map(content.host) }
  }
  return content
}

function mapLayout(layout: unknown, map: IdMap): unknown {
  if (!isRecord(layout)) return layout
  if (layout.type === 'leaf') {
    const pane = layout.pane
    if (!isRecord(pane)) return layout
    const content = mapContent(pane.content, map)
    return content === pane.content ? layout : { ...layout, pane: { ...pane, content } }
  }
  if (layout.type === 'split' && Array.isArray(layout.children)) {
    return { ...layout, children: layout.children.map((child) => mapLayout(child, map)) }
  }
  return layout
}

/**
 * local → wire over a whole split tree (full or size-stripped): the host of
 * `tmux-session.hostId`, `source.hostId` of editor / image-preview /
 * pdf-preview when `source.type === 'daemon'`, and `execution.host` ('' is
 * left alone). Other kinds untouched; an unknown id passes through; the input
 * is never mutated.
 */
export function layoutToWire<L>(layout: L, identity: HostIdentity): L {
  return mapLayout(layout, (id) => identity.toWire.get(id) ?? id) as L
}

/** wire → local over a whole split tree, through `resolve` (same fields as `layoutToWire`). */
export function layoutFromWire<L>(layout: L, resolve: WireResolver): L {
  return mapLayout(layout, resolve) as L
}

// === settings: purdex-host-settings.hosts ===

/** local → wire for the host-settings record (keyed by host id). An unknown key passes through. */
export function hostSettingsToWire<T>(record: Record<string, T>, identity: HostIdentity): Record<string, T> {
  if (!isRecord(record)) return record
  return rekey(record, (id) => identity.toWire.get(id) ?? id)
}

/**
 * wire → local for the host-settings record. When a canonical and a legacy
 * key resolve to one host, the canonical (sync-id) entry wins.
 */
export function hostSettingsFromWire<T>(record: Record<string, T>, resolve: WireResolver): Record<string, T> {
  if (!isRecord(record)) return record
  return rekey(record, resolve)
}

// === settings: purdex-newtab-layout.presets ===

/**
 * The New Tab block id prefixes that end in a host id (`<prefix>:<hostId>`) —
 * `sessionsProviderId` and `headlessProviderId`. THE one list: a new per-host
 * block type must be added here or its columns will not survive a sync.
 */
export const HOST_BEARING_COLUMN_PREFIXES = ['sessions', 'headless'] as const

function mapColumnId(id: unknown, map: IdMap): unknown {
  if (typeof id !== 'string') return id
  const colon = id.indexOf(':')
  if (colon < 0) return id
  const prefix = id.slice(0, colon)
  const hostId = id.slice(colon + 1)
  if (hostId === '' || !(HOST_BEARING_COLUMN_PREFIXES as readonly string[]).includes(prefix)) return id
  return `${prefix}:${map(hostId)}`
}

/** local → wire for one New Tab column id; a non-host column is returned unchanged. */
export function presetColumnIdToWire(id: string, identity: HostIdentity): string {
  return mapColumnId(id, (h) => identity.toWire.get(h) ?? h) as string
}

/** wire → local for one New Tab column id. */
export function presetColumnIdFromWire(id: string, resolve: WireResolver): string {
  return mapColumnId(id, resolve) as string
}

function mapPresets<P>(presets: P, map: IdMap): P {
  if (!isRecord(presets)) return presets
  const out: Record<string, unknown> = {}
  for (const [key, preset] of Object.entries(presets)) {
    if (!isRecord(preset) || !Array.isArray(preset.columns)) {
      setOwn(out, key, preset)
      continue
    }
    const columns = preset.columns.map((col: unknown) => (Array.isArray(col) ? col.map((id) => mapColumnId(id, map)) : col))
    setOwn(out, key, { ...preset, columns })
  }
  return out as P
}

/** local → wire over the presets shape (`{ '3col'|'2col'|'1col': { enabled, columns: string[][] } }`). */
export function presetColumnsToWire<P>(presets: P, identity: HostIdentity): P {
  return mapPresets(presets, (h) => identity.toWire.get(h) ?? h)
}

/** wire → local over the presets shape. */
export function presetColumnsFromWire<P>(presets: P, resolve: WireResolver): P {
  return mapPresets(presets, resolve)
}
