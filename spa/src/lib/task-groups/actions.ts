// spa/src/lib/task-groups/actions.ts — what the sidebar's task-group view does with a manual group (name pool v2 spec §7, NA-6c): put a session in a group, start a group from one, take it out.
// A manual group stores LINEAGE ROOTS, so every write is given the session's root and refuses (`no-root`, before the store is touched) when that is not known: the view's `useSessionRoots` knows a root only from the
// quota rows the daemon listed, and a session's own id is never offered in its place — it would file the wrong thing the moment the session is relayed. The store adds its own refusals (unsafe, not loaded, host
// changed, conflict); an edit that finds its target gone (another device deleted the group) judges that on the copy the write actually meets, so a retried change cannot undo a delete.
import type { TaskGroup } from './api'
import { useTaskGroupsStore, type UpdateResult } from '../../stores/useTaskGroupsStore'
import { addRoot, newGroup, newGroupId, removeRoot, renameGroup, deleteGroup, groupNameError, type EditError, type EditResult } from './edits'

export type ActionResult = UpdateResult | { ok: false; reason: EditError | 'no-root' }

/** Runs an edit through the store; the edit's own refusal (judged on the copy it met) wins over the store's "nothing to write". */
async function run(hostId: string, edit: (groups: readonly TaskGroup[], now: number) => EditResult): Promise<ActionResult> {
  let refused: EditError | null = null
  const result = await useTaskGroupsStore.getState().update(hostId, (groups) => {
    refused = null
    const r = edit(groups, Date.now())
    if ('error' in r) { refused = r.error; return null }
    // an edit that changed no group hands back the same group objects: nothing to write (and no revision spent)
    return r.groups.length === groups.length && r.groups.every((g, i) => g === groups[i]) ? null : r.groups
  })
  return refused !== null ? { ok: false, reason: refused } : result
}

export function putInGroup(hostId: string, root: string | null, groupId: string): Promise<ActionResult> {
  if (root === null) return Promise.resolve({ ok: false, reason: 'no-root' })
  return run(hostId, (groups, now) => addRoot(groups, groupId, root, now))
}

export function startGroupWith(hostId: string, root: string | null, name: string): Promise<ActionResult> {
  if (root === null) return Promise.resolve({ ok: false, reason: 'no-root' })
  if (groupNameError(name)) return Promise.resolve({ ok: false, reason: 'bad-name' })
  // the id is drawn once, outside the change: a retry on the daemon's copy keeps it (and says so if the copy took it)
  const used = (useTaskGroupsStore.getState().byHost[hostId]?.groups ?? []).map((g) => g.id)
  const id = newGroupId(used)
  return run(hostId, (groups, now) => newGroup(groups, { id, name, root, now }))
}

export function takeOutOfGroup(hostId: string, root: string | null): Promise<ActionResult> {
  if (root === null) return Promise.resolve({ ok: false, reason: 'no-root' })
  return run(hostId, (groups, now) => removeRoot(groups, root, now))
}

export function renameTaskGroup(hostId: string, groupId: string, name: string): Promise<ActionResult> {
  return run(hostId, (groups, now) => renameGroup(groups, groupId, name, now))
}

/** Deletes the group, the roots it still holds (sessions that are not live) with it: the caller has asked the person once. */
export function deleteTaskGroup(hostId: string, groupId: string): Promise<ActionResult> {
  return run(hostId, (groups) => deleteGroup(groups, groupId))
}

/** 「<base> N」: the first N whose name no group on the host has. */
export function defaultGroupName(base: string, groups: readonly TaskGroup[]): string {
  const names = new Set(groups.map((g) => g.name))
  for (let n = 1; ; n++) if (!names.has(`${base} ${n}`)) return `${base} ${n}`
}
