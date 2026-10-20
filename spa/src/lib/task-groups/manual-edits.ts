// spa/src/lib/task-groups/manual-edits.ts — the pure edits of the manual task groups (task groups spec §4.1, TG-2a): each
// maps the groups state to the next one under the store's limits, or says why it cannot. Functions of the state alone, so a
// caller can apply one to whatever the store holds right now. A no-op hands back the very same state object.
import type { ManualGroupMember, ManualGroups } from '../../stores/useManualGroupsStore'

// The store's limits live here (the store imports them): this module has no runtime import from the store.
export const MAX_GROUPS = 32
export const MAX_MEMBERS = 64

const MAX_NAME_BYTES = 64
const COLOR = /^#[0-9a-fA-F]{6}$/
const GROUP_ID = /^[0-9a-f]{8}$/

export type ManualEditError = 'group-gone' | 'group-full' | 'too-many-groups' | 'bad-name' | 'bad-color' | 'bad-id' | 'id-taken'
export type ManualEditResult = { state: ManualGroups } | { error: ManualEditError }

/** The name's rule is the team name's (`peers.ValidateTitle`): 1–64 bytes, printable. `null` = fine. */
export function groupNameError(name: string): 'bad-name' | null {
  const n = name.trim()
  if (n === '' || new TextEncoder().encode(n).length > MAX_NAME_BYTES) return 'bad-name'
  // eslint-disable-next-line no-control-regex -- control characters are what is refused
  if (/[\u0000-\u001f\u007f-\u009f]/.test(n)) return 'bad-name'
  return null
}

/** A fresh 8-hex id, not among `used`. */
export function newManualGroupId(used: readonly string[]): string {
  for (;;) {
    const b = crypto.getRandomValues(new Uint8Array(4))
    const id = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
    if (!used.includes(id)) return id
  }
}

const same = (a: ManualGroupMember, b: ManualGroupMember): boolean => a.host === b.host && a.root === b.root

/** The id of the group holding `member`, or null. */
export function memberGroupOf(state: ManualGroups, member: ManualGroupMember): string | null {
  for (const id of state.order) if (state.groups[id]?.members.some((m) => same(m, member))) return id
  return null
}

/** `state` without `member` in any group; the same state when it was in none. */
function withoutMember(state: ManualGroups, member: ManualGroupMember): ManualGroups {
  let groups: ManualGroups['groups'] | null = null
  for (const [id, g] of Object.entries(state.groups)) {
    if (!g.members.some((m) => same(m, member))) continue
    groups ??= { ...state.groups }
    groups[id] = { ...g, members: g.members.filter((m) => !same(m, member)) }
  }
  return groups === null ? state : { order: state.order, groups }
}

export function newGroup(
  state: ManualGroups,
  p: { id: string; name: string; color?: string; member?: ManualGroupMember; now: number },
): ManualEditResult {
  if (groupNameError(p.name)) return { error: 'bad-name' }
  if (p.color !== undefined && !COLOR.test(p.color)) return { error: 'bad-color' }
  if (!GROUP_ID.test(p.id)) return { error: 'bad-id' }
  if (Object.hasOwn(state.groups, p.id)) return { error: 'id-taken' }
  if (state.order.length >= MAX_GROUPS) return { error: 'too-many-groups' }
  const base = p.member === undefined ? state : withoutMember(state, p.member)
  return {
    state: {
      order: [...base.order, p.id],
      groups: {
        ...base.groups,
        [p.id]: { name: p.name.trim(), ...(p.color !== undefined ? { color: p.color } : {}), members: p.member === undefined ? [] : [{ ...p.member }], createdAt: p.now },
      },
    },
  }
}

export function renameGroup(state: ManualGroups, id: string, name: string): ManualEditResult {
  const group = Object.hasOwn(state.groups, id) ? state.groups[id] : undefined
  if (group === undefined) return { error: 'group-gone' }
  if (groupNameError(name)) return { error: 'bad-name' }
  const next = name.trim()
  if (group.name === next) return { state }
  return { state: { order: state.order, groups: { ...state.groups, [id]: { ...group, name: next } } } }
}

/** `color` undefined clears the colour (the key goes). */
export function setGroupColor(state: ManualGroups, id: string, color: string | undefined): ManualEditResult {
  const group = Object.hasOwn(state.groups, id) ? state.groups[id] : undefined
  if (group === undefined) return { error: 'group-gone' }
  if (color !== undefined && !COLOR.test(color)) return { error: 'bad-color' }
  if (group.color === color) return { state }
  const { color: _old, ...rest } = group
  void _old
  return { state: { order: state.order, groups: { ...state.groups, [id]: color === undefined ? rest : { ...rest, color } } } }
}

export function deleteGroup(state: ManualGroups, id: string): ManualEditResult {
  if (!Object.hasOwn(state.groups, id)) return { error: 'group-gone' }
  const groups = { ...state.groups }
  delete groups[id]
  return { state: { order: state.order.filter((x) => x !== id), groups } }
}

/** Puts `member` in the group, out of whichever group held it (a member is in at most one). A full target refuses and leaves the member where it was. */
export function addMember(state: ManualGroups, groupId: string, member: ManualGroupMember): ManualEditResult {
  const target = Object.hasOwn(state.groups, groupId) ? state.groups[groupId] : undefined
  if (target === undefined) return { error: 'group-gone' }
  if (target.members.some((m) => same(m, member))) return { state }
  if (target.members.length >= MAX_MEMBERS) return { error: 'group-full' }
  const base = withoutMember(state, member)
  const group = base.groups[groupId]
  return { state: { order: base.order, groups: { ...base.groups, [groupId]: { ...group, members: [...group.members, { ...member }] } } } }
}

/** Takes `member` out of whichever group holds it; the same state when none does. */
export function removeMember(state: ManualGroups, member: ManualGroupMember): ManualEditResult {
  return { state: withoutMember(state, member) }
}

export function moveMember(state: ManualGroups, member: ManualGroupMember, toGroupId: string): ManualEditResult {
  return addMember(state, toGroupId, member)
}
