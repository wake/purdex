// spa/src/lib/notify/group-labels-writer.test.ts — when the app writes its group names to a host (notify title spec §3.2; plan N-3 #2, #5).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const hostFetch = vi.fn()
vi.mock('../host-api', () => ({ hostFetch: (...a: unknown[]) => hostFetch(...a) }))
vi.mock('../client-identity', () => ({ getClientId: () => 'c_aaaaaaaaaaaa' }))

import { startGroupLabelsWriter, GROUP_LABELS_DEBOUNCE_MS } from './group-labels-writer'
import { NOTIFY_GROUP_LABELS_MAX } from './types'
import { useHostStore } from '../../stores/useHostStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useRootCacheStore } from '../task-groups/root-cache'
import { setRootsInterest } from '../task-groups/session-roots-lifecycle'
import { manualGroupKey } from '../task-groups/group-key'
import type { Tab } from '../../types/tab'

const setSupport = (hostId: string, v: 'yes' | 'no' | 'unknown') =>
  useUnattendedStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], support: 'unknown', notifyGroupLabelsSupport: v } } }) as never)
const group = (name: string, members: { host: string; root: string }[]) => ({ name, members, createdAt: 1 })
const setGroups = (groups: Record<string, ReturnType<typeof group>>) => useManualGroupsStore.setState({ order: Object.keys(groups), groups })
const bodies = () => hostFetch.mock.calls.map((c) => JSON.parse((c[2] as RequestInit).body as string))
const tick = () => vi.advanceTimersByTime(GROUP_LABELS_DEBOUNCE_MS)

let stop: () => void
beforeEach(() => {
  vi.useFakeTimers()
  hostFetch.mockReset()
  hostFetch.mockResolvedValue({ ok: true, status: 200 })
  useHostStore.setState({ hosts: { h1: { id: 'h1' }, h2: { id: 'h2' } } as never, runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } } as never })
  useUnattendedStore.setState({ byHost: {} })
  useTabStore.setState({ tabs: {} })
  useRootCacheStore.setState({ roots: {} })
  setGroups({ g1: group('Alpha', [{ host: 'h1', root: 'R1' }]) })
  stop = startGroupLabelsWriter()
})
afterEach(() => { stop(); setRootsInterest(false, [], 'notify'); vi.useRealTimers() })

describe('capability gate', () => {
  it('writes only a connected host that lists the capability (an older daemon has no route)', () => {
    setSupport('h1', 'yes'); setSupport('h2', 'no')
    tick()
    expect(hostFetch).toHaveBeenCalledTimes(1)
    expect(hostFetch.mock.calls[0][0]).toBe('h1')
    expect(hostFetch.mock.calls[0][1]).toBe('/api/notify/group-labels')
    expect((hostFetch.mock.calls[0][2] as RequestInit).method).toBe('PUT')
    expect(bodies()[0]).toEqual({ device: 'c_aaaaaaaaaaaa', labels: [{ root_session_id: 'R1', group: 'Alpha' }] })
  })
  it('unknown support writes nothing', () => {
    tick()
    expect(hostFetch).not.toHaveBeenCalled()
  })
})

describe('when it writes', () => {
  beforeEach(() => { setSupport('h1', 'yes'); tick(); hostFetch.mockClear() })
  it('an unchanged projection is not written again', () => {
    setGroups({ g1: group('Alpha', [{ host: 'h1', root: 'R1' }]) })
    useTabStore.setState({ tabs: {} })
    tick()
    expect(hostFetch).not.toHaveBeenCalled()
  })
  it('a rename is one debounced write of the whole set', () => {
    setGroups({ g1: group('A1', [{ host: 'h1', root: 'R1' }]) })
    setGroups({ g1: group('A2', [{ host: 'h1', root: 'R1' }]) })
    expect(hostFetch).not.toHaveBeenCalled()
    tick()
    expect(bodies()).toEqual([{ device: 'c_aaaaaaaaaaaa', labels: [{ root_session_id: 'R1', group: 'A2' }] }])
  })
  it('deleting the last group sends an explicit empty set', () => {
    setGroups({})
    tick()
    expect(bodies()).toEqual([{ device: 'c_aaaaaaaaaaaa', labels: [] }])
  })
  it('a reconnect sends the set again', () => {
    useHostStore.setState((s) => ({ runtime: { ...s.runtime, h1: { status: 'disconnected' } } }) as never)
    useUnattendedStore.getState().invalidateSupport('h1')
    tick()
    expect(hostFetch).not.toHaveBeenCalled()
    setSupport('h1', 'yes')
    useHostStore.setState((s) => ({ runtime: { ...s.runtime, h1: { status: 'connected' } } }) as never)
    tick()
    expect(hostFetch).toHaveBeenCalledTimes(1)
  })
  it('a failed write is logged and not retried until something changes', async () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    hostFetch.mockResolvedValue({ ok: false, status: 403 })
    setGroups({ g1: group('Alpha2', [{ host: 'h1', root: 'R1' }]) })
    tick()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(hostFetch).toHaveBeenCalledTimes(1)
    expect(err).toHaveBeenCalled()
    err.mockRestore()
  })
  it('a newer set waits for the write still out, then goes (codex R1: no overtaking)', async () => {
    let finish: (v: unknown) => void = () => {}
    hostFetch.mockImplementationOnce(() => new Promise((r) => { finish = r }))
    setGroups({ g1: group('First', [{ host: 'h1', root: 'R1' }]) })
    tick()
    setGroups({ g1: group('Second', [{ host: 'h1', root: 'R1' }]) })
    tick()
    expect(hostFetch).toHaveBeenCalledTimes(1)
    finish({ ok: true, status: 200 })
    await vi.advanceTimersByTimeAsync(GROUP_LABELS_DEBOUNCE_MS)
    expect(bodies().map((b) => b.labels[0].group)).toEqual(['First', 'Second'])
  })
  it('more than the cap is not sent and not cut', () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    setGroups({ g1: group('Big', Array.from({ length: NOTIFY_GROUP_LABELS_MAX + 1 }, (_, i) => ({ host: 'h1', root: `R${i}` }))) })
    tick()
    expect(hostFetch).not.toHaveBeenCalled()
    expect(err).toHaveBeenCalled()
    err.mockRestore()
  })
})

describe('tab.group sessions', () => {
  it('are written once their root is cached', async () => {
    const tab = { id: 't1', createdAt: 1, group: manualGroupKey('g1'), layout: { type: 'leaf', pane: { id: 'p', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'c', mode: 'terminal', cachedName: 'c', tmuxInstance: '', rebuild: { agent: { sessionId: 'S1' } } } } } } as unknown as Tab
    setSupport('h1', 'yes')
    useTabStore.setState({ tabs: { t1: tab } })
    tick()
    expect(bodies().at(-1)?.labels).toEqual([{ root_session_id: 'R1', group: 'Alpha' }])
    useRootCacheStore.setState({ roots: { h1: { S1: 'R7' } } })
    await vi.advanceTimersByTimeAsync(0) // the first write answered
    await vi.advanceTimersByTimeAsync(GROUP_LABELS_DEBOUNCE_MS)
    expect(bodies().at(-1)?.labels).toEqual([{ root_session_id: 'R1', group: 'Alpha' }, { root_session_id: 'R7', group: 'Alpha' }])
  })
})
