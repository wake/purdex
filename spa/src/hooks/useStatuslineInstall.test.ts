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
})
