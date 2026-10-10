// spa/src/lib/workbook/workbook-loader.test.ts — the fetch bound of plan WA-1.3: seats × connection generations, only on a
// workbook.v1 host; the capability probe feeds the generation; a removed or re-pointed host is forgotten.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { HostInfo } from '../../stores/useHostStore'

const fetchConversation = vi.fn()
vi.mock('./api', () => ({ fetchConversation: (...a: unknown[]) => fetchConversation(...a) }))
const fetchHostInfo = vi.fn<(hostId: string) => Promise<HostInfo>>()
vi.mock('../host-api', () => ({ fetchHostInfo: (hostId: string) => fetchHostInfo(hostId) }))

import { seatTargets, startWorkbookLoader } from './workbook-loader'
import { startUnattendedSupport } from '../team/unattended-support'
import { startRosterForget } from '../team/roster-forget'
import { useHostStore } from '../../stores/useHostStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { selectWorkbookSupport, useWorkbookStore } from '../../stores/useWorkbookStore'
import type { RosterMember, TeamRoster } from '../team/roster'

const host = (id: string, ip = '100.64.0.2', daemonId?: string) => ({ id, name: id, ip, port: 7860, token: 't', order: 0, ...(daemonId ? { daemonId } : {}) })
const sess = (id: string, extra: Record<string, unknown> = {}) => ({ session_id: id, ref: '_aaaaaa', address: `h/${id}`, live: true, ...extra })
const member = (id: string, extra: Record<string, unknown> = {}): RosterMember => ({ ...sess(id, extra), state: 'active', origin: 'spawn', joined_at: 1 }) as RosterMember
const team = (id: string, members: RosterMember[] = [], lead = 'L'): TeamRoster =>
  ({ id, host_id: 'd1', created_at: 1, team_name: '', team_label: '', lead: sess(lead), members }) as TeamRoster
const info = (capabilities: string[]): HostInfo =>
  ({ host_id: 'x', tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '', capabilities }) as HostInfo
const flush = () => new Promise((r) => setTimeout(r, 0))
const roster = (h: string, teams: TeamRoster[]) => useTeamRosterStore.getState().apply(h, teams)
const calls = () => fetchConversation.mock.calls.map((c) => `${c[0]}/${c[2]}`)

let stops: Array<() => void> = []
beforeEach(() => {
  useHostStore.getState().reset()
  useTeamRosterStore.getState().reset()
  useWorkbookStore.getState().reset()
  fetchConversation.mockReset()
  fetchConversation.mockResolvedValue({ kind: 'ok', page: { convKey: 'c', status: '', statusAt: 0, entries: [] } })
  fetchHostInfo.mockReset()
  fetchHostInfo.mockResolvedValue(info(['workbook.v1', 'workbook.v2']))
  useHostStore.setState({ hosts: { h1: host('h1', '100.64.0.2', 'd1'), h2: host('h2', '100.64.0.4', 'd2') }, hostOrder: ['h1', 'h2'], runtime: {} })
})
afterEach(() => { stops.forEach((f) => f()); stops = [] })

const verified = (_id: string, daemonId: string, ip: string) => ({ endpoint: `${ip}:7860`, daemonId })
describe('seatTargets', () => {
  it('lead and members on the roster\'s host; a remote member on the host its daemon id maps to; the unmappable and untrusted skipped', () => {
    useHostStore.setState({ runtime: { h1: { daemonIdVerified: verified('h1', 'd1', '100.64.0.2') }, h2: { daemonIdVerified: verified('h2', 'd2', '100.64.0.4') } } as never })
    const t = team('a', [member('m1'), member('m2', { host_id: 'd2' }), member('m3', { host_id: 'dX' }), member('m4', { host_untrusted: true })])
    const targets = seatTargets({ h1: [t] }, useHostStore.getState()).map((x) => `${x.hostId}/${x.sessionId}`)
    expect(targets).toEqual(['h1/L', 'h1/m1', 'h2/m2'])
  })
})

describe('a remote seat never goes to a host whose daemon id is not verified', () => {
  const remote = () => [team('a', [member('m2', { host_id: 'd2' })])]
  const remoteCalls = () => calls().filter((c) => c.startsWith('h2/'))
  beforeEach(() => { useWorkbookStore.getState().setSupport('h2', { v1: true, v2: false }) })

  it('a stored (synced) daemon id nothing verified yet: no fetch; once verified, one', async () => {
    stops.push(startWorkbookLoader())
    roster('h1', remote())
    await flush()
    expect(remoteCalls()).toEqual([])
    useHostStore.setState({ runtime: { h2: { daemonIdVerified: verified('h2', 'd2', '100.64.0.4') } } as never })
    await flush()
    expect(remoteCalls()).toEqual(['h2/m2'])
  })

  it('a daemon id verified at an old endpoint (stale after a re-point): no fetch', async () => {
    useHostStore.setState({ runtime: { h2: { daemonIdVerified: verified('h2', 'd2', '1.1.1.1') } } as never })
    stops.push(startWorkbookLoader())
    roster('h1', remote())
    await flush()
    expect(remoteCalls()).toEqual([])
  })

  it('a mismatch (another daemon answered there): no fetch', async () => {
    useHostStore.setState({ runtime: { h2: { daemonIdMismatch: { endpoint: '100.64.0.4:7860', stored: 'd2', observed: 'zz' } } } as never })
    stops.push(startWorkbookLoader())
    roster('h1', remote())
    await flush()
    expect(remoteCalls()).toEqual([])
  })
})

describe('the loader', () => {
  it('a seat is fetched once when it first appears, not again on the next roster frame', async () => {
    useWorkbookStore.getState().setSupport('h1', { v1: true, v2: false })
    stops.push(startWorkbookLoader())
    roster('h1', [team('a', [member('m1')])])
    roster('h1', [team('a', [member('m1')])]) // the same seats again
    await flush()
    expect(calls().sort()).toEqual(['h1/L', 'h1/m1'])
    expect(fetchConversation.mock.calls[0][3]).toEqual({ limit: 1 })
    roster('h1', [team('a', [member('m1'), member('m2')])]) // a new seat
    await flush()
    expect(calls().sort()).toEqual(['h1/L', 'h1/m1', 'h1/m2'])
  })

  it('a seat that leaves the roster stops being a held seat in the store', async () => {
    useWorkbookStore.getState().setSupport('h1', { v1: true, v2: false })
    stops.push(startWorkbookLoader())
    roster('h1', [team('a', [member('m1')])])
    await flush()
    expect(Object.keys(useWorkbookStore.getState().seatGen.h1).sort()).toEqual(['L', 'm1'])
    roster('h1', [team('a')])
    expect(Object.keys(useWorkbookStore.getState().seatGen.h1)).toEqual(['L'])
  })

  it('capability off: no fetch; it starts once the host is known to list workbook.v1', async () => {
    stops.push(startWorkbookLoader())
    roster('h1', [team('a', [member('m1')])])
    useWorkbookStore.getState().setSupport('h1', { v1: false, v2: false })
    await flush()
    expect(fetchConversation).not.toHaveBeenCalled()
    useWorkbookStore.getState().setSupport('h1', { v1: true, v2: false })
    await flush()
    expect(calls().sort()).toEqual(['h1/L', 'h1/m1'])
  })

  it('a reconnect (a new probe answer) fetches every seat once more, and only once', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    stops.push(startUnattendedSupport(), startWorkbookLoader())
    roster('h1', [team('a', [member('m1')])])
    await flush()
    expect(fetchConversation).toHaveBeenCalledTimes(2)
    useHostStore.setState({ runtime: { h1: { status: 'reconnecting' } } })
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } }) // the probe runs again
    await flush()
    expect(fetchConversation).toHaveBeenCalledTimes(4)
    roster('h1', [team('a', [member('m1')])])
    await flush()
    expect(fetchConversation).toHaveBeenCalledTimes(4)
  })

  it('a connection that stays up (latency ticks, the same roster) never refetches a seat; a drop and return does, once', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    stops.push(startUnattendedSupport(), startWorkbookLoader())
    roster('h1', [team('a', [member('m1')])])
    await flush()
    expect(fetchConversation).toHaveBeenCalledTimes(2)
    for (let i = 0; i < 5; i++) useHostStore.setState({ runtime: { h1: { status: 'connected', latency: i } } })
    await flush()
    expect(fetchConversation).toHaveBeenCalledTimes(2)
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
    useHostStore.setState({ runtime: { h1: { status: 'reconnecting' } } })
    expect(selectWorkbookSupport('h1').v1).toBe(false) // the drop fences at once
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    await flush()
    expect(fetchConversation).toHaveBeenCalledTimes(4)
  })

  it('a probe that fails after a drop leaves support unknown and fetches nothing', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    stops.push(startUnattendedSupport(), startWorkbookLoader())
    roster('h1', [team('a')])
    await flush()
    fetchConversation.mockClear()
    fetchHostInfo.mockRejectedValue(new Error('down'))
    useHostStore.setState({ runtime: { h1: { status: 'reconnecting' } } })
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    await flush()
    expect(selectWorkbookSupport('h1').v1).toBe(false)
    expect(fetchConversation).not.toHaveBeenCalled()
  })

  it('the probe feeds selectWorkbookSupport: v1 and v2 from the capabilities, none from an old daemon', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } } })
    fetchHostInfo.mockImplementation(async (id) => info(id === 'h1' ? ['workbook.v1'] : []))
    stops.push(startUnattendedSupport())
    await flush()
    expect(selectWorkbookSupport('h1')).toMatchObject({ v1: true, v2: false })
    expect(selectWorkbookSupport('h2')).toMatchObject({ v1: false, v2: false })
  })

  it('a removed or re-pointed host is forgotten (entries, support), another host is kept', () => {
    stops.push(startRosterForget())
    for (const h of ['h1', 'h2']) {
      useWorkbookStore.getState().setSupport(h, { v1: true, v2: false })
      useWorkbookStore.getState().applyStatus(h, { convKey: 'c', sessionId: 's', status: 'x', updatedAt: 1 })
    }
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: host('h1', '9.9.9.9', 'd1') } })) // re-point h1
    expect(useWorkbookStore.getState().byHost.h1).toBeUndefined()
    expect(selectWorkbookSupport('h1').v1).toBe(false)
    expect(useWorkbookStore.getState().byHost.h2).toBeDefined()
    const { h2: _g, ...rest } = useHostStore.getState().hosts
    useHostStore.setState({ hosts: rest, hostOrder: ['h1'] }) // remove h2
    expect(useWorkbookStore.getState().byHost.h2).toBeUndefined()
  })
})
