import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { generateId } from '../lib/id'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import { deriveDaemonBase, deriveWsBase } from '../lib/host-endpoint'
import { getPlatformCapabilities } from '../lib/platform'

/* ─── Interfaces ─── */

export interface HostConfig {
  id: string
  name: string
  ip: string
  port: number
  // Connection scheme. Absent is treated as 'http' for backward compatibility
  // with persisted hosts. 'https' is required for browser clients over TLS
  // (e.g. purdex.mlab.host) so WS derives to wss:// and avoids mixed-content.
  scheme?: 'http' | 'https'
  // `null` is the explicit "token cleared, re-auth required" sentinel written by
  // the sync deserialize path when a host is new or its endpoint (ip/port) changed.
  // Distinct from `undefined` (field simply absent); `null` survives JSON round-trips.
  token?: string | null
  order: number
}

export interface HostRuntime {
  status: 'connected' | 'disconnected' | 'reconnecting' | 'auth-error'
  latency?: number
  info?: HostInfo
  daemonState?: 'connected' | 'refused' | 'unreachable' | 'auth-error'
  tmuxState?: 'ok' | 'unavailable'
  manualRetry?: () => Promise<void> | void  // safe: runtime excluded from persist partialize
}

export interface HostInfo {
  host_id: string
  tmux_instance: string
  purdex_version: string
  tmux_version: string
  os: string
  arch: string
}

/* ─── Store ─── */

interface HostState {
  hosts: Record<string, HostConfig>
  hostOrder: string[]
  runtime: Record<string, HostRuntime>
  activeHostId: string | null

  addHost: (opts: { id?: string; name: string; ip: string; port: number; scheme?: 'http' | 'https'; token?: string | null }) => string
  updateHost: (hostId: string, updates: Partial<Pick<HostConfig, 'name' | 'ip' | 'port' | 'scheme' | 'token'>>) => void
  removeHost: (hostId: string) => void
  reorderHosts: (orderedIds: string[]) => void
  setActiveHost: (hostId: string) => void
  setRuntime: (hostId: string, runtime: Partial<HostRuntime>) => void
  getDaemonBase: (hostId: string) => string
  getWsBase: (hostId: string) => string
  getAuthHeaders: (hostId: string) => Record<string, string>
  reset: () => void
}

const DEFAULT_ID = generateId()

function createDefaultState() {
  // Seed the local mlab default host only in the Electron desktop app, where
  // `100.64.0.2:7860` is the canonical local daemon. A browser client must
  // start with no hosts: on an https origin a hardcoded http host would become
  // the active host and fail with mixed-content + 401 before the user reaches
  // the origin-suggestion flow (HostPage). The empty state routes a fresh web
  // user straight to that flow. Persist rehydration overrides this for any
  // returning user with saved hosts, so this only affects first load.
  if (!getPlatformCapabilities().isElectron) {
    return {
      hosts: {} as Record<string, HostConfig>,
      hostOrder: [] as string[],
      runtime: {} as Record<string, HostRuntime>,
      activeHostId: null as string | null,
    }
  }
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
  }
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
          return {
            hosts: { ...state.hosts, [hostId]: { ...host, ...updates } },
          }
        }),

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

      setRuntime: (hostId, runtime) =>
        set((state) => ({
          runtime: {
            ...state.runtime,
            [hostId]: { ...state.runtime[hostId], ...runtime } as HostRuntime,
          },
        })),

      getDaemonBase: (hostId) => {
        const host = get().hosts[hostId]
        if (host) return deriveDaemonBase(host)
        const fallbackId = get().activeHostId ?? get().hostOrder[0]
        const fallback = fallbackId ? get().hosts[fallbackId] : undefined
        if (!fallback) return 'http://127.0.0.1:7860'
        return deriveDaemonBase(fallback)
      },

      getWsBase: (hostId) => {
        const host = get().hosts[hostId]
        if (host) return deriveWsBase(host)
        const fallbackId = get().activeHostId ?? get().hostOrder[0]
        const fallback = fallbackId ? get().hosts[fallbackId] : undefined
        if (!fallback) return 'ws://127.0.0.1:7860'
        return deriveWsBase(fallback)
      },

      getAuthHeaders: (hostId) => {
        const host = get().hosts[hostId]
        if (!host?.token) return {} as Record<string, string>
        return { Authorization: `Bearer ${host.token}` }
      },

      reset: () => set(createDefaultState()),
    }),
    {
      name: STORAGE_KEYS.HOSTS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        hosts: state.hosts,
        hostOrder: state.hostOrder,
        activeHostId: state.activeHostId,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.HOSTS, useHostStore)
