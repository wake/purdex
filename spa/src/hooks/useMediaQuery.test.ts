import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useMediaQuery } from './useMediaQuery'

describe('useMediaQuery', () => {
  it('returns false when matchMedia reports false', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((q: string) => ({
      matches: false, media: q,
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
    })))
    const { result } = renderHook(() => useMediaQuery('(min-width: 1024px)'))
    expect(result.current).toBe(false)
    vi.unstubAllGlobals()
  })

  it('returns true when matchMedia reports true', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((q: string) => ({
      matches: true, media: q,
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
    })))
    const { result } = renderHook(() => useMediaQuery('(min-width: 1024px)'))
    expect(result.current).toBe(true)
    vi.unstubAllGlobals()
  })

  it('registers and cleans up change listener', () => {
    const add = vi.fn()
    const remove = vi.fn()
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((q: string) => ({
      matches: false, media: q,
      addEventListener: add, removeEventListener: remove,
    })))
    const { unmount } = renderHook(() => useMediaQuery('(min-width: 640px)'))
    expect(add).toHaveBeenCalledWith('change', expect.any(Function))
    unmount()
    expect(remove).toHaveBeenCalledWith('change', expect.any(Function))
    vi.unstubAllGlobals()
  })
})
