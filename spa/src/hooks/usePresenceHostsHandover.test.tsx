import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { usePresenceHostsHandover } from './usePresenceHostsHandover'
import { useHostStore } from '../stores/useHostStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

const setHosts = vi.fn<(p: { rev: number; hosts: Array<{ hostId: string; baseUrl: string; token: string | null }> }) => Promise<boolean>>()
const win = window as unknown as { electronAPI?: unknown }

beforeEach(() => {
  setHosts.mockReset()
  setHosts.mockResolvedValue(true)
  useHostStore.getState().reset()
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'a', ip: '100.64.0.2', port: 7860, token: 'tok-1', order: 0 }, h2: { id: 'h2', name: 'b', ip: '1.2.3.4', port: 80, order: 1 } },
    hostOrder: ['h1', 'h2'], hostsRev: 7,
  })
})
afterEach(() => { delete win.electronAPI; vi.restoreAllMocks() })

describe('usePresenceHostsHandover', () => {
  it('does nothing without the bridge (plain browser)', () => {
    renderHook(() => usePresenceHostsHandover())
    useHostStore.getState().addHost({ name: 'c', ip: '9.9.9.9', port: 1 })
    expect(setHosts).not.toHaveBeenCalled()
  })

  it('does nothing when the shell has the group but not setHosts', () => {
    win.electronAPI = { presence: {} }
    renderHook(() => usePresenceHostsHandover())
    useHostStore.getState().addHost({ name: 'c', ip: '9.9.9.9', port: 1 })
    expect(setHosts).not.toHaveBeenCalled()
  })

  it('sends rev, base url and token on mount', () => {
    win.electronAPI = { presence: { setHosts } }
    renderHook(() => usePresenceHostsHandover())
    expect(setHosts).toHaveBeenCalledTimes(1)
    expect(setHosts).toHaveBeenCalledWith({
      rev: 7,
      hosts: [
        { hostId: 'h1', baseUrl: 'http://100.64.0.2:7860', token: 'tok-1' },
        { hostId: 'h2', baseUrl: 'http://1.2.3.4:80', token: null },
      ],
    })
  })

  it('sends again when hostsRev changes, not when it stays', () => {
    win.electronAPI = { presence: { setHosts } }
    renderHook(() => usePresenceHostsHandover())
    setHosts.mockClear()
    useHostStore.getState().setActiveHost('h2') // unrelated change
    useHostStore.getState().reorderHosts(['h2', 'h1'])
    expect(setHosts).not.toHaveBeenCalled()
    useHostStore.getState().updateHost('h2', { port: 81 })
    expect(setHosts).toHaveBeenCalledTimes(1)
    const arg = setHosts.mock.calls[0][0]
    expect(arg.rev).toBe(useHostStore.getState().hostsRev)
    expect(arg.hosts.find((h) => h.hostId === 'h2')?.baseUrl).toBe('http://1.2.3.4:81')
  })

  it('sends after a rehydrate from another window brought a newer list', async () => {
    win.electronAPI = { presence: { setHosts } }
    renderHook(() => usePresenceHostsHandover())
    setHosts.mockClear()
    localStorage.setItem(STORAGE_KEYS.HOSTS, JSON.stringify({
      state: { hosts: { h9: { id: 'h9', name: 'z', ip: '5.5.5.5', port: 5, token: 'tz', order: 0 } }, hostOrder: ['h9'], activeHostId: 'h9', devHostId: null, hostsRev: 99 },
      version: 1,
    }))
    await useHostStore.persist.rehydrate()
    expect(setHosts).toHaveBeenCalledTimes(1)
    expect(setHosts).toHaveBeenCalledWith({ rev: 99, hosts: [{ hostId: 'h9', baseUrl: 'http://5.5.5.5:5', token: 'tz' }] })
  })

  it('resends when the content changes under the same rev (two windows writing in one millisecond)', async () => {
    win.electronAPI = { presence: { setHosts } }
    renderHook(() => usePresenceHostsHandover())
    setHosts.mockClear()
    localStorage.setItem(STORAGE_KEYS.HOSTS, JSON.stringify({
      state: { hosts: { h9: { id: 'h9', name: 'z', ip: '5.5.5.5', port: 5, token: 'tz', order: 0 } }, hostOrder: ['h9'], activeHostId: 'h9', devHostId: null, hostsRev: 7 },
      version: 1,
    }))
    await useHostStore.persist.rehydrate()
    expect(setHosts).toHaveBeenCalledTimes(1)
    expect(setHosts).toHaveBeenCalledWith({ rev: 7, hosts: [{ hostId: 'h9', baseUrl: 'http://5.5.5.5:5', token: 'tz' }] })
  })

  it('does not resend an unchanged payload (name-only edit)', () => {
    win.electronAPI = { presence: { setHosts } }
    renderHook(() => usePresenceHostsHandover())
    setHosts.mockClear()
    useHostStore.getState().updateHost('h2', { name: 'renamed' })
    expect(setHosts).not.toHaveBeenCalled()
  })

  it('drops hosts main would reject (bad id, non-origin base) and keeps the valid ones, logging counts only', () => {
    win.electronAPI = { presence: { setHosts } }
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    useHostStore.setState({
      hosts: {
        h1: { id: 'h1', name: 'a', ip: '100.64.0.2', port: 7860, token: 'SECRET-TOKEN', order: 0 },
        'bad id!': { id: 'bad id!', name: 'b', ip: '1.2.3.4', port: 80, token: 'SECRET-2', order: 1 },
        h3: { id: 'h3', name: 'c', ip: '1.2.3.4/path?x', port: 80, token: 'SECRET-3', order: 2 },
      },
      hostOrder: ['h1', 'bad id!', 'h3'],
    })
    renderHook(() => usePresenceHostsHandover())
    expect(setHosts).toHaveBeenCalledTimes(1)
    expect(setHosts.mock.calls[0][0].hosts).toEqual([{ hostId: 'h1', baseUrl: 'http://100.64.0.2:7860', token: 'SECRET-TOKEN' }])
    expect(warn).toHaveBeenCalled()
    const logged = JSON.stringify(warn.mock.calls)
    expect(logged).not.toContain('SECRET')
    expect(logged).not.toContain('bad id')
    expect(logged).not.toContain('path')
  })

  it('caps the list at 64 hosts', () => {
    win.electronAPI = { presence: { setHosts } }
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const hosts: Record<string, { id: string; name: string; ip: string; port: number; order: number }> = {}
    const order: string[] = []
    for (let i = 0; i < 70; i++) { hosts[`x${i}`] = { id: `x${i}`, name: 'n', ip: '1.1.1.1', port: i + 1, order: i }; order.push(`x${i}`) }
    useHostStore.setState({ hosts, hostOrder: order })
    renderHook(() => usePresenceHostsHandover())
    expect(setHosts.mock.calls[0][0].hosts).toHaveLength(64)
  })

  describe('unconfirmed sends', () => {
    beforeEach(() => { vi.useFakeTimers() })
    afterEach(() => { vi.useRealTimers() })

    it('false then true: retried with backoff and confirmed, then quiet', async () => {
      win.electronAPI = { presence: { setHosts } }
      setHosts.mockResolvedValueOnce(false).mockResolvedValue(true)
      renderHook(() => usePresenceHostsHandover())
      await vi.advanceTimersByTimeAsync(0)
      expect(setHosts).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(1000)
      expect(setHosts).toHaveBeenCalledTimes(2)
      await vi.advanceTimersByTimeAsync(60_000)
      expect(setHosts).toHaveBeenCalledTimes(2)
    })

    it('a rejection is retried 1 s, 5 s, 30 s, then waits for the next change', async () => {
      win.electronAPI = { presence: { setHosts } }
      setHosts.mockRejectedValue(new Error('boom'))
      renderHook(() => usePresenceHostsHandover())
      await vi.advanceTimersByTimeAsync(0)
      expect(setHosts).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(1000)
      expect(setHosts).toHaveBeenCalledTimes(2)
      await vi.advanceTimersByTimeAsync(5000)
      expect(setHosts).toHaveBeenCalledTimes(3)
      await vi.advanceTimersByTimeAsync(30_000)
      expect(setHosts).toHaveBeenCalledTimes(4)
      await vi.advanceTimersByTimeAsync(10 * 60_000)
      expect(setHosts).toHaveBeenCalledTimes(4)
      useHostStore.getState().updateHost('h2', { port: 81 })
      expect(setHosts).toHaveBeenCalledTimes(5)
    })

    it('a newer change cancels the pending retry of the older payload', async () => {
      win.electronAPI = { presence: { setHosts } }
      setHosts.mockResolvedValueOnce(false).mockResolvedValue(true)
      renderHook(() => usePresenceHostsHandover())
      await vi.advanceTimersByTimeAsync(0)
      useHostStore.getState().updateHost('h2', { port: 81 })
      await vi.advanceTimersByTimeAsync(0)
      expect(setHosts).toHaveBeenCalledTimes(2)
      await vi.advanceTimersByTimeAsync(60_000)
      expect(setHosts).toHaveBeenCalledTimes(2)
    })
  })

  it('stops sending after unmount, and a rejected call is swallowed', () => {
    win.electronAPI = { presence: { setHosts } }
    setHosts.mockRejectedValue(new Error('boom'))
    const { unmount } = renderHook(() => usePresenceHostsHandover())
    unmount()
    setHosts.mockClear()
    useHostStore.getState().updateHost('h1', { port: 1 })
    expect(setHosts).not.toHaveBeenCalled()
  })
})
