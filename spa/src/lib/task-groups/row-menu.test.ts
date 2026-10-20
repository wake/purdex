// spa/src/lib/task-groups/row-menu.test.ts — the rows the task-group view adds to a solo session's right-click menu (task groups spec §6.1, TG-2b). Every group — of any host — is offered; nothing is blocked up
// front (the root a write needs is got when it is chosen); a refusal is said in words through `report`.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const fileSession = vi.fn()
const startGroupWithSession = vi.fn()
const takeSessionOut = vi.fn()
vi.mock('./session-actions', () => ({
  fileSession: (...a: unknown[]) => fileSession(...a),
  startGroupWithSession: (...a: unknown[]) => startGroupWithSession(...a),
  takeSessionOut: (...a: unknown[]) => takeSessionOut(...a),
}))

import { rowMenuItems, failureText, type RowMenuInput } from './row-menu'

const g = (id: string) => ({ id, name: `G ${id}` })
const t = (key: string, vars?: Record<string, unknown>) => (vars ? `${key}:${JSON.stringify(vars)}` : key)
const session = { hostId: 'h1', agentSessionId: 'AG1', tabId: 't1' }
const base = (over: Partial<RowMenuInput> = {}): RowMenuInput => ({ session, groups: [g('a'), g('b')], currentGroupId: null, t, report: vi.fn(), ...over })
const keys = (items: ReturnType<typeof rowMenuItems>) => items.map((i) => i.key)

beforeEach(() => {
  fileSession.mockReset().mockResolvedValue({ ok: true })
  startGroupWithSession.mockReset().mockResolvedValue({ ok: true, id: 'new' })
  takeSessionOut.mockReset().mockResolvedValue({ ok: true })
})

describe('rowMenuItems', () => {
  it('offers each group by name, in the store\'s order, then a new one — all enabled, whatever the root', () => {
    const items = rowMenuItems(base({ session: { ...session, agentSessionId: null } }))
    expect(keys(items)).toEqual(['put:a', 'put:b', 'new'])
    expect(items.every((i) => !i.disabled)).toBe(true)
    expect(items[0].label).toContain('G a')
  })

  it('in a group: that group becomes 「take out」 and the others stay offered', () => {
    const items = rowMenuItems(base({ currentGroupId: 'a' }))
    expect(keys(items)).toEqual(['put:b', 'new', 'out'])
    expect(items[2].label).toContain('G a')
  })

  it('runs the session actions, and reports a refusal in words', async () => {
    const report = vi.fn()
    fileSession.mockResolvedValueOnce({ ok: false, reason: 'no-root' })
    const items = rowMenuItems(base({ report, currentGroupId: 'b' }))
    items[0].onSelect()
    await vi.waitFor(() => expect(report).toHaveBeenCalledWith('taskgroups.err.no-root'))
    expect(fileSession).toHaveBeenCalledWith(session, 'a')
    items[1].onSelect()
    expect(startGroupWithSession).toHaveBeenCalledWith(session, expect.stringContaining('taskgroups.default_name'))
    items[2].onSelect()
    expect(takeSessionOut).toHaveBeenCalledWith(session)
    await vi.waitFor(() => expect(report).toHaveBeenCalledWith(null))
  })

  it('a 33rd group is not offered', () => {
    const many = Array.from({ length: 32 }, (_, i) => g(`g${i}`))
    const fresh = rowMenuItems(base({ groups: many })).find((i) => i.key === 'new')!
    expect(fresh.disabled).toBe(true)
    expect(fresh.title).toBe('taskgroups.err.too-many-groups')
  })
})

describe('failureText', () => {
  it('says each refusal in words', () => {
    expect(failureText(t, { ok: false, reason: 'no-root' })).toBe('taskgroups.err.no-root')
    expect(failureText(t, { ok: false, reason: 'group-gone' })).toBe('taskgroups.err.group-gone')
  })
})
