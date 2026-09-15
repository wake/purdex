// spa/src/stores/useHostConfigStore.ts — per-host cache of the daemon's
// projects / commands / resume templates (spec §4.1). Not persisted, not
// synced: the daemon is the source of truth and every write is a CAS.
import { create } from 'zustand'
import {
  fetchHostConfig,
  HostConfigApiError,
  HostConfigConflictError,
  putHostConfig,
  type HostCommand,
  type HostConfigCollection,
  type HostConfigCollectionItems,
  type HostProject,
  type ResumeTemplateOverrides,
} from '../lib/host-config-api'
import { useHostStore } from './useHostStore'

export type HostConfigStatus = 'idle' | 'loading' | 'ready' | 'unsupported' | 'error'

export interface HostConfigEntry {
  status: HostConfigStatus
  projects: HostProject[]
  commands: HostCommand[]
  resumeTemplates: ResumeTemplateOverrides
  revisions: { projects: number; commands: number; resumeTemplates: number }
  error?: string
}

export function emptyHostConfigEntry(status: HostConfigStatus = 'idle'): HostConfigEntry {
  return { status, projects: [], commands: [], resumeTemplates: {}, revisions: { projects: 0, commands: 0, resumeTemplates: 0 } }
}

/** Stable fallback for selectors — a fresh object per call would loop useSyncExternalStore. */
export const EMPTY_HOST_CONFIG: HostConfigEntry = Object.freeze(emptyHostConfigEntry()) as HostConfigEntry

type ConfigField = keyof HostConfigEntry['revisions']

interface HostConfigState {
  byHost: Record<string, HostConfigEntry>
  load: (hostId: string) => Promise<void>
  ensureLoaded: (hostId: string) => Promise<void>
  saveProjects: (hostId: string, items: HostProject[]) => Promise<void>
  saveCommands: (hostId: string, items: HostCommand[]) => Promise<void>
  saveResumeTemplates: (hostId: string, items: ResumeTemplateOverrides) => Promise<void>
  forget: (hostId: string) => void
}

/**
 * What a request was asked of, and what makes its answer still usable.
 *
 * A host id is not an endpoint: the same id can point at another daemon a
 * moment later (the address, the port or the token changed), or at no daemon at
 * all (the host was removed). A response that lands after either happened
 * describes a machine this cache no longer speaks to, and writing it here is
 * silent corruption — the next save would PUT the previous daemon's items,
 * under the previous daemon's revision, to the new one.
 *
 * So every request carries the endpoint it was sent to plus a generation, and
 * nothing it returns reaches the store unless both still hold.
 */
interface RequestToken { gen: number; endpoint: string }

interface Inflight { promise: Promise<void>; endpoint: string; abort: AbortController }

const inflight = new Map<string, Inflight>()
const generations = new Map<string, number>()

/** The address a request for `hostId` would go to, or `null` when there is none. */
function endpointOf(hostId: string): string | null {
  const h = useHostStore.getState().hosts[hostId]
  return h ? `${h.ip}:${h.port}:${h.token ?? ''}` : null
}

function beginRequest(hostId: string, endpoint: string): RequestToken {
  return { gen: generations.get(hostId) ?? 0, endpoint }
}

/** True only while the host still exists at the same endpoint, un-invalidated. */
function stillCurrent(hostId: string, token: RequestToken): boolean {
  if ((generations.get(hostId) ?? 0) !== token.gen) return false
  return endpointOf(hostId) === token.endpoint
}

/** Abandon every answer in flight for `hostId`; aborts the fetch when it can. */
function invalidate(hostId: string): void {
  generations.set(hostId, (generations.get(hostId) ?? 0) + 1)
  const running = inflight.get(hostId)
  if (!running) return
  inflight.delete(hostId)
  running.abort.abort()
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

export const useHostConfigStore = create<HostConfigState>()((set, get) => {
  const patch = (hostId: string, update: Partial<HostConfigEntry>) =>
    set((s) => ({ byHost: { ...s.byHost, [hostId]: { ...(s.byHost[hostId] ?? emptyHostConfigEntry()), ...update } } }))

  async function save<C extends HostConfigCollection>(
    hostId: string,
    collection: C,
    field: ConfigField,
    items: HostConfigCollectionItems[C],
  ): Promise<void> {
    const entry = get().byHost[hostId]
    if (!entry || entry.status !== 'ready') throw new Error(`host config for ${hostId} is not loaded`)
    const endpoint = endpointOf(hostId)
    if (endpoint === null) throw new Error(`host ${hostId} is not configured`)
    const token = beginRequest(hostId, endpoint)
    try {
      const stored = await putHostConfig(hostId, collection, items, entry.revisions[field])
      // The PUT went to the daemon that was there when it was sent. If that is
      // no longer this host's daemon, its answer says nothing about the one
      // whose copy the cache now holds.
      if (!stillCurrent(hostId, token)) return
      const now = get().byHost[hostId] ?? entry
      patch(hostId, { [field]: stored.items, revisions: { ...now.revisions, [field]: stored.revision } })
    } catch (err) {
      if (err instanceof HostConfigConflictError && stillCurrent(hostId, token)) {
        const now = get().byHost[hostId] ?? entry
        patch(hostId, { [field]: err.current.items, revisions: { ...now.revisions, [field]: err.current.revision } })
      }
      throw err
    }
  }

  return {
    byHost: {},

    load: (hostId) => {
      const endpoint = endpointOf(hostId)
      // A host with no address is not a host this cache can hold anything for.
      if (endpoint === null) return Promise.resolve()
      const running = inflight.get(hostId)
      // Dedupe only within one endpoint: a request sent to the old daemon can
      // never answer for the new one, so it is abandoned rather than awaited.
      if (running) {
        if (running.endpoint === endpoint) return running.promise
        invalidate(hostId)
      }
      const abort = new AbortController()
      const token = beginRequest(hostId, endpoint)
      const run = (async () => {
        // A refresh of a ready host keeps showing its data while it loads.
        if (get().byHost[hostId]?.status !== 'ready') patch(hostId, { status: 'loading', error: undefined })
        try {
          const p = await fetchHostConfig(hostId, abort.signal)
          if (!stillCurrent(hostId, token)) return
          patch(hostId, {
            status: 'ready',
            error: undefined,
            projects: p.projects.items ?? [],
            commands: p.commands.items ?? [],
            resumeTemplates: p.resumeTemplates.items ?? {},
            revisions: { projects: p.projects.revision, commands: p.commands.revision, resumeTemplates: p.resumeTemplates.revision },
          })
        } catch (err) {
          // A failure is as endpoint-specific as a success: the old daemon
          // being unreachable is not this host's status any more.
          if (!stillCurrent(hostId, token)) return
          if (err instanceof HostConfigApiError && err.status === 404) {
            patch(hostId, { ...emptyHostConfigEntry('unsupported'), error: undefined })
          } else {
            patch(hostId, { status: 'error', error: errorText(err) })
          }
        } finally {
          // Only this request's own entry: a newer one may already hold the slot.
          if (inflight.get(hostId)?.abort === abort) inflight.delete(hostId)
        }
      })()
      inflight.set(hostId, { promise: run, endpoint, abort })
      return run
    },

    ensureLoaded: async (hostId) => {
      const status = get().byHost[hostId]?.status
      if (status === 'ready' || status === 'unsupported') return
      try { await get().load(hostId) } catch { /* load never throws; belt and braces */ }
    },

    saveProjects: (hostId, items) => save(hostId, 'projects', 'projects', items),
    saveCommands: (hostId, items) => save(hostId, 'commands', 'commands', items),
    saveResumeTemplates: (hostId, items) => save(hostId, 'resume-templates', 'resumeTemplates', items),

    forget: (hostId) => {
      // Dropping the entry is only half of it: an answer already in flight
      // would put the forgotten daemon's copy straight back.
      invalidate(hostId)
      set((s) => {
        if (!(hostId in s.byHost)) return s
        const rest = { ...s.byHost }
        delete rest[hostId]
        return { byHost: rest }
      })
    },
  }
})
