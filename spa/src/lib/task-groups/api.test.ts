// spa/src/lib/task-groups/api.test.ts — the daemon's manual task groups (name pool v2 spec §7, NA-6a): read from `GET /api/hostconfig` (`taskGroups`), written by `PUT /api/hostconfig/task-groups` with
// `{items, baseRevision}`. The answer is a trust boundary: a row that is not the wire shape is left out, never half-read.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const hostFetch = vi.fn()
vi.mock('../host-api', () => ({ hostFetch: (...a: unknown[]) => hostFetch(...a) }))
const knownHosts: Record<string, unknown> = { h1: {} }
vi.mock('../../stores/useHostStore', () => ({ useHostStore: { getState: () => ({ hosts: knownHosts }) } }))

import { fetchTaskGroups, parseTaskGroups, putTaskGroups, type TaskGroup } from './api'
import { HostConfigApiError, HostConfigConflictError } from '../host-config-api'

const res = (status: number, body: unknown, text = '') => ({
  ok: status >= 200 && status < 300, status, statusText: '',
  json: async () => body, text: async () => text || (typeof body === 'string' ? body : JSON.stringify(body)),
})
const group = (id: string, over: Partial<TaskGroup> = {}): TaskGroup => ({ id, name: `Group ${id}`, roots: [], created_at: 1, updated_at: 2, ...over })

beforeEach(() => hostFetch.mockReset())

describe('parseTaskGroups', () => {
  it('reads the rows, the revision and the dropped marker', () => {
    const r = parseTaskGroups({ taskGroups: { items: [group('a', { color: '#3b82f6', roots: ['r1', 'r2'] })], revision: 5, dropped: { count: 1, reasons: ['root "x" is already in group "a"'] } } })
    expect(r.items).toEqual([group('a', { color: '#3b82f6', roots: ['r1', 'r2'] })])
    expect(r.revision).toBe(5)
    expect(r.dropped).toEqual({ count: 1, reasons: ['root "x" is already in group "a"'] })
    expect(r.invalid).toBe(false)
  })

  it('a daemon without the row reads as no groups, revision 0', () => {
    expect(parseTaskGroups({})).toEqual({ items: [], revision: 0, dropped: null, invalid: false })
    expect(parseTaskGroups(null)).toEqual({ items: [], revision: 0, dropped: null, invalid: false })
  })

  it('leaves out a row that is not the wire shape, and keeps the others in order', () => {
    const r = parseTaskGroups({ taskGroups: { revision: 1, items: [
      group('a'), { id: '', name: 'x', roots: [], created_at: 1, updated_at: 1 }, { id: 'b', name: 7, roots: [], created_at: 1, updated_at: 1 },
      { id: 'c', name: 'C', roots: ['r', 3], created_at: 1, updated_at: 1 }, { id: 'd', name: 'D', roots: [], created_at: 'x', updated_at: 1 }, group('e', { color: 'red' }), 'junk', group('f'),
    ] } })
    expect(r.items.map((g) => g.id)).toEqual(['a', 'f'])
  })

  it('marks an invalid stored value', () => {
    expect(parseTaskGroups({ taskGroups: { items: [], revision: 3, invalid: true } })).toMatchObject({ invalid: true, revision: 3 })
  })
})

describe('fetchTaskGroups', () => {
  it('asks GET /api/hostconfig of that host and parses the row', async () => {
    hostFetch.mockResolvedValue(res(200, { taskGroups: { items: [group('a')], revision: 2 } }))
    const r = await fetchTaskGroups('h1')
    expect(hostFetch).toHaveBeenCalledWith('h1', '/api/hostconfig', expect.anything())
    expect(r.items).toHaveLength(1)
  })

  it('a host that is not configured never reaches the network', async () => {
    await expect(fetchTaskGroups('nope')).rejects.toBeInstanceOf(HostConfigApiError)
    expect(hostFetch).not.toHaveBeenCalled()
  })

  it('a non-2xx is an error carrying the status', async () => {
    hostFetch.mockResolvedValue(res(500, 'boom'))
    await expect(fetchTaskGroups('h1')).rejects.toMatchObject({ status: 500 })
  })
})

describe('putTaskGroups', () => {
  it('PUTs {items, baseRevision} and answers the new revision', async () => {
    hostFetch.mockResolvedValue(res(200, { items: [group('a')], revision: 6 }))
    const out = await putTaskGroups('h1', [group('a')], 5)
    const [host, path, init] = hostFetch.mock.calls[0]
    expect([host, path, init.method]).toEqual(['h1', '/api/hostconfig/task-groups', 'PUT'])
    expect(JSON.parse(init.body)).toEqual({ items: [group('a')], baseRevision: 5 })
    expect(out.revision).toBe(6)
    expect(out.items).toEqual([group('a')])
  })

  it('a lost compare-and-set carries the daemon\'s copy, parsed like a read', async () => {
    hostFetch.mockResolvedValue(res(409, { items: [group('z')], revision: 9 }))
    const err = await putTaskGroups('h1', [], 5).catch((e) => e)
    expect(err).toBeInstanceOf(HostConfigConflictError)
    expect(parseTaskGroups({ taskGroups: err.current })).toMatchObject({ revision: 9, items: [group('z')] })
  })

  it('a 400 says why, in the daemon\'s words', async () => {
    hostFetch.mockResolvedValue(res(400, '', 'task group 0: root "r" is already in group "b"'))
    await expect(putTaskGroups('h1', [], 1)).rejects.toMatchObject({ status: 400, message: expect.stringContaining('already in group') })
  })
})
