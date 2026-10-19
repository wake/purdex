// spa/src/lib/task-groups/row-menu.ts — the rows the task-group view adds to a solo session's right-click menu (name pool v2 spec §7, NA-6c): 「加入 <group>」 for each group, 「新建任務編組」, and — for a session already in
// a group — 「移出 <group>」. When the session cannot be filed they are still there, disabled, each saying why: the host's groups cannot be written to (unsafe; the daemon left rows out or the value is not a list), the
// groups or the session's lineage root have not been read yet. Pure but for the actions it hands to the rows; a failure comes back through `report` in words.
import type { TabMenuExtra } from '../../stores/useTabMenuExtrasStore'
import type { TaskGroupsEntry } from '../../stores/useTaskGroupsStore'
import { defaultGroupName, putInGroup, startGroupWith, takeOutOfGroup, type ActionResult } from './actions'
import { MAX_GROUPS } from './edits'

type Translate = (key: string, vars?: Record<string, string | number>) => string

export interface RowMenuInput {
  hostId: string
  /** The session's lineage root, or null when it is not known (yet). */
  root: string | null
  /** What the store holds for the host; undefined when nothing has been read. */
  entry: TaskGroupsEntry | undefined
  /** The manual group the session is in now, if any. */
  currentGroupId: string | null
  t: Translate
  /** A failure in words, or null to clear the last one. */
  report: (message: string | null) => void
}

/** A refusal in words (the keys are `taskgroups.err.<reason>`; a daemon rejection carries its own message). */
export function failureText(t: Translate, r: Extract<ActionResult, { ok: false }>): string {
  return r.reason === 'rejected' ? t('taskgroups.err.rejected', { message: r.message }) : t(`taskgroups.err.${r.reason}`)
}

/** Why a host's groups cannot be edited right now, in words; null = they can: unsafe (the daemon left rows out, or the value is not a list), or not read yet. */
export function hostBlockedReason(entry: TaskGroupsEntry | undefined, t: Translate): string | null {
  if (entry && (entry.invalid || entry.dropped !== null)) return entry.invalid || !entry.dropped || entry.dropped.count <= 0 ? t('taskgroups.unsafe_edit_nocount') : t('taskgroups.unsafe_edit', { n: entry.dropped.count })
  if (!entry || !entry.loaded) return t('taskgroups.groups_reading')
  return null
}

/** Why nothing can be filed from this host/session right now, in words; null = it can. */
function blockedReason(i: RowMenuInput): string | null {
  return hostBlockedReason(i.entry, i.t) ?? (i.root === null ? i.t('taskgroups.root_reading') : null)
}

export function rowMenuItems(i: RowMenuInput): TabMenuExtra[] {
  const { t, hostId, entry, currentGroupId } = i
  const blocked = blockedReason(i)
  const groups = entry?.groups ?? []
  const current = groups.find((g) => g.id === currentGroupId)
  const done = (r: ActionResult) => i.report(r.ok ? null : failureText(t, r))

  if (blocked !== null) {
    return [
      { key: 'put', label: t('taskgroups.menu_put'), onSelect: () => {}, disabled: true, title: blocked },
      { key: 'new', label: t('taskgroups.menu_new'), onSelect: () => {}, disabled: true, title: blocked },
      ...(currentGroupId !== null ? [{ key: 'out', label: t('taskgroups.menu_out', { name: current?.name ?? '' }), onSelect: () => {}, disabled: true, title: blocked }] : []),
    ]
  }

  const root = i.root as string // blockedReason is null only for a known root
  const sorted = [...groups].sort((a, b) => a.created_at - b.created_at || a.id.localeCompare(b.id))
  const items: TabMenuExtra[] = sorted
    .filter((g) => g.id !== currentGroupId)
    .map((g) => ({ key: `put:${g.id}`, label: t('taskgroups.menu_put_to', { name: g.name }), onSelect: () => { void putInGroup(hostId, root, g.id).then(done) } }))
  const full = groups.length >= MAX_GROUPS
  items.push({
    key: 'new', label: t('taskgroups.menu_new'), disabled: full, title: full ? t('taskgroups.err.too-many-groups') : undefined,
    onSelect: () => { void startGroupWith(hostId, root, defaultGroupName(t('taskgroups.default_name'), groups)).then(done) },
  })
  if (currentGroupId !== null) items.push({ key: 'out', label: t('taskgroups.menu_out', { name: current?.name ?? '' }), onSelect: () => { void takeOutOfGroup(hostId, root).then(done) } })
  return items
}
