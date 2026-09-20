// spa/src/lib/storage/__tests__/world-fence.test.ts — the epoch fence of the three
// world stores, on its own: what it lets through, what it drops, and what a drop
// sets in motion. (Two real windows: lib/profile/world-fence.windows.test.ts.)
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../keys'
import { syncManager } from '../sync'
import { MAX_WORLD_EPOCH, fencedWorldStorage, isWorldEpoch, nextWorldEpoch, persistedWorldEpoch, raiseWorldEpochFence, readWorldEpochFence, registerFencedStore } from '../world-fence'

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

  it('a value above MAX_WORLD_EPOCH is junk too → 0 (only corrupt storage gets there, and it must not be a dead end); the bound itself is a value', () => {
    localStorage.setItem(FENCE, String(MAX_WORLD_EPOCH + 1))
    expect(readWorldEpochFence()).toBe(0)
    localStorage.setItem(FENCE, String(Number.MAX_SAFE_INTEGER))
    expect(readWorldEpochFence()).toBe(0)
    localStorage.setItem(FENCE, String(MAX_WORLD_EPOCH))
    expect(readWorldEpochFence()).toBe(MAX_WORLD_EPOCH)
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

/** `raiseWorldEpochFence`, for a test that expects it to succeed. */
function raised(epoch: number): () => void {
  const lower = raiseWorldEpochFence(epoch)
  if (lower === null) throw new Error(`the fence was not raised to ${epoch}`)
  return lower
}

describe('raiseWorldEpochFence', () => {
  it('writes the epoch as a decimal string; lowering puts back what was there — nothing, when nothing was', () => {
    const lower = raised(1)
    expect(localStorage.getItem(FENCE)).toBe('1')
    lower()
    expect(localStorage.getItem(FENCE)).toBeNull()
  })

  it('lowering puts the previous value back, byte for byte, and only once', () => {
    localStorage.setItem(FENCE, '3')
    const lower = raised(4)
    expect(localStorage.getItem(FENCE)).toBe('4')
    lower()
    expect(localStorage.getItem(FENCE)).toBe('3')
    localStorage.setItem(FENCE, '9')
    lower()
    expect(localStorage.getItem(FENCE)).toBe('9')
  })

  it('lowering leaves a fence that ANOTHER window has moved since — higher, which is the only way it moves — alone', () => {
    localStorage.setItem(FENCE, '3')
    const lower = raised(4)
    localStorage.setItem(FENCE, '6') // the other window's operation, begun after ours
    lower()
    expect(localStorage.getItem(FENCE)).toBe('6')
  })

  it.each([7, 8])('a fence that is already AT or ABOVE the target (fence 8, target %i) is a FAILURE, not a no-op: two operations never share an epoch', (target) => {
    localStorage.setItem(FENCE, '8')
    expect(raiseWorldEpochFence(target)).toBeNull()
    expect(localStorage.getItem(FENCE)).toBe('8')
  })

  it.each([0, -1, 1.5, Number.NaN, Number.MAX_SAFE_INTEGER + 2])('a target that is no positive safe integer (%s) fails too', (target) => {
    expect(raiseWorldEpochFence(target)).toBeNull()
    expect(localStorage.getItem(FENCE)).toBeNull()
  })

  it('does not announce: the side key is no store', () => {
    const notify = vi.spyOn(syncManager, 'notify')
    raised(2)()
    expect(notify).not.toHaveBeenCalled()
  })
})

describe('nextWorldEpoch — unique per operation, and above everything there is', () => {
  const NOW = 1_800_000_000_000

  beforeEach(() => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW)
    vi.spyOn(Math, 'random').mockReturnValue(0.4567)
  })

  it('the clock in microseconds plus a random 0–999: two windows in the same millisecond differ 999 times in 1000', () => {
    expect(nextWorldEpoch([0, 0, 0])).toBe(NOW * 1000 + 456)
    vi.spyOn(Math, 'random').mockReturnValue(0.9999999)
    expect(nextWorldEpoch([0, 0, 0])).toBe(NOW * 1000 + 999)
    vi.spyOn(Math, 'random').mockReturnValue(0)
    expect(nextWorldEpoch([0, 0, 0])).toBe(NOW * 1000)
  })

  it('is a safe integer for every date this app will see', () => {
    vi.spyOn(Date, 'now').mockReturnValue(Date.UTC(2200, 0, 1))
    expect(Number.isSafeInteger(nextWorldEpoch([0]))).toBe(true)
  })

  it('strictly above the fence — a clock that went backwards does not bring an epoch back', () => {
    localStorage.setItem(FENCE, String(NOW * 1000 + 5_000))
    expect(nextWorldEpoch([0, 0, 0])).toBe(NOW * 1000 + 5_001)
  })

  it.each([0, 1, 2])('strictly above each of the epochs the stores hold in memory (store %i is ahead)', (ahead) => {
    const epochs = [1, 2, 3]
    epochs[ahead] = NOW * 1000 + 7_000
    expect(nextWorldEpoch(epochs)).toBe(NOW * 1000 + 7_001)
  })

  it('an epoch that is no safe integer (storage junk) is not a bound', () => {
    expect(nextWorldEpoch([Number.NaN, 'x' as unknown as number, Infinity])).toBe(NOW * 1000 + 456)
  })

  it('an epoch above MAX_WORLD_EPOCH — in a store or in the fence — is no bound: the next one comes from the clock, and can be raised over the junk', () => {
    localStorage.setItem(FENCE, String(Number.MAX_SAFE_INTEGER))
    const next = nextWorldEpoch([Number.MAX_SAFE_INTEGER, MAX_WORLD_EPOCH + 1, 3])
    expect(next).toBe(NOW * 1000 + 456)
    expect(raiseWorldEpochFence(next)).not.toBeNull()
    expect(localStorage.getItem(FENCE)).toBe(String(next))
  })

  it('MAX_WORLD_EPOCH is far above the clock (year 2223) and below 2^53', () => {
    expect(MAX_WORLD_EPOCH).toBeLessThan(Number.MAX_SAFE_INTEGER)
    expect(Date.UTC(2200, 0, 1) * 1000 + 999).toBeLessThan(MAX_WORLD_EPOCH)
    expect([0, 5, MAX_WORLD_EPOCH].every(isWorldEpoch)).toBe(true)
    expect([-1, 1.5, Number.NaN, '5', null, MAX_WORLD_EPOCH + 1].some(isWorldEpoch)).toBe(false)
  })

  it('persistedWorldEpoch: what a world store\'s record in storage says — undefined when absent or unreadable', () => {
    expect(persistedWorldEpoch(KEY)).toBeUndefined()
    localStorage.setItem(KEY, '{nope')
    expect(persistedWorldEpoch(KEY)).toBeUndefined()
    localStorage.setItem(KEY, JSON.stringify(value(7)))
    expect(persistedWorldEpoch(KEY)).toBe(7)
    localStorage.setItem(KEY, JSON.stringify(value('junk')))
    expect(persistedWorldEpoch(KEY)).toBe('junk')
  })

  it('what it returns can be raised', () => {
    localStorage.setItem(FENCE, String(NOW * 1000 + 5_000))
    expect(raiseWorldEpochFence(nextWorldEpoch([0]))).not.toBeNull()
  })
})
