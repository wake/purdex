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
  /** The stored value is not a list of groups at all (the daemon shows none): see `update`. */
  invalid: boolean
}

export type UpdateResult =
  | { ok: true }
  /**
   * `not-loaded`: no answer yet, so no revision. `host-changed`: the host was forgotten (removed, or re-pointed) after the change was asked for — it is the old daemon's change and is not sent.
   * `unsafe`: the daemon left rows out of its answer (`dropped`) or the stored value is not a list (`invalid`): the visible list is not the whole stored list, and writing it back whole would erase the rest.
   */
  | { ok: false; reason: 'not-loaded' | 'host-changed' | 'unsafe' | 'conflict' | 'error' }
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

/**
 * Bumped by `forget` only. A write's answer belongs to the daemon it was made to: if the host was forgotten while it was out (removed, or re-pointed and added back under the same id) the answer
 * is the old daemon's and is not adopted, even though an entry for that id exists again by then.
 */
const incarnations = new Map<string, number>()

/** Writes in flight, one chain per host. */
const queues = new Map<string, Promise<unknown>>()

const entryOf = (r: TaskGroupsRead, status: TaskGroupsStatus = 'ready'): TaskGroupsEntry => ({ status, groups: r.items, revision: r.revision, loaded: true, dropped: r.dropped, invalid: r.invalid })

export const useTaskGroupsStore = create<TaskGroupsState>()((set, get) => {
  const put = (hostId: string, entry: TaskGroupsEntry) => set((s) => ({ byHost: { ...s.byHost, [hostId]: entry } }))

  async function write(hostId: string, incarnation: number, change: (groups: TaskGroup[]) => TaskGroup[] | null): Promise<UpdateResult> {
    // `incarnation` was taken when the change was ASKED for: a forget since (queued behind another write, or between a lost compare-and-set and its retry) makes it the old daemon's, and nothing is sent.
    const alive = () => (incarnations.get(hostId) ?? 0) === incarnation
    const mine = () => alive() && get().byHost[hostId] !== undefined
    for (let attempt = 0; attempt < 2; attempt++) {
      const cur = get().byHost[hostId]
      if (!alive()) return { ok: false, reason: 'host-changed' }
      if (!cur?.loaded) return { ok: false, reason: 'not-loaded' }
      if (cur.invalid || cur.dropped !== null) return { ok: false, reason: 'unsafe' }
      const next = change(cur.groups)
      if (next === null) return { ok: true }
      try {
        const stored = await putTaskGroups(hostId, next, cur.revision)
        if (mine()) put(hostId, entryOf(stored))
        return { ok: true }
      } catch (e) {
        if (e instanceof HostConfigConflictError) {
          const theirs = parseTaskGroups({ taskGroups: e.current })
          // A 409 carries the stored copy as `{items, revision}` and none of the GET's marks, so what the parser left out is counted here: a copy with rows the App could not read is as unsafe to write back as a GET that said so.
          const rawItems = (e.current as { items?: unknown } | null)?.items
          if (!Array.isArray(rawItems)) theirs.invalid = true
          else if (rawItems.length > theirs.items.length) theirs.dropped = { count: rawItems.length - theirs.items.length, reasons: ['the stored copy has rows this App could not read'] }
          if (mine()) put(hostId, entryOf(theirs))
          if (attempt === 1) return { ok: false, reason: 'conflict' }
          if (!alive()) return { ok: false, reason: 'host-changed' }
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
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'loading', groups: prev?.groups ?? [], revision: prev?.revision ?? 0, loaded: prev?.loaded ?? false, dropped: prev?.dropped ?? null, invalid: prev?.invalid ?? false } } }))
      try {
        const r = await fetchTaskGroups(hostId)
        if (latest.get(hostId) !== mine) return
        // A write that finished while this read was out is newer than the answer: a revision behind the one held is the older state, and is not adopted.
        const held = get().byHost[hostId]
        if (held?.loaded && r.revision < held.revision) {
          put(hostId, { ...held, status: 'ready' })
          return
        }
        put(hostId, entryOf(r))
      } catch {
        if (latest.get(hostId) !== mine) return
        set((s) => {
          const cur = s.byHost[hostId]
          return { byHost: { ...s.byHost, [hostId]: { status: 'error', groups: cur?.groups ?? [], revision: cur?.revision ?? 0, loaded: cur?.loaded ?? false, dropped: cur?.dropped ?? null, invalid: cur?.invalid ?? false } } }
        })
      }
    },
    forget(hostId) {
      bump(hostId)
      incarnations.set(hostId, (incarnations.get(hostId) ?? 0) + 1)
      queues.delete(hostId)
      set((s) => {
        const { [hostId]: _dropped, ...rest } = s.byHost
        return { byHost: rest }
      })
    },
    update(hostId, change) {
      const incarnation = incarnations.get(hostId) ?? 0
      const run = (queues.get(hostId) ?? Promise.resolve()).then(() => write(hostId, incarnation, change))
      queues.set(hostId, run.catch(() => undefined))
      return run
    },
  }
})
