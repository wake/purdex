// spa/src/stores/useSessionStore.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useSessionStore } from './useSessionStore'

vi.mock('../lib/api', () => ({
  listSessions: vi.fn().mockResolvedValue([
    { id: 1, name: 'test', tmux_target: 'test:0', cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0 },
  ]),
}))

beforeEach(() => {
  // Reset zustand store between tests
  useSessionStore.setState({ sessions: [], activeId: null })
})

describe('useSessionStore', () => {
  it('fetches sessions', async () => {
    const { result } = renderHook(() => useSessionStore())
    await act(async () => { await result.current.fetch('http://localhost:7860') })
    expect(result.current.sessions).toHaveLength(1)
    expect(result.current.sessions[0].name).toBe('test')
  })

  it('sets active session', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => { result.current.setActive(1) })
    expect(result.current.activeId).toBe(1)
  })

  it('persists to localStorage', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => { result.current.setActive(42) })
    // Zustand persist middleware writes to localStorage
    const stored = JSON.parse(localStorage.getItem('tbox-sessions') || '{}')
    expect(stored.state?.activeId).toBe(42)
  })
})
