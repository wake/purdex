// spa/src/lib/profile/start.ironrule.test.ts — the iron rule of P2b plan Task 11,
// proven with NOTHING mocked: the real collector, executor, lease and api sit
// behind `startProfileSync()`. A user who never set a master must get the app
// they have today — no subscription but the one to `useProfileStore`, no hash,
// no lease, no request, no timer.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useProfileStore } from '../../stores/useProfileStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { STORAGE_KEYS } from '../storage/keys'
import { startProfileSync } from './start'

let stop: () => void = () => {}

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, autoSync: true })
})

afterEach(() => {
  stop()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('no master → the app is exactly what it was', () => {
  it('subscribes to useProfileStore and to nothing else; no request, no digest, no lease, no timer', async () => {
    vi.advanceTimersByTime(1) // jsdom's own 0 ms timers from the setup above
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    const digest = vi.spyOn(crypto.subtle, 'digest')
    const profileSub = vi.spyOn(useProfileStore, 'subscribe')
    const hostSub = vi.spyOn(useHostStore, 'subscribe')
    const tabSub = vi.spyOn(useTabStore, 'subscribe')
    const wsSub = vi.spyOn(useWorkspaceStore, 'subscribe')
    const listen = vi.spyOn(window, 'addEventListener')
    const setItem = vi.spyOn(Storage.prototype, 'setItem')

    stop = startProfileSync()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    await vi.advanceTimersByTimeAsync(60_000)

    expect(profileSub).toHaveBeenCalledTimes(1)
    expect(hostSub).not.toHaveBeenCalled()
    expect(tabSub).not.toHaveBeenCalled()
    expect(wsSub).not.toHaveBeenCalled()
    expect(listen).not.toHaveBeenCalled()
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(digest).not.toHaveBeenCalled()
    expect(setItem.mock.calls.filter(([k]) => String(k).startsWith('purdex-profile'))).toEqual([])
    expect(localStorage.getItem(STORAGE_KEYS.PROFILE_LEADER)).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
  })
})
