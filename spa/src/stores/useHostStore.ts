import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { generateId } from '../lib/id'

/* ─── Interfaces ─── */

export interface HostConfig {
  id: string
  name: string
  ip: string
  port: number
  token?: string
  order: number
}

export interface HostRuntime {
  status: 'connected' | 'disconnected' | 'reconnecting'
  latency?: number
  info?: HostInfo
}

export interface HostInfo {
  tbox_version: string
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

  addHost: (opts: { name: string; ip: string; port: number; token?: string }) => string
  updateHost: (hostId: string, updates: Partial<Pick<HostConfig, 'name' | 'ip' | 'port' | 'token'>>) => void
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
        const id = generateId()
        const order = get().hostOrder.length
        const host: HostConfig = { id, ...opts, order }
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

      reset: () => set(createDefaultState()),
    }),
    {
      name: 'tbox-hosts',
      version: 1,
      migrate: (persisted: any, version: number) => {
        if (version === 0 || version === undefined) {
          // v0 → v1: address→ip, fixed 'local' key → keep as-is but restructure
          const oldHosts = persisted?.hosts ?? {}
          const newHosts: Record<string, any> = {}
          const hostOrder: string[] = []
          let order = 0
          for (const [id, host] of Object.entries(oldHosts) as any[]) {
            newHosts[id] = {
              id,
              name: host.name ?? 'Host',
              ip: host.address ?? host.ip ?? '127.0.0.1',
              port: host.port ?? 7860,
              token: host.token,
              order: order++,
            }
            hostOrder.push(id)
          }
          // If empty, let createDefaultState handle it
          if (hostOrder.length === 0) return {}
          return { hosts: newHosts, hostOrder, activeHostId: hostOrder[0] }
        }
        return persisted
      },
      partialize: (state) => ({
        hosts: state.hosts,
        hostOrder: state.hostOrder,
        activeHostId: state.activeHostId,
      }),
    },
  ),
)
