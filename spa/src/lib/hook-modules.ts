import { hostFetch } from './host-api'

/* ─── Types ─── */

export interface HookModuleEvent {
  installed: boolean
  command?: string | null
}

export interface HookModuleStatus {
  installed: boolean
  events: Record<string, HookModuleEvent>
  issues?: string[]
}

export interface HookModule {
  id: string
  labelKey: string
  descKey: string
  fetchStatus: (hostId: string) => Promise<HookModuleStatus>
  setup: (hostId: string, action: 'install' | 'remove') => Promise<HookModuleStatus>
  getLastTrigger?: (hostId: string, events: Record<string, { event_name: string; broadcast_ts: number }>) => Record<string, number> | null
}

/* ─── Shared fetch helper ─── */

async function hookFetch(hostId: string, path: string, init?: RequestInit): Promise<HookModuleStatus> {
  const res = await hostFetch(hostId, path, init)
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

/* ─── Module configs ─── */

const TMUX_HOOKS: HookModule = {
  id: 'tmux',
  labelKey: 'hosts.tmux_hooks',
  descKey: 'hosts.tmux_hooks_desc',
  fetchStatus: (hostId) => hookFetch(hostId, '/api/hooks/tmux/status'),
  setup: (hostId, action) =>
    hookFetch(hostId, '/api/hooks/tmux/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    }),
}

const CC_HOOKS: HookModule = {
  id: 'cc',
  labelKey: 'hosts.agent_hooks',
  descKey: 'hosts.agent_hooks_desc',
  fetchStatus: (hostId) => hookFetch(hostId, '/api/hooks/cc/status'),
  setup: (hostId, action) =>
    hookFetch(hostId, '/api/hooks/cc/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    }),
  getLastTrigger: (hostId, events) => {
    const prefix = `${hostId}:`
    const result: Record<string, number> = {}
    for (const [key, event] of Object.entries(events)) {
      if (!key.startsWith(prefix)) continue
      const existing = result[event.event_name]
      if (!existing || event.broadcast_ts > existing) {
        result[event.event_name] = event.broadcast_ts
      }
    }
    return Object.keys(result).length > 0 ? result : null
  },
}

export const HOOK_MODULES: HookModule[] = [TMUX_HOOKS, CC_HOOKS]
