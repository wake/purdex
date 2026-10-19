// spa/src/stores/useTaskGroupsStore.test.ts — per-host cache of the daemon's manual task groups (name pool v2 spec §7, NA-6a). Not persisted: the daemon is the source of truth. `update` is a read-modify-write
// guarded by the revision: a lost compare-and-set re-applies the change on the daemon's copy once; nothing is written from a host that has not been read.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const fetchTaskGroups = vi.fn()
const putTaskGroups = vi.fn()
vi.mock('../lib/task-groups/api', async (orig) => ({ ...(await orig<typeof import('../lib/task-groups/api')>()), fetchTaskGroups: (...a: unknown[]) => fetchTaskGroups(...a), putTaskGroups: (...a: unknown[]) => putTaskGroups(...a) }))

import { useTaskGroupsStore } from './useTaskGroupsStore'
import { HostConfigApiError, HostConfigConflictError } from '../lib/host-config-api'
import type { TaskGroup, TaskGroupsRead } from '../lib/task-groups/api'

const g = (id: string, roots: string[] = [], over: Partial<TaskGroup> = {}): TaskGroup => ({ id, name: `G${id}`, roots, created_at: 1, updated_at: 1, ...over })
const read = (items: TaskGroup[], revision: number): TaskGroupsRead => ({ items, revision, dropped: null, invalid: false })
const get = () => useTaskGroupsStore.getState()
const entry = (h = 'h1') => get().byHost[h]

beforeEach(() => {
  fetchTaskGroups.mockReset()
  putTaskGroups.mockReset()
  useTaskGroupsStore.setState({ byHost: {} })
})

describe('load', () => {
  it('reads the groups and the revision', async () => {
    fetchTaskGroups.mockResolvedValue(read([g('a', ['r1'])], 3))
    await get().load('h1')
    expect(entry()).toMatchObject({ status: 'ready', revision: 3, groups: [g('a', ['r1'])] })
  })

  it('a failed read keeps what was there and says so', async () => {
    fetchTaskGroups.mockResolvedValueOnce(read([g('a')], 1))
    await get().load('h1')
    fetchTaskGroups.mockRejectedValueOnce(new Error('down'))
    await get().load('h1')
    expect(entry()).toMatchObject({ status: 'error', revision: 1, groups: [g('a')] })
  })

  it('an answer that lands after a newer load, or after the host was forgotten, is dropped', async () => {
    let first!: (v: TaskGroupsRead) => void
    fetchTaskGroups.mockImplementationOnce(() => new Promise<TaskGroupsRead>((r) => { first = r }))
    const slow = get().load('h1')
    fetchTaskGroups.mockResolvedValueOnce(read([g('new')], 5))
    await get().load('h1')
    first(read([g('old')], 2))
    await slow
    expect(entry()).toMatchObject({ revision: 5, groups: [g('new')] })
    let late!: (v: TaskGroupsRead) => void
    fetchTaskGroups.mockImplementationOnce(() => new Promise<TaskGroupsRead>((r) => { late = r }))
    const p = get().load('h2')
    get().forget('h2')
    late(read([g('x')], 1))
    await p
    expect(get().byHost.h2).toBeUndefined()
  })
})

describe('update', () => {
  beforeEach(async () => {
    fetchTaskGroups.mockResolvedValue(read([g('a', ['r1'])], 4))
    await get().load('h1')
  })

  it('writes the changed list with the revision it read, and keeps the stored copy', async () => {
    putTaskGroups.mockResolvedValue(read([g('a', ['r1', 'r2'])], 5))
    const out = await get().update('h1', (groups) => groups.map((x) => (x.id === 'a' ? { ...x, roots: [...x.roots, 'r2'] } : x)))
    expect(out).toEqual({ ok: true })
    expect(putTaskGroups).toHaveBeenCalledWith('h1', [g('a', ['r1', 'r2'])], 4)
    expect(entry()).toMatchObject({ revision: 5, groups: [g('a', ['r1', 'r2'])] })
  })

  it('a change that changes nothing writes nothing', async () => {
    expect(await get().update('h1', () => null)).toEqual({ ok: true })
    expect(putTaskGroups).not.toHaveBeenCalled()
  })

  it('a lost compare-and-set re-applies the change on the daemon\'s copy, once', async () => {
    putTaskGroups
      .mockRejectedValueOnce(new HostConfigConflictError({ items: [g('a', ['r1']), g('b', ['r9'])], revision: 6 }))
      .mockResolvedValueOnce(read([g('a', ['r1', 'r2']), g('b', ['r9'])], 7))
    const out = await get().update('h1', (groups) => groups.map((x) => (x.id === 'a' ? { ...x, roots: [...x.roots, 'r2'] } : x)))
    expect(out).toEqual({ ok: true })
    expect(putTaskGroups).toHaveBeenCalledTimes(2)
    expect(putTaskGroups.mock.calls[1]).toEqual(['h1', [g('a', ['r1', 'r2']), g('b', ['r9'])], 6])
    expect(entry()).toMatchObject({ revision: 7 })
  })

  it('a second lost compare-and-set gives up, adopts the daemon\'s copy, and says conflict', async () => {
    putTaskGroups
      .mockRejectedValueOnce(new HostConfigConflictError({ items: [g('a')], revision: 6 }))
      .mockRejectedValueOnce(new HostConfigConflictError({ items: [g('a'), g('c')], revision: 8 }))
    const out = await get().update('h1', (groups) => [...groups, g('n')])
    expect(out).toEqual({ ok: false, reason: 'conflict' })
    expect(entry()).toMatchObject({ revision: 8, groups: [g('a'), g('c')] })
  })

  it.each([
    ['a row the App could not read', { items: [g('a'), { id: 'bad', name: 7 }], revision: 6 }],
    ['a body that is not a list', { items: 'junk', revision: 6 }],
  ])('a lost compare-and-set whose copy has %s: the retry does not write it back', async (_n, theirs) => {
    putTaskGroups.mockRejectedValueOnce(new HostConfigConflictError(theirs as never))
    const out = await get().update('h1', (groups) => [...groups, g('n')])
    expect(out).toEqual({ ok: false, reason: 'unsafe' })
    expect(putTaskGroups).toHaveBeenCalledTimes(1)
  })

  it('a refusal says why in the daemon\'s words and leaves the copy alone', async () => {
    putTaskGroups.mockRejectedValue(new HostConfigApiError(400, 'task group 0: root "r1" is already in group "b"'))
    const out = await get().update('h1', (groups) => [...groups, g('n', ['r1'])])
    expect(out).toEqual({ ok: false, reason: 'rejected', message: 'task group 0: root "r1" is already in group "b"' })
    expect(entry()).toMatchObject({ revision: 4, groups: [g('a', ['r1'])] })
  })

  it('a network failure is "error"', async () => {
    putTaskGroups.mockRejectedValue(new TypeError('Failed to fetch'))
    expect(await get().update('h1', (groups) => [...groups, g('n')])).toMatchObject({ ok: false, reason: 'error' })
  })

  it('a host that has not been read is never written', async () => {
    expect(await get().update('never-read', (groups) => [...groups, g('n')])).toEqual({ ok: false, reason: 'not-loaded' })
    expect(putTaskGroups).not.toHaveBeenCalled()
  })

  it('a write\'s answer that lands after the host was forgotten and added back is the old daemon\'s: not adopted', async () => {
    let release!: (v: TaskGroupsRead) => void
    putTaskGroups.mockImplementationOnce(() => new Promise<TaskGroupsRead>((r) => { release = r }))
    const w = get().update('h1', (groups) => [...groups, g('old')])
    await new Promise((r) => setTimeout(r, 0))
    get().forget('h1')
    fetchTaskGroups.mockResolvedValueOnce(read([g('fresh')], 1))
    await get().load('h1')
    release(read([g('a', ['r1']), g('old')], 5))
    await w
    expect(entry()).toMatchObject({ revision: 1, groups: [g('fresh')] })
  })

  it('a read that was out while a write finished does not bring the older state back', async () => {
    let answer!: (v: TaskGroupsRead) => void
    fetchTaskGroups.mockImplementationOnce(() => new Promise<TaskGroupsRead>((r) => { answer = r }))
    const reading = get().load('h1')
    putTaskGroups.mockResolvedValueOnce(read([g('a', ['r1']), g('n')], 5))
    await get().update('h1', (groups) => [...groups, g('n')])
    answer(read([g('a', ['r1'])], 4)) // the state before the write
    await reading
    expect(entry()).toMatchObject({ status: 'ready', revision: 5, groups: [g('a', ['r1']), g('n')] })
  })

  it('a change queued behind another write, when the host is forgotten and added back, is not sent to the new endpoint', async () => {
    let release!: (v: TaskGroupsRead) => void
    putTaskGroups.mockImplementationOnce(() => new Promise<TaskGroupsRead>((r) => { release = r }))
    const first = get().update('h1', (groups) => [...groups, g('n1')])
    const queued = get().update('h1', (groups) => [...groups, g('n2')])
    await new Promise((r) => setTimeout(r, 0))
    get().forget('h1')
    fetchTaskGroups.mockResolvedValueOnce(read([g('fresh')], 1))
    await get().load('h1')
    putTaskGroups.mockClear()
    release(read([g('n1')], 9))
    await first
    expect(await queued).toEqual({ ok: false, reason: 'host-changed' })
    expect(putTaskGroups).not.toHaveBeenCalled()
  })

  it('after a lost compare-and-set, a forget before the retry stops the retry', async () => {
    let lose!: () => void
    putTaskGroups.mockImplementationOnce(() => new Promise((_r, rej) => { lose = () => rej(new HostConfigConflictError({ items: [g('a')], revision: 6 })) }))
    const w = get().update('h1', (groups) => [...groups, g('n')])
    await new Promise((r) => setTimeout(r, 0))
    get().forget('h1')
    fetchTaskGroups.mockResolvedValueOnce(read([g('fresh')], 1))
    await get().load('h1')
    putTaskGroups.mockClear()
    lose()
    expect(await w).toEqual({ ok: false, reason: 'host-changed' })
    expect(putTaskGroups).not.toHaveBeenCalled()
    expect(entry()).toMatchObject({ revision: 1, groups: [g('fresh')] })
  })

  it.each([
    ['rows the daemon left out', { dropped: { count: 1, reasons: ['root "x" is already in group "a"'] }, invalid: false }],
    ['a stored value that is not a list of groups', { dropped: null, invalid: true }],
  ])('%s: the visible list is not the whole stored list, so nothing is written back', async (_n, marks) => {
    fetchTaskGroups.mockResolvedValue({ ...read([g('a')], 4), ...marks })
    await get().load('h1')
    expect(await get().update('h1', (groups) => [...groups, g('n')])).toEqual({ ok: false, reason: 'unsafe' })
    expect(putTaskGroups).not.toHaveBeenCalled()
  })

  it('one write at a time per host: a second one waits for the first and sees its result', async () => {
    let release!: (v: TaskGroupsRead) => void
    putTaskGroups.mockImplementationOnce(() => new Promise<TaskGroupsRead>((r) => { release = r }))
    const first = get().update('h1', (groups) => [...groups, g('n1')])
    const second = get().update('h1', (groups) => [...groups, g('n2')])
    await new Promise((r) => setTimeout(r, 0))
    expect(putTaskGroups).toHaveBeenCalledTimes(1) // the second waits
    putTaskGroups.mockResolvedValueOnce(read([g('a', ['r1']), g('n1'), g('n2')], 6))
    release(read([g('a', ['r1']), g('n1')], 5))
    await first
    await second
    expect(putTaskGroups.mock.calls[1]).toEqual(['h1', [g('a', ['r1']), g('n1'), g('n2')], 5])
  })
})
