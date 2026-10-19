// spa/src/lib/task-groups/row-menu.test.ts — the rows the task-group view adds to a solo session's right-click menu (name pool v2 spec §7, NA-6c). A host that cannot be written to (unsafe) or a session whose lineage root
// is not known yet gets the same rows, disabled, each saying why; nothing is sent from a disabled row.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const putInGroup = vi.fn()
const startGroupWith = vi.fn()
const takeOutOfGroup = vi.fn()
vi.mock('./actions', async (orig) => ({
  ...(await orig<typeof import('./actions')>()),
  putInGroup: (...a: unknown[]) => putInGroup(...a),
  startGroupWith: (...a: unknown[]) => startGroupWith(...a),
  takeOutOfGroup: (...a: unknown[]) => takeOutOfGroup(...a),
}))

import { rowMenuItems, failureText, type RowMenuInput } from './row-menu'
import type { TaskGroup } from './api'
import type { TaskGroupsEntry } from '../../stores/useTaskGroupsStore'

const g = (id: string, roots: string[] = [], created_at = 1): TaskGroup => ({ id, name: `G ${id}`, roots, created_at, updated_at: created_at })
const entry = (groups: TaskGroup[], over: Partial<TaskGroupsEntry> = {}): TaskGroupsEntry => ({ status: 'ready', groups, revision: 1, loaded: true, dropped: null, invalid: false, ...over })
const t = (key: string, vars?: Record<string, unknown>) => (vars ? `${key}:${JSON.stringify(vars)}` : key)
const base = (over: Partial<RowMenuInput> = {}): RowMenuInput => ({ hostId: 'h1', root: 'R1', entry: entry([g('a'), g('b')]), currentGroupId: null, t, report: vi.fn(), ...over })
const keys = (items: ReturnType<typeof rowMenuItems>) => items.map((i) => i.key)

beforeEach(() => {
  putInGroup.mockReset().mockResolvedValue({ ok: true })
  startGroupWith.mockReset().mockResolvedValue({ ok: true })
  takeOutOfGroup.mockReset().mockResolvedValue({ ok: true })
})

describe('rowMenuItems — a session that can be filed', () => {
  it('offers each group by name, then a new one', () => {
    const items = rowMenuItems(base())
    expect(keys(items)).toEqual(['put:a', 'put:b', 'new'])
    expect(items.every((i) => !i.disabled)).toBe(true)
    expect(items[0].label).toContain('G a')
  })

  it('in a group: that group becomes 「take out」 and the others stay offered', () => {
    const items = rowMenuItems(base({ currentGroupId: 'a', entry: entry([g('a', ['R1']), g('b')]) }))
    expect(keys(items)).toEqual(['put:b', 'new', 'out'])
  })

  it('runs the actions with the root, and reports a failure in words', async () => {
    const report = vi.fn()
    putInGroup.mockResolvedValueOnce({ ok: false, reason: 'group-gone' })
    const items = rowMenuItems(base({ report }))
    items[0].onSelect()
    await vi.waitFor(() => expect(report).toHaveBeenCalledWith(failureText(t, { ok: false, reason: 'group-gone' })))
    expect(putInGroup).toHaveBeenCalledWith('h1', 'R1', 'a')
    items[2].onSelect()
    expect(startGroupWith).toHaveBeenCalledWith('h1', 'R1', expect.stringContaining('taskgroups.default_name'))
  })

  it('a 33rd group is not offered', () => {
    const many = Array.from({ length: 32 }, (_, i) => g(`g${i}`))
    const items = rowMenuItems(base({ entry: entry(many) }))
    const fresh = items.find((i) => i.key === 'new')!
    expect(fresh.disabled).toBe(true)
    expect(fresh.title).toBe('taskgroups.err.too-many-groups')
  })
})

describe('rowMenuItems — what blocks it', () => {
  it('the root is not known yet: rows are there, disabled, saying it is being read', () => {
    const items = rowMenuItems(base({ root: null }))
    expect(keys(items)).toEqual(['put', 'new'])
    for (const i of items) { expect(i.disabled).toBe(true); expect(i.title).toBe('taskgroups.root_reading') }
  })

  it('an unsafe host: disabled, with the count; no count when the stored value is not a list', () => {
    const items = rowMenuItems(base({ entry: entry([g('a')], { dropped: { count: 3, reasons: [] } }) }))
    for (const i of items) { expect(i.disabled).toBe(true); expect(i.title).toBe('taskgroups.unsafe_edit:{"n":3}') }
    const bad = rowMenuItems(base({ entry: entry([], { invalid: true }) }))
    for (const i of bad) expect(i.title).toBe('taskgroups.unsafe_edit_nocount')
  })

  it('unsafe wins over an unknown root; groups not read yet say so; a disabled row sends nothing', () => {
    const both = rowMenuItems(base({ root: null, entry: entry([], { invalid: true }) }))
    expect(both[0].title).toBe('taskgroups.unsafe_edit_nocount')
    const unread = rowMenuItems(base({ entry: undefined }))
    expect(unread[0].title).toBe('taskgroups.groups_reading')
    unread[0].onSelect()
    both[0].onSelect()
    expect(putInGroup).not.toHaveBeenCalled()
    expect(startGroupWith).not.toHaveBeenCalled()
  })

  it('a blocked row in a group still shows 「take out」, disabled', () => {
    const items = rowMenuItems(base({ root: null, currentGroupId: 'a' }))
    expect(keys(items)).toEqual(['put', 'new', 'out'])
    expect(items[2].disabled).toBe(true)
  })
})

describe('failureText', () => {
  it('says each refusal in words; a rejection carries the daemon\'s message', () => {
    expect(failureText(t, { ok: false, reason: 'unsafe' })).toBe('taskgroups.err.unsafe')
    expect(failureText(t, { ok: false, reason: 'rejected', message: 'name: too long' })).toBe('taskgroups.err.rejected:{"message":"name: too long"}')
  })
})
