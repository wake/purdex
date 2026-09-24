import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { generateId } from '../lib/id'
import { isValidDaemonId } from '../lib/daemon-id'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import { syncIdOfSync, wireIdOfHost } from '../lib/profile/host-identity'
import { useHostLookStore, type HostLookEntry } from './useHostLookStore'
import {
  clampHostAlpha,
  HOST_COLOR_ALPHA_DEFAULTS,
  HOST_COLOR_LAYER_NAMES,
  isHostColorMode,
  isIconWeight,
  isPhosphorIconName,
  isValidHostColor,
  sanitizeHostConfig,
} from '../lib/host-color'
// host-api.ts imports useHostStore at runtime, so this must stay a type-only
// import to avoid a require cycle.
import type { NexInfo } from '../lib/host-api'
import type { TransferChange } from '../lib/host-transfer-plan'
import type { IconWeight } from '../types/tab'
import type { HostColorLayer, HostColorLayerName, HostColorMode, HostColorSet } from '../lib/host-color'

/* ─── Interfaces ─── */

export interface HostConfig {
  id: string
  name: string
  ip: string
  port: number
  // `null` is the explicit "token cleared, re-auth required" sentinel written by
  // the sync deserialize path when a host is new or its endpoint (ip/port) changed.
  // Distinct from `undefined` (field simply absent); `null` survives JSON round-trips.
  token?: string | null
  order: number
  /**
   * @deprecated Legacy single color; read-only (spec D10). New code writes `colors`.
   * Strict `#rrggbb` (see `isValidHostColor`). Absent means "no color" (the key is
   * removed, never set to null). Synced with the host config; always re-validated
   * with `isValidHostColor` before reaching CSS.
   */
  color?: string
  /**
   * Per-mode tri-color for the host badge (spec 2026-09-18 host-color-modes §4.1).
   * Absent mode → inherits `console`; absent `console` → legacy `color`, then "no color".
   */
  colors?: Partial<Record<HostColorMode, HostColorSet>>
  /**
   * Phosphor icon name shown in the host badge, e.g. `'Laptop'`. Absent means
   * "use `DEFAULT_HOST_ICON`" (the key is removed, never set to null).
   */
  icon?: string
  /** Phosphor weight for `icon`; absent means `'regular'`. Re-validated with `isIconWeight`. */
  iconWeight?: IconWeight
  /**
   * The daemon's own stable identity (`/api/info` → `host_id`) as claimed for this
   * entry (spec 2026-09-23 D1/D2). Present only when non-empty — never `""`. Synced;
   * on pull the SOT value wins. Whether it holds on *this* device is the runtime
   * `daemonIdMismatch`, never this field.
   */
  daemonId?: string
  /**
   * Profile Sync's memory of the legacy wire keys (another device's local ids)
   * this host's canonical `hosts` row was matched from (host-sync-identity spec
   * §11.2). Written by the hosts apply, read back by the hosts builder as the
   * row's wire `aliases` — never shown, never edited. Absent when empty;
   * canonical per `mergeAliases` (sanitised on rehydrate).
   */
  syncAliases?: string[]
}

/** This device's verification of `HostConfig.daemonId` failed (spec D2/D3). Runtime only. */
export interface DaemonIdMismatch {
  stored: string
  observed: string
  /** `ip:port` the answer came from. */
  endpoint: string
}

export interface HostRuntime {
  status: 'connected' | 'disconnected' | 'reconnecting' | 'auth-error'
  latency?: number
  /**
   * Terminal attach gate (spec §4.6): true only once the *current* host-events
   * connection has delivered and reconciled a `sessions` payload. Closed on
   * every (re)connect and on every drop, so a pane can never attach to a
   * session code that a tmux restart handed to a stranger. Not persisted.
   */
  attachReady?: boolean
  daemonState?: 'connected' | 'refused' | 'unreachable' | 'auth-error'
  tmuxState?: 'ok' | 'unavailable'
  manualRetry?: () => Promise<void> | void  // safe: runtime excluded from persist partialize
  /** Read through `selectDaemonIdMismatch` — a raw flag may be stale after a re-point. */
  daemonIdMismatch?: DaemonIdMismatch
  /**
   * This session observed `daemonId` at `endpoint` (a learned or equal answer; token-agnostic).
   * Read through `selectDaemonIdVerified` (PR review #1).
   */
  daemonIdVerified?: { endpoint: string; daemonId: string }
}

export interface HostInfo {
  host_id: string
  tmux_instance: string
  purdex_version: string
  tmux_version: string
  os: string
  arch: string
  nex?: NexInfo
}

/** `looks`: whether step 2 (the look store, plan §0.20) was written; `'failed'` leaves the hosts added. */
export type TransferApplyResult =
  | { kind: 'applied'; created: string[]; overwritten: string[]; looks: 'ok' | 'failed' }
  | { kind: 'stale' }

/* ─── Store ─── */

interface HostState {
  hosts: Record<string, HostConfig>
  hostOrder: string[]
  runtime: Record<string, HostRuntime>
  activeHostId: string | null
  /** Host the Development page targets. Device-local, not synced (spec D3). */
  devHostId: string | null

  /** Never writes `daemonId` — only `observeDaemonId` does (spec D3). */
  addHost: (opts: { id?: string; name: string; ip: string; port: number; token?: string | null }) => string
  /**
   * Never writes `daemonId` (only `observeDaemonId` does; sync applies through its own
   * setState path). A re-point (ip or port changes) clears it unconditionally (spec D3).
   */
  updateHost: (hostId: string, updates: Partial<Pick<HostConfig, 'name' | 'ip' | 'port' | 'token'>>) => void
  /**
   * The single entry point for every `/api/info` answer (spec D3). `atRequest` is
   * `requestAtOf(host)` captured before the request; an answer for a host that is gone,
   * has moved or changed token since is dropped, as is an `observed` that fails `isValidDaemonId` (`""` included).
   */
  observeDaemonId: (hostId: string, observed: string, atRequest: HostRequestAt) => void
  /**
   * Commits a received host transfer (spec §6.4.5, plan R2/R4) in ONE `set()`: every overwrite target must still be
   * at the planned `{endpoint, token, daemonId}`, and no touched row may share an endpoint or a daemonId with another
   * row afterwards — any difference (or a throw) refuses the whole change and writes nothing (`stale`). Created rows
   * and overwritten rows learn their daemonId through `applyObservedDaemonId` with their own `{endpoint, token}`, so
   * each ends verified. `HostConfig` gets the payload's NAME only (spec §6.4 step 7): a created row its name, an
   * overwritten row its name / ip / port / token, its look fields untouched.
   *
   * Then, only when that set applied, step 2 (plan §0.20): `applyTransferLooks(transferLookEntries(change))` — the
   * received `{ name, ...look }` goes to the look store where no entry exists. Its failure never undoes step 1: the
   * result says `looks: 'failed'` and the caller may retry step 2 alone.
   */
  applyHostTransfer: (change: TransferChange) => TransferApplyResult
  /*
   * The look writers (H2c-2, spec §4.2 / §4.4): each writes the look store's entry under `lookKeyOf` (the host's
   * CURRENT wire id, or its local id while that entry awaits the re-key) and never `HostConfig`. An absent entry is first seeded from the host's `HostConfig` look, so a
   * first edit of one field keeps the others; clearing removes the group's keys (plan §0.5 option A — no tombstone).
   * An unknown host, an invalid value, or a write that changes nothing writes nothing.
   */
  /** Legacy entry point kept for the current color UI: writes `colors.console.main` (alpha preserved, default 100); `null` clears the console set. */
  setHostColor: (hostId: string, color: string | null) => void
  /**
   * Write one layer of one mode (spec §4.1). `main` with a valid color creates the
   * set when absent; `null` on `main` clears the mode. `middle` / `light` require an
   * existing set (no-op otherwise); `null` removes just that layer.
   * Validation is strict and never throws: `color` (when present) must already pass
   * `isValidHostColor` (it is lowercased, never normalized — the UI normalizes), `alpha`
   * must be a finite number (then clamped to an integer 0–100). Anything else, an
   * unknown host / mode / layer, is a no-op. Every *applied* write deletes the legacy
   * `color` key (spec D10); clearing a mode that has no set is a no-op, except
   * `console` on a host that has a legacy `color` and no console set (whether or not
   * other mode sets exist): there it removes the legacy `color`, because the resolver
   * shows the legacy color as the console color and "No color" must clear what the
   * user sees. Removing a middle/light layer that is already absent is a no-op.
   */
  setHostColorLayer: (hostId: string, mode: HostColorMode, layer: HostColorLayerName, value: HostColorLayer | null) => void
  /** Remove the whole set for a mode; drops `colors` when it becomes empty. */
  clearHostColorMode: (hostId: string, mode: HostColorMode) => void
  /**
   * Set the host's Phosphor icon (and optionally its weight), or `null` / a blank
   * string to remove both keys. An invalid weight is ignored; unknown hosts are no-ops.
   */
  setHostIcon: (hostId: string, icon: string | null, weight?: IconWeight) => void
  /** Rename in this workbench: trimmed; a blank name is a no-op. `HostConfig.name` is untouched. */
  setHostName: (hostId: string, name: string) => void
  /**
   * "Hosts added later" (spec §4.3, plan §0.19): writes the host's `HostConfig` look into the look store under its
   * current wire id — only when no entry is there (a workbench look already under that key wins). Called by the add
   * paths (the add-host dialog, `registerLocalHost`), never by `addHost` (also the undo of a deletion).
   */
  seedHostLook: (hostId: string) => void
  /** Seeds the look of a host it CREATES (`seedHostLook`); re-registering an endpoint seeds nothing. */
  registerLocalHost: (result: { url: string; token: string; hostname: string }) => string
  removeHost: (hostId: string) => void
  reorderHosts: (orderedIds: string[]) => void
  setActiveHost: (hostId: string) => void
  setDevHost: (hostId: string | null) => void
  setRuntime: (hostId: string, runtime: Partial<HostRuntime>) => void
  getDaemonBase: (hostId: string) => string
  getWsBase: (hostId: string) => string
  getAuthHeaders: (hostId: string) => Record<string, string>
  /** Test isolation: the default state, and an empty look store (plan §0.18). */
  reset: () => void
}

const DEFAULT_ID = generateId()

function createDefaultState() {
  const defaultHost: HostConfig = {
    id: DEFAULT_ID,
    name: 'mlab',
    ip: '100.64.0.2',
    port: 7860,
    order: 0,
  }
  return {
    hosts: { [DEFAULT_ID]: defaultHost },
    hostOrder: [DEFAULT_ID],
    runtime: {} as Record<string, HostRuntime>,
    activeHostId: DEFAULT_ID as string | null,
    devHostId: null as string | null,
  }
}

/** `ip:port` — the endpoint part of what `observeDaemonId` guards on. */
export function hostEndpoint(h: Pick<HostConfig, 'ip' | 'port'>): string {
  return `${h.ip}:${h.port}`
}

/**
 * The host part of `ip` in the form two spellings of one address share, for endpoint equality checks: a bracketed
 * IPv6 literal becomes the URL parser's form (`[0:0:0:0:0:0:0:1]` → `[::1]`, `[::ffff:100.64.0.2]` →
 * `[::ffff:6440:2]`, lowercase), anything else is lowercased (DNS names are case-insensitive). Host transfer only
 * (H4b PR #1397 critic): `hostEndpoint` and the add-host dialog keep comparing the stored string as is.
 */
export function canonicalHostPart(ip: string): string {
  if (ip.startsWith('[')) {
    try {
      const hostname = new URL(`http://${ip}:1`).hostname
      if (hostname.startsWith('[')) return hostname
    } catch {
      // not an IPv6 literal a URL parser accepts: fall through to the plain form
    }
  }
  return ip.toLowerCase()
}

/** `hostEndpoint` over `canonicalHostPart` — equal for two spellings of one endpoint. */
export function canonicalEndpoint(h: Pick<HostConfig, 'ip' | 'port'>): string {
  return `${canonicalHostPart(h.ip)}:${h.port}`
}

/** What an `/api/info` request went to: endpoint + token, captured before the request (spec D3). */
export interface HostRequestAt {
  endpoint: string
  token: string
}

export function requestAtOf(h: Pick<HostConfig, 'ip' | 'port' | 'token'>): HostRequestAt {
  return { endpoint: hostEndpoint(h), token: h.token ?? '' }
}

/** The host's mismatch flag, only while it still describes the host as it is now
 *  (same endpoint, same stored claim) — a re-point, local or by sync, never
 *  inherits an old flag (spec D3). */
export function selectDaemonIdMismatch(
  state: Pick<HostState, 'hosts' | 'runtime'>,
  hostId: string,
): DaemonIdMismatch | undefined {
  const host = state.hosts[hostId]
  const flag = state.runtime[hostId]?.daemonIdMismatch
  if (!host || !flag) return undefined
  if (flag.endpoint !== hostEndpoint(host) || flag.stored !== host.daemonId) return undefined
  return flag
}

/** True only while this session verified the host's current stored `daemonId` at its current
 *  endpoint (PR review #1). Stale after a re-point or a change of the stored value, local or by sync. */
export function selectDaemonIdVerified(state: Pick<HostState, 'hosts' | 'runtime'>, hostId: string): boolean {
  const host = state.hosts[hostId]
  const mark = state.runtime[hostId]?.daemonIdVerified
  return !!host && !!mark && !!host.daemonId && mark.endpoint === hostEndpoint(host) && mark.daemonId === host.daemonId
}

// (host, observed) pairs already warned about — one console.warn each (spec D3).
const warnedMismatch = new Set<string>()

function withoutVerification(runtime: Record<string, HostRuntime>, hostId: string): Record<string, HostRuntime> {
  const rt = runtime[hostId]
  if (!rt || (!('daemonIdMismatch' in rt) && !('daemonIdVerified' in rt))) return runtime
  const { daemonIdMismatch: _m, daemonIdVerified: _v, ...rest } = rt
  return { ...runtime, [hostId]: rest as HostRuntime }
}

function withVerified(runtime: Record<string, HostRuntime>, hostId: string, endpoint: string, daemonId: string): Record<string, HostRuntime> {
  const { daemonIdMismatch: _m, ...rest } = runtime[hostId] ?? ({} as HostRuntime)
  return { ...runtime, [hostId]: { ...rest, daemonIdVerified: { endpoint, daemonId } } as HostRuntime }
}

/**
 * The body of `observeDaemonId` (spec D3), pure over `{hosts, runtime}` so one `set()` can apply it to rows it has
 * just written (`applyHostTransfer`, plan R2). Returns the next `{hosts, runtime}`, or null when the answer is
 * dropped: the host is gone, `observed` fails `isValidDaemonId` ("" included), or the host has moved or changed
 * token since `atRequest`. Its only side effect is the one-time console.warn per (host, observed) mismatch.
 */
export function applyObservedDaemonId(
  state: Pick<HostState, 'hosts' | 'runtime'>,
  hostId: string,
  observed: string,
  atRequest: HostRequestAt,
): Pick<HostState, 'hosts' | 'runtime'> | null {
  const host = state.hosts[hostId]
  // An invalid id ("" included) is "no stable id": nothing learned, flagged or verified.
  if (!host || !isValidDaemonId(observed)) return null
  const now = requestAtOf(host)
  if (now.endpoint !== atRequest.endpoint || now.token !== atRequest.token) return null
  const endpointAtRequest = atRequest.endpoint
  if (!host.daemonId) {
    return {
      hosts: { ...state.hosts, [hostId]: { ...host, daemonId: observed } },
      runtime: withVerified(state.runtime, hostId, endpointAtRequest, observed),
    }
  }
  if (host.daemonId === observed) {
    return { hosts: state.hosts, runtime: withVerified(state.runtime, hostId, endpointAtRequest, observed) }
  }
  const key = `${hostId}\u0000${observed}`
  if (!warnedMismatch.has(key)) {
    warnedMismatch.add(key)
    console.warn(
      `[purdex] host ${hostId}: daemon at ${endpointAtRequest} reports host_id ${observed}, stored ${host.daemonId}`,
    )
  }
  const daemonIdMismatch: DaemonIdMismatch = { stored: host.daemonId, observed, endpoint: endpointAtRequest }
  const { daemonIdVerified: _v, ...rest } = state.runtime[hostId] ?? ({} as HostRuntime)
  return {
    hosts: state.hosts,
    runtime: { ...state.runtime, [hostId]: { ...rest, daemonIdMismatch } as HostRuntime },
  }
}

/** `applyHostTransfer`'s updater body: the next state, or null when the change no longer fits `state`. */
function transferPatch(
  state: Pick<HostState, 'hosts' | 'hostOrder' | 'runtime'>,
  change: TransferChange,
): { patch: Pick<HostState, 'hosts' | 'hostOrder' | 'runtime'>; created: string[]; overwritten: string[] } | null {
  const hosts: Record<string, HostConfig> = { ...state.hosts }
  const hostOrder = [...state.hostOrder]
  let runtime = state.runtime
  // [hostId, the daemonId it must end verified with, the endpoint + token it was observed at]
  const observe: [string, string, HostRequestAt][] = []
  const overwritten: string[] = []
  for (const o of change.overwrite) {
    const h = hosts[o.hostId]
    if (!h || overwritten.includes(o.hostId)) return null
    const at = requestAtOf(h)
    if (at.endpoint !== o.expect.endpoint || at.token !== o.expect.token || h.daemonId !== o.expect.daemonId) return null
    const next = sanitizeHostConfig({ ...h, name: o.name, ip: o.ip, port: o.port, token: o.token })
    hosts[o.hostId] = next
    overwritten.push(o.hostId)
    const nextAt = requestAtOf(next)
    // A new endpoint or token is a new connection (useMultiHostEventWs keys it on both and rebuilds it): nothing the
    // old one established — status, latency, attach gate, daemon / tmux state, its retry hook — describes the new
    // address, so the runtime starts over as a host that was never connected (no entry), exactly what a fresh host
    // has; below only `daemonIdVerified` is written back. A look / name-only overwrite keeps the connection as is.
    if (nextAt.endpoint !== at.endpoint || nextAt.token !== at.token) {
      const { [o.hostId]: _old, ...rest } = runtime
      runtime = rest
    }
    observe.push([o.hostId, o.expect.daemonId, nextAt])
  }
  const created: string[] = []
  for (const c of change.create) {
    const id = generateId()
    const host = sanitizeHostConfig({ id, name: c.name, ip: c.ip, port: c.port, token: c.token, order: hostOrder.length })
    hosts[id] = host
    hostOrder.push(id)
    created.push(id)
    observe.push([id, c.daemonId, requestAtOf(host)])
  }
  let working: Pick<HostState, 'hosts' | 'runtime'> = { hosts, runtime }
  for (const [id, daemonId, at] of observe) {
    const next = applyObservedDaemonId(working, id, daemonId, at)
    if (!next) return null
    working = next
  }
  const all = Object.values(working.hosts)
  for (const [id, daemonId] of observe) {
    if (!selectDaemonIdVerified(working, id)) return null
    // Canonical on both sides: a local row may hold another spelling of the same address (H4b PR #1397 critic).
    const endpoint = canonicalEndpoint(working.hosts[id])
    if (all.some((h) => h.id !== id && (canonicalEndpoint(h) === endpoint || h.daemonId === daemonId))) return null
  }
  return { patch: { hosts: working.hosts, hostOrder, runtime: working.runtime }, created, overwritten }
}

/* ─── Look writes (H2c-2): pure over a look entry ─── */

/** The look `HostConfig` holds, present fields only — the seed of an absent look entry (spec §4.3). */
function lookSeedOf(host: HostConfig): HostLookEntry {
  const { name, colors, color, icon, iconWeight } = host
  const seed: HostLookEntry = {}
  if (name !== undefined) seed.name = name
  if (colors !== undefined) seed.colors = colors
  if (color !== undefined) seed.color = color
  if (icon !== undefined) seed.icon = icon
  if (iconWeight !== undefined) seed.iconWeight = iconWeight
  return seed
}

/**
 * THE key a local host's look lives under — shared by the selector (`host-look.ts`) and the writers below, so both
 * always agree. Normally the host's wire id (`d1_…` once its daemonId is known, else its local id). One exception:
 * the host knows its daemonId, `looks[d1_…]` is absent and `looks[host.id]` is present — the re-resolve pass has not
 * moved that entry yet (lock busy, stores not hydrated). The local-id entry IS the look then: read there and write
 * there, and the pass later moves it to `d1_…` intact (codex critic review-mufd6v5g-2gh633). An existing `d1_…` entry
 * (e.g. synced from another device) always wins. Pure.
 */
export function lookKeyOf(host: HostConfig, looks: Record<string, HostLookEntry>): string {
  const wire = wireIdOfHost(host)
  if (wire !== host.id && !Object.hasOwn(looks, wire) && Object.hasOwn(looks, host.id)) return host.id
  return wire
}

/* ─── Transfer step 2 (H2c-3, plan §0.20): the received looks ─── */

/**
 * The look entries a transfer brings: per created / overwritten row, `{ name, ...look }` (a row without a look still
 * gives `{ name }`) under `syncIdOfSync(daemonId)` — the created row's observed id, the overwritten row's own. Every
 * such host ends with that daemonId, so the key is its `wireIdOfHost` (what `lookKeyOf` reads). Pure.
 *
 * Lives here, not in `host-transfer-plan.ts`: that module imports this one at runtime, so importing it back would be
 * a cycle.
 */
export function transferLookEntries(change: TransferChange): Record<string, HostLookEntry> {
  const out: Record<string, HostLookEntry> = {}
  const add = (daemonId: string, name: string, look: HostLookEntry | undefined) => {
    const key = syncIdOfSync(daemonId)
    if (!Object.hasOwn(out, key)) out[key] = { name, ...look }
  }
  for (const c of change.create) add(c.daemonId, c.name, c.look)
  for (const o of change.overwrite) add(o.expect.daemonId, o.name, o.look)
  return out
}

/**
 * Step 2 of a transfer, alone (also the dialog's Retry): writes each entry whose key has none (skip-if-present, so
 * it is idempotent and a look that arrived meanwhile is never overwritten). A key whose host still reads its look
 * under its LOCAL id (`lookKeyOf`: the re-key has not moved that entry yet) is skipped too — that entry IS the
 * workbench look, and a `d1_…` entry next to it would take its place. `'failed'` when the write throws.
 */
export function applyTransferLooks(entries: Record<string, HostLookEntry>): 'ok' | 'failed' {
  try {
    const looks = useHostLookStore.getState().looks
    const readElsewhere = new Set<string>()
    for (const host of Object.values(useHostStore.getState().hosts)) {
      const wire = wireIdOfHost(host)
      if (lookKeyOf(host, looks) !== wire) readElsewhere.add(wire)
    }
    const put: Record<string, HostLookEntry> = {}
    for (const key of Object.keys(entries)) if (!readElsewhere.has(key)) put[key] = entries[key]
    useHostLookStore.getState().putLooksIfAbsent(put)
    return 'ok'
  } catch {
    return 'failed'
  }
}

/** One look edit: the next entry, or `null` when the edit changes nothing (or is invalid) — nothing is written. */
type LookEdit = (look: HostLookEntry) => HostLookEntry | null

/**
 * Applies `edit` to the look of `hostId` in the look store: key = `lookKeyOf` (the host's current wire id, or its
 * local id while that entry awaits the re-key); base = the entry, or
 * the `HostConfig` seed when there is none. Unknown host / `null` edit → no write. Never writes `HostConfig`.
 */
function editHostLook(hosts: Record<string, HostConfig>, hostId: string, edit: LookEdit): void {
  const host = Object.hasOwn(hosts, hostId) ? hosts[hostId] : undefined
  if (!host) return
  useHostLookStore.getState().patchLook(lookKeyOf(host, useHostLookStore.getState().looks), (current) => {
    const base = current ?? lookSeedOf(host)
    const next = edit(base)
    // The same object back from `patchLook`'s callback writes nothing: an edit that changes nothing is no write.
    return next === null || sameValue(next, base) ? current : next
  })
}

/** Structural equality of two JSON-shaped values (key order ignored). */
function sameValue(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false
  const ka = Object.keys(a)
  const kb = Object.keys(b)
  if (ka.length !== kb.length) return false
  return ka.every((k) => Object.hasOwn(b, k) && sameValue((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]))
}

/** The look the colour / icon writers build on — the entry, else the seed (what the selector shows, option A). */
function currentLookOf(hosts: Record<string, HostConfig>, hostId: string): HostLookEntry | undefined {
  const host = Object.hasOwn(hosts, hostId) ? hosts[hostId] : undefined
  if (!host) return undefined
  const looks = useHostLookStore.getState().looks
  const key = lookKeyOf(host, looks)
  return Object.hasOwn(looks, key) ? looks[key] : lookSeedOf(host)
}

/** `setHostColorLayer`'s rules over a look (see the action's doc); `null` = no change. */
function withColorLayer(look: HostLookEntry, mode: HostColorMode, layer: HostColorLayerName, value: HostColorLayer | null): HostLookEntry | null {
  if (!isHostColorMode(mode) || !HOST_COLOR_LAYER_NAMES.includes(layer)) return null
  const colors = { ...look.colors }
  const existing = colors[mode]

  if (value === null) {
    if (layer === 'main') {
      // Clearing a mode that has no set is a no-op — unless it is `console` on a
      // look with a legacy color and no console set, where "clear" must drop
      // the legacy color regardless of other mode sets.
      if (!existing && !(mode === 'console' && 'color' in look)) return null
      delete colors[mode]
    } else if (existing && layer in existing) {
      const { [layer]: _dropped, ...rest } = existing
      colors[mode] = rest as HostColorSet
    } else return null
  } else {
    if (typeof value !== 'object' || value === null) return null
    const { alpha: rawAlpha, color: rawColor } = value as { alpha?: unknown; color?: unknown }
    if (typeof rawAlpha !== 'number' || !Number.isFinite(rawAlpha)) return null
    const alpha = clampHostAlpha(rawAlpha)
    let color: string | undefined
    if (rawColor !== undefined) {
      if (!isValidHostColor(rawColor)) return null
      color = rawColor.toLowerCase()
    }
    if (layer === 'main') {
      if (!color) return null
      colors[mode] = { ...existing, main: { color, alpha } }
    } else {
      if (!existing) return null
      colors[mode] = { ...existing, [layer]: color ? { color, alpha } : { alpha } }
    }
  }

  const { color: _legacy, ...next } = look
  delete next.colors
  if (Object.keys(colors).length > 0) next.colors = colors
  return next
}

/** `setHostIcon`'s rules over a look; `null` = no change. */
function withIcon(look: HostLookEntry, icon: string | null, weight: IconWeight | undefined): HostLookEntry | null {
  if (icon === null || (typeof icon === 'string' && icon.trim() === '')) {
    if (!('icon' in look) && !('iconWeight' in look)) return null
    const { icon: _i, iconWeight: _w, ...rest } = look
    return rest
  }
  // Anything that is not a real catalog name is dropped, not normalized:
  // `WorkspaceIcon` would render it as literal text in the host badge.
  if (!isPhosphorIconName(icon)) return null
  const nextWeight = isIconWeight(weight) ? weight : look.iconWeight
  if (look.icon === icon && look.iconWeight === nextWeight) return null
  const next: HostLookEntry = { ...look, icon }
  if (isIconWeight(weight)) next.iconWeight = weight
  return next
}

/** The rename over a look; `null` = no change (blank, or the same name). */
function withName(look: HostLookEntry, name: string): HostLookEntry | null {
  const trimmed = typeof name === 'string' ? name.trim() : ''
  if (trimmed === '' || look.name === trimmed) return null
  return { ...look, name: trimmed }
}

/** Exact-endpoint lookup shared by registerLocalHost and the Local daemon UI
 *  (spec §3.3). Strict ip+port equality — 127.0.0.1 and a Tailscale IP are
 *  two endpoints, never merged. */
export function findHostByEndpoint(hosts: Record<string, HostConfig>, ip: string, port: number): HostConfig | undefined {
  return Object.values(hosts).find((h) => h.ip === ip && h.port === port)
}

/** Dev host as the Development page must see it: null unless the persisted
 *  id still resolves to a host (deleted locally or dropped by a sync
 *  full-replace → unset, user re-picks). Pure so hooks can pass it directly. */
export function selectDevHostId(state: Pick<HostState, 'devHostId' | 'hosts'>): string | null {
  return state.devHostId !== null && state.hosts[state.devHostId] ? state.devHostId : null
}

export const useHostStore = create<HostState>()(
  persist(
    (set, get) => ({
      ...createDefaultState(),

      addHost: (opts) => {
        const id = opts.id ?? generateId()
        // Dedup: if host already exists, just return existing id
        if (get().hosts[id]) return id
        const order = get().hostOrder.length
         
        const { id: _discardId, ...restOpts } = opts
        const host: HostConfig = { id, ...restOpts, order }
        delete host.daemonId
        delete host.syncAliases
        set((state) => ({
          hosts: { ...state.hosts, [id]: host },
          hostOrder: [...state.hostOrder, id],
        }))
        return id
      },

      updateHost: (hostId, updates) =>
        set((state) => {
          const host = state.hosts[hostId]
          if (!host) return state
          const { daemonId: _ignored, ...allowed } = updates as Partial<HostConfig>
          const next: HostConfig = { ...host, ...allowed }
          const repoint = hostEndpoint(next) !== hostEndpoint(host)
          // A re-point is learned afresh for the new endpoint (spec D3).
          if (repoint) delete next.daemonId
          return {
            hosts: { ...state.hosts, [hostId]: next },
            ...(repoint ? { runtime: withoutVerification(state.runtime, hostId) } : {}),
          }
        }),

      observeDaemonId: (hostId, observed, atRequest) =>
        set((state) => applyObservedDaemonId(state, hostId, observed, atRequest) ?? state),

      applyHostTransfer: (change) => {
        // `as`: assigned inside the updater, so the declaration must not narrow it to `null`.
        let applied = null as { created: string[]; overwritten: string[] } | null
        // Step 1: the host store, one set, all or nothing.
        set((state) => {
          try {
            const next = transferPatch(state, change)
            if (!next) return state
            applied = { created: next.created, overwritten: next.overwritten }
            return next.patch
          } catch {
            applied = null
            return state
          }
        })
        if (applied === null) return { kind: 'stale' }
        // Step 2: the look store — only after step 1 applied; its failure is reported, never propagated.
        const { created, overwritten } = applied
        return { kind: 'applied', created, overwritten, looks: applyTransferLooks(transferLookEntries(change)) }
      },

      setHostColor: (hostId, color) => {
        const { setHostColorLayer, clearHostColorMode, hosts } = get()
        if (color === null) {
          clearHostColorMode(hostId, 'console')
          return
        }
        // The alpha the user sees now: the look's (entry, else the HostConfig seed — what `hostLookOf` shows).
        const alpha = currentLookOf(hosts, hostId)?.colors?.console?.main.alpha ?? HOST_COLOR_ALPHA_DEFAULTS.main
        setHostColorLayer(hostId, 'console', 'main', { color, alpha })
      },

      setHostColorLayer: (hostId, mode, layer, value) =>
        editHostLook(get().hosts, hostId, (look) => withColorLayer(look, mode, layer, value)),

      clearHostColorMode: (hostId, mode) => get().setHostColorLayer(hostId, mode, 'main', null),

      setHostIcon: (hostId, icon, weight) => editHostLook(get().hosts, hostId, (look) => withIcon(look, icon, weight)),

      setHostName: (hostId, name) => editHostLook(get().hosts, hostId, (look) => withName(look, name)),

      seedHostLook: (hostId) => {
        const hosts = get().hosts
        const host = Object.hasOwn(hosts, hostId) ? hosts[hostId] : undefined
        if (!host) return
        useHostLookStore.getState().putLooksIfAbsent({ [lookKeyOf(host, useHostLookStore.getState().looks)]: lookSeedOf(host) })
      },

      // Idempotent registration used by the local-daemon installer
      // (spec 2026-09-14 §3.4): one host per endpoint, and a token is only
      // filled in when the existing one is empty — never overwritten.
      registerLocalHost: ({ url, token, hostname }) => {
        const u = new URL(url)
        const ip = u.hostname
        // URL drops a default port (":80" / ":443") — restore it by scheme.
        const port = u.port ? Number(u.port) : (u.protocol === 'https:' ? 443 : 80)
        const existing = findHostByEndpoint(get().hosts, ip, port)
        if (existing) {
          if (!existing.token) get().updateHost(existing.id, { token })
          return existing.id
        }
        const id = get().addHost({ name: hostname, ip, port, token })
        get().seedHostLook(id)
        return id
      },

      removeHost: (hostId) =>
        set((state) => {
          if (Object.keys(state.hosts).length <= 1) return state
           
          const { [hostId]: _, ...rest } = state.hosts
          const newOrder = state.hostOrder.filter((id) => id !== hostId)
           
          const { [hostId]: __, ...restRuntime } = state.runtime
          const activeHostId =
            state.activeHostId === hostId ? newOrder[0] ?? null : state.activeHostId
          return {
            hosts: rest,
            hostOrder: newOrder,
            runtime: restRuntime,
            activeHostId,
            devHostId: state.devHostId === hostId ? null : state.devHostId,
          }
        }),

      reorderHosts: (orderedIds) =>
        set((state) => {
          const hosts = { ...state.hosts }
          orderedIds.forEach((id, i) => {
            if (hosts[id]) hosts[id] = { ...hosts[id], order: i }
          })
          return { hosts, hostOrder: orderedIds }
        }),

      setActiveHost: (hostId) =>
        set((state) => (state.hosts[hostId] ? { activeHostId: hostId } : state)),

      setDevHost: (hostId) =>
        set((state) => (hostId === null || state.hosts[hostId] ? { devHostId: hostId } : state)),

      setRuntime: (hostId, runtime) =>
        set((state) => ({
          runtime: {
            ...state.runtime,
            [hostId]: { ...state.runtime[hostId], ...runtime } as HostRuntime,
          },
        })),

      getDaemonBase: (hostId) => {
        const host = get().hosts[hostId]
        if (host) return `http://${host.ip}:${host.port}`
        const fallbackId = get().activeHostId ?? get().hostOrder[0]
        const fallback = fallbackId ? get().hosts[fallbackId] : undefined
        if (!fallback) return 'http://127.0.0.1:7860'
        return `http://${fallback.ip}:${fallback.port}`
      },

      getWsBase: (hostId) => {
        const host = get().hosts[hostId]
        if (host) return `ws://${host.ip}:${host.port}`
        const fallbackId = get().activeHostId ?? get().hostOrder[0]
        const fallback = fallbackId ? get().hosts[fallbackId] : undefined
        if (!fallback) return 'ws://127.0.0.1:7860'
        return `ws://${fallback.ip}:${fallback.port}`
      },

      getAuthHeaders: (hostId) => {
        const host = get().hosts[hostId]
        if (!host?.token) return {} as Record<string, string>
        return { Authorization: `Bearer ${host.token}` }
      },

      reset: () => {
        warnedMismatch.clear()
        set(createDefaultState())
        useHostLookStore.setState({ looks: {} })
      },
    }),
    {
      name: STORAGE_KEYS.HOSTS,
      storage: purdexStorage,
      version: 1,
      // Default shallow merge, plus dropping invalid host color / icon fields from
      // persisted (possibly corrupted / cross-tab) state before it reaches the store.
      merge: (persisted, current) => {
        const p = (persisted ?? {}) as Partial<HostState>
        if (!p.hosts || typeof p.hosts !== 'object') return { ...current, ...p }
        const hosts: Record<string, HostConfig> = {}
        for (const [id, host] of Object.entries(p.hosts)) hosts[id] = sanitizeHostConfig(host)
        return { ...current, ...p, hosts }
      },
      partialize: (state) => ({
        hosts: state.hosts,
        hostOrder: state.hostOrder,
        activeHostId: state.activeHostId,
        devHostId: state.devHostId,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.HOSTS, useHostStore)
