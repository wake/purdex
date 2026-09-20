// spa/src/lib/storage/__tests__/world-lock.test.ts — the cross-renderer mutex
// around a world-moving block, on its own. (With real blocks and two windows:
// lib/profile/world-fence.windows.test.ts.)
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { WORLD_LOCK_NAME, WORLD_LOCK_TIMEOUT_MS, withWorldLock } from '../world-lock'
import { FakeLockManager, navigatorWithLocks } from './fake-web-locks'

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('without Web Locks (no secure context: plain http on a tailnet IP)', () => {
  it('this environment has none — so every other test of this repo IS the old path', () => {
    expect((navigator as unknown as { locks?: unknown }).locks).toBeUndefined()
  })

  it('the block runs INSIDE the call, synchronously, exactly as before; its value is the promise\'s', async () => {
    let ran = false
    const pending = withWorldLock(() => {
      ran = true
      return 'done'
    }, () => 'busy')
    expect(ran).toBe(true)
    expect(await pending).toBe('done')
  })

  it('a block that throws rejects the promise (and no timer is left behind)', async () => {
    vi.useFakeTimers()
    await expect(withWorldLock(() => {
      throw new Error('boom')
    }, () => 'busy')).rejects.toThrow('boom')
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('with Web Locks', () => {
  let locks: FakeLockManager

  beforeEach(() => {
    vi.useFakeTimers()
    locks = new FakeLockManager()
    vi.stubGlobal('navigator', navigatorWithLocks(locks))
  })

  it('the block runs under the exclusive lock `purdex-world-switch` — and only once it is granted, never inside the call', async () => {
    let ran = false
    const pending = withWorldLock(() => {
      ran = locks.isHeld(WORLD_LOCK_NAME)
      return 'done'
    }, () => 'busy')
    expect(ran).toBe(false)
    expect(await pending).toBe('done')
    expect(ran).toBe(true)
    expect(locks.grants.map((g) => g.name)).toEqual([WORLD_LOCK_NAME])
    expect(locks.isHeld(WORLD_LOCK_NAME)).toBe(false)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('two blocks never overlap, and run in the order they were asked for', async () => {
    const log: string[] = []
    const first = withWorldLock(() => log.push('first'), () => -1)
    const second = withWorldLock(() => log.push(`second sees ${log.join()}`), () => -1)
    await Promise.all([first, second])
    expect(log).toEqual(['first', 'second sees first'])
  })

  it('not granted within 3 s: `busy` — and the block NEVER runs, not even when the lock comes free later', async () => {
    let release = (): void => {}
    void locks.request(WORLD_LOCK_NAME, {}, () => new Promise<void>((done) => (release = done))) // another renderer, stuck
    const body = vi.fn(() => 'done')
    const pending = withWorldLock(body, () => 'busy')
    await vi.advanceTimersByTimeAsync(WORLD_LOCK_TIMEOUT_MS - 1)
    expect(body).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(await pending).toBe('busy')
    release()
    await vi.advanceTimersByTimeAsync(10)
    expect(body).not.toHaveBeenCalled()
    expect(locks.isHeld(WORLD_LOCK_NAME)).toBe(false)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a block that was granted is not timed out afterwards: the clock stops at the grant', async () => {
    const pending = withWorldLock(() => 'done', () => 'busy')
    await vi.advanceTimersByTimeAsync(WORLD_LOCK_TIMEOUT_MS * 2)
    expect(await pending).toBe('done')
  })

  it('a block that throws: the promise rejects with it, and the lock is free for the next one', async () => {
    await expect(withWorldLock(() => {
      throw new Error('boom')
    }, () => 'busy')).rejects.toThrow('boom')
    expect(locks.isHeld(WORLD_LOCK_NAME)).toBe(false)
    expect(await withWorldLock(() => 'next', () => 'busy')).toBe('next')
  })

  it('`busy` is only ever said of a request that was NOT granted: a block that threw is a throw, even if the clock ran out on the way in', async () => {
    const late = {
      request: async (_name: string, _options: unknown, callback: () => unknown) => {
        vi.advanceTimersByTime(WORLD_LOCK_TIMEOUT_MS) // the abort fires — and the browser grants the lock all the same
        return callback()
      },
    }
    vi.stubGlobal('navigator', Object.create(navigator, { locks: { value: late } }))
    await expect(withWorldLock(() => {
      throw new Error('boom')
    }, () => 'busy')).rejects.toThrow('boom')
  })

  it('a request that fails for another reason than our timeout is not called `busy`', async () => {
    vi.stubGlobal('navigator', Object.create(navigator, { locks: { value: { request: () => Promise.reject(new Error('SecurityError')) } } }))
    await expect(withWorldLock(() => 'done', () => 'busy')).rejects.toThrow('SecurityError')
  })
})
