import { describe, it, expect, vi } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useModuleHook } from './useModuleHook'
import type { HookModule, HookModuleStatus } from '../lib/hook-modules'

const OK_STATUS: HookModuleStatus = {
  installed: true,
  events: { 'event-a': { installed: true }, 'event-b': { installed: true } },
  issues: [],
}

const PARTIAL_STATUS: HookModuleStatus = {
  installed: false,
  events: { 'event-a': { installed: true }, 'event-b': { installed: false } },
  issues: ['event-b hook not installed'],
}

function mockModule(overrides?: Partial<HookModule>): HookModule {
  return {
    id: 'test',
    labelKey: 'test.label',
    descKey: 'test.desc',
    fetchStatus: vi.fn(() => Promise.resolve(OK_STATUS)),
    setup: vi.fn(() => Promise.resolve(OK_STATUS)),
    ...overrides,
  }
}

describe('useModuleHook', () => {
  it('fetches status on mount', async () => {
    const mod = mockModule()
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.status).toEqual(OK_STATUS)
    expect(result.current.error).toBeNull()
    expect(mod.fetchStatus).toHaveBeenCalledWith('host-1')
  })

  it('exposes error on fetch failure (4xx/5xx)', async () => {
    const mod = mockModule({
      fetchStatus: vi.fn(() => Promise.reject(new Error('403 Forbidden'))),
    })
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.status).toBeNull()
    expect(result.current.error).toBe('403 Forbidden')
  })

  it('cancels stale fetch when hostId changes', async () => {
    let resolveFirst: (v: HookModuleStatus) => void
    const firstPromise = new Promise<HookModuleStatus>((r) => { resolveFirst = r })
    const mod = mockModule({
      fetchStatus: vi.fn()
        .mockReturnValueOnce(firstPromise)
        .mockReturnValueOnce(Promise.resolve(PARTIAL_STATUS)),
    })
    const { result, rerender } = renderHook(
      ({ hostId }) => useModuleHook(mod, hostId, 0),
      { initialProps: { hostId: 'host-1' } },
    )
    rerender({ hostId: 'host-2' })
    await waitFor(() => expect(result.current.loading).toBe(false))
    resolveFirst!(OK_STATUS)
    await new Promise((r) => setTimeout(r, 50))
    expect(result.current.status).toEqual(PARTIAL_STATUS)
  })

  it('setup() updates status from return value', async () => {
    const mod = mockModule({
      setup: vi.fn(() => Promise.resolve(PARTIAL_STATUS)),
    })
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))
    await waitFor(() => expect(result.current.loading).toBe(false))
    await act(async () => { await result.current.setup('remove') })
    expect(result.current.status).toEqual(PARTIAL_STATUS)
    expect(mod.setup).toHaveBeenCalledWith('host-1', 'remove')
  })

  it('setup() failure shows error', async () => {
    const mod = mockModule({
      setup: vi.fn(() => Promise.reject(new Error('500 Internal Server Error'))),
    })
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))
    await waitFor(() => expect(result.current.loading).toBe(false))
    await act(async () => { await result.current.setup('install') })
    expect(result.current.error).toBe('500 Internal Server Error')
  })

  it('refreshKey change triggers re-fetch', async () => {
    const mod = mockModule()
    const { result, rerender } = renderHook(
      ({ refreshKey }) => useModuleHook(mod, 'host-1', refreshKey),
      { initialProps: { refreshKey: 0 } },
    )
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(mod.fetchStatus).toHaveBeenCalledTimes(1)
    rerender({ refreshKey: 1 })
    await waitFor(() => expect(mod.fetchStatus).toHaveBeenCalledTimes(2))
  })

  it('returns lastTrigger from module.getLastTrigger', async () => {
    const triggers = { SessionStart: 1700000000000 }
    const mod = mockModule({ getLastTrigger: () => triggers })
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.lastTrigger).toEqual(triggers)
  })

  it('returns null lastTrigger when module has no getLastTrigger', async () => {
    const mod = mockModule()
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.lastTrigger).toBeNull()
  })
})
