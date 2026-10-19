// spa/src/lib/task-groups/edits.ts — the pure edits of a host's manual task groups (name pool v2 spec §7, NA-6c). Each maps the groups to the new list under the daemon's limits (hostconfig/taskgroups.go: ≤32 groups, ≤64 roots
// a group, a root in at most one group, a title-shaped name) or says why it cannot. Functions of the list alone, because the store may apply a change twice (a lost compare-and-set re-runs it on the daemon's copy), so
// "the group is gone" and "the group is full" are judged on whatever copy the change meets.
import type { TaskGroup } from './api'

export const MAX_GROUPS = 32
export const MAX_ROOTS = 64
const MAX_NAME_BYTES = 64

export type EditError = 'group-gone' | 'group-full' | 'too-many-groups' | 'bad-name' | 'id-taken' | 'group-changed' | 'group-in-use'
export type EditResult = { groups: TaskGroup[] } | { error: EditError }

/** The name's rule is the team name's (`peers.ValidateTitle`): 1–64 bytes, printable. `null` = fine. */
export function groupNameError(name: string): 'bad-name' | null {
  const n = name.trim()
  if (n === '' || new TextEncoder().encode(n).length > MAX_NAME_BYTES) return 'bad-name'
  // eslint-disable-next-line no-control-regex -- control characters are what is refused
  if (/[\u0000-\u001f\u007f-\u009f]/.test(n)) return 'bad-name'
  return null
}

/** A fresh 8-hex id, not among `used` (the daemon's shape for a generated id). */
export function newGroupId(used: readonly string[]): string {
  for (;;) {
    const b = crypto.getRandomValues(new Uint8Array(4))
    const id = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
    if (!used.includes(id)) return id
  }
}

const without = (g: TaskGroup, root: string, now: number): TaskGroup => (g.roots.includes(root) ? { ...g, roots: g.roots.filter((r) => r !== root), updated_at: now } : g)

/** Puts `root` in the group, out of whichever group held it. */
export function addRoot(groups: readonly TaskGroup[], groupId: string, root: string, now: number): EditResult {
  const target = groups.find((g) => g.id === groupId)
  if (!target) return { error: 'group-gone' }
  if (target.roots.includes(root)) return { groups: [...groups] }
  if (target.roots.length >= MAX_ROOTS) return { error: 'group-full' }
  return { groups: groups.map((g) => (g.id === groupId ? { ...g, roots: [...g.roots, root], updated_at: now } : without(g, root, now))) }
}

/** Appends a group holding `root` (taken from the group that held it). */
export function newGroup(groups: readonly TaskGroup[], p: { id: string; name: string; root: string; now: number }): EditResult {
  if (groupNameError(p.name)) return { error: 'bad-name' }
  if (groups.length >= MAX_GROUPS) return { error: 'too-many-groups' }
  if (groups.some((g) => g.id === p.id)) return { error: 'id-taken' }
  const kept = groups.map((g) => without(g, p.root, p.now))
  return { groups: [...kept, { id: p.id, name: p.name.trim(), roots: [p.root], created_at: p.now, updated_at: p.now }] }
}

/** Takes `root` out of whichever group holds it (nothing to do when none does). */
export function removeRoot(groups: readonly TaskGroup[], root: string, now: number): EditResult {
  return { groups: groups.map((g) => without(g, root, now)) }
}

export function renameGroup(groups: readonly TaskGroup[], groupId: string, name: string, now: number): EditResult {
  if (!groups.some((g) => g.id === groupId)) return { error: 'group-gone' }
  if (groupNameError(name)) return { error: 'bad-name' }
  return { groups: groups.map((g) => (g.id === groupId ? { ...g, name: name.trim(), updated_at: now } : g)) }
}

export function deleteGroup(groups: readonly TaskGroup[], groupId: string): EditResult {
  if (!groups.some((g) => g.id === groupId)) return { error: 'group-gone' }
  return { groups: groups.filter((g) => g.id !== groupId) }
}

/**
 * Deletes the group only while what the person confirmed still holds, judged on the copy this edit meets (so a retry after a lost compare-and-set re-checks it): the group still holds exactly the roots that were
 * shown (another device has not filed anything in it since), and none of them is a live session (`live` = the roots of every live session of the host, read just before).
 */
export function deleteGroupIf(groups: readonly TaskGroup[], groupId: string, expectedRoots: readonly string[], live: ReadonlySet<string>): EditResult {
  const g = groups.find((x) => x.id === groupId)
  if (!g) return { error: 'group-gone' }
  if (g.roots.length !== expectedRoots.length || !expectedRoots.every((r) => g.roots.includes(r))) return { error: 'group-changed' }
  if (g.roots.some((r) => live.has(r))) return { error: 'group-in-use' }
  return { groups: groups.filter((x) => x.id !== groupId) }
}
