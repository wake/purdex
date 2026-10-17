// spa/src/lib/team/unattended-support.journal.test.ts — the journal.v1 probe and its sticky answer (J-2a T-2a.1).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { HostInfo } from '../../stores/useHostStore'

const fetchHostInfo = vi.fn<(hostId: string) => Promise<HostInfo>>()
vi.mock('../host-api', () => ({ fetchHostInfo: (hostId: string) => fetchHostInfo(hostId) }))

import { startUnattendedSupport } from './unattended-support'
import { useHostStore } from '../../stores/useHostStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { liveSupport, stickySupport, useJournalStore } from '../../stores/useJournalStore'

const host = (id: string, ip = '100.64.0.2') => ({ id, name: id, ip, port: 7860, token: 't', order: 0 })
const info = (capabilities?: unknown): HostInfo =>
  ({ host_id: 'mini-lab:abc', tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '', ...(capabilities === undefined ? {} : { capabilities }) })
const flush = () => new Promise((r) => setTimeout(r, 0))
const setStatus = (status: 'connected' | 'disconnected') => useHostStore.setState({ runtime: { h1: { status } } })

let stop: () => void = () => {}

beforeEach(() => {
  useHostStore.getState().reset()
  useUnattendedStore.getState().reset()
  useJournalStore.getState().reset()
  fetchHostInfo.mockReset()
  fetchHostInfo.mockResolvedValue(info(['journal.v1']))
  useHostStore.setState({ hosts: { h1: host('h1') }, hostOrder: ['h1'], runtime: {} })
})
afterEach(() => { stop(); vi.restoreAllMocks() })

describe('journal.v1 support', () => {
  it('yes when listed, no when not, unknown (but nothing sticky) when the probe fails', async () => {
    setStatus('connected')
    stop = startUnattendedSupport()
    await flush()
    expect(liveSupport('h1')).toBe(true)
    expect(stickySupport('h1')).toBe(true)
    stop()
    useJournalStore.getState().reset()
    fetchHostInfo.mockResolvedValue(info(['workbook.v1']))
    stop = startUnattendedSupport()
    await flush()
    expect(liveSupport('h1')).toBe(false)
    expect(stickySupport('h1')).toBe(false)
    stop()
    useJournalStore.getState().reset()
    fetchHostInfo.mockRejectedValue(new Error('down'))
    stop = startUnattendedSupport()
    await flush()
    expect(liveSupport('h1')).toBeUndefined()
    expect(stickySupport('h1')).toBeUndefined()
  })

  it('a disconnect makes the live answer unknown but the sticky yes stays, through the reconnect probe too', async () => {
    setStatus('connected')
    stop = startUnattendedSupport()
    await flush()
    setStatus('disconnected')
    expect(liveSupport('h1')).toBeUndefined()
    expect(stickySupport('h1')).toBe(true)
    let release!: (v: HostInfo) => void
    fetchHostInfo.mockReturnValueOnce(new Promise((r) => { release = r }))
    setStatus('connected')
    expect(liveSupport('h1')).toBeUndefined() // reconnected, not answered yet
    expect(stickySupport('h1')).toBe(true)
    release(info(['journal.v1']))
    await flush()
    expect(liveSupport('h1')).toBe(true)
  })

  it('a re-point forgets the sticky answer; a removal does too', async () => {
    setStatus('connected')
    stop = startUnattendedSupport()
    await flush()
    fetchHostInfo.mockReturnValue(new Promise(() => {})) // the new daemon has not answered
    useHostStore.setState({ hosts: { h1: host('h1', '100.64.0.9') } })
    expect(stickySupport('h1')).toBeUndefined()
    useJournalStore.getState().setSupport('h1', true)
    useHostStore.setState({ hosts: {} })
    expect(stickySupport('h1')).toBeUndefined()
  })

  it('a disconnect while a probe is out: the late answer does not set it', async () => {
    let release!: (v: HostInfo) => void
    fetchHostInfo.mockReturnValueOnce(new Promise((r) => { release = r }))
    setStatus('connected')
    stop = startUnattendedSupport()
    setStatus('disconnected')
    release(info(['journal.v1']))
    await flush()
    expect(liveSupport('h1')).toBeUndefined()
    expect(stickySupport('h1')).toBeUndefined()
  })

  it('journal.sessions.v1 is its own flag, read from the same probe', async () => {
    fetchHostInfo.mockResolvedValue(info(['journal.v1', 'journal.sessions.v1']))
    setStatus('connected')
    stop = startUnattendedSupport()
    await flush()
    expect(useJournalStore.getState().sessionsSupport.h1).toBe(true)
    stop()
    useJournalStore.getState().reset()
    fetchHostInfo.mockResolvedValue(info(['journal.v1']))
    stop = startUnattendedSupport()
    await flush()
    expect(useJournalStore.getState().sessionsSupport.h1).toBe(false)
  })
})
