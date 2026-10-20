// spa/src/lib/pool/use-monitor-hosts.test.ts — the sidebar's host list from the pool only (monitoring tabs MON-1c; M13, M21, W4).
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useMonitorHosts, resetRememberedPoolHosts } from './use-monitor-hosts'
import { usePoolStore } from './store'
import { usePoolHostsStore } from '../../stores/usePoolHostsStore'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { parseFacts, parseHostsAnswer } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import hostsJson from './__fixtures__/pool_hosts_response.json'
import { useJobsStore } from './jobs-store'
import type { PoolHost } from './types'
import type { PoolJob } from './jobs-types'

const A = '82b11c521df16c12'
const B = '0123456789abcdef'
const C = 'aaaaaaaaaaaaaaaa'
const facts = parseFacts(darwinJson)!
const base = parseHostsAnswer(hostsJson)![0]
const NOW = facts.at + 5000

const host = (host_id: string, alias: string, over: Partial<PoolHost> = {}): PoolHost => ({ ...base, host_id, alias, reachable: true, facts, liveness: undefined, ...over }) as PoolHost
const start = vi.fn()
const stop = vi.fn()

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(NOW)
  resetRememberedPoolHosts()
  usePoolStore.setState({ start, stop, hosts: [], sourceHostId: null, lastFactsAt: {}, error: null } as never)
  usePoolHostsStore.setState({ order: [], looks: {} })
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {} } as never)
  useNexHostStore.setState({ byHost: {} } as never)
})

const answer = (hosts: PoolHost[], lastFactsAt: Record<string, number> = {}) => usePoolStore.setState({ hosts, sourceHostId: 'cfg-a', lastFactsAt } as never)

const wjob = (id: string, origin: string, over: Record<string, unknown> = {}): PoolJob => ({
  id, origin, state: 'queued', task: 'go-all', args: [], class: 'dev', labels: [], locks: [], manual: '', manual_seq: 0, claim: null, result: null, created_at: facts.at - 1000,
  repo: { name: 'purdex', root: '', remote: '' }, submitter: { session: '', ref: '', team: '', host: origin }, supersede_key: '', cacheable: true, interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, timeout_s: 0, ...over,
}) as unknown as PoolJob
const withWorker = (h: PoolHost): PoolHost => ({ ...h, worker: { enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 0, light: 0 } }, labels: [] }) as unknown as PoolHost
const seedJobs = (jobs: PoolJob[]) => {
  const byOrigin: Record<string, { jobs: Record<string, PoolJob>; cursor: string; epoch: string; via: string }> = {}
  for (const j of jobs) (byOrigin[j.origin] ??= { jobs: {}, cursor: '', epoch: '', via: '' }).jobs[j.id] = j
  useJobsStore.setState({ byOrigin } as never)
}

describe('useMonitorHosts — the count at the corner (M6, M25)', () => {
  beforeEach(() => { seedJobs([]) })

  it('carries each ok host\'s count: what it sent that waits plus what its worker has claimed', () => {
    answer([withWorker(host(A, 'mlab')), withWorker(host(B, 'mserv'))])
    seedJobs([wjob('a', 'mlab'), wjob('b', 'mlab'), wjob('c', 'mlab', { state: 'running', claim: { worker: 'mserv', attempt: 1, since: 0 } })])
    const { result } = renderHook(() => useMonitorHosts())
    expect(result.current.map((h) => h.count)).toEqual([{ n: 2, tone: 'neutral' }, { n: 1, tone: 'neutral' }])
  })
  it('nothing to count is null, not zero', () => {
    answer([withWorker(host(A, 'mlab'))])
    expect(renderHook(() => useMonitorHosts()).result.current[0].count).toBeNull()
  })
  it('a host that is not ok has no count (M17–M19)', () => {
    answer([withWorker(host(A, 'mlab', { reachable: false, facts: null, liveness: 'host_down' }))])
    seedJobs([wjob('a', 'mlab')])
    expect(renderHook(() => useMonitorHosts()).result.current[0].count).toBeNull()
  })
  it('a pool nobody can read has no counts, whatever the jobs store holds (M25)', () => {
    useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'x', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['cfg-a'] } as never)
    answer([withWorker(host(A, 'mlab'))])
    seedJobs([wjob('a', 'mlab')])
    const { result } = renderHook(() => useMonitorHosts())
    expect(result.current[0].count).toEqual({ n: 1, tone: 'neutral' })
    act(() => { usePoolStore.setState({ hosts: [], sourceHostId: null, error: { code: 'no_answer', since: 1 } } as never) })
    expect(result.current.map((h) => h.count)).toEqual([null])
  })
})

describe('useMonitorHosts', () => {
  it('lists the pool\'s hosts in the order of the store, the unlisted after them in the pool\'s order (M13)', () => {
    answer([host(A, 'mlab'), host(B, 'mserv'), host(C, 'air26')])
    usePoolHostsStore.setState({ order: [C, A] })
    const { result } = renderHook(() => useMonitorHosts())
    expect(result.current.map((h) => h.alias)).toEqual(['air26', 'mlab', 'mserv'])
  })

  it('each host carries its state (M21) and its look', () => {
    answer([host(A, 'mlab'), host(B, 'mserv', { reachable: false, facts: null, liveness: 'host_down' })], { [B]: facts.at - 90_000 })
    usePoolHostsStore.setState({ looks: { [A]: { icon: 'Cube' } } })
    const { result } = renderHook(() => useMonitorHosts())
    expect(result.current[0].state.state).toBe('ok')
    expect(result.current[0].look.icon).toBe('Cube')
    expect(result.current[1].state).toMatchObject({ state: 'host_down', lastFactsAt: facts.at - 90_000 })
  })

  it('the daemon host id is the App host that fronts the pool host, or null', () => {
    answer([host(A, 'mlab'), host(B, 'mserv')])
    useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'x', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['cfg-a'] } as never)
    useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: 1, daemonCapabilities: [], wwd: { hostId: A, alias: 'mlab' } } } } as never)
    const { result } = renderHook(() => useMonitorHosts())
    expect(result.current.map((h) => h.daemonHostId)).toEqual(['cfg-a', null])
  })

  it('the clock moves facts from ok to unknown/stalled without a new answer', () => {
    answer([host(A, 'mlab')])
    const { result } = renderHook(() => useMonitorHosts())
    expect(result.current[0].state.state).toBe('ok')
    act(() => { vi.advanceTimersByTime(60_000) })
    expect(result.current[0].state).toMatchObject({ state: 'unknown', stalled: true })
  })

  it('W4: when no source answers, the last non-empty list stays, every host unknown', () => {
    useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'x', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['cfg-a'] } as never)
    answer([host(A, 'mlab'), host(B, 'mserv')])
    const first = renderHook(() => useMonitorHosts())
    expect(first.result.current).toHaveLength(2)
    first.unmount()
    usePoolStore.setState({ hosts: [], sourceHostId: null, error: { code: 'wwd_unreachable', since: 1 } } as never)
    const { result } = renderHook(() => useMonitorHosts())
    expect(result.current.map((h) => h.alias)).toEqual(['mlab', 'mserv'])
    expect(result.current.every((h) => h.state.state === 'unknown')).toBe(true)
  })

  it('W4: one hook mounted through empty, a successful answer and then a total failure keeps the list, every host unknown, with the pool\'s failure', () => {
    useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'x', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['cfg-a'] } as never)
    const { result } = renderHook(() => useMonitorHosts())
    expect(result.current).toEqual([])
    act(() => { answer([host(A, 'mlab'), host(B, 'mserv')]) })
    expect(result.current.map((h) => h.state.state)).toEqual(['ok', 'ok'])
    expect(result.current.every((h) => h.poolFailure === null)).toBe(true)
    act(() => { usePoolStore.setState({ hosts: [], sourceHostId: null, error: { code: 'wwd_unavailable', since: 7 } } as never) })
    expect(result.current.map((h) => h.alias)).toEqual(['mlab', 'mserv'])
    expect(result.current.every((h) => h.state.state === 'unknown')).toBe(true)
    expect(result.current[0].poolFailure).toEqual({ code: 'wwd_unavailable', since: 7 })
  })

  it('W4: with no failure code (no daemon connected) the failure is carried as null', () => {
    useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'x', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['cfg-a'] } as never)
    answer([host(A, 'mlab')])
    renderHook(() => useMonitorHosts()).unmount()
    usePoolStore.setState({ hosts: [], sourceHostId: null, error: null } as never)
    expect(renderHook(() => useMonitorHosts()).result.current[0].poolFailure).toEqual({ code: null, since: null })
  })

  it('W4: an empty answer from a source replaces the kept list: a later total failure does not bring the older hosts back', () => {
    useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'x', ip: '1', port: 1, token: 't', order: 0 }, 'cfg-b': { id: 'cfg-b', name: 'y', ip: '2', port: 1, token: 't', order: 1 } }, hostOrder: ['cfg-a', 'cfg-b'] } as never)
    const { result } = renderHook(() => useMonitorHosts())
    act(() => { usePoolStore.setState({ hosts: [host(A, 'mlab')], sourceHostId: 'cfg-a' } as never) })
    expect(result.current).toHaveLength(1)
    act(() => { usePoolStore.setState({ hosts: [], sourceHostId: 'cfg-b' } as never) })
    expect(result.current).toEqual([])
    act(() => { usePoolStore.setState({ hosts: [], sourceHostId: null, error: { code: 'no_answer', since: 1 } } as never) })
    expect(result.current).toEqual([])
  })

  it('W4: the kept list is dropped once the App host that served it is gone (another world, a removed host)', () => {
    useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'x', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['cfg-a'] } as never)
    answer([host(A, 'mlab')])
    const first = renderHook(() => useMonitorHosts())
    first.unmount()
    usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
    useHostStore.setState({ hosts: {}, hostOrder: [] } as never)
    expect(renderHook(() => useMonitorHosts()).result.current).toEqual([])
  })

  it('W4: never read since the start is an empty list; an empty answer from a source is empty too', () => {
    const { result, rerender } = renderHook(() => useMonitorHosts())
    expect(result.current).toEqual([])
    answer([host(A, 'mlab')])
    rerender()
    expect(result.current).toHaveLength(1)
    answer([])
    rerender()
    expect(result.current).toEqual([])
  })

  it('a fleet host the pool does not list is not here: the fleet is not read at all', () => {
    answer([host(A, 'mlab')])
    const { result } = renderHook(() => useMonitorHosts())
    expect(result.current.map((h) => h.hostId)).toEqual([A])
  })
})
