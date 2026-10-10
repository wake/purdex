// spa/src/lib/workbook/workbook-loader.retry.test.ts — #2435: a roster seat whose first load got no answer (network / 5xx) is
// retried with the same bounded backoff as the tab's own workbook (1s / 2s / 4s, at most 3 retries); a 404 or a success is not.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

const fetchConversation = vi.fn()
vi.mock('./api', () => ({ fetchConversation: (...a: unknown[]) => fetchConversation(...a) }))

import { startWorkbookLoader } from './workbook-loader'
import { useHostStore } from '../../stores/useHostStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import type { TeamRoster } from '../team/roster'

const host = (id: string) => ({ id, name: id, ip: '100.64.0.2', port: 7860, token: 't', order: 0, daemonId: 'd1' })
const sess = (id: string) => ({ session_id: id, ref: '_aaaaaa', address: `h/${id}`, live: true })
const team = (members: string[] = []): TeamRoster =>
  ({ id: 'a', host_id: 'd1', created_at: 1, team_name: '', team_label: '', lead: sess('L'), members: members.map((m) => ({ ...sess(m), state: 'active', origin: 'spawn', joined_at: 1 })) }) as unknown as TeamRoster
const ok = { kind: 'ok', page: { convKey: 'c', status: '', statusAt: 0, entries: [], todos: null, refreshAvailable: null } }
const roster = (teams: TeamRoster[]) => useTeamRosterStore.getState().apply('h1', teams)
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms)
const askedLead = () => fetchConversation.mock.calls.filter((c) => c[2] === 'L').length

let stop: (() => void) | null = null
beforeEach(() => {
  vi.useFakeTimers()
  useHostStore.getState().reset()
  useTeamRosterStore.getState().reset()
  useWorkbookStore.getState().reset()
  fetchConversation.mockReset()
  useHostStore.setState({ hosts: { h1: host('h1') }, hostOrder: ['h1'], runtime: {} })
  useWorkbookStore.getState().setSupport('h1', { v1: true, v2: false })
})
afterEach(() => { stop?.(); stop = null; vi.useRealTimers() })

describe('roster seat first-load retry', () => {
  it('a failed first load is retried after 1s, 2s, 4s and then given up (3 retries)', async () => {
    fetchConversation.mockRejectedValue(new Error('offline'))
    stop = startWorkbookLoader()
    roster([team()])
    await tick(0)
    expect(askedLead()).toBe(1)
    await tick(999); expect(askedLead()).toBe(1)
    await tick(1); expect(askedLead()).toBe(2)
    await tick(1999); expect(askedLead()).toBe(2)
    await tick(1); expect(askedLead()).toBe(3)
    await tick(3999); expect(askedLead()).toBe(3)
    await tick(1); expect(askedLead()).toBe(4)
    await tick(60000)
    expect(askedLead()).toBe(4)
  })

  it('a retry that succeeds stops the retrying', async () => {
    fetchConversation.mockRejectedValueOnce(new Error('offline')).mockResolvedValue(ok)
    stop = startWorkbookLoader()
    roster([team()])
    await tick(1000)
    expect(askedLead()).toBe(2)
    await tick(60000)
    expect(askedLead()).toBe(2)
    expect(useWorkbookStore.getState().convOfSession.h1.L).toBe('c')
  })

  it('a 404 is an answer: not retried', async () => {
    fetchConversation.mockResolvedValue({ kind: 'not_found' })
    stop = startWorkbookLoader()
    roster([team()])
    await tick(60000)
    expect(askedLead()).toBe(1)
  })

  it('a seat that leaves the roster is no longer retried', async () => {
    fetchConversation.mockRejectedValue(new Error('offline'))
    stop = startWorkbookLoader()
    roster([team(['m1'])])
    await tick(0)
    roster([team()])
    await tick(60000)
    expect(fetchConversation.mock.calls.filter((c) => c[2] === 'm1').length).toBe(1)
  })

  it('a new connection generation drops the pending retry and starts over with the new probe answer', async () => {
    fetchConversation.mockRejectedValue(new Error('offline'))
    stop = startWorkbookLoader()
    roster([team()])
    await tick(0)
    useWorkbookStore.getState().fence('h1') // the old generation's retry is cancelled
    await tick(60000)
    expect(askedLead()).toBe(1)
    useWorkbookStore.getState().setSupport('h1', { v1: true, v2: false }) // the new generation: asked again, with a fresh retry budget
    await tick(0)
    expect(askedLead()).toBe(2)
    await tick(1000)
    expect(askedLead()).toBe(3)
  })

  it('stopping the loader clears every pending retry', async () => {
    fetchConversation.mockRejectedValue(new Error('offline'))
    stop = startWorkbookLoader()
    roster([team(['m1', 'm2'])])
    await tick(0)
    expect(vi.getTimerCount()).toBe(3)
    stop()
    stop = null
    expect(vi.getTimerCount()).toBe(0)
    await tick(60000)
    expect(fetchConversation).toHaveBeenCalledTimes(3)
  })

  it('a roster frame during the wait does not skip the backoff', async () => {
    fetchConversation.mockRejectedValue(new Error('offline'))
    stop = startWorkbookLoader()
    roster([team()])
    await tick(0)
    roster([team(['m1'])])
    await tick(0)
    expect(askedLead()).toBe(1)
  })
})
