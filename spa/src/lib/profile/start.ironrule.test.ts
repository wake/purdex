// spa/src/lib/profile/start.ironrule.test.ts — the iron rule of P2b plan Task 11,
// proven with NOTHING mocked: the real collector, executor, lease and api sit
// behind `startProfileSync()`. A user who never set a master must get the app
// they have today. PRECISELY: `startProfileSync()` subscribes to
// `useProfileStore` and, with no master, to no other store; it computes no hash,
// neither reads nor writes the lease, sends no request, schedules no timer and
// opens no BroadcastChannel. `useProfileStore` ITSELF is an ordinary persisted,
// `syncManager`-registered store like the eighteen others on main — its one
// storage key and its entry in the (singleton) sync channel's registry exist for
// everybody and are not part of "nothing".
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
    const getItem = vi.spyOn(Storage.prototype, 'getItem')
    // Channels opened from here on. (Counted around the CALL, not around the
    // import: by now the stores imported above have long created syncManager's
    // singleton channel, and module order would make an import-time count fragile.)
    let channels = 0
    const Original = globalThis.BroadcastChannel as typeof BroadcastChannel | undefined
    vi.stubGlobal(
      'BroadcastChannel',
      Original === undefined
        ? class {
            constructor() {
              channels += 1
            }
          }
        : class extends Original {
            constructor(name: string) {
              super(name)
              channels += 1
            }
          },
    )

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
    expect(getItem.mock.calls.filter(([k]) => k === STORAGE_KEYS.PROFILE_LEADER)).toEqual([])
    expect(channels).toBe(0)
    expect(localStorage.getItem(STORAGE_KEYS.PROFILE_LEADER)).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
  })
})
