// spa/src/stores/useSessionStore.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useSessionStore } from './useSessionStore'
import { useHostStore } from './useHostStore'
import { STORAGE_KEYS } from '../lib/storage'

const MOCK_SESSIONS = [
  { code: 'abc123', name: 'test', cwd: '/tmp', mode: 'terminal' as const, cc_session_id: '', cc_model: '', has_relay: false },
  { code: 'def456', name: 'dev', cwd: '/home', mode: 'stream' as const, cc_session_id: '', cc_model: '', has_relay: false },
]

vi.mock('../lib/api', () => ({
  listSessions: vi.fn().mockResolvedValue([
    { code: 'abc123', name: 'test', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
  ]),
}))

const HOST_ID = useHostStore.getState().hostOrder[0]

beforeEach(() => {
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
})

describe('useSessionStore', () => {
  it('fetchHost populates sessions for a host', async () => {
    const { result } = renderHook(() => useSessionStore())
    await act(async () => {
      await result.current.fetchHost(HOST_ID, 'http://localhost:7860')
    })
    expect(result.current.sessions[HOST_ID]).toHaveLength(1)
    expect(result.current.sessions[HOST_ID][0].name).toBe('test')
  })

  it('replaceHost replaces sessions for a host', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => {
      result.current.replaceHost(HOST_ID, MOCK_SESSIONS)
    })
    expect(result.current.sessions[HOST_ID]).toHaveLength(2)
    expect(result.current.sessions[HOST_ID][0].code).toBe('abc123')
    expect(result.current.sessions[HOST_ID][1].code).toBe('def456')

    // Replace with empty
    act(() => {
      result.current.replaceHost(HOST_ID, [])
    })
    expect(result.current.sessions[HOST_ID]).toHaveLength(0)
  })

  it('removeHost clears sessions and resets active if matching', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => {
      result.current.replaceHost(HOST_ID, MOCK_SESSIONS)
      result.current.setActive(HOST_ID, 'abc123')
    })
    expect(result.current.sessions[HOST_ID]).toHaveLength(2)
    expect(result.current.activeHostId).toBe(HOST_ID)

    act(() => {
      result.current.removeHost(HOST_ID)
    })
    expect(result.current.sessions[HOST_ID]).toBeUndefined()
    expect(result.current.activeHostId).toBeNull()
    expect(result.current.activeCode).toBeNull()
  })

  it('removeHost preserves active if different host', () => {
    const { result } = renderHook(() => useSessionStore())
    const OTHER_HOST = 'other-host-id'
    act(() => {
      result.current.replaceHost(HOST_ID, MOCK_SESSIONS)
      result.current.replaceHost(OTHER_HOST, MOCK_SESSIONS)
      result.current.setActive(HOST_ID, 'abc123')
    })

    act(() => {
      result.current.removeHost(OTHER_HOST)
    })
    expect(result.current.sessions[OTHER_HOST]).toBeUndefined()
    expect(result.current.activeHostId).toBe(HOST_ID)
    expect(result.current.activeCode).toBe('abc123')
  })

  it('setActive sets hostId and code', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => {
      result.current.setActive(HOST_ID, 'abc123')
    })
    expect(result.current.activeHostId).toBe(HOST_ID)
    expect(result.current.activeCode).toBe('abc123')
  })

  it('setActive can clear to null', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => {
      result.current.setActive(HOST_ID, 'abc123')
    })
    act(() => {
      result.current.setActive(null, null)
    })
    expect(result.current.activeHostId).toBeNull()
    expect(result.current.activeCode).toBeNull()
  })

  it('persists activeHostId and activeCode', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => {
      result.current.setActive(HOST_ID, 'abc123')
    })
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEYS.SESSIONS) || '{}')
    expect(stored.state?.activeHostId).toBe(HOST_ID)
    expect(stored.state?.activeCode).toBe('abc123')
  })

  it('does not persist sessions', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => {
      result.current.replaceHost(HOST_ID, MOCK_SESSIONS)
    })
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEYS.SESSIONS) || '{}')
    expect(stored.state?.sessions).toBeUndefined()
  })
})
