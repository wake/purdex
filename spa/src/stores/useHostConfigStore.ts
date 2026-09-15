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

const inflight = new Map<string, Promise<void>>()

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
    try {
      const stored = await putHostConfig(hostId, collection, items, entry.revisions[field])
      const now = get().byHost[hostId] ?? entry
      patch(hostId, { [field]: stored.items, revisions: { ...now.revisions, [field]: stored.revision } })
    } catch (err) {
      if (err instanceof HostConfigConflictError) {
        const now = get().byHost[hostId] ?? entry
        patch(hostId, { [field]: err.current.items, revisions: { ...now.revisions, [field]: err.current.revision } })
      }
      throw err
    }
  }

  return {
    byHost: {},

    load: (hostId) => {
      const running = inflight.get(hostId)
      if (running) return running
      const run = (async () => {
        // A refresh of a ready host keeps showing its data while it loads.
        if (get().byHost[hostId]?.status !== 'ready') patch(hostId, { status: 'loading', error: undefined })
        try {
          const p = await fetchHostConfig(hostId)
          patch(hostId, {
            status: 'ready',
            error: undefined,
            projects: p.projects.items ?? [],
            commands: p.commands.items ?? [],
            resumeTemplates: p.resumeTemplates.items ?? {},
            revisions: { projects: p.projects.revision, commands: p.commands.revision, resumeTemplates: p.resumeTemplates.revision },
          })
        } catch (err) {
          if (err instanceof HostConfigApiError && err.status === 404) {
            patch(hostId, { ...emptyHostConfigEntry('unsupported'), error: undefined })
          } else {
            patch(hostId, { status: 'error', error: errorText(err) })
          }
        } finally {
          inflight.delete(hostId)
        }
      })()
      inflight.set(hostId, run)
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

    forget: (hostId) => set((s) => {
      if (!(hostId in s.byHost)) return s
      const rest = { ...s.byHost }
      delete rest[hostId]
      return { byHost: rest }
    }),
  }
})
