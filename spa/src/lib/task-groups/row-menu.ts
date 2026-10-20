// spa/src/lib/task-groups/row-menu.ts — the rows the task-group view adds to a solo session's right-click menu (task groups spec §6.1, TG-2b; name pool v2 spec §7, NA-6c): 「加入 <group>」 for every other group — of any
// host — 「新建任務編組」, and, for a session already in a group, 「移出 <group>」. Nothing is blocked up front: the root a write needs is got when it is chosen (session-actions.ts), and a session whose host cannot
// answer is told so in words through `report`. Pure but for the actions it hands to the rows.
import type { TabMenuExtra } from '../../stores/useTabMenuExtrasStore'
import { MAX_GROUPS } from './manual-edits'
import { defaultManualGroupName, type ManualActionResult } from './manual-actions'
import { fileSession, startGroupWithSession, takeSessionOut, type SessionRef } from './session-actions'

type Translate = (key: string, vars?: Record<string, string | number>) => string

export interface RowMenuInput {
  session: SessionRef
  /** Every manual group, in the store's order. */
  groups: ReadonlyArray<{ id: string; name: string }>
  /** The manual group the session is in now, if any. */
  currentGroupId: string | null
  t: Translate
  /** A failure in words, or null to clear the last one. */
  report: (message: string | null) => void
}

/** A refusal in words (the keys are `taskgroups.err.<reason>`). */
export function failureText(t: Translate, r: Extract<ManualActionResult, { ok: false }>): string {
  return t(`taskgroups.err.${r.reason}`)
}

export function rowMenuItems(i: RowMenuInput): TabMenuExtra[] {
  const { t, session, groups, currentGroupId } = i
  const current = groups.find((g) => g.id === currentGroupId)
  const done = (r: { ok: true } | Extract<ManualActionResult, { ok: false }>) => i.report(r.ok ? null : failureText(t, r))

  const items: TabMenuExtra[] = groups
    .filter((g) => g.id !== currentGroupId)
    .map((g) => ({ key: `put:${g.id}`, label: t('taskgroups.menu_put_to', { name: g.name }), onSelect: () => { void fileSession(session, g.id).then(done) } }))
  const full = groups.length >= MAX_GROUPS
  items.push({
    key: 'new', label: t('taskgroups.menu_new'), disabled: full, title: full ? t('taskgroups.err.too-many-groups') : undefined,
    onSelect: () => { void startGroupWithSession(session, defaultManualGroupName(t('taskgroups.default_name'))).then(done) },
  })
  if (currentGroupId !== null) items.push({ key: 'out', label: t('taskgroups.menu_out', { name: current?.name ?? '' }), onSelect: () => { void takeSessionOut(session).then(done) } })
  return items
}
