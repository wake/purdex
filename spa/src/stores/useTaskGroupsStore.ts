// spa/src/stores/useTaskGroupsStore.ts — per-host cache of the daemon's manual task groups (name pool v2 spec §7, NA-6a). Not persisted: the daemon is the source of truth and each host keeps its own groups (L9).
// `update` is a read-modify-write guarded by the revision: the change is a function of the list (so it can be re-applied), writes go one at a time per host, a lost compare-and-set re-applies the change on the
// daemon's copy once, and a host that has not been read is never written (the revision is what makes a write safe).
import { create } from 'zustand'
import { fetchTaskGroups, parseTaskGroups, putTaskGroups, type TaskGroup, type TaskGroupsRead } from '../lib/task-groups/api'
import { HostConfigApiError, HostConfigConflictError } from '../lib/host-config-api'

export type TaskGroupsStatus = 'loading' | 'ready' | 'error'

export interface TaskGroupsEntry {
  status: TaskGroupsStatus
  groups: TaskGroup[]
  /** The revision the groups were read at; the next write is guarded by it. */
  revision: number
  /** An answer has landed at least once: without one there is no revision, and nothing is written. */
  loaded: boolean
  /** Rows the daemon left out of its answer (the user's data is not lost, the daemon says what and why). */
  dropped: TaskGroupsRead['dropped']
}

export type UpdateResult =
  | { ok: true }
  | { ok: false; reason: 'not-loaded' | 'conflict' | 'error' }
  | { ok: false; reason: 'rejected'; message: string }

interface TaskGroupsState {
  byHost: Record<string, TaskGroupsEntry>
  load: (hostId: string) => Promise<void>
  forget: (hostId: string) => void
  /**
   * `change` maps the host's current groups to the new list, or answers `null` when there is nothing to write. It may run twice (a lost compare-and-set runs it again on the daemon's copy),
   * so it must be a function of its argument only.
   */
  update: (hostId: string, change: (groups: TaskGroup[]) => TaskGroup[] | null) => Promise<UpdateResult>
}

/** A load's answer is usable only while no newer load started and the host was not forgotten since (the persona store's rule). */
const latest = new Map<string, number>()
function bump(hostId: string): number {
  const n = (latest.get(hostId) ?? 0) + 1
  latest.set(hostId, n)
  return n
}

/** Writes in flight, one chain per host. */
const queues = new Map<string, Promise<unknown>>()

const entryOf = (r: TaskGroupsRead, status: TaskGroupsStatus = 'ready'): TaskGroupsEntry => ({ status, groups: r.items, revision: r.revision, loaded: true, dropped: r.dropped })

export const useTaskGroupsStore = create<TaskGroupsState>()((set, get) => {
  const put = (hostId: string, entry: TaskGroupsEntry) => set((s) => ({ byHost: { ...s.byHost, [hostId]: entry } }))

  async function write(hostId: string, change: (groups: TaskGroup[]) => TaskGroup[] | null): Promise<UpdateResult> {
    for (let attempt = 0; attempt < 2; attempt++) {
      const cur = get().byHost[hostId]
      if (!cur?.loaded) return { ok: false, reason: 'not-loaded' }
      const next = change(cur.groups)
      if (next === null) return { ok: true }
      try {
        const stored = await putTaskGroups(hostId, next, cur.revision)
        if (get().byHost[hostId] !== undefined) put(hostId, entryOf(stored))
        return { ok: true }
      } catch (e) {
        if (e instanceof HostConfigConflictError) {
          const theirs = parseTaskGroups({ taskGroups: e.current })
          if (get().byHost[hostId] !== undefined) put(hostId, entryOf(theirs))
          if (attempt === 1) return { ok: false, reason: 'conflict' }
          continue
        }
        if (e instanceof HostConfigApiError && e.status === 400) return { ok: false, reason: 'rejected', message: e.message }
        return { ok: false, reason: 'error' }
      }
    }
    return { ok: false, reason: 'conflict' }
  }

  return {
    byHost: {},
    async load(hostId) {
      const mine = bump(hostId)
      const prev = get().byHost[hostId]
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'loading', groups: prev?.groups ?? [], revision: prev?.revision ?? 0, loaded: prev?.loaded ?? false, dropped: prev?.dropped ?? null } } }))
      try {
        const r = await fetchTaskGroups(hostId)
        if (latest.get(hostId) !== mine) return
        put(hostId, entryOf(r))
      } catch {
        if (latest.get(hostId) !== mine) return
        set((s) => {
          const cur = s.byHost[hostId]
          return { byHost: { ...s.byHost, [hostId]: { status: 'error', groups: cur?.groups ?? [], revision: cur?.revision ?? 0, loaded: cur?.loaded ?? false, dropped: cur?.dropped ?? null } } }
        })
      }
    },
    forget(hostId) {
      bump(hostId)
      queues.delete(hostId)
      set((s) => {
        const { [hostId]: _dropped, ...rest } = s.byHost
        return { byHost: rest }
      })
    },
    update(hostId, change) {
      const run = (queues.get(hostId) ?? Promise.resolve()).then(() => write(hostId, change))
      queues.set(hostId, run.catch(() => undefined))
      return run
    },
  }
})
