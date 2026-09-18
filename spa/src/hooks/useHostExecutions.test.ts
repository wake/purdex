// spa/src/hooks/useHostExecutions.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useHostExecutions } from './useHostExecutions'
import { resetExecutionListForTests, useExecutionListStore } from '../stores/useExecutionListStore'
import { useNexHostStore, type NexHostEntry } from '../stores/useNexHostStore'
import { useHostStore } from '../stores/useHostStore'
import { subscriptionSlots } from '../lib/nex/subscription-slots'
import type { ExecutionSummary, ExecutionsPage } from '../lib/nex/types'
import * as api from '../lib/nex/nex-api'
import * as sse from '../lib/nex/nex-sse'

vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))

const H = 'host-a'
const row = (id: string): ExecutionSummary =>
  ({ id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false }) as ExecutionSummary
const readyEntry: NexHostEntry = {
  info: { configured: true, mounted: true, ready: true, init_error: '', effective: null },
  capabilities: null, phase: 'ready', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:t',
}

let sseClose: ReturnType<typeof vi.fn<() => void>>
let ensure: ReturnType<typeof vi.fn<(hostId: string) => Promise<void>>>

describe('useHostExecutions', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    subscriptionSlots.resetForTests()
    resetExecutionListForTests()
    useExecutionListStore.setState({ byHost: {} })
    ensure = vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined)
    useNexHostStore.setState({ byHost: { [H]: readyEntry }, ensure })
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'A', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [H], activeHostId: H, runtime: {} })
    sseClose = vi.fn<() => void>()
    vi.mocked(sse.openNexSse).mockReset().mockImplementation(() => ({ close: sseClose }))
    vi.mocked(api.listExecutions).mockReset().mockResolvedValue({ items: [row('exc_1')], next_cursor: '' })
  })
  afterEach(() => vi.useRealTimers())

  it('mount ensures the nex host and subscribes; rows arrive through the store', async () => {
    const { result } = renderHook(() => useHostExecutions(H))
    expect(ensure).toHaveBeenCalledWith(H)
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(result.current.items).toEqual([])
    expect(result.current.phase).toBe('loading')
    expect(result.current.refreshRevision).toBe(0)

    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(result.current.items.map((r) => r.id)).toEqual(['exc_1'])
    expect(result.current.phase).toBe('ready')
    expect(result.current.error).toBeNull()
    expect(result.current.refreshRevision).toBe(1)
  })

  it('unmount unsubscribes: the SSE closes and the lane is released', () => {
    const { unmount } = renderHook(() => useHostExecutions(H))
    expect(sseClose).not.toHaveBeenCalled()
    unmount()
    expect(sseClose).toHaveBeenCalledTimes(1)
  })

  it('re-mount shows cached rows immediately', async () => {
    const first = renderHook(() => useHostExecutions(H))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(first.result.current.items).toHaveLength(1)
    first.unmount()

    vi.mocked(api.listExecutions).mockImplementation(() => new Promise<ExecutionsPage>(() => {}))
    const second = renderHook(() => useHostExecutions(H))
    expect(second.result.current.items.map((r) => r.id)).toEqual(['exc_1'])
    expect(second.result.current.phase).toBe('ready')
    expect(sse.openNexSse).toHaveBeenCalledTimes(2)
  })

  it('returns a stable empty array and idle phase for a host that is not nex-ready', () => {
    const { result, rerender } = renderHook(() => useHostExecutions('ghost'))
    const items = result.current.items
    rerender()
    expect(result.current.items).toBe(items)
    expect(result.current.phase).toBe('idle')
    expect(sse.openNexSse).not.toHaveBeenCalled()
  })
})
