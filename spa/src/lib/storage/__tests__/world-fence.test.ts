// spa/src/lib/storage/__tests__/world-fence.test.ts — the epoch fence of the three
// world stores, on its own: what it lets through, what it drops, and what a drop
// sets in motion. (Two real windows: lib/profile/world-fence.windows.test.ts.)
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../keys'
import { syncManager } from '../sync'
import { fencedWorldStorage, raiseWorldEpochFence, readWorldEpochFence, registerFencedStore } from '../world-fence'

const KEY = STORAGE_KEYS.WORKSPACES
const FENCE = STORAGE_KEYS.WORLD_EPOCH

const value = (worldEpoch: unknown, marker = 'x') => ({ state: { marker, worldEpoch }, version: 1 })
const onDisk = (): { state: { marker: string; worldEpoch: unknown } } | null => JSON.parse(localStorage.getItem(KEY) ?? 'null')

async function flush(): Promise<void> {
  for (let i = 0; i < 5; i++) await Promise.resolve()
}

let rehydrate: ReturnType<typeof vi.fn<() => void>>

beforeEach(() => {
  localStorage.clear()
  rehydrate = vi.fn<() => void>()
  registerFencedStore(KEY, { persist: { rehydrate } })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('readWorldEpochFence', () => {
  it('no side key → 0', () => {
    expect(readWorldEpochFence()).toBe(0)
  })

  it.each(['', 'abc', '1.5', '-1', '1e3', ' 4', '9007199254740993', 'null'])('a side key that is not a decimal safe integer (%j) → 0', (raw) => {
    localStorage.setItem(FENCE, raw)
    expect(readWorldEpochFence()).toBe(0)
  })

  it('a decimal integer → that integer', () => {
    localStorage.setItem(FENCE, '4')
    expect(readWorldEpochFence()).toBe(4)
  })
})

describe('a user who never switched a profile', () => {
  it('writes exactly what the plain storage writes, notifies like it — and no side key appears', () => {
    const notify = vi.spyOn(syncManager, 'notify')
    fencedWorldStorage.setItem(KEY, value(0))
    expect(localStorage.getItem(KEY)).toBe(JSON.stringify(value(0)))
    expect(notify).toHaveBeenCalledExactlyOnceWith(KEY)
    expect(localStorage.getItem(FENCE)).toBeNull()
    expect(Object.keys(localStorage)).toEqual([KEY])
  })

  it('an unchanged value is not written again and not announced again', () => {
    fencedWorldStorage.setItem(KEY, value(0))
    const notify = vi.spyOn(syncManager, 'notify')
    const write = vi.spyOn(Storage.prototype, 'setItem')
    fencedWorldStorage.setItem(KEY, value(0))
    expect(write).not.toHaveBeenCalled()
    expect(notify).not.toHaveBeenCalled()
  })

  it('getItem parses, removeItem removes and announces', () => {
    fencedWorldStorage.setItem(KEY, value(0))
    expect(fencedWorldStorage.getItem(KEY)).toEqual(value(0))
    const notify = vi.spyOn(syncManager, 'notify')
    fencedWorldStorage.removeItem(KEY)
    expect(localStorage.getItem(KEY)).toBeNull()
    expect(notify).toHaveBeenCalledExactlyOnceWith(KEY)
  })
})

describe('behind a raised fence', () => {
  beforeEach(() => {
    fencedWorldStorage.setItem(KEY, value(4, 'new-world'))
    localStorage.setItem(FENCE, '4')
  })

  it('a state of an OLDER epoch is not written, and the store is rehydrated — once, however many writes were dropped', async () => {
    const notify = vi.spyOn(syncManager, 'notify')
    fencedWorldStorage.setItem(KEY, value(3, 'stale'))
    fencedWorldStorage.setItem(KEY, value(3, 'stale-again'))
    expect(onDisk()?.state.marker).toBe('new-world')
    expect(notify).not.toHaveBeenCalled()
    expect(rehydrate).not.toHaveBeenCalled() // not inside the write: a microtask later
    await flush()
    expect(rehydrate).toHaveBeenCalledTimes(1)
  })

  it('the same epoch, and a newer one, are written', async () => {
    fencedWorldStorage.setItem(KEY, value(4, 'edit'))
    expect(onDisk()?.state.marker).toBe('edit')
    fencedWorldStorage.setItem(KEY, value(5, 'next'))
    expect(onDisk()?.state.marker).toBe('next')
    await flush()
    expect(rehydrate).not.toHaveBeenCalled()
  })

  it('a state whose epoch is no number at all is written: nothing says it is older, and dropping it for ever would lose the tabs', async () => {
    fencedWorldStorage.setItem(KEY, value('junk', 'junk-epoch'))
    expect(onDisk()?.state.marker).toBe('junk-epoch')
    await flush()
    expect(rehydrate).not.toHaveBeenCalled()
  })

  it('a write dropped BY the rehydrate it caused (a migrate that re-persists) does not ask for another: no loop', async () => {
    rehydrate.mockImplementation(() => fencedWorldStorage.setItem(KEY, value(3, 'still-stale')))
    fencedWorldStorage.setItem(KEY, value(3, 'stale'))
    await flush()
    await flush()
    expect(rehydrate).toHaveBeenCalledTimes(1)
    // …and the next stale write, a turn later, is answered again.
    rehydrate.mockImplementation(() => {})
    fencedWorldStorage.setItem(KEY, value(3, 'stale'))
    await flush()
    expect(rehydrate).toHaveBeenCalledTimes(2)
  })

  it('a rehydrate that throws is contained', async () => {
    rehydrate.mockImplementation(() => {
      throw new Error('boom')
    })
    fencedWorldStorage.setItem(KEY, value(3, 'stale'))
    await flush()
    expect(rehydrate).toHaveBeenCalledTimes(1)
  })
})

describe('raiseWorldEpochFence', () => {
  it('writes the epoch as a decimal string; lowering puts back what was there — nothing, when nothing was', () => {
    const lower = raiseWorldEpochFence(1)
    expect(localStorage.getItem(FENCE)).toBe('1')
    lower()
    expect(localStorage.getItem(FENCE)).toBeNull()
  })

  it('lowering puts the previous value back, byte for byte, and only once', () => {
    localStorage.setItem(FENCE, '3')
    const lower = raiseWorldEpochFence(4)
    expect(localStorage.getItem(FENCE)).toBe('4')
    lower()
    expect(localStorage.getItem(FENCE)).toBe('3')
    localStorage.setItem(FENCE, '9')
    lower()
    expect(localStorage.getItem(FENCE)).toBe('9')
  })

  it('never lowers a fence another window raised higher', () => {
    localStorage.setItem(FENCE, '7')
    raiseWorldEpochFence(4)
    expect(localStorage.getItem(FENCE)).toBe('7')
  })

  it('does not announce: the side key is no store', () => {
    const notify = vi.spyOn(syncManager, 'notify')
    raiseWorldEpochFence(2)()
    expect(notify).not.toHaveBeenCalled()
  })
})
