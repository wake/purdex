import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../lib/storage/keys'
import { selectMaster, useProfileStore } from './useProfileStore'

const PROFILE = 'p_0123456789ab'
const OTHER_PROFILE = 'p_ba9876543210'
const EP = '100.64.0.2:7860'

/** Merge-mode reset with every mutable field listed (the harness convention). */
const resetStore = (): void => {
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, autoSync: true, pendingDirection: null, attachGeneration: 0, masterEndpoint: null, suspendedUntil: null })
}

const persistedEnvelope = (): { state: Record<string, unknown>; version: number } =>
  JSON.parse(localStorage.getItem(STORAGE_KEYS.PROFILE) ?? 'null')

/** Put `state` where a previous session would have left it, then rehydrate. */
async function rehydrateFrom(state: unknown): Promise<void> {
  localStorage.setItem(STORAGE_KEYS.PROFILE, JSON.stringify({ state, version: 1 }))
  await useProfileStore.persist.rehydrate()
}

beforeEach(() => {
  localStorage.clear()
  resetStore()
})

describe('useProfileStore', () => {
  it('uses its own storage key', () => {
    expect(STORAGE_KEYS.PROFILE).toBe('purdex-profile')
    expect(useProfileStore.persist.getOptions().name).toBe('purdex-profile')
    expect(useProfileStore.persist.getOptions().version).toBe(1)
  })

  it('starts detached, with autoSync on', () => {
    const s = useProfileStore.getState()
    expect(s.masterHostId).toBeNull()
    expect(s.masterProfileId).toBeNull()
    expect(s.autoSync).toBe(true)
    expect(selectMaster(s)).toBeNull()
  })

  describe('setMaster', () => {
    it('sets both halves and reports true', () => {
      expect(useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)).toBe(true)
      const s = useProfileStore.getState()
      expect(s.masterHostId).toBe('host-1')
      expect(s.masterProfileId).toBe(PROFILE)
      expect(selectMaster(s)).toEqual({ hostId: 'host-1', profileId: PROFILE })
    })

    it.each([
      ['an empty host id', '', PROFILE],
      ['an empty profile id', 'host-1', ''],
      ['a profile id without the prefix', 'host-1', '0123456789ab'],
      ['a profile id that is too short', 'host-1', 'p_0123456789a'],
      ['a profile id that is too long', 'host-1', 'p_0123456789abc'],
      ['a profile id in upper case', 'host-1', 'p_0123456789AB'],
      ['a non-string host id', 7 as unknown as string, PROFILE],
      ['a non-string profile id', 'host-1', null as unknown as string],
    ])('refuses %s and leaves the current master alone', (_label, hostId, profileId) => {
      useProfileStore.getState().setMaster('host-0', OTHER_PROFILE, 'pull', EP)

      expect(useProfileStore.getState().setMaster(hostId, profileId, 'pull', EP)).toBe(false)

      const s = useProfileStore.getState()
      expect(s.masterHostId).toBe('host-0')
      expect(s.masterProfileId).toBe(OTHER_PROFILE)
    })

    it('a refusal while detached stays detached (never half a master)', () => {
      expect(useProfileStore.getState().setMaster('host-1', 'nope', 'pull', EP)).toBe(false)
      const s = useProfileStore.getState()
      expect(s.masterHostId).toBeNull()
      expect(s.masterProfileId).toBeNull()
    })
  })

  it('clearMaster clears both halves and leaves autoSync alone', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().setAutoSync(false)

    useProfileStore.getState().clearMaster()

    const s = useProfileStore.getState()
    expect(s.masterHostId).toBeNull()
    expect(s.masterProfileId).toBeNull()
    expect(s.autoSync).toBe(false)
    expect(selectMaster(s)).toBeNull()
  })

  it('setAutoSync flips the flag and leaves the master alone', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().setAutoSync(false)
    expect(useProfileStore.getState().autoSync).toBe(false)
    expect(selectMaster(useProfileStore.getState())).toEqual({ hostId: 'host-1', profileId: PROFILE })
    useProfileStore.getState().setAutoSync(true)
    expect(useProfileStore.getState().autoSync).toBe(true)
  })

  it('selectMaster is null for half a master, whichever half', () => {
    const base = useProfileStore.getState()
    expect(selectMaster({ ...base, masterHostId: 'host-1', masterProfileId: null })).toBeNull()
    expect(selectMaster({ ...base, masterHostId: null, masterProfileId: PROFILE })).toBeNull()
  })

  it('persists exactly the seven fields', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().setAutoSync(false)

    const envelope = persistedEnvelope()
    expect(envelope.version).toBe(1)
    expect(envelope.state).toEqual({ masterHostId: 'host-1', masterProfileId: PROFILE, autoSync: false, pendingDirection: 'pull', attachGeneration: 1, masterEndpoint: EP, suspendedUntil: null })
  })

  describe('rehydrate sanitises what storage holds', () => {
    it('keeps a well-formed record as is', async () => {
      await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, autoSync: false })
      const s = useProfileStore.getState()
      expect(s.masterHostId).toBe('host-1')
      expect(s.masterProfileId).toBe(PROFILE)
      expect(s.autoSync).toBe(false)
    })

    it.each([
      ['only the host id', { masterHostId: 'host-1', masterProfileId: null, autoSync: true }],
      ['only the profile id', { masterHostId: null, masterProfileId: PROFILE, autoSync: true }],
      ['a missing profile id', { masterHostId: 'host-1', autoSync: true }],
      ['a numeric host id', { masterHostId: 42, masterProfileId: PROFILE, autoSync: true }],
      ['an object profile id', { masterHostId: 'host-1', masterProfileId: { id: PROFILE }, autoSync: true }],
      ['an empty host id', { masterHostId: '', masterProfileId: PROFILE, autoSync: true }],
      ['a malformed profile id', { masterHostId: 'host-1', masterProfileId: 'p_nothex000000', autoSync: true }],
    ])('%s → detached, both halves null', async (_label, state) => {
      // Start attached, so "null afterwards" cannot be the untouched default.
      useProfileStore.getState().setMaster('host-0', OTHER_PROFILE, 'pull', EP)

      await rehydrateFrom(state)

      const s = useProfileStore.getState()
      expect(s.masterHostId).toBeNull()
      expect(s.masterProfileId).toBeNull()
      expect(s.autoSync).toBe(true)
    })

    it.each([
      ['a string', 'false'],
      ['a number', 0],
      ['null', null],
      ['missing', undefined],
    ])('autoSync that is %s → true, master kept', async (_label, autoSync) => {
      useProfileStore.getState().setAutoSync(false)

      await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, autoSync })

      const s = useProfileStore.getState()
      expect(s.autoSync).toBe(true)
      expect(s.masterHostId).toBe('host-1')
      expect(s.masterProfileId).toBe(PROFILE)
    })

    it.each([
      ['null', null],
      ['a string', 'garbage'],
      ['an array', [1, 2]],
    ])('a persisted state that is %s → defaults, actions intact', async (_label, state) => {
      useProfileStore.getState().setMaster('host-0', OTHER_PROFILE, 'pull', EP)

      await rehydrateFrom(state)

      const s = useProfileStore.getState()
      expect(s.masterHostId).toBeNull()
      expect(s.masterProfileId).toBeNull()
      expect(s.autoSync).toBe(true)
      expect(typeof s.setMaster).toBe('function')
    })

    it('never lets persisted junk replace an action', async () => {
      await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, autoSync: true, setMaster: 'junk', extra: 1 })
      const s = useProfileStore.getState()
      expect(typeof s.setMaster).toBe('function')
      expect('extra' in s).toBe(false)
    })
  })
})

// ---------------------------------------------------------------------------
// Two windows. Each `openWindow()` is a fresh module graph — its own store, its
// own syncManager, its own channel — over the SAME localStorage and one shared
// BroadcastChannel bus (the pattern of usePlaceholderFilesStore.cross-window).
// ---------------------------------------------------------------------------

class FakeBroadcastChannel {
  static bus = new Set<FakeBroadcastChannel>()
  name: string
  onmessage: ((event: MessageEvent) => void) | null = null
  constructor(name: string) {
    this.name = name
    FakeBroadcastChannel.bus.add(this)
  }
  postMessage(data: unknown): void {
    for (const peer of FakeBroadcastChannel.bus) {
      if (peer === this || peer.name !== this.name) continue
      peer.onmessage?.({ data } as MessageEvent)
    }
  }
  close(): void {
    FakeBroadcastChannel.bus.delete(this)
  }
}

type Win = typeof import('./useProfileStore')

async function openWindow(): Promise<Win> {
  vi.resetModules()
  const mod = await import('./useProfileStore')
  await mod.useProfileStore.persist.rehydrate()
  return mod
}

async function flush(): Promise<void> {
  for (let i = 0; i < 5; i++) await Promise.resolve()
}

describe('pendingDirection — which side wins the first reconciliation', () => {
  it('starts null', () => {
    expect(useProfileStore.getState().pendingDirection).toBeNull()
  })

  it.each(['push', 'pull'] as const)('setMaster records %s', (direction) => {
    expect(useProfileStore.getState().setMaster('host-1', PROFILE, direction, EP)).toBe(true)
    expect(useProfileStore.getState().pendingDirection).toBe(direction)
  })

  it.each([undefined, null, '', 'both', 'PULL', 1])('setMaster refuses the direction %j and changes nothing', (direction) => {
    useProfileStore.getState().setMaster('host-0', OTHER_PROFILE, 'push', EP)
    expect(useProfileStore.getState().setMaster('host-1', PROFILE, direction as never, EP)).toBe(false)
    const s = useProfileStore.getState()
    expect(s.masterHostId).toBe('host-0')
    expect(s.pendingDirection).toBe('push')
  })

  it('a refused master does not touch the direction either', () => {
    useProfileStore.getState().setMaster('host-0', OTHER_PROFILE, 'push', EP)
    expect(useProfileStore.getState().setMaster('host-1', 'nope', 'pull', EP)).toBe(false)
    expect(useProfileStore.getState().pendingDirection).toBe('push')
  })

  it('clearPendingDirection clears it and leaves the master alone', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().clearPendingDirection()
    const s = useProfileStore.getState()
    expect(s.pendingDirection).toBeNull()
    expect(s.masterHostId).toBe('host-1')
    expect(s.masterProfileId).toBe(PROFILE)
  })

  it('clearMaster clears it too', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'push', EP)
    useProfileStore.getState().clearMaster()
    expect(useProfileStore.getState().pendingDirection).toBeNull()
  })

  it('survives a reload', async () => {
    await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, autoSync: true, pendingDirection: 'push' })
    expect(useProfileStore.getState().pendingDirection).toBe('push')
  })

  it.each([
    ['a direction without a master', { masterHostId: null, masterProfileId: null, pendingDirection: 'pull' }],
    ['a direction with half a master', { masterHostId: 'host-1', masterProfileId: null, pendingDirection: 'pull' }],
    ['an unknown direction', { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, pendingDirection: 'sideways' }],
    ['a non-string direction', { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, pendingDirection: 1 }],
    ['a record from before the field existed', { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP }],
  ])('rehydrate: %s → null', async (_label, state) => {
    await rehydrateFrom(state)
    expect(useProfileStore.getState().pendingDirection).toBeNull()
  })
})

describe('attachGeneration — every attach is a new one, the same master included', () => {
  it('starts at 0 and goes up by one with every accepted setMaster', () => {
    expect(useProfileStore.getState().attachGeneration).toBe(0)
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    expect(useProfileStore.getState().attachGeneration).toBe(1)
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP) // the SAME master, the same direction
    expect(useProfileStore.getState().attachGeneration).toBe(2)
    useProfileStore.getState().setMaster('host-0', OTHER_PROFILE, 'push', EP)
    expect(useProfileStore.getState().attachGeneration).toBe(3)
  })

  it('a refused setMaster, clearMaster, clearPendingDirection and setAutoSync leave it alone (it never goes down)', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().setMaster('host-1', 'nope', 'pull', EP)
    useProfileStore.getState().setMaster('host-1', PROFILE, 'sideways' as never, EP)
    useProfileStore.getState().clearPendingDirection()
    useProfileStore.getState().setAutoSync(false)
    useProfileStore.getState().clearMaster()
    expect(useProfileStore.getState().attachGeneration).toBe(1)
  })

  it('survives a reload', async () => {
    await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, autoSync: true, pendingDirection: null, attachGeneration: 7 })
    expect(useProfileStore.getState().attachGeneration).toBe(7)
  })

  it.each([
    ['missing', undefined],
    ['negative', -1],
    ['a fraction', 1.5],
    ['NaN', Number.NaN],
    ['a string', '3'],
    ['beyond the safe integers', 2 ** 60],
    ['null', null],
  ])('rehydrate: %s → 0', async (_label, attachGeneration) => {
    await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, attachGeneration })
    expect(useProfileStore.getState().attachGeneration).toBe(0)
  })
})

describe('masterEndpoint — where the daemon was when the bases were agreed', () => {
  it('starts null; setMaster records it; a new attach replaces it', () => {
    expect(useProfileStore.getState().masterEndpoint).toBeNull()
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', '10.0.0.1:7860')
    expect(useProfileStore.getState().masterEndpoint).toBe('10.0.0.1:7860')
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', '10.0.0.2:7999')
    expect(useProfileStore.getState().masterEndpoint).toBe('10.0.0.2:7999')
  })

  it.each([undefined, null, '', 7])('setMaster refuses the endpoint %j and changes nothing', (endpoint) => {
    useProfileStore.getState().setMaster('host-0', OTHER_PROFILE, 'push', EP)
    const before = useProfileStore.getState().attachGeneration
    expect(useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', endpoint as never)).toBe(false)
    expect(useProfileStore.getState()).toMatchObject({ masterHostId: 'host-0', masterEndpoint: EP, attachGeneration: before })
  })

  it('clearMaster clears it; clearPendingDirection and setAutoSync do not', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().clearPendingDirection()
    useProfileStore.getState().setAutoSync(false)
    expect(useProfileStore.getState().masterEndpoint).toBe(EP)
    useProfileStore.getState().clearMaster()
    expect(useProfileStore.getState().masterEndpoint).toBeNull()
  })

  it('survives a reload', async () => {
    await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP })
    expect(useProfileStore.getState().masterEndpoint).toBe(EP)
  })

  it.each([
    ['an endpoint without a master', { masterHostId: null, masterProfileId: null, masterEndpoint: EP }],
    ['an endpoint with half a master', { masterHostId: 'host-1', masterProfileId: null, masterEndpoint: EP }],
  ])('rehydrate: %s → null', async (_label, state) => {
    await rehydrateFrom(state)
    expect(useProfileStore.getState().masterEndpoint).toBeNull()
  })

  // FAIL CLOSED. Bases whose daemon is unknown must not be used against whatever the host points at today,
  // and there is nothing to migrate: the field is older than any shipped build.
  it.each([
    ['no endpoint (attached before the field existed)', {}],
    ['a non-string', { masterEndpoint: 7860 }],
    ['an empty string', { masterEndpoint: '' }],
    ['null', { masterEndpoint: null }],
  ])('rehydrate: a master with %s is NO master — and no direction; preferences survive', async (_label, over) => {
    await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, pendingDirection: 'pull', autoSync: false, attachGeneration: 4, ...over })
    const s = useProfileStore.getState()
    expect(selectMaster(s)).toBeNull()
    expect(s).toMatchObject({ masterHostId: null, masterProfileId: null, pendingDirection: null, masterEndpoint: null, autoSync: false, attachGeneration: 4 })
  })

  it('selectMaster fails closed too: a master without an endpoint (only a hand-built state can hold one) is no master', () => {
    expect(selectMaster({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: null })).toBeNull()
    expect(selectMaster({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP })).toEqual({ hostId: 'host-1', profileId: PROFILE })
  })
})

describe('suspendedUntil — every driver, in every window, stands still while an attach is being made', () => {
  it('starts null; suspend sets it; resume, setMaster and clearMaster clear it', () => {
    expect(useProfileStore.getState().suspendedUntil).toBeNull()
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().suspend(5_000)
    expect(useProfileStore.getState().suspendedUntil).toBe(5_000)
    useProfileStore.getState().resume()
    expect(useProfileStore.getState().suspendedUntil).toBeNull()

    useProfileStore.getState().suspend(6_000)
    useProfileStore.getState().setMaster('host-1', PROFILE, 'push', EP)
    expect(useProfileStore.getState().suspendedUntil).toBeNull()

    useProfileStore.getState().suspend(7_000)
    useProfileStore.getState().clearMaster()
    expect(useProfileStore.getState().suspendedUntil).toBeNull()
  })

  it('without a master there is nothing to suspend', () => {
    useProfileStore.getState().suspend(5_000)
    expect(useProfileStore.getState().suspendedUntil).toBeNull()
  })

  it.each([Number.NaN, Number.POSITIVE_INFINITY, '5000', null, undefined])('suspend(%j) is ignored', (until) => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().suspend(until as never)
    expect(useProfileStore.getState().suspendedUntil).toBeNull()
  })

  it('a refused setMaster does not lift it', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().suspend(5_000)
    useProfileStore.getState().setMaster('host-1', 'nope', 'pull', EP)
    expect(useProfileStore.getState().suspendedUntil).toBe(5_000)
  })

  it('survives a reload (the attach may have died with the window: it carries its own expiry)', async () => {
    await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, suspendedUntil: 9_000 })
    expect(useProfileStore.getState().suspendedUntil).toBe(9_000)
  })

  it.each([
    ['without a master', { masterHostId: null, masterProfileId: null, suspendedUntil: 9_000 }],
    ['with a master that fails closed', { masterHostId: 'host-1', masterProfileId: PROFILE, suspendedUntil: 9_000 }],
    ['a string', { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, suspendedUntil: '9000' }],
    ['a boolean', { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, suspendedUntil: true }],
    ['NaN', { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, suspendedUntil: Number.NaN }],
    ['missing', { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP }],
  ])('rehydrate: %s → null', async (_label, state) => {
    await rehydrateFrom(state)
    expect(useProfileStore.getState().suspendedUntil).toBeNull()
  })
})

describe('every window agrees on the master', () => {
  beforeEach(() => {
    FakeBroadcastChannel.bus.clear()
    localStorage.clear()
    vi.stubGlobal('BroadcastChannel', FakeBroadcastChannel)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.resetModules()
  })

  it('an attach in window A reaches window B, which was already open', async () => {
    const a = await openWindow()
    const b = await openWindow()
    expect(a.useProfileStore).not.toBe(b.useProfileStore)

    a.useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    await flush()

    expect(b.selectMaster(b.useProfileStore.getState())).toEqual({ hostId: 'host-1', profileId: PROFILE })
  })

  it('a RE-attach to the same master in window A reaches window B as a new generation', async () => {
    const a = await openWindow()
    const b = await openWindow()
    a.useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    await flush()
    const before = b.useProfileStore.getState().attachGeneration
    a.useProfileStore.getState().setMaster('host-1', PROFILE, 'push', EP)
    await flush()
    expect(b.useProfileStore.getState().attachGeneration).toBe(before + 1)
    expect(b.useProfileStore.getState().pendingDirection).toBe('push')
  })

  it('a suspension in window A reaches window B', async () => {
    const a = await openWindow()
    const b = await openWindow()
    a.useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    a.useProfileStore.getState().suspend(5_000)
    await flush()
    expect(b.useProfileStore.getState().suspendedUntil).toBe(5_000)
  })

  it('a detach in window B (a follower) reaches window A (the leader)', async () => {
    const a = await openWindow()
    const b = await openWindow()
    a.useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    await flush()
    expect(b.useProfileStore.getState().masterProfileId).toBe(PROFILE)

    b.useProfileStore.getState().clearMaster()
    await flush()

    expect(a.selectMaster(a.useProfileStore.getState())).toBeNull()
    expect(a.useProfileStore.getState().masterHostId).toBeNull()
  })

  it('autoSync off in window A reaches window B', async () => {
    const a = await openWindow()
    const b = await openWindow()

    a.useProfileStore.getState().setAutoSync(false)
    await flush()

    expect(b.useProfileStore.getState().autoSync).toBe(false)
  })
})
