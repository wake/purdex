import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useStatuslineInstall } from './useStatuslineInstall'
import { hostFetch } from '../lib/host-api'

vi.mock('../lib/host-api', () => ({
  hostFetch: vi.fn(),
}))

const mockFetch = hostFetch as unknown as ReturnType<typeof vi.fn>

beforeEach(() => mockFetch.mockReset())

describe('useStatuslineInstall', () => {
  it('loads status on mount', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.state.mode).toBe('none'))
  })

  it('install with mode=pdx POSTs and refreshes', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.state.mode).toBe('none'))

    await act(async () => {
      await result.current.install('pdx')
    })
    expect(mockFetch).toHaveBeenCalledWith('host1', '/api/agent/cc/statusline/setup', expect.objectContaining({ method: 'POST' }))
    expect(result.current.state.mode).toBe('pdx')
  })

  it('install with mode=wrap passes inner', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'unmanaged', installed: true, innerCommand: 'ccstatusline', settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'wrapped', installed: true, innerCommand: 'ccstatusline', settingsPath: '/x' }) })

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.state.mode).toBe('unmanaged'))

    await act(async () => {
      await result.current.install('wrap', 'ccstatusline')
    })
    const call = mockFetch.mock.calls[1]
    expect(JSON.parse(call[2].body)).toMatchObject({ action: 'install', mode: 'wrap', inner: 'ccstatusline' })
  })

  it('remove POSTs action=remove', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.state.mode).toBe('pdx'))

    await act(async () => {
      await result.current.remove()
    })
    expect(result.current.state.mode).toBe('none')
  })

  // --- Error path tests ---

  it('refresh error when response not ok', async () => {
    mockFetch.mockResolvedValueOnce({ ok: false, status: 500 })

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.phase).toBe('error'))
    expect(result.current.error).toContain('500')
  })

  it('install error when response not ok', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: false, status: 500 })

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.phase).toBe('ready'))

    await act(async () => {
      await result.current.install('pdx')
    })
    expect(result.current.phase).toBe('error')
    expect(result.current.error).toContain('500')
  })

  it('remove 409 sets unmanaged error message', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'unmanaged', installed: true, settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: false, status: 409 })

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.phase).toBe('ready'))

    await act(async () => {
      await result.current.remove()
    })
    expect(result.current.phase).toBe('error')
    expect(result.current.error).toContain('Cannot remove unmanaged statusLine')
  })

  it('install network throw sets error state', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
      .mockRejectedValueOnce(new Error('network down'))

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.phase).toBe('ready'))

    await act(async () => {
      await result.current.install('pdx')
    })
    expect(result.current.phase).toBe('error')
    expect(result.current.error).toContain('network down')
  })

  // --- Stale error clearing (Fix 1) ---

  it('install clears stale error from previous failure', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: false, status: 500 })  // first install fails
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })  // second install succeeds

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.state.mode).toBe('none'))

    await act(async () => { await result.current.install('pdx') })
    expect(result.current.error).not.toBeNull()

    await act(async () => { await result.current.install('pdx') })
    expect(result.current.error).toBeNull()
    expect(result.current.state.mode).toBe('pdx')
  })

  it('remove clears stale error from previous failure', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: false, status: 500 })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.state.mode).toBe('pdx'))

    await act(async () => { await result.current.remove() })
    expect(result.current.error).not.toBeNull()

    await act(async () => { await result.current.remove() })
    expect(result.current.error).toBeNull()
  })
})
