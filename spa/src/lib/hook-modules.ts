import { hostFetch } from './host-api'
import type { NormalizedEvent } from '../stores/useAgentStore'

/* ─── Types ─── */

export interface HookModuleEvent {
  installed: boolean
  command?: string | null
  futureOnly?: boolean
}

export interface HookModuleStatus {
  installed: boolean
  /**
   * Managed is true when the host has pdx-owned artifacts on disk even
   * if the install has drifted. Drives whether the Remove button is
   * enabled (Finding #2).
   */
  managed?: boolean
  /**
   * UpgradesAvailable lists declared-but-not-installed FutureOnly
   * events. Non-empty while installed=true means "install is valid
   * but new events are available" — drives the Upgrade hint and
   * keeps the Install button enabled on legacy pre-expansion users
   * (Finding #4).
   */
  upgradesAvailable?: string[]
  events: Record<string, HookModuleEvent>
  issues?: string[]
  agentVersion?: string
  supportedVersion?: string
  exceedsSupport?: boolean
}

export interface HookModule {
  id: string
  labelKey: string
  descKey: string
  fetchStatus: (hostId: string) => Promise<HookModuleStatus>
  setup: (hostId: string, action: 'install' | 'remove') => Promise<HookModuleStatus>
  getLastTrigger?: (hostId: string, events: Record<string, NormalizedEvent>) => Record<string, number> | null
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
  labelKey: 'hosts.cc_hooks',
  descKey: 'hosts.cc_hooks_desc',
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
      if (!key.startsWith(prefix) || event.agent_type !== 'cc') continue
      const existing = result[event.raw_event_name]
      if (!existing || event.broadcast_ts > existing) {
        result[event.raw_event_name] = event.broadcast_ts
      }
    }
    return Object.keys(result).length > 0 ? result : null
  },
}

const CODEX_HOOKS: HookModule = {
  id: 'codex',
  labelKey: 'hosts.codex_hooks',
  descKey: 'hosts.codex_hooks_desc',
  fetchStatus: (hostId) => hookFetch(hostId, '/api/hooks/codex/status'),
  setup: (hostId, action) =>
    hookFetch(hostId, '/api/hooks/codex/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    }),
  getLastTrigger: (hostId, events) => {
    const prefix = `${hostId}:`
    const result: Record<string, number> = {}
    for (const [key, event] of Object.entries(events)) {
      if (!key.startsWith(prefix) || event.agent_type !== 'codex') continue
      const existing = result[event.raw_event_name]
      if (!existing || event.broadcast_ts > existing) {
        result[event.raw_event_name] = event.broadcast_ts
      }
    }
    return Object.keys(result).length > 0 ? result : null
  },
}

const OPENCODE_HOOKS: HookModule = {
  id: 'opencode',
  labelKey: 'hosts.opencode_hooks',
  descKey: 'hosts.opencode_hooks_desc',
  fetchStatus: (hostId) => hookFetch(hostId, '/api/hooks/opencode/status'),
  setup: (hostId, action) =>
    hookFetch(hostId, '/api/hooks/opencode/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    }),
  getLastTrigger: (hostId, events) => {
    const prefix = `${hostId}:`
    const result: Record<string, number> = {}
    for (const [key, event] of Object.entries(events)) {
      if (!key.startsWith(prefix) || event.agent_type !== 'opencode') continue
      const existing = result[event.raw_event_name]
      if (!existing || event.broadcast_ts > existing) {
        result[event.raw_event_name] = event.broadcast_ts
      }
    }
    return Object.keys(result).length > 0 ? result : null
  },
}

export const HOOK_MODULES: HookModule[] = [TMUX_HOOKS, CC_HOOKS, CODEX_HOOKS, OPENCODE_HOOKS]
