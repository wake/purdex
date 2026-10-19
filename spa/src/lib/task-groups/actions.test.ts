// spa/src/lib/task-groups/actions.test.ts — what the sidebar's task-group view does with a manual group (name pool v2 spec §7, NA-6c): put a session in a group, start a group from one, take it out. A write needs the
// session's LINEAGE ROOT: when it is not known nothing is sent (never the session's own id in its place — that would file the wrong thing the moment the session is relayed). A host the store calls unsafe is not written either.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const fetchTaskGroups = vi.fn()
const putTaskGroups = vi.fn()
vi.mock('./api', async (orig) => ({ ...(await orig<typeof import('./api')>()), fetchTaskGroups: (...a: unknown[]) => fetchTaskGroups(...a), putTaskGroups: (...a: unknown[]) => putTaskGroups(...a) }))

import { putInGroup, startGroupWith, takeOutOfGroup, defaultGroupName } from './actions'
import { useTaskGroupsStore } from '../../stores/useTaskGroupsStore'
import type { TaskGroup, TaskGroupsRead } from './api'

const g = (id: string, roots: string[] = []): TaskGroup => ({ id, name: `G${id}`, roots, created_at: 1, updated_at: 1 })
const read = (items: TaskGroup[], revision = 1, over: Partial<TaskGroupsRead> = {}): TaskGroupsRead => ({ items, revision, dropped: null, invalid: false, ...over })
const loaded = async (r: TaskGroupsRead) => { fetchTaskGroups.mockResolvedValue(r); await useTaskGroupsStore.getState().load('h1') }
const sent = () => putTaskGroups.mock.calls.map((c) => c[1] as TaskGroup[])

beforeEach(() => {
  fetchTaskGroups.mockReset()
  putTaskGroups.mockReset()
  putTaskGroups.mockImplementation(async (_h: string, items: TaskGroup[], base: number) => read(items, base + 1))
  useTaskGroupsStore.setState({ byHost: {} })
})

describe('a write needs the lineage root', () => {
  it.each([
    ['put in a group', () => putInGroup('h1', null, 'a')],
    ['start a group', () => startGroupWith('h1', null, 'New')],
    ['take out of a group', () => takeOutOfGroup('h1', null)],
  ])('%s: with no root known nothing is sent', async (_n, run) => {
    await loaded(read([g('a')]))
    expect(await run()).toEqual({ ok: false, reason: 'no-root' })
    expect(putTaskGroups).not.toHaveBeenCalled()
  })
})

describe('putInGroup', () => {
  it('files the root in the group, out of the one that held it', async () => {
    await loaded(read([g('a', ['R1']), g('b')]))
    expect(await putInGroup('h1', 'R1', 'b')).toEqual({ ok: true })
    expect(sent()[0].map((x) => x.roots)).toEqual([[], ['R1']])
  })
  it('says the group is gone when another device deleted it, and sends nothing', async () => {
    await loaded(read([g('a')]))
    expect(await putInGroup('h1', 'R1', 'zz')).toEqual({ ok: false, reason: 'group-gone' })
    expect(putTaskGroups).not.toHaveBeenCalled()
  })
  it('re-applies on the daemon\'s copy after a lost compare-and-set, and judges the edit on THAT copy', async () => {
    const { HostConfigConflictError } = await import('../host-config-api')
    await loaded(read([g('a')]))
    putTaskGroups.mockRejectedValueOnce(new HostConfigConflictError({ items: [], revision: 2 }))
    // the daemon's copy has no group 'a' any more: the retry finds the group gone
    expect(await putInGroup('h1', 'R1', 'a')).toEqual({ ok: false, reason: 'group-gone' })
  })
})

describe('startGroupWith', () => {
  it('appends a group holding the root, named as asked', async () => {
    await loaded(read([g('a')]))
    expect(await startGroupWith('h1', 'R1', 'Fresh')).toEqual({ ok: true })
    const out = sent()[0]
    expect(out.map((x) => x.name)).toEqual(['Ga', 'Fresh'])
    expect(out[1].roots).toEqual(['R1'])
    expect(out[1].id).toMatch(/^[0-9a-f]{8}$/)
  })
})

describe('takeOutOfGroup', () => {
  it('takes the root out', async () => {
    await loaded(read([g('a', ['R1', 'R2'])]))
    expect(await takeOutOfGroup('h1', 'R1')).toEqual({ ok: true })
    expect(sent()[0][0].roots).toEqual(['R2'])
  })
})

describe('a host the store calls unsafe', () => {
  it('is not written, whatever the action', async () => {
    await loaded(read([g('a')], 1, { dropped: { count: 2, reasons: ['bad row'] } }))
    expect(await putInGroup('h1', 'R1', 'a')).toEqual({ ok: false, reason: 'unsafe' })
    expect(await startGroupWith('h1', 'R1', 'X')).toEqual({ ok: false, reason: 'unsafe' })
    expect(putTaskGroups).not.toHaveBeenCalled()
  })
  it('a host not read yet is not written either', async () => {
    expect(await putInGroup('h1', 'R1', 'a')).toEqual({ ok: false, reason: 'not-loaded' })
  })
})

describe('defaultGroupName', () => {
  it('numbers past the names in use', () => {
    expect(defaultGroupName('任務編組', [])).toBe('任務編組 1')
    expect(defaultGroupName('任務編組', [g('a'), { ...g('b'), name: '任務編組 1' }, { ...g('c'), name: '任務編組 2' }])).toBe('任務編組 3')
  })
})
