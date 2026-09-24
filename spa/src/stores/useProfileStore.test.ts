import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../lib/storage/keys'
import { PENDING_DETACH_MAX, endpointOfHost, pendingDetachKey, selectMaster, useProfileStore } from './useProfileStore'

const PROFILE = 'p_0123456789ab'
const OTHER_PROFILE = 'p_ba9876543210'
const EP = '100.64.0.2:7860'

/** Merge-mode reset with every mutable field listed (the harness convention). */
const resetStore = (): void => {
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, autoSync: true, pendingDirection: null, attachGeneration: 0, attachId: null, masterEndpoint: null, suspension: null, pendingDetaches: [] })
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

  it('persists exactly its nine fields', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().setAutoSync(false)

    const envelope = persistedEnvelope()
    expect(envelope.version).toBe(1)
    const attachId = useProfileStore.getState().attachId
    expect(envelope.state).toEqual({ masterHostId: 'host-1', masterProfileId: PROFILE, autoSync: false, pendingDirection: 'pull', attachGeneration: 1, attachId, masterEndpoint: EP, suspension: null, pendingDetaches: [] })
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

  it('a refused setMaster, clearPendingDirection, setAutoSync, suspend and resume leave it alone (it never goes down)', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().setMaster('host-1', 'nope', 'pull', EP)
    useProfileStore.getState().setMaster('host-1', PROFILE, 'sideways' as never, EP)
    useProfileStore.getState().clearPendingDirection()
    useProfileStore.getState().setAutoSync(false)
    useProfileStore.getState().suspend('t', 5_000)
    useProfileStore.getState().resume('t')
    expect(useProfileStore.getState().attachGeneration).toBe(1)
  })

  it('clearMaster moves it too: it is the generation of the CONTROL PLANE — an attach still in flight elsewhere must be able to see that a detach overtook it', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().clearMaster()
    expect(useProfileStore.getState().attachGeneration).toBe(2)
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    expect(useProfileStore.getState().attachGeneration).toBe(3)
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

describe('attachId — WHICH attach, not how many (codex critic: two stale windows can reach one generation)', () => {
  it('starts null; every accepted setMaster writes a new one, the same master included; clearMaster clears it', () => {
    expect(useProfileStore.getState().attachId).toBeNull()
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    const first = useProfileStore.getState().attachId
    expect(typeof first === 'string' && first.length >= 32).toBe(true)
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    const second = useProfileStore.getState().attachId
    expect(second).not.toBe(first)
    useProfileStore.getState().clearMaster()
    expect(useProfileStore.getState().attachId).toBeNull()
  })

  it('a refused setMaster, clearPendingDirection, suspend and resume leave it alone', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    const id = useProfileStore.getState().attachId
    useProfileStore.getState().setMaster('host-1', 'nope', 'pull', EP)
    useProfileStore.getState().clearPendingDirection()
    useProfileStore.getState().suspend('t', 5_000)
    useProfileStore.getState().resume('t')
    expect(useProfileStore.getState().attachId).toBe(id)
  })

  it('two windows from the same stale generation: the same attachGeneration, different attach ids', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    const a = useProfileStore.getState()
    useProfileStore.setState({ attachGeneration: 0 }) // the other window, still on the old value
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    const b = useProfileStore.getState()
    expect(b.attachGeneration).toBe(a.attachGeneration)
    expect(b.attachId).not.toBe(a.attachId)
  })

  it('survives a reload with its master', async () => {
    await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, attachGeneration: 3, attachId: 'abc123' })
    expect(useProfileStore.getState().attachId).toBe('abc123')
  })

  it.each([
    ['missing (a record from before the field)', { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP }],
    ['empty', { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, attachId: '' }],
    ['not a string', { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, attachId: 7 }],
    ['without a master', { masterHostId: null, masterProfileId: null, masterEndpoint: null, attachId: 'abc123' }],
  ])('rehydrate: %s → null', async (_label, state) => {
    await rehydrateFrom(state)
    expect(useProfileStore.getState().attachId).toBeNull()
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

describe('suspension — every driver, in every window, stands still while an attach is being made; it has an OWNER', () => {
  const attached = (): void => void useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)

  it('starts null; suspend sets it; resume with the same token lifts it', () => {
    expect(useProfileStore.getState().suspension).toBeNull()
    attached()
    useProfileStore.getState().suspend('tok-a', 5_000)
    expect(useProfileStore.getState().suspension).toEqual({ token: 'tok-a', until: 5_000 })
    useProfileStore.getState().resume('tok-a')
    expect(useProfileStore.getState().suspension).toBeNull()
  })

  it('a newer attach REPLACES the suspension; the older one can no longer lift it — a failed A must not wake the drivers under B', () => {
    attached()
    useProfileStore.getState().suspend('tok-a', 5_000)
    useProfileStore.getState().suspend('tok-b', 6_000)
    expect(useProfileStore.getState().suspension).toEqual({ token: 'tok-b', until: 6_000 })

    const before = useProfileStore.getState()
    const writes = vi.spyOn(Storage.prototype, 'setItem')
    useProfileStore.getState().resume('tok-a')
    expect(useProfileStore.getState()).toBe(before) // the same state reference: no notification, no persist
    expect(writes).not.toHaveBeenCalled()
    writes.mockRestore()

    useProfileStore.getState().resume('tok-b')
    expect(useProfileStore.getState().suspension).toBeNull()
  })

  it('setMaster lifts ITS OWN suspension only: A succeeds while B is still attaching → the master is A\'s, the suspension is still B\'s', () => {
    attached()
    useProfileStore.getState().suspend('tok-a', 5_000)
    useProfileStore.getState().suspend('tok-b', 6_000)
    expect(useProfileStore.getState().setMaster('host-0', OTHER_PROFILE, 'push', EP, 'tok-a')).toBe(true)
    expect(useProfileStore.getState()).toMatchObject({ masterHostId: 'host-0', masterProfileId: OTHER_PROFILE, suspension: { token: 'tok-b', until: 6_000 } })
    expect(useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP, 'tok-b')).toBe(true)
    expect(useProfileStore.getState().suspension).toBeNull()
  })

  it('setMaster without a token lifts nobody\'s', () => {
    attached()
    useProfileStore.getState().suspend('tok-a', 5_000)
    useProfileStore.getState().setMaster('host-1', PROFILE, 'push', EP)
    expect(useProfileStore.getState().suspension).toEqual({ token: 'tok-a', until: 5_000 })
  })

  it('clearMaster lifts everything: detach means stop', () => {
    attached()
    useProfileStore.getState().suspend('tok-a', 7_000)
    useProfileStore.getState().clearMaster()
    expect(useProfileStore.getState().suspension).toBeNull()
  })

  it('without a master there is nothing to suspend', () => {
    useProfileStore.getState().suspend('tok-a', 5_000)
    expect(useProfileStore.getState().suspension).toBeNull()
  })

  it.each([
    ['tok-a', Number.NaN],
    ['tok-a', Number.POSITIVE_INFINITY],
    ['tok-a', '5000'],
    ['', 5_000],
    [7, 5_000],
    [undefined, 5_000],
  ])('suspend(%j, %j) is ignored', (token, until) => {
    attached()
    useProfileStore.getState().suspend(token as never, until as never)
    expect(useProfileStore.getState().suspension).toBeNull()
  })

  it('a refused setMaster does not lift it', () => {
    attached()
    useProfileStore.getState().suspend('tok-a', 5_000)
    useProfileStore.getState().setMaster('host-1', 'nope', 'pull', EP, 'tok-a')
    expect(useProfileStore.getState().suspension).toEqual({ token: 'tok-a', until: 5_000 })
  })

  it('survives a reload (the attach may have died with the window: it carries its own expiry)', async () => {
    await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, suspension: { token: 'tok-a', until: 9_000 } })
    expect(useProfileStore.getState().suspension).toEqual({ token: 'tok-a', until: 9_000 })
  })

  const M = { masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP }
  it.each([
    ['without a master', { masterHostId: null, masterProfileId: null, suspension: { token: 't', until: 9_000 } }],
    ['with a master that fails closed', { masterHostId: 'host-1', masterProfileId: PROFILE, suspension: { token: 't', until: 9_000 } }],
    ['a bare number (the previous shape)', { ...M, suspension: 9_000 }],
    ['the previous field', { ...M, suspendedUntil: 9_000 }],
    ['no token', { ...M, suspension: { until: 9_000 } }],
    ['an empty token', { ...M, suspension: { token: '', until: 9_000 } }],
    ['a non-string token', { ...M, suspension: { token: 1, until: 9_000 } }],
    ['no time', { ...M, suspension: { token: 't' } }],
    ['NaN', { ...M, suspension: { token: 't', until: Number.NaN } }],
    ['a string time', { ...M, suspension: { token: 't', until: '9000' } }],
    ['an array', { ...M, suspension: ['t', 9_000] }],
    ['missing', M],
  ])('rehydrate: %s → null', async (_label, state) => {
    await rehydrateFrom(state)
    expect(useProfileStore.getState().suspension).toBeNull()
  })

  it('extra keys of a stored suspension are dropped', async () => {
    await rehydrateFrom({ ...M, suspension: { token: 't', until: 9_000, junk: true } })
    expect(useProfileStore.getState().suspension).toEqual({ token: 't', until: 9_000 })
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
    a.useProfileStore.getState().suspend('tok-a', 5_000)
    await flush()
    expect(b.useProfileStore.getState().suspension).toEqual({ token: 'tok-a', until: 5_000 })
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

describe('pendingDetaches — every detach the daemon was not told of, kept until it is (or the user gives up)', () => {
  const LEFT = { hostId: 'host-1', profileId: PROFILE, endpoint: EP, detail: 'network', at: 1000 }
  const SECOND = { hostId: 'host-1', profileId: OTHER_PROFILE, endpoint: EP, detail: 'timeout', at: 2000 }
  const list = () => useProfileStore.getState().pendingDetaches
  const add = (left: unknown) => useProfileStore.getState().addPendingDetach(left as never)

  it('is empty by default, added whole, persisted, and survives a reload WITHOUT a master', async () => {
    expect(list()).toEqual([])
    expect(add(LEFT)).toBe(true)
    expect(list()).toEqual([LEFT])
    expect(persistedEnvelope().state.pendingDetaches).toEqual([LEFT])
    const stored = persistedEnvelope().state
    resetStore()
    await rehydrateFrom(stored)
    expect(selectMaster(useProfileStore.getState())).toBeNull()
    expect(list()).toEqual([LEFT])
  })

  it('A SECOND FAILURE IS ADDED, NOT WRITTEN OVER THE FIRST (review F3): A→B→C with both drops failing leaves BOTH ghosts on record', () => {
    add(LEFT)
    add(SECOND)
    expect(list()).toEqual([LEFT, SECOND])
    expect(persistedEnvelope().state.pendingDetaches).toEqual([LEFT, SECOND])
  })

  it('the key is endpoint + host + profile: the same profile id on ANOTHER daemon is another record; the same three → the record is brought up to date, in place', () => {
    const elsewhere = { ...LEFT, endpoint: '100.64.0.9:7860' }
    add(LEFT)
    add(elsewhere)
    add(SECOND)
    expect(list()).toEqual([LEFT, elsewhere, SECOND])
    expect(new Set(list().map(pendingDetachKey)).size).toBe(3)
    add({ ...LEFT, detail: 'server (HTTP 502)', at: 5000 })
    expect(list()).toEqual([{ ...LEFT, detail: 'server (HTTP 502)', at: 5000 }, elsewhere, SECOND])
  })

  it('no key can pass for part of another', () => {
    expect(pendingDetachKey({ ...LEFT, hostId: 'a|b', endpoint: 'c' })).not.toBe(pendingDetachKey({ ...LEFT, hostId: 'b', endpoint: 'c|a' }))
  })

  it.each([
    ['no host', { ...LEFT, hostId: '' }],
    ['a malformed profile id', { ...LEFT, profileId: 'nope' }],
    ['a detail that is no text', { ...LEFT, detail: 7 }],
    ['a time that is no number', { ...LEFT, at: 'now' }],
    ['an endpoint that is no text', { ...LEFT, endpoint: 7 }],
    ['an empty endpoint', { ...LEFT, endpoint: '' }],
    ['not an object', 'x'],
  ])('%s → refused by the setter, dropped by a rehydrate — and the good records beside it stay', async (_label, bad) => {
    expect(add(bad)).toBe(false)
    expect(list()).toEqual([])
    await rehydrateFrom({ autoSync: true, pendingDetaches: [bad, LEFT] })
    expect(list()).toEqual([LEFT])
  })

  it('WHERE the daemon was is part of the record: the address the master was attached at, as `masterEndpoint` writes it', () => {
    expect(endpointOfHost({ ip: '100.64.0.2', port: 7860 })).toBe(EP)
    add(LEFT)
    expect(persistedEnvelope().state.pendingDetaches).toMatchObject([{ endpoint: EP }])
  })

  describe('what alpha.420 shipped — ONE record under `pendingDetach` — is not lost on the way', () => {
    it('the single object becomes a list of one', async () => {
      await rehydrateFrom({ autoSync: true, pendingDetach: LEFT })
      expect(list()).toEqual([LEFT])
    })

    it('… and the next persist writes the new field only', async () => {
      await rehydrateFrom({ autoSync: true, pendingDetach: LEFT })
      add(SECOND)
      expect(persistedEnvelope().state.pendingDetaches).toEqual([LEFT, SECOND])
      expect('pendingDetach' in persistedEnvelope().state).toBe(false)
    })

    it('both fields in storage (a newer window wrote beside an older one): the list, and the single one added unless it is in it', async () => {
      await rehydrateFrom({ autoSync: true, pendingDetach: LEFT, pendingDetaches: [SECOND] })
      expect(list()).toEqual([SECOND, LEFT])
      await rehydrateFrom({ autoSync: true, pendingDetach: { ...LEFT, detail: 'older' }, pendingDetaches: [LEFT] })
      expect(list()).toEqual([LEFT])
    })

    it('a record from before the endpoint was written down is KEPT, with the endpoint unknown (null) — never guessed from the host of today', async () => {
      const { endpoint: _dropped, ...legacy } = LEFT
      void _dropped
      await rehydrateFrom({ autoSync: true, pendingDetach: legacy })
      expect(list()).toEqual([{ ...LEFT, endpoint: null }])
    })

    it('junk in either field is nothing', async () => {
      await rehydrateFrom({ autoSync: true, pendingDetach: 'x', pendingDetaches: { not: 'a list' } })
      expect(list()).toEqual([])
    })
  })

  it('… but nobody WRITES one without an endpoint: the setter refuses (whoever writes knows where the daemon was)', () => {
    expect(add({ ...LEFT, endpoint: null })).toBe(false)
    const { endpoint: _dropped, ...legacy } = LEFT
    void _dropped
    expect(add(legacy)).toBe(false)
    expect(list()).toEqual([])
  })

  it('the detail is a short reason, not a transcript: cut at 120 characters, by the setter and by a rehydrate', async () => {
    const long = 'x'.repeat(500)
    add({ ...LEFT, detail: long })
    expect(list()[0].detail).toHaveLength(120)
    await rehydrateFrom({ autoSync: true, pendingDetaches: [{ ...LEFT, detail: long }] })
    expect(list()[0].detail).toHaveLength(120)
  })

  it('it does not grow for ever: the newest PENDING_DETACH_MAX are kept, by the setter and by a rehydrate; duplicates in storage collapse', async () => {
    const many = Array.from({ length: PENDING_DETACH_MAX + 5 }, (_, i) => ({ ...LEFT, endpoint: `10.0.0.${i}:7860`, at: i }))
    for (const left of many) add(left)
    expect(list()).toEqual(many.slice(5))
    resetStore()
    await rehydrateFrom({ autoSync: true, pendingDetaches: [...many, many[10]] })
    expect(list()).toHaveLength(PENDING_DETACH_MAX)
    expect(list()).toEqual(many.slice(5))
  })

  it('is cleared ONE RECORD AT A TIME, by its key: the others stay', () => {
    add(LEFT)
    add(SECOND)
    useProfileStore.getState().clearPendingDetach('nope')
    expect(list()).toEqual([LEFT, SECOND])
    useProfileStore.getState().clearPendingDetach(pendingDetachKey(LEFT))
    expect(list()).toEqual([SECOND])
    useProfileStore.getState().clearPendingDetach(pendingDetachKey(SECOND))
    expect(list()).toEqual([])
  })

  it('attaching to that very profile AT THAT ADDRESS again makes the attachment wanted: THAT record goes, and only that one', () => {
    const elsewhere = { ...LEFT, endpoint: '100.64.0.9:7860' }
    add(LEFT)
    add(elsewhere)
    add(SECOND)
    useProfileStore.getState().setMaster('host-1', 'p_cccccccccccc', 'pull', EP)
    expect(list()).toEqual([LEFT, elsewhere, SECOND])
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    expect(list()).toEqual([elsewhere, SECOND]) // the one on the other daemon is still a ghost there
  })

  it('a detach does not clear it, and it moves no generation', () => {
    useProfileStore.getState().setMaster('host-1', 'p_cccccccccccc', 'pull', EP)
    const generation = useProfileStore.getState().attachGeneration
    add(LEFT)
    expect(useProfileStore.getState().attachGeneration).toBe(generation)
    useProfileStore.getState().clearMaster()
    expect(list()).toEqual([LEFT])
  })
})

describe('the pull guard\'s store half is gone (host ownership H3b): no `pendingPullHosts`, `setMaster` takes five arguments', () => {
  it('setMaster(host, profile, direction, endpoint, token): five parameters; a pull stores its direction and nothing of hosts', () => {
    expect(useProfileStore.getState().setMaster.length).toBe(5)
    expect(useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)).toBe(true)
    const s = useProfileStore.getState()
    expect(s.pendingDirection).toBe('pull')
    expect('pendingPullHosts' in s).toBe(false)
    expect('pendingPullHosts' in persistedEnvelope().state).toBe(false)
  })

  it('a persisted state that holds one (an older build) rehydrates without it, and the next write does not carry it', async () => {
    await rehydrateFrom({ masterHostId: 'host-1', masterProfileId: PROFILE, masterEndpoint: EP, attachId: 'abc123', pendingDirection: 'pull', pendingPullHosts: { rev: 7, hash: 'a'.repeat(64) } })
    const s = useProfileStore.getState()
    expect(s).toMatchObject({ masterHostId: 'host-1', pendingDirection: 'pull', attachId: 'abc123' })
    expect('pendingPullHosts' in s).toBe(false)
    useProfileStore.getState().setAutoSync(false)
    expect('pendingPullHosts' in persistedEnvelope().state).toBe(false)
  })

  it('clearPendingDirection clears the direction', () => {
    useProfileStore.getState().setMaster('host-1', PROFILE, 'pull', EP)
    useProfileStore.getState().clearPendingDirection()
    expect(useProfileStore.getState().pendingDirection).toBeNull()
  })
})
