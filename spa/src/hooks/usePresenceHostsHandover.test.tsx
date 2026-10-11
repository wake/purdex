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
