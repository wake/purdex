// spa/src/lib/pool/use-cell-models.test.tsx — useRunPool keeps the pool's daemon identities fresh (monitoring tabs MON-1a).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { usePoolStore } from './store'
import { refreshPoolIdentities } from './daemons'
import { useRunPool } from './use-cell-models'
import { NEX_HOST_TTL_MS } from '../../stores/useNexHostStore'

vi.mock('./daemons', async (importOriginal) => ({ ...(await importOriginal<typeof import('./daemons')>()), refreshPoolIdentities: vi.fn() }))

beforeEach(() => {
  vi.useFakeTimers()
  vi.mocked(refreshPoolIdentities).mockClear()
  usePoolStore.setState({ start: vi.fn(), stop: vi.fn() } as never)
})
afterEach(() => vi.useRealTimers())

describe('useRunPool', () => {
  it('asks for missing identities on mount, when the pool hosts change, and again after each cache TTL', () => {
    const { unmount } = renderHook(() => useRunPool())
    expect(refreshPoolIdentities).toHaveBeenCalledTimes(1)
    usePoolStore.setState({ hosts: [] })
    expect(refreshPoolIdentities).toHaveBeenCalledTimes(2)
    vi.advanceTimersByTime(NEX_HOST_TTL_MS)
    expect(refreshPoolIdentities).toHaveBeenCalledTimes(3)
    unmount()
    usePoolStore.setState({ hosts: [{ alias: 'x', host_id: 'y' }] as never })
    vi.advanceTimersByTime(NEX_HOST_TTL_MS * 2)
    expect(refreshPoolIdentities).toHaveBeenCalledTimes(3)
  })
})
