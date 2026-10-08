// spa/src/lib/team/unattended-toggle.test.ts — one press of the title-bar button (unattended spec D-U23-5; plan PU-2b):
// off or partial → on, on → off; one PUT per reachable shown host, in parallel; failures collected, never guessed.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ApprovalApiError } from './approval-api'
import { putUnattended } from './unattended-api'
import { toggleUnattended } from './unattended-toggle'
import type { UnattendedAggregate } from './unattended-aggregate'
import type { UnattendedView } from './types'

vi.mock('./unattended-api', () => ({ putUnattended: vi.fn() }))
const mockedPut = vi.mocked(putUnattended)

const view = (on: boolean, over: Partial<UnattendedView> = {}): UnattendedView =>
  ({ on, since: 1, changed_at: 1, approved: [], truncated: false, ...over })

const agg = (over: Partial<UnattendedAggregate>): UnattendedAggregate =>
  ({ mode: 'off', on: [], off: [], unreachable: [], unsupported: [], reachable: [], ...over })

beforeEach(() => {
  mockedPut.mockReset()
  mockedPut.mockImplementation(async (_hostId, on) => view(on))
})

describe('toggleUnattended', () => {
  it('off → on, written to every reachable host and nothing else', async () => {
    const r = await toggleUnattended(agg({ mode: 'off', off: ['a', 'b'], reachable: ['a', 'b'] }))
    expect(r).toEqual({ target: true, failed: [] })
    expect(mockedPut.mock.calls).toEqual([['a', true], ['b', true]])
  })

  it('partial → on, for the reachable hosts only (never the unreachable or unsupported ones)', async () => {
    const r = await toggleUnattended(agg({
      mode: 'partial', on: ['a'], off: ['b'], unreachable: ['c'], unsupported: ['d'], reachable: ['a', 'b'],
    }))
    expect(r.target).toBe(true)
    expect(mockedPut.mock.calls).toEqual([['a', true], ['b', true]])
  })

  it('on → off', async () => {
    const r = await toggleUnattended(agg({ mode: 'on', on: ['a', 'b'], reachable: ['a', 'b'] }))
    expect(r).toEqual({ target: false, failed: [] })
    expect(mockedPut.mock.calls).toEqual([['a', false], ['b', false]])
  })

  it('none writes nothing', async () => {
    const r = await toggleUnattended(agg({ mode: 'none' }))
    expect(r.failed).toEqual([])
    expect(mockedPut).not.toHaveBeenCalled()
  })

  it('the PUTs run in parallel: the second starts before the first answers', async () => {
    const resolvers: Array<() => void> = []
    mockedPut.mockImplementation((_hostId, on) => new Promise((res) => resolvers.push(() => res(view(on)))))
    const p = toggleUnattended(agg({ mode: 'off', off: ['a', 'b'], reachable: ['a', 'b'] }))
    await Promise.resolve()
    expect(mockedPut).toHaveBeenCalledTimes(2)
    for (const r of resolvers) r()
    expect((await p).failed).toEqual([])
  })

  it('failures are collected per host with their code; the others still succeed', async () => {
    mockedPut.mockImplementation(async (hostId, on) => {
      if (hostId === 'b') throw new ApprovalApiError(0, 'network', 'Failed to fetch')
      if (hostId === 'c') throw new ApprovalApiError(200, 'bad_response')
      return view(on)
    })
    const r = await toggleUnattended(agg({ mode: 'off', off: ['a', 'b', 'c'], reachable: ['a', 'b', 'c'] }))
    expect(r).toEqual({ target: true, failed: [{ hostId: 'b', code: 'network' }, { hostId: 'c', code: 'bad_response' }] })
  })

  it('an error that is not the API error still fails that host', async () => {
    mockedPut.mockRejectedValueOnce(new Error('boom'))
    const r = await toggleUnattended(agg({ mode: 'on', on: ['a'], reachable: ['a'] }))
    expect(r.failed).toEqual([{ hostId: 'a', code: 'error' }])
  })

  it('a 200 with list_failed is a success (only the state is read from it)', async () => {
    mockedPut.mockImplementation(async (_hostId, on) => view(on, { list_failed: true }))
    const r = await toggleUnattended(agg({ mode: 'off', off: ['a'], reachable: ['a'] }))
    expect(r.failed).toEqual([])
  })
})
