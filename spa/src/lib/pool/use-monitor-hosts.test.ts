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
import type { PoolHost } from './types'

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
    answer([host(A, 'mlab'), host(B, 'mserv')])
    const first = renderHook(() => useMonitorHosts())
    expect(first.result.current).toHaveLength(2)
    first.unmount()
    usePoolStore.setState({ hosts: [], sourceHostId: null, error: { code: 'wwd_unreachable', since: 1 } } as never)
    const { result } = renderHook(() => useMonitorHosts())
    expect(result.current.map((h) => h.alias)).toEqual(['mlab', 'mserv'])
    expect(result.current.every((h) => h.state.state === 'unknown')).toBe(true)
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
