// spa/src/lib/storage/__tests__/fake-web-locks.ts — a faithful-enough Web Locks
// `LockManager` for tests: ONE exclusive FIFO queue per name, shared by every
// "window" of a test (as the real one is shared by every renderer of an origin).
// Faithful where it matters to world-lock.ts: the callback never runs inside
// `request()` itself; the lock is held until the callback's result has settled
// and is released when it throws or rejects; an aborted signal takes a WAITING
// request out of the queue (rejecting with an AbortError) and does nothing to one
// that has been granted.
interface Waiting {
  run: () => void
  cancelled: boolean
}

export class FakeLockManager {
  private readonly held = new Set<string>()
  private readonly queues = new Map<string, Waiting[]>()
  /** Every grant, in order: the lock's name and what the callback returned (a Promise, if it did). */
  readonly grants: { name: string; returned: unknown }[] = []

  isHeld(name: string): boolean {
    return this.held.has(name)
  }

  request<T>(name: string, options: { mode?: string; signal?: AbortSignal }, callback: (lock: { name: string }) => T | Promise<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const waiting: Waiting = {
        cancelled: false,
        run: () => {
          void Promise.resolve().then(async () => {
            try {
              const returned = callback({ name })
              this.grants.push({ name, returned })
              resolve(await returned)
            } catch (err) {
              reject(err)
            } finally {
              this.release(name)
            }
          })
        },
      }
      options.signal?.addEventListener('abort', () => {
        const queue = this.queues.get(name) ?? []
        if (!queue.includes(waiting)) return // granted already (or gone): an abort does not take a lock back
        waiting.cancelled = true
        this.queues.set(name, queue.filter((w) => w !== waiting))
        reject(new DOMException('The request was aborted.', 'AbortError'))
      })
      if (this.held.has(name)) {
        this.queues.set(name, [...(this.queues.get(name) ?? []), waiting])
        return
      }
      this.held.add(name)
      waiting.run()
    })
  }

  private release(name: string): void {
    const next = (this.queues.get(name) ?? []).shift()
    if (next === undefined) this.held.delete(name)
    else next.run()
  }
}

/** `navigator` as it is, plus `locks`. For `vi.stubGlobal('navigator', …)`. */
export function navigatorWithLocks(locks: FakeLockManager): Navigator {
  return Object.create(navigator, { locks: { value: locks } }) as Navigator
}
