import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { usePathCheck } from './usePathCheck'
import * as api from '../../lib/host-config-api'

vi.mock('../../lib/host-config-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-config-api')>()),
  checkHostPath: vi.fn(),
}))

beforeEach(() => { vi.useFakeTimers(); vi.mocked(api.checkHostPath).mockReset() })
afterEach(() => vi.useRealTimers())

describe('usePathCheck', () => {
  it('debounces 400 ms and reports the daemon verdict', async () => {
    vi.mocked(api.checkHostPath).mockResolvedValue({ status: 'dir', resolved: '/w' })
    const { result, rerender } = renderHook(({ path }) => usePathCheck('h1', path), { initialProps: { path: '~/a' } })
    expect(result.current).toBe('checking')
    rerender({ path: '~/w' })
    await act(async () => { vi.advanceTimersByTime(399) })
    expect(api.checkHostPath).not.toHaveBeenCalled()
    await act(async () => { vi.advanceTimersByTime(1); await Promise.resolve() })
    expect(api.checkHostPath).toHaveBeenCalledTimes(1)
    expect(vi.mocked(api.checkHostPath).mock.calls[0].slice(0, 2)).toEqual(['h1', '~/w'])
    expect(result.current).toBe('dir')
  })

  it('an empty path is idle and never checked', async () => {
    const { result } = renderHook(() => usePathCheck('h1', '  '))
    await act(async () => { vi.advanceTimersByTime(1000) })
    expect(result.current).toBe('idle')
    expect(api.checkHostPath).not.toHaveBeenCalled()
  })

  it('a stale answer for an earlier path is ignored', async () => {
    let resolveFirst: (v: api.PathCheck) => void = () => {}
    vi.mocked(api.checkHostPath)
      .mockImplementationOnce(() => new Promise((r) => { resolveFirst = r }))
      .mockResolvedValueOnce({ status: 'missing', resolved: '/b' })
    const { result, rerender } = renderHook(({ path }) => usePathCheck('h1', path), { initialProps: { path: '/a' } })
    await act(async () => { vi.advanceTimersByTime(400) })
    rerender({ path: '/b' })
    await act(async () => { resolveFirst({ status: 'dir', resolved: '/a' }); await Promise.resolve() })
    expect(result.current).toBe('checking')
    await act(async () => { vi.advanceTimersByTime(400); await Promise.resolve() })
    expect(result.current).toBe('missing')
  })
})
