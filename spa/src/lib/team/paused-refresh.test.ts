import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act } from '@testing-library/react'
import { PAUSED_POLL_MS, __resetPausedRefreshForTests, refreshPaused, startPausedRefresh } from './paused-refresh'
import { fetchPaused, type PausedSession } from './paused-api'
import { usePausedStore } from '../../stores/usePausedStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useHostStore } from '../../stores/useHostStore'

vi.mock('./paused-api', async (orig) => ({ ...(await orig<typeof import('./paused-api')>()), fetchPaused: vi.fn() }))
const fetchMock = vi.mocked(fetchPaused)

const H = 'h1'
const sess = (id: string): PausedSession => ({ session_id: id, ref: `_${id}`, title: id, role: 'solo', causes: ['quota'], since: 1, lead_ref: '', bounced: 0 })
const host = (over: Record<string, unknown> = {}) => ({ id: H, name: 'm', ip: '1.1.1.1', port: 1, token: 't', order: 0, ...over })
const state = (on: boolean) => ({ on, since: 1, changed_at: 1 })
const seed = (pause: 'yes' | 'no' | 'unknown', on: boolean) => useUnattendedStore.setState({ byHost: { [H]: { support: 'yes', pauseSupport: pause, state: state(on) } } })
const tick = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms) })

let stop: () => void
beforeEach(() => {
  vi.useFakeTimers()
  fetchMock.mockReset()
  fetchMock.mockResolvedValue([sess('a')])
  __resetPausedRefreshForTests()
  usePausedStore.getState().reset()
  useTeamRosterStore.getState().reset()
  useUnattendedStore.getState().reset()
  useHostStore.setState({ hosts: { [H]: host() } as never, hostOrder: [H], runtime: { [H]: { status: 'connected' } } as never })
  stop = startPausedRefresh()
})
afterEach(() => { stop(); vi.useRealTimers() })

describe('startPausedRefresh', () => {
  it('reads once when the host announces team.pause.v1', async () => {
    seed('yes', false)
    await tick(0)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(usePausedStore.getState().byHost[H].sessions.map((s) => s.session_id)).toEqual(['a'])
  })

  it('never asks a host without the capability', async () => {
    seed('no', true)
    useTeamRosterStore.getState().apply(H, [])
    await tick(PAUSED_POLL_MS * 3)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(usePausedStore.getState().byHost[H]).toBeUndefined()
  })

  it('re-reads on each team.roster event of the host', async () => {
    seed('yes', false)
    await tick(0)
    fetchMock.mockClear()
    act(() => useTeamRosterStore.getState().apply(H, []))
    await tick(0)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    act(() => useTeamRosterStore.getState().apply(H, []))
    await tick(0)
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('polls every 30 s only while unattended is on, and stops when it goes off', async () => {
    seed('yes', false)
    await tick(0)
    fetchMock.mockClear()
    await tick(PAUSED_POLL_MS * 2)
    expect(fetchMock).not.toHaveBeenCalled() // switch off: no poll
    act(() => useUnattendedStore.getState().applyState(H, state(true)))
    await tick(0)
    expect(fetchMock).not.toHaveBeenCalled() // off -> on is not a read by itself
    await tick(PAUSED_POLL_MS)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await tick(PAUSED_POLL_MS)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    act(() => useUnattendedStore.getState().applyState(H, state(false)))
    await tick(0)
    expect(fetchMock).toHaveBeenCalledTimes(3) // on -> off: one read
    await tick(PAUSED_POLL_MS * 3)
    expect(fetchMock).toHaveBeenCalledTimes(3) // and the poll is gone
  })

  it('a refresh asked while one is out joins it and runs once after it', async () => {
    seed('yes', false)
    await tick(0)
    fetchMock.mockClear()
    let release!: (v: PausedSession[]) => void
    fetchMock.mockImplementationOnce(() => new Promise((r) => { release = r }))
    const first = refreshPaused(H)
    const second = refreshPaused(H)
    const third = refreshPaused(H)
    expect(fetchMock).toHaveBeenCalledTimes(1) // no overlap
    release([sess('x')])
    await act(async () => { await Promise.all([first, second, third]) })
    expect(fetchMock).toHaveBeenCalledTimes(2) // one more, not two
  })

  it('drops an answer that arrives after the host was removed', async () => {
    seed('yes', false)
    await tick(0)
    usePausedStore.getState().reset()
    fetchMock.mockClear()
    let release!: (v: PausedSession[]) => void
    fetchMock.mockImplementationOnce(() => new Promise((r) => { release = r }))
    const p = refreshPaused(H)
    act(() => useHostStore.setState({ hosts: {} as never, hostOrder: [] }))
    release([sess('late')])
    await act(async () => { await p })
    expect(usePausedStore.getState().byHost[H]).toBeUndefined()
  })

  it('drops an answer for a host re-pointed meanwhile', async () => {
    seed('yes', false)
    await tick(0)
    usePausedStore.getState().reset()
    let release!: (v: PausedSession[]) => void
    fetchMock.mockImplementationOnce(() => new Promise((r) => { release = r }))
    const p = refreshPaused(H)
    act(() => useHostStore.setState({ hosts: { [H]: host({ ip: '2.2.2.2' }) } as never }))
    release([sess('old')])
    await act(async () => { await p })
    expect(usePausedStore.getState().byHost[H]).toBeUndefined()
  })

  it('forgets a removed host and a host that lost the capability', async () => {
    seed('yes', false)
    await tick(0)
    expect(usePausedStore.getState().byHost[H]).toBeDefined()
    act(() => useUnattendedStore.getState().invalidateSupport(H))
    expect(usePausedStore.getState().byHost[H]).toBeUndefined()
  })
})
