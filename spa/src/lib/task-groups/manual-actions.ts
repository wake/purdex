// spa/src/lib/task-groups/manual-actions.ts — what the sidebar does with a manual group, on `useManualGroupsStore` (task
// groups spec §4.1 / §5, TG-2a): start one, rename, recolour, delete, put a session in, take it out. A member is a (host,
// lineage root): every write that needs a root is GIVEN it by the caller — read from the root cache or the daemon — and
// refuses with `no-root` before the store is touched when it is not known (a session's own id is never offered in its
// place: it would file the wrong thing the moment the session is relayed). The group store is a settings store, so
// there is no per-host revision or retry here: an edit is applied to whatever the store holds right now (the settings
// sync decides conflicts, spec §4.3).
//
// The tab side (spec §5, "user writes"): putting a session in a group, or taking it out, clears the `group` of the
// named on-screen tab; deleting a group clears the `group` of every on-screen tab naming it. A rank is the tab's place
// in a bar, not its placement, and is left alone.
import { useManualGroupsStore, type ManualGroupMember, type ManualGroups } from '../../stores/useManualGroupsStore'
import { useTabStore } from '../../stores/useTabStore'
import { TEAM_COLORS } from '../../components/team/team-display'
import { manualGroupKey } from './group-key'
import {
  addMember, deleteGroup, newGroup, newManualGroupId, removeMember, renameGroup, setGroupColor, type ManualEditError, type ManualEditResult,
} from './manual-edits'

export type ManualActionResult = { ok: true } | { ok: false; reason: ManualEditError | 'no-root' }
export type CreateResult = { ok: true; id: string } | { ok: false; reason: ManualEditError | 'no-root' }

/** A member whose root is known, or null. */
type Given = { host: string; root: string | null }
const known = (m: Given): ManualGroupMember | null => (m.root === null ? null : { host: m.host, root: m.root })

/** Applies an edit to what the store holds now; the store is not touched when the edit refuses or changes nothing. */
function apply(edit: (state: ManualGroups) => ManualEditResult): ManualActionResult {
  const { order, groups } = useManualGroupsStore.getState()
  const r = edit({ order, groups })
  if ('error' in r) return { ok: false, reason: r.error }
  if (r.state.groups !== groups || r.state.order !== order) useManualGroupsStore.setState({ order: r.state.order, groups: r.state.groups })
  return { ok: true }
}

/** Clears the `group` of the on-screen tabs `which` picks. */
function clearTabGroups(which: (group: string) => boolean): void {
  const { tabs, setTabGroup } = useTabStore.getState()
  for (const [id, t] of Object.entries(tabs)) if (t.group !== undefined && which(t.group)) setTabGroup(id, undefined)
}

/** The colour a new group is given (spec §6.1): the first swatch colour no other manual group uses; with all eight used, the swatch again from the start (by the number of groups), so a new group is never the grey of 「未分組」. */
export function nextGroupColor(groups: ManualGroups['groups']): string {
  const used = new Set(Object.values(groups).map((g) => g.color?.toLowerCase()))
  return TEAM_COLORS.find((c) => !used.has(c)) ?? TEAM_COLORS[Object.keys(groups).length % TEAM_COLORS.length]
}

/** `tabId`, with `member`, is the session's tab: on success it loses its own `group` (it is a member now). */
export function createManualGroup(p: { name: string; color?: string; member?: Given; tabId?: string }): CreateResult {
  let member: ManualGroupMember | undefined
  if (p.member !== undefined) {
    const m = known(p.member)
    if (m === null) return { ok: false, reason: 'no-root' }
    member = m
  }
  // the id is drawn once: nothing is retried, and the store is read and written in one synchronous stretch
  const id = newManualGroupId(useManualGroupsStore.getState().order)
  const r = apply((s) => newGroup(s, { id, name: p.name, color: p.color ?? nextGroupColor(useManualGroupsStore.getState().groups), ...(member !== undefined ? { member } : {}), now: Date.now() }))
  if (!r.ok) return r
  if (member !== undefined && p.tabId !== undefined) useTabStore.getState().setTabGroup(p.tabId, undefined)
  return { ok: true, id }
}

export function renameManualGroup(id: string, name: string): ManualActionResult {
  return apply((s) => renameGroup(s, id, name))
}

export function recolorManualGroup(id: string, color: string | undefined): ManualActionResult {
  return apply((s) => setGroupColor(s, id, color))
}

/** Deletes the group — its members go with it — and clears the `group` of every on-screen tab naming it. */
export function deleteManualGroup(id: string): ManualActionResult {
  const r = apply((s) => deleteGroup(s, id))
  if (r.ok) {
    const key = manualGroupKey(id)
    clearTabGroups((group) => group === key)
  }
  return r
}

/** Puts a session in the group (out of whichever held it); `tabId`, when given, is the session's tab, which then loses its own `group`. */
export function putMemberInGroup(member: Given, groupId: string, tabId?: string): ManualActionResult {
  const m = known(member)
  if (m === null) return { ok: false, reason: 'no-root' }
  const r = apply((s) => addMember(s, groupId, m))
  if (r.ok && tabId !== undefined) useTabStore.getState().setTabGroup(tabId, undefined)
  return r
}

/**
 * 「移出」: the member goes out of its group and the tab (`tabId`) loses its `group`. A plain tab has no root: with only a
 * `tabId` it just loses its `group`. Neither a root nor a tab is `no-root`.
 */
export function takeOutOfGroups(p: { host: string; root: string | null; tabId?: string }): ManualActionResult {
  const m = known(p)
  if (m === null && p.tabId === undefined) return { ok: false, reason: 'no-root' }
  if (m !== null) {
    const r = apply((s) => removeMember(s, m))
    if (!r.ok) return r
  }
  if (p.tabId !== undefined) useTabStore.getState().setTabGroup(p.tabId, undefined)
  return { ok: true }
}

/** 「<base> N」: the first N whose name no group has. */
export function defaultManualGroupName(base: string): string {
  const names = new Set(Object.values(useManualGroupsStore.getState().groups).map((g) => g.name))
  for (let n = 1; ; n++) if (!names.has(`${base} ${n}`)) return `${base} ${n}`
}
