import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { generateId } from '../lib/id'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
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
   * The single entry point for every `/api/info` answer (spec D3). `endpointAtRequest`
   * is the host's `ip:port` captured before the request; an answer for a host that is
   * gone or has moved since is dropped, as is an empty `observed`.
   */
  observeDaemonId: (hostId: string, observed: string, endpointAtRequest: string) => void
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
  registerLocalHost: (result: { url: string; token: string; hostname: string }) => string
  removeHost: (hostId: string) => void
  reorderHosts: (orderedIds: string[]) => void
  setActiveHost: (hostId: string) => void
  setDevHost: (hostId: string | null) => void
  setRuntime: (hostId: string, runtime: Partial<HostRuntime>) => void
  getDaemonBase: (hostId: string) => string
  getWsBase: (hostId: string) => string
  getAuthHeaders: (hostId: string) => Record<string, string>
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

/** `ip:port` — the endpoint key `observeDaemonId` guards on. */
export function hostEndpoint(h: Pick<HostConfig, 'ip' | 'port'>): string {
  return `${h.ip}:${h.port}`
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

// (host, observed) pairs already warned about — one console.warn each (spec D3).
const warnedMismatch = new Set<string>()

function withoutMismatch(runtime: Record<string, HostRuntime>, hostId: string): Record<string, HostRuntime> {
  const rt = runtime[hostId]
  if (!rt || !('daemonIdMismatch' in rt)) return runtime
  const { daemonIdMismatch: _m, ...rest } = rt
  return { ...runtime, [hostId]: rest as HostRuntime }
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
            ...(repoint ? { runtime: withoutMismatch(state.runtime, hostId) } : {}),
          }
        }),

      observeDaemonId: (hostId, observed, endpointAtRequest) =>
        set((state) => {
          const host = state.hosts[hostId]
          if (!host || !observed || hostEndpoint(host) !== endpointAtRequest) return state
          if (!host.daemonId) {
            return {
              hosts: { ...state.hosts, [hostId]: { ...host, daemonId: observed } },
              runtime: withoutMismatch(state.runtime, hostId),
            }
          }
          if (host.daemonId === observed) {
            const runtime = withoutMismatch(state.runtime, hostId)
            return runtime === state.runtime ? state : { runtime }
          }
          const key = `${hostId}\u0000${observed}`
          if (!warnedMismatch.has(key)) {
            warnedMismatch.add(key)
            console.warn(
              `[purdex] host ${hostId}: daemon at ${endpointAtRequest} reports host_id ${observed}, stored ${host.daemonId}`,
            )
          }
          const daemonIdMismatch: DaemonIdMismatch = { stored: host.daemonId, observed, endpoint: endpointAtRequest }
          return {
            runtime: { ...state.runtime, [hostId]: { ...state.runtime[hostId], daemonIdMismatch } as HostRuntime },
          }
        }),

      setHostColor: (hostId, color) => {
        const { setHostColorLayer, clearHostColorMode, hosts } = get()
        if (color === null) {
          clearHostColorMode(hostId, 'console')
          return
        }
        const alpha = hosts[hostId]?.colors?.console?.main.alpha ?? HOST_COLOR_ALPHA_DEFAULTS.main
        setHostColorLayer(hostId, 'console', 'main', { color, alpha })
      },

      setHostColorLayer: (hostId, mode, layer, value) =>
        set((state) => {
          const host = state.hosts[hostId]
          if (!host || !isHostColorMode(mode) || !HOST_COLOR_LAYER_NAMES.includes(layer)) return state
          const colors = { ...host.colors }
          const existing = colors[mode]

          if (value === null) {
            if (layer === 'main') {
              // Clearing a mode that has no set is a no-op — unless it is `console` on a
              // host with a legacy color and no console set, where "clear" must drop
              // the legacy color regardless of other mode sets.
              if (!existing && !(mode === 'console' && 'color' in host)) return state
              delete colors[mode]
            } else if (existing && layer in existing) {
              const { [layer]: _dropped, ...rest } = existing
              colors[mode] = rest as HostColorSet
            } else return state
          } else {
            if (typeof value !== 'object' || value === null) return state
            const { alpha: rawAlpha, color: rawColor } = value as { alpha?: unknown; color?: unknown }
            if (typeof rawAlpha !== 'number' || !Number.isFinite(rawAlpha)) return state
            const alpha = clampHostAlpha(rawAlpha)
            let color: string | undefined
            if (rawColor !== undefined) {
              if (!isValidHostColor(rawColor)) return state
              color = rawColor.toLowerCase()
            }
            if (layer === 'main') {
              if (!color) return state
              colors[mode] = { ...existing, main: { color, alpha } }
            } else {
              if (!existing) return state
              colors[mode] = { ...existing, [layer]: color ? { color, alpha } : { alpha } }
            }
          }

          const { color: _legacy, ...next } = host as HostConfig
          delete next.colors
          if (Object.keys(colors).length > 0) next.colors = colors
          return { hosts: { ...state.hosts, [hostId]: next as HostConfig } }
        }),

      clearHostColorMode: (hostId, mode) => get().setHostColorLayer(hostId, mode, 'main', null),

      setHostIcon: (hostId, icon, weight) =>
        set((state) => {
          const host = state.hosts[hostId]
          if (!host) return state
          if (icon === null || (typeof icon === 'string' && icon.trim() === '')) {
            const { icon: _i, iconWeight: _w, ...rest } = host
            return { hosts: { ...state.hosts, [hostId]: rest } }
          }
          // Anything that is not a real catalog name is dropped, not normalized:
          // `WorkspaceIcon` would render it as literal text in the host badge.
          if (!isPhosphorIconName(icon)) return state
          const next: HostConfig = { ...host, icon }
          if (isIconWeight(weight)) next.iconWeight = weight
          return { hosts: { ...state.hosts, [hostId]: next } }
        }),

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
        return get().addHost({ name: hostname, ip, port, token })
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
